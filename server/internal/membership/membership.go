// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package membership

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/serialx/hashring"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
)

type Membership interface {
	Start() errors.CategorizedError
	Stop()

	MemberID() string
	GetNodeForKey(key string) string
	GetAddress(memberID string) string
	GetShardsForMember(memberID string, maxShards int) []int32
}

// membershipImpl manages gossip membership and consistent-hash key routing.
// ShardManager and Matching each run a separate instance.
type membershipImpl struct {
	list     *memberlist.Memberlist
	hashRing *hashring.HashRing

	cfg             *config.MembershipConfig
	logger          log.Logger
	memberID        string
	internalAddress string

	mu              sync.RWMutex
	memberAddresses map[string]string // memberID -> gRPC address
	members         map[string]struct{}

	// onRebalance is called (outside the lock) on every membership change. May be nil.
	onRebalance func()

	// onAddressRemoved is called (outside the lock) with each gRPC address that
	// leaves the ring, so a retired pod IP's pooled connection is evicted. May be nil.
	onAddressRemoved func(addr string)

	discoveryCancel context.CancelFunc
	discoveryWG     sync.WaitGroup
}

func NewMembership(
	cfg *config.MembershipConfig,
	logger log.Logger,
	memberID string,
	internalAddress string,
	onRebalance func(),
	onAddressRemoved func(addr string),
) Membership {
	if cfg == nil {
		panic("membership cfg is nil")
	}
	if logger == nil {
		panic("membership logger is nil")
	}
	if memberID == "" {
		panic("membership memberID is empty")
	}

	return &membershipImpl{
		cfg:              cfg,
		logger:           logger,
		memberID:         memberID,
		internalAddress:  resolveInternalAddress(cfg.AdvertiseAddress, internalAddress),
		memberAddresses:  make(map[string]string),
		members:          map[string]struct{}{memberID: {}},
		onRebalance:      onRebalance,
		onAddressRemoved: onAddressRemoved,
	}
}

func resolveInternalAddress(advertiseAddress, internalAddress string) string {
	if advertiseAddress == "" {
		return internalAddress
	}
	advertiseHost, _ := ParseHostPort(advertiseAddress)
	_, internalPort := ParseHostPort(internalAddress)
	return fmt.Sprintf("%s:%d", advertiseHost, internalPort)
}

func (m *membershipImpl) Start() errors.CategorizedError {
	m.hashRing = hashring.NewWithWeights(map[string]int{m.memberID: m.cfg.NumberOfVNodes})

	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = m.memberID
	mlConfig.BindAddr, mlConfig.BindPort = ParseHostPort(m.cfg.BindAddress)
	if m.cfg.AdvertiseAddress != "" {
		mlConfig.AdvertiseAddr, mlConfig.AdvertisePort = ParseHostPort(m.cfg.AdvertiseAddress)
	}

	// Same-name pod restart can reclaim identity immediately.
	mlConfig.DeadNodeReclaimTime = 1 * time.Millisecond

	// Larger probe interval reduces TCP fallback storms on flaky UDP (e.g. kind).
	mlConfig.ProbeInterval = 5 * time.Second
	mlConfig.ProbeTimeout = 3 * time.Second
	mlConfig.SuspicionMaxTimeoutMult = 6

	mlConfig.Events = &eventDelegate{m: m}
	mlConfig.Delegate = &metaDelegate{m: m}

	list, err := memberlist.Create(mlConfig)
	if err != nil {
		return errors.NewInternalError("failed to create memberlist", err)
	}
	m.list = list

	m.joinAddresses(m.cfg.StaticAddresses, "static addresses")
	m.startDiscoveryLoop()

	if m.cfg.MinMembersBeforeReady > 1 {
		m.waitForMinMembers()
	}

	return nil
}

func (m *membershipImpl) Stop() {
	if m.discoveryCancel != nil {
		m.discoveryCancel()
		m.discoveryWG.Wait()
	}
	if m.list != nil {
		if err := m.list.Leave(5 * time.Second); err != nil {
			m.logger.Warn("failed to leave memberlist", tag.Error(err))
		}
		if err := m.list.Shutdown(); err != nil {
			m.logger.Warn("failed to shutdown memberlist", tag.Error(err))
		}
	}
}

// MemberID returns this node's cluster identity (memberlist Name).
func (m *membershipImpl) MemberID() string { return m.memberID }

// GetNodeForKey returns the member that should own the given key.
func (m *membershipImpl) GetNodeForKey(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.hashRing == nil {
		return m.memberID
	}
	owner, _ := m.hashRing.GetNode(key)
	return owner
}

// GetAddress returns the gRPC address for a member.
func (m *membershipImpl) GetAddress(memberID string) string {
	if memberID == m.memberID {
		return m.internalAddress
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.memberAddresses[memberID]
}

// GetShardsForMember returns shard IDs in [0, maxShards) owned by memberID.
func (m *membershipImpl) GetShardsForMember(memberID string, maxShards int) []int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.hashRing == nil {
		return nil
	}
	var shards []int32
	for i := int32(0); i < int32(maxShards); i++ {
		owner, _ := m.hashRing.GetNode(strconv.Itoa(int(i)))
		if owner == memberID {
			shards = append(shards, i)
		}
	}
	return shards
}

func (m *membershipImpl) startDiscoveryLoop() {
	if !m.usesDNSDiscovery() {
		return
	}

	m.joinDNSDiscoveredAddresses()

	ctx, cancel := context.WithCancel(context.Background())
	m.discoveryCancel = cancel
	m.discoveryWG.Add(1)
	go func() {
		defer m.discoveryWG.Done()

		ticker := time.NewTicker(m.discoveryRefreshInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.joinDNSDiscoveredAddresses()
			}
		}
	}()
}

func (m *membershipImpl) usesDNSDiscovery() bool {
	return m.cfg.Discovery.Mode == "dns" && m.cfg.Discovery.ServiceDNS != ""
}

func (m *membershipImpl) discoveryRefreshInterval() time.Duration {
	if m.cfg.Discovery.RefreshInterval > 0 {
		return m.cfg.Discovery.RefreshInterval
	}
	return config.DefaultDiscoveryConfig().RefreshInterval
}

func (m *membershipImpl) joinDNSDiscoveredAddresses() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hosts, err := net.DefaultResolver.LookupHost(ctx, m.cfg.Discovery.ServiceDNS)
	if err != nil {
		m.logger.Warn("failed to resolve discovery DNS", tag.Error(err))
		return
	}

	m.joinAddresses(buildDiscoveryTargets(m.cfg, hosts), "dns discovery")
}

func (m *membershipImpl) joinAddresses(addresses []string, source string) {
	if m.list == nil || len(addresses) == 0 {
		return
	}
	if _, err := m.list.Join(addresses); err != nil {
		m.logger.Warn("failed to join cluster", tag.Source(source), tag.Error(err))
	}
}

func (m *membershipImpl) waitForMinMembers() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		m.mu.RLock()
		current := len(m.members)
		m.mu.RUnlock()
		if current >= m.cfg.MinMembersBeforeReady {
			m.logger.Info("minimum members reached",
				tag.NumMembers(current),
				tag.MinMembers(m.cfg.MinMembersBeforeReady))
			return
		}
		m.logger.Info("waiting for minimum members",
			tag.NumMembers(current),
			tag.MinMembers(m.cfg.MinMembersBeforeReady))
		<-ticker.C
	}
}

func (m *membershipImpl) notifyAddressRemoved(addr string) {
	if addr == "" || m.onAddressRemoved == nil {
		return
	}
	m.onAddressRemoved(addr)
}

func buildDiscoveryTargets(cfg *config.MembershipConfig, hosts []string) []string {
	if len(hosts) == 0 {
		return nil
	}

	port := cfg.Discovery.Port
	if port == 0 {
		_, port = ParseHostPort(cfg.BindAddress)
	}

	selfHosts := map[string]bool{}
	for _, addr := range []string{cfg.BindAddress, cfg.AdvertiseAddress} {
		host, _ := ParseHostPort(addr)
		if host != "" && host != "0.0.0.0" {
			selfHosts[host] = true
		}
	}

	unique := make(map[string]bool, len(hosts))
	var targets []string
	for _, host := range hosts {
		if host == "" || selfHosts[host] {
			continue
		}
		target := fmt.Sprintf("%s:%d", host, port)
		if unique[target] {
			continue
		}
		unique[target] = true
		targets = append(targets, target)
	}
	return targets
}

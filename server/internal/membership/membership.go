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

	MyMemberID() string
	GetMemberIDForKey(key string) string
	GetGrpcAddressForMember(memberID string) string
	GetShardsForMember(memberID string, maxShards int) []int32
}

// membershipImpl manages gossip membership and consistent-hash key routing.
// ShardManager and Matching each run a separate instance.
type membershipImpl struct {
	mlist *memberlist.Memberlist
	hring *hashring.HashRing

	cfg      *config.MembershipConfig
	logger   log.Logger
	memberID string
	// for peer to forward grpc requests
	grpcAddress string

	memberAddresses map[string]string // memberID -> gRPC address
	// for memberAddresses
	memberMu sync.RWMutex

	// onRebalance is called (outside the lock) on every membership change.
	// Used by cluster(shardManager/matching) to handle rebalance
	onRebalance func()

	// onAddressRemoved is called (outside the lock) with each gRPC address that
	// leaves the ring.
	// Used by CachedPeerConnection to evict the retired pod IP
	onAddressRemoved func(addr string)

	stopCh chan struct{}

	// used as port for dns discovered address
	// NOTE: not for grpc
	dnsAdvertisePort int
}

func NewMembership(
	cfg *config.MembershipConfig,
	logger log.Logger,
	memberID string,
	grpcAddress string,
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
	if grpcAddress == "" {
		panic("membership grpcAddressForPeer is empty")
	}

	return &membershipImpl{
		hring: hashring.NewWithWeights(map[string]int{memberID: cfg.NumberOfVNodes}),

		cfg:         cfg,
		logger:      logger,
		memberID:    memberID,
		grpcAddress: grpcAddress,

		memberAddresses:  make(map[string]string),
		onRebalance:      onRebalance,
		onAddressRemoved: onAddressRemoved,

		stopCh: make(chan struct{}),
	}
}

func (m *membershipImpl) Start() errors.CategorizedError {
	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = m.memberID
	var err errors.CategorizedError
	mlConfig.BindAddr, mlConfig.BindPort, err = splitHostPort(m.cfg.BindAddress)
	if err != nil {
		return err
	}
	advertiseAddress := m.cfg.AdvertiseAddress
	if advertiseAddress == "" {
		advertiseAddress = mlConfig.BindAddr
	}

	mlConfig.AdvertiseAddr, mlConfig.AdvertisePort, err = splitHostPort(m.cfg.AdvertiseAddress)
	if err != nil {
		return err
	}
	m.dnsAdvertisePort = m.cfg.Discovery.DNSPort
	if m.dnsAdvertisePort == 0 {
		m.dnsAdvertisePort = mlConfig.AdvertisePort
	}

	// Same-name pod restart can reclaim identity immediately.
	mlConfig.DeadNodeReclaimTime = 1 * time.Millisecond

	// Larger probe interval reduces TCP fallback storms on flaky UDP (e.g. kind).
	mlConfig.ProbeInterval = 5 * time.Second
	mlConfig.ProbeTimeout = 3 * time.Second
	mlConfig.SuspicionMaxTimeoutMult = 6

	mlConfig.Events = newEventDelegate(m)
	mlConfig.Delegate = newMetaDelegate(m)

	list, rerr := memberlist.Create(mlConfig)
	if rerr != nil {
		return errors.NewInternalError("failed to create memberlist", rerr)
	}
	m.mlist = list

	return m.bootstrap()
}

func (m *membershipImpl) Stop() {
	close(m.stopCh)
	if err := m.mlist.Leave(5 * time.Second); err != nil {
		m.logger.Error("failed to leave memberlist", tag.Error(err))
	}
	if err := m.mlist.Shutdown(); err != nil {
		m.logger.Error("failed to shutdown memberlist", tag.Error(err))
	}
}

// MyMemberID returns this node's cluster identity (memberlist Name).
func (m *membershipImpl) MyMemberID() string { return m.memberID }

// GetMemberIDForKey returns the memberID that should own the given key.
func (m *membershipImpl) GetMemberIDForKey(key string) string {
	m.memberMu.RLock()
	defer m.memberMu.RUnlock()
	owner, ok := m.hring.GetNode(key)
	if !ok {
		panic("failed to get node for key, something wrong with hash ring")
	}
	return owner
}

// GetGrpcAddressForMember returns the gRPC address for a member.
func (m *membershipImpl) GetGrpcAddressForMember(memberID string) string {
	if memberID == m.memberID {
		return m.grpcAddress
	}
	m.memberMu.RLock()
	defer m.memberMu.RUnlock()
	return m.memberAddresses[memberID]
}

// GetShardsForMember returns shard IDs in [0, maxShards) owned by memberID.
func (m *membershipImpl) GetShardsForMember(memberID string, totalShards int) []int32 {
	m.memberMu.RLock()
	defer m.memberMu.RUnlock()
	var shardIDs []int32
	for i := int32(0); i < int32(totalShards); i++ {
		owner, ok := m.hring.GetNode(strconv.Itoa(int(i)))
		if !ok {
			panic("failed to get node for key, something wrong with hash ring")
		}
		if owner == memberID {
			shardIDs = append(shardIDs, i)
		}
	}
	return shardIDs
}

func (m *membershipImpl) bootstrap() errors.CategorizedError {
	if m.cfg.Discovery.Mode == "static" && len(m.cfg.Discovery.StaticAddresses) > 0 {
		_, err := m.mlist.Join(m.cfg.Discovery.StaticAddresses)
		if err != nil {
			return errors.NewInternalError("failed to join static address at bootstrap", err)
		}
	}
	if m.cfg.Discovery.Mode == "dns" && m.cfg.Discovery.DNSAddress != "" {
		addrs, err := m.lookupDNSAddress()
		if err != nil {
			return errors.NewInternalError("failed to lookup dns address at bootstrap", err)
		}

		_, rerr := m.mlist.Join(addrs)
		if rerr != nil {
			return errors.NewInternalError("failed to join dns address at bootstrap", rerr)
		}
	}

	if m.cfg.MinMembersBeforeReady > 1 {
		m.waitForMinMembers()
	}

	m.startDNSRefreshLoop()

	return nil
}

func (m *membershipImpl) startDNSRefreshLoop() {
	go func() {
		ticker := time.NewTicker(m.cfg.Discovery.DNSRefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				addrs, err := m.lookupDNSAddress()
				if err != nil {
					m.logger.Error("failed to lookup dns address", tag.Error(err))
				} else {
					_, rerr := m.mlist.Join(addrs)
					if rerr != nil {
						m.logger.Error("failed to join dns address", tag.Error(rerr))
					}
				}
			}
		}
	}()
}

func (m *membershipImpl) lookupDNSAddress() ([]string, errors.CategorizedError) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hosts, err := net.DefaultResolver.LookupHost(ctx, m.cfg.Discovery.DNSAddress)
	if err != nil {
		return nil, errors.NewInternalError("failed to resolve discovery DNS", err)
	}

	addrs := make([]string, len(hosts))
	for i, addr := range hosts {
		addrs[i] = fmt.Sprintf("%s:%d", addr, m.dnsAdvertisePort)
	}
	return addrs, nil
}

func (m *membershipImpl) waitForMinMembers() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.memberMu.RLock()
			current := len(m.memberAddresses)
			m.memberMu.RUnlock()
			if current >= m.cfg.MinMembersBeforeReady {
				m.logger.Info("minimum members reached",
					tag.NumMembers(current),
					tag.MinMembers(m.cfg.MinMembersBeforeReady))
				return
			}
			m.logger.Info("waiting for minimum members",
				tag.NumMembers(current),
				tag.MinMembers(m.cfg.MinMembersBeforeReady))
		}
	}
}

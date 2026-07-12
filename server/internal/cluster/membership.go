package cluster

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/hashicorp/memberlist"
	"github.com/serialx/hashring"
)

// Membership manages cluster membership via gossip and maps keys to members
// via consistent hashing. Used by both ShardManager (run-service) and the
// tasklist Registry (matching-service) with separate memberlist instances.
type Membership struct {
	cfg             config.ClusterConfig
	logger          log.Logger
	memberID        string
	internalAddress string

	list     *memberlist.Memberlist
	hashRing *hashring.HashRing

	mu              sync.RWMutex
	memberAddresses map[string]string // memberID -> gRPC address
	members         map[string]struct{}

	onRebalance func() // called on membership change; may be nil

	// onAddressRemoved is called (outside the lock) with each gRPC address
	// that leaves the ring, so the RemoteClient can close its pooled conn
	// and a retired pod IP's connection doesn't linger. May be nil.
	onAddressRemoved func(addr string)

	discoveryCancel context.CancelFunc
	discoveryWG     sync.WaitGroup
}

func NewMembership(
	cfg config.ClusterConfig,
	logger log.Logger,
	memberID string,
	internalAddress string,
	onRebalance func(),
	onAddressRemoved func(addr string),
) *Membership {
	resolvedAddr := internalAddress
	if cfg.AdvertiseAddress != "" {
		advertiseHost, _ := ParseHostPort(cfg.AdvertiseAddress)
		_, internalPort := ParseHostPort(internalAddress)
		resolvedAddr = fmt.Sprintf("%s:%d", advertiseHost, internalPort)
	}

	return &Membership{
		cfg:              cfg,
		logger:           logger,
		memberID:         memberID,
		internalAddress:  resolvedAddr,
		memberAddresses:  make(map[string]string),
		members:          map[string]struct{}{memberID: {}},
		onRebalance:      onRebalance,
		onAddressRemoved: onAddressRemoved,
	}
}

func (m *Membership) Start() error {
	m.hashRing = hashring.NewWithWeights(map[string]int{m.memberID: m.cfg.NumberOfVNodes})

	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = m.memberID
	mlConfig.BindAddr, mlConfig.BindPort = ParseHostPort(m.cfg.BindAddress)
	if m.cfg.AdvertiseAddress != "" {
		mlConfig.AdvertiseAddr, mlConfig.AdvertisePort = ParseHostPort(m.cfg.AdvertiseAddress)
	}

	// Allow a restarted pod (same name, new IP) to immediately reclaim its
	// identity. Without this, memberlist rejects the new node for up to 30s
	// while the old address is still in the dead node list.
	mlConfig.DeadNodeReclaimTime = 1 * time.Millisecond

	// In environments where UDP is unreliable (e.g., kind clusters), the
	// default 1s ProbeInterval causes constant TCP fallback probes that
	// saturate the node. Increase to reduce overhead while still detecting
	// failures within a reasonable window.
	mlConfig.ProbeInterval = 5 * time.Second
	mlConfig.ProbeTimeout = 3 * time.Second
	mlConfig.SuspicionMaxTimeoutMult = 6

	mlConfig.Events = &eventDelegate{m: m}
	mlConfig.Delegate = &metaDelegate{m: m}

	list, err := memberlist.Create(mlConfig)
	if err != nil {
		return fmt.Errorf("failed to create memberlist: %w", err)
	}
	m.list = list

	m.joinAddresses(m.cfg.StaticAddresses, "static addresses")
	m.startDiscoveryLoop()

	if m.cfg.MinMembersBeforeReady > 1 {
		m.waitForMinMembers()
	}

	return nil
}

func (m *Membership) Stop() {
	if m.discoveryCancel != nil {
		m.discoveryCancel()
		m.discoveryWG.Wait()
	}
	if m.list != nil {
		m.list.Leave(5e9) // 5s
		m.list.Shutdown()
	}
}

func (m *Membership) startDiscoveryLoop() {
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

func (m *Membership) usesDNSDiscovery() bool {
	return m.cfg.Discovery.Mode == "dns" && m.cfg.Discovery.ServiceDNS != ""
}

func (m *Membership) discoveryRefreshInterval() time.Duration {
	if m.cfg.Discovery.RefreshInterval > 0 {
		return m.cfg.Discovery.RefreshInterval
	}
	return config.DefaultDiscoveryConfig().RefreshInterval
}

func (m *Membership) joinDNSDiscoveredAddresses() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hosts, err := net.DefaultResolver.LookupHost(ctx, m.cfg.Discovery.ServiceDNS)
	if err != nil {
		m.logger.Warn("Failed to resolve discovery DNS", tag.Error(err))
		return
	}

	m.joinAddresses(buildDiscoveryTargets(m.cfg, hosts), "dns discovery")
}

func (m *Membership) joinAddresses(addresses []string, source string) {
	if m.list == nil || len(addresses) == 0 {
		return
	}
	if _, err := m.list.Join(addresses); err != nil {
		m.logger.Warn("Failed to join cluster", tag.Source(source), tag.Error(err))
	}
}

// notifyAddressRemoved invokes the onAddressRemoved hook (if set) for a
// departed gRPC address. addr may be empty (self / unknown), in which case
// it is a no-op.
func (m *Membership) notifyAddressRemoved(addr string) {
	if addr == "" || m.onAddressRemoved == nil {
		return
	}
	m.onAddressRemoved(addr)
}

// buildHashRingLocked returns a fresh consistent-hash ring covering exactly
// the given members, each weighted by NumberOfVNodes. Caller holds m.mu.
func (m *Membership) buildHashRingLocked(members map[string]struct{}) *hashring.HashRing {
	weights := make(map[string]int, len(members))
	for name := range members {
		weights[name] = m.cfg.NumberOfVNodes
	}
	return hashring.NewWithWeights(weights)
}

func (m *Membership) waitForMinMembers() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		m.mu.RLock()
		current := len(m.members)
		m.mu.RUnlock()
		if current >= m.cfg.MinMembersBeforeReady {
			m.logger.Info("Minimum members reached",
				tag.NumMembers(current),
				tag.MinMembers(m.cfg.MinMembersBeforeReady))
			return
		}
		m.logger.Info("Waiting for minimum members",
			tag.NumMembers(current),
			tag.MinMembers(m.cfg.MinMembersBeforeReady))
		<-ticker.C
	}
}

// MemberID returns the unique identifier of this node within the cluster.
func (m *Membership) MemberID() string { return m.memberID }

// GetNodeForKey returns the member that should own the given key
// (shard ID, tasklist partition key, etc.).
func (m *Membership) GetNodeForKey(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.hashRing == nil {
		return m.memberID
	}
	owner, _ := m.hashRing.GetNode(key)
	return owner
}

// GetAddress returns the gRPC address for a member.
func (m *Membership) GetAddress(memberID string) string {
	if memberID == m.memberID {
		return m.internalAddress
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.memberAddresses[memberID]
}

// GetShardsForMember returns which shards should be owned by the given member.
func (m *Membership) GetShardsForMember(memberID string, maxShards int) []int32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var shards []int32
	for i := int32(0); i < int32(maxShards); i++ {
		owner, _ := m.hashRing.GetNode(strconv.Itoa(int(i)))
		if owner == memberID {
			shards = append(shards, i)
		}
	}
	return shards
}

// --- Memberlist Event Delegate ---

type eventDelegate struct {
	m *Membership
}

func (d *eventDelegate) NotifyJoin(node *memberlist.Node) {
	d.m.logger.Info("Member joined", tag.NodeName(node.Name))

	d.m.mu.Lock()
	d.m.members[node.Name] = struct{}{}
	if len(node.Meta) > 0 {
		d.m.memberAddresses[node.Name] = string(node.Meta)
	}
	if d.m.hashRing != nil {
		d.m.hashRing = d.m.hashRing.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
	}
	d.m.mu.Unlock()

	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

func (d *eventDelegate) NotifyLeave(node *memberlist.Node) {
	d.m.logger.Info("Member left", tag.NodeName(node.Name))

	d.m.mu.Lock()
	departedAddr := d.m.memberAddresses[node.Name]
	delete(d.m.members, node.Name)
	delete(d.m.memberAddresses, node.Name)
	if d.m.hashRing != nil {
		d.m.hashRing = d.m.hashRing.RemoveNode(node.Name)
	}
	d.m.mu.Unlock()

	d.m.notifyAddressRemoved(departedAddr)
	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

// NotifyUpdate fires when a node's metadata changes, notably when a
// same-name pod restarts with a new IP (memberlist reclaims the name and
// updates node.Meta). We refresh the cached address so GetAddress stops
// returning the dead IP immediately, rather than waiting for the reconcile
// tick.
func (d *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	newAddr := string(node.Meta)
	if newAddr == "" {
		return
	}

	d.m.mu.Lock()
	changed := d.m.memberAddresses[node.Name] != newAddr
	if changed {
		d.m.members[node.Name] = struct{}{}
		d.m.memberAddresses[node.Name] = newAddr
		if d.m.hashRing != nil {
			// AddWeightedNode is idempotent: a no-op if already present.
			d.m.hashRing = d.m.hashRing.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
		}
	}
	d.m.mu.Unlock()

	if !changed {
		return
	}
	d.m.logger.Info("Member address updated", tag.NodeName(node.Name))
	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

// --- Memberlist Meta Delegate ---

type metaDelegate struct {
	m *Membership
}

func (d *metaDelegate) NodeMeta(limit int) []byte {
	meta := []byte(d.m.internalAddress)
	if len(meta) > limit {
		return meta[:limit]
	}
	return meta
}

func (d *metaDelegate) NotifyMsg([]byte)                           {}
func (d *metaDelegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *metaDelegate) LocalState(join bool) []byte                { return nil }
func (d *metaDelegate) MergeRemoteState(buf []byte, join bool)     {}

// --- Helpers ---

func ParseHostPort(addr string) (string, int) {
	host := "0.0.0.0"
	port := 7946

	if addr == "" {
		return host, port
	}

	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host = addr[:i]
			if p, err := strconv.Atoi(addr[i+1:]); err == nil {
				port = p
			}
			break
		}
	}

	if host == "" {
		host = "0.0.0.0"
	}
	return host, port
}

func buildDiscoveryTargets(cfg config.ClusterConfig, hosts []string) []string {
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

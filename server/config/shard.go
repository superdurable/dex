package config

import (
	"fmt"
	"time"
)

type ShardConfig struct {
	// MaxShards is the maximum number of logical shards the cluster manages.
	// Each shard is the unit of ownership -- exactly one member owns a shard and
	// processes its tasks at any time. More shards = finer-grained load balancing
	// across members, but more lease renewal overhead and more DB rows to poll.
	//
	// CAN BE INCREASED but NEVER DECREASED. When increased, new shard IDs become
	// available and the hash ring distributes them to members on the next rebalance.
	// Decreasing would orphan existing runs mapped to the removed shard IDs
	// (via namespace's numShards) with no owner to process them.
	//
	// Note: this is independent of per-namespace numShards (which is IMMUTABLE).
	// MaxShards >= max(namespace.numShards) for all namespaces must always hold.
	// A good starting point is 10x the expected max number of members.
	MaxShards int `yaml:"maxShards"`
	// LeaseDuration is the TTL of a shard lease. If the owner fails to renew
	// before expiry, other members can steal the shard. Default: 30s.
	LeaseDuration time.Duration `yaml:"leaseDuration"`
	// LeaseRenewInterval is how often the owner renews each shard lease.
	// Task offsets (watermarks) are committed atomically with each renewal.
	// Must be significantly less than LeaseDuration to avoid accidental expiry.
	// Default: 10s.
	LeaseRenewInterval time.Duration `yaml:"leaseRenewInterval"`
	// LeaseRenewJitter adds randomness to the renewal interval to prevent
	// all shards from renewing at the same instant. Default: 2s.
	LeaseRenewJitter time.Duration `yaml:"leaseRenewJitter"`
	// LeaseExpiryBuffer is subtracted from leaseExpiresAt when computing
	// capped context deadlines (GetCappedContext):
	//   deadline = leaseExpiresAt - LeaseExpiryBuffer
	//
	// Why not use leaseExpiresAt directly as the deadline?
	// Context cancellation only prevents Go code from STARTING new operations.
	// An already in-flight MongoDB write (network packet sent, server still
	// executing) is NOT cancelled by context — it may complete AFTER the lease
	// expires. If a new owner has already claimed the shard by then, the late
	// write from the old owner corrupts state.
	//
	// The buffer ensures all operations finish (or their network timeouts fire)
	// before the lease actually expires, leaving a safety gap for the new owner.
	//
	// AttemptTimeout (TaskProcessorConfig) should be <= LeaseExpiryBuffer so
	// that a single task attempt cannot outlive the safety window.
	// Default: 5s.
	LeaseExpiryBuffer time.Duration `yaml:"leaseExpiryBuffer"`
	// ShutdownGracefulPeriod is how long the shard manager waits between
	// stopping per-shard components and releasing the lease in the DB.
	//
	// Shutdown sequence:
	//   1. close(shutdownCh)           — signals readers/deleters to stop
	//   2. factory.StopComponents()    — deleters drain DoneCh + DeleteByIDBatch
	//   3. time.Sleep(GracefulPeriod)  — lets in-flight worker pool tasks finish
	//   4. ReleaseShard in DB          — allows other members to claim
	//
	// During step 3, the worker pool (instance-level, outlives the shard) may
	// still be processing tasks from this shard. Those tasks have capped
	// contexts (deadline = leaseExpiresAt - LeaseExpiryBuffer), so they will
	// fail-fast if GracefulPeriod + LeaseExpiryBuffer < LeaseDuration.
	//
	// Too short: in-flight tasks may still be running when the new owner
	// claims the shard, causing duplicate processing.
	// Too long: delays rebalancing; other members wait longer to pick up
	// the released shard.
	//
	// Relationship to other configs:
	//   GracefulPeriod + LeaseExpiryBuffer < LeaseDuration
	//   (5s + 5s = 10s < 30s ✓ with defaults)
	//
	// Default: 5s.
	ShutdownGracefulPeriod time.Duration `yaml:"shutdownGracefulPeriod"`
	// DefaultShardsForNewNamespaces is the numShards used for any namespace
	// not explicitly listed in NamespaceShardsRegistry. Most namespaces share
	// this value. Default: 2.
	DefaultShardsForNewNamespaces int `yaml:"defaultShardsForNewNamespaces"`
	// NamespaceShardsRegistry overrides numShards for specific namespaces.
	//
	// WARNING: Only add a namespace here if it has NEVER been used before.
	// Existing runs were sharded using DefaultShardsForNewNamespaces; changing
	// the effective numShards would remap those runs to different shards,
	// breaking ownership invariants.
	NamespaceShardsRegistry map[string]int `yaml:"namespaceShardsRegistry"`
	// Cluster holds membership/gossip settings. The deployment is always a
	// cluster; a single-node deployment is simply a 1-member cluster.
	Cluster ClusterConfig `yaml:"cluster"`
}

type ClusterConfig struct {
	// BindAddress is the host:port for the memberlist gossip listener (e.g., "0.0.0.0:7946").
	BindAddress string `yaml:"bindAddress"`
	// AdvertiseAddress is the routable gossip address other members use to reach this node.
	// If empty, BindAddress is used. In containerized environments this should be set
	// to the pod/host IP (e.g., "10.0.1.5:7946").
	AdvertiseAddress string `yaml:"advertiseAddress"`
	// AdvertiseGRPCAddress is the routable gRPC address other server instances use
	// for request forwarding. Broadcast as memberlist node metadata.
	// If empty, defaults to hostname:port derived from the server's GRPCListenAddress.
	// Example: "10.0.1.5:7233"
	AdvertiseGRPCAddress string `yaml:"advertiseGrpcAddress"`
	// StaticAddresses are seed nodes to join on startup. At least one existing member
	// must be reachable for a new node to join the cluster. Can be empty for the first node.
	StaticAddresses []string `yaml:"staticAddresses"`
	// NumberOfVNodes controls the consistent hash ring resolution.
	//
	// The ring is populated with virtual nodes (vnodes) belonging to cluster members.
	// Each member gets NumberOfVNodes copies of itself placed at different positions
	// around the ring. To determine which member owns a shard, the shard ID is hashed
	// onto the ring and the nearest member vnode is found.
	//
	// Example with 3 members and NumberOfVNodes=4 (simplified):
	//   Ring: [A-v0, B-v0, A-v1, C-v0, B-v1, A-v2, C-v1, B-v2, A-v3, C-v2, B-v3, C-v3]
	//   Shard 7 hashes to position between B-v1 and A-v2 -> owned by A
	//
	// Without vnodes (just 3 physical points), one member could end up owning 60%+
	// of shards. With 128 vnodes per member, distribution is nearly uniform.
	// Higher = more even distribution but slightly slower ring lookups.
	// 128 is a good default for clusters up to ~50 members.
	//
	// IMMUTABLE AFTER CLUSTER CREATION. Changing this value repositions all vnodes,
	// reshuffling every shard-to-member mapping simultaneously. If members update
	// at different times, they see inconsistent rings = split-brain risk.
	NumberOfVNodes int `yaml:"numberOfVNodes"`
	// MinMembersBeforeReady delays shard claiming until at least N members are in the
	// cluster. Prevents a single early-starting node from claiming all shards only to
	// release most of them when other nodes join seconds later. Set to 1 to disable.
	MinMembersBeforeReady int `yaml:"minMembersBeforeReady"`
	// ClaimRetryInterval is how often to retry claiming a shard whose previous owner's
	// lease hasn't expired yet. The new owner polls at this interval waiting for either
	// the lease to expire or ReleasedAt to be set (graceful handoff).
	ClaimRetryInterval time.Duration `yaml:"claimRetryInterval"`
	// ClaimRetryIntervalJitter is random jitter added to ClaimRetryInterval to prevent
	// all pending shard claims from hitting the DB at the same instant after a node crash.
	ClaimRetryIntervalJitter time.Duration `yaml:"claimRetryIntervalJitter"`

	// Discovery controls how this node finds peer seed addresses.
	Discovery DiscoveryConfig `yaml:"discovery"`

	// OwnershipOpsMaxAttempts is how many times each shard / tasklist
	// ownership Claim* / Renew* store call may be tried (including the
	// first attempt) on transient failures (internal/timeout/unavailable).
	// CAS / range_id mismatch is never retried.
	// Default: 3 (1 initial + 2 retries with backoff between attempts).
	OwnershipOpsMaxAttempts int `yaml:"ownershipOpsMaxAttempts"`
}

// DiscoveryConfig controls how a memberlist cluster discovers seed nodes.
type DiscoveryConfig struct {
	// Mode selects the discovery mechanism. Supported values:
	// - "" / "static": use only StaticAddresses
	// - "dns": resolve ServiceDNS periodically and join the returned endpoints
	Mode string `yaml:"mode"`

	// ServiceDNS is the headless service DNS name to resolve when Mode == "dns".
	ServiceDNS string `yaml:"serviceDns"`

	// Port overrides the gossip port used for resolved DNS targets. If zero,
	// BindAddress's port is used.
	Port int `yaml:"port"`

	// RefreshInterval controls how often DNS discovery is refreshed.
	RefreshInterval time.Duration `yaml:"refreshInterval"`
}

func DefaultShardConfig() ShardConfig {
	return ShardConfig{
		MaxShards:                     128,
		LeaseDuration:                 60 * time.Second,
		LeaseRenewInterval:            15 * time.Second,
		LeaseRenewJitter:              2 * time.Second,
		LeaseExpiryBuffer:             5 * time.Second,
		ShutdownGracefulPeriod:        10 * time.Second,
		DefaultShardsForNewNamespaces: 128,
		Cluster:                       DefaultClusterConfig(),
	}
}

// Validate checks shard configuration constraints.
func (c ShardConfig) Validate() error {
	if err := c.Cluster.Validate(); err != nil {
		return err
	}
	// GracefulPeriod + LeaseExpiryBuffer must be less than LeaseDuration.
	// Otherwise in-flight operations from the old owner may still be running
	// when the lease expires and a new owner claims the shard.
	overhead := c.ShutdownGracefulPeriod + c.LeaseExpiryBuffer
	if overhead >= c.LeaseDuration {
		return fmt.Errorf("shard: ShutdownGracefulPeriod (%v) + LeaseExpiryBuffer (%v) = %v, must be < LeaseDuration (%v)",
			c.ShutdownGracefulPeriod, c.LeaseExpiryBuffer, overhead, c.LeaseDuration)
	}
	// LeaseRenewInterval + LeaseRenewJitter must be less than LeaseDuration - LeaseExpiryBuffer.
	// Otherwise the renewal might not happen before the capped context deadline.
	renewMax := c.LeaseRenewInterval + c.LeaseRenewJitter
	deadline := c.LeaseDuration - c.LeaseExpiryBuffer
	if renewMax >= deadline {
		return fmt.Errorf("shard: LeaseRenewInterval (%v) + LeaseRenewJitter (%v) = %v, must be < LeaseDuration - LeaseExpiryBuffer (%v)",
			c.LeaseRenewInterval, c.LeaseRenewJitter, renewMax, deadline)
	}
	return nil
}

// Validate checks cluster configuration constraints shared by shard and tasklist ownership.
func (c ClusterConfig) Validate() error {
	if c.OwnershipOpsMaxAttempts < 1 {
		return fmt.Errorf("cluster: OwnershipOpsMaxAttempts (%d) must be >= 1", c.OwnershipOpsMaxAttempts)
	}
	// AdvertiseGRPCAddress is the dial target for both peer forwarding and
	// this node's local cross-service loopback clients, so it must be set.
	if c.AdvertiseGRPCAddress == "" {
		return fmt.Errorf("cluster: AdvertiseGRPCAddress must be set")
	}
	return nil
}

func DefaultClusterConfig() ClusterConfig {
	return ClusterConfig{
		BindAddress:              "0.0.0.0:7946",
		NumberOfVNodes:           128,
		MinMembersBeforeReady:    2,
		ClaimRetryInterval:       10 * time.Second,
		ClaimRetryIntervalJitter: 2 * time.Second,
		OwnershipOpsMaxAttempts:  3,
		Discovery:                DefaultDiscoveryConfig(),
	}
}

func DefaultDiscoveryConfig() DiscoveryConfig {
	return DiscoveryConfig{
		Mode:            "static",
		RefreshInterval: 30 * time.Second,
	}
}

package tasklist

import (
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/superdurable/dex/server/config"
)

// LoadBalancer is server-side only. It picks tasklist partition IDs for:
//   - Write path (DispatchRun): taskprocessor calls PickWritePartition
//     before sending DispatchRun to matching. The picked partition is
//     encoded into the DispatchRunRequest.task_list_name and matching
//     routes to the owner of that specific partition (root or sub).
//   - Read path (PollForRun): MatchingServiceHandler.PollForRun receives
//     the user-specified base name, calls PickReadPartition to choose a
//     partition to serve. If this node owns it, serve directly; otherwise
//     forward to the partition's owner.
//
// Workers SDK never sees partition IDs — they always pass the base name
// (the user-specified tasklist name) in PollForRun / DispatchRun.
//
// Strategy:
//   - Round-robin per (namespace, baseName): a single global atomic
//     counter per tasklist. Even distribution across N partitions.
//   - NumWritePartitions and NumReadPartitions are configured separately
//     (via TasklistConfig). Typically equal for symmetric load, but
//     splitting is useful for migrations (e.g. expand writes first to
//     spread load, then expand reads after backlog drains).
//
// Usage:
//   - The picker is stateless across calls — no warmup or refresh needed.
//   - Per-namespace overrides come from cfg.PerTasklistOverrides; if no
//     override, falls back to the cluster-wide defaults.
type LoadBalancer struct {
	cfg config.TasklistConfig

	mu       sync.RWMutex
	counters map[string]*atomic.Uint64 // key: namespace/baseName/role (write|read)
}

// NewLoadBalancer constructs a load balancer for the given partition
// config. The returned instance is safe for concurrent use.
func NewLoadBalancer(cfg config.TasklistConfig) *LoadBalancer {
	return &LoadBalancer{
		cfg:      cfg,
		counters: make(map[string]*atomic.Uint64),
	}
}

// ResolveReadIdentifier turns a wire task_list_name into a fully-qualified
// Identifier suitable for the read path (PollForRun). If the wire name
// is already partition-encoded (i.e., comes from another matching node
// that already picked), it's parsed and returned as-is. Otherwise it's
// treated as a bare user base name and a read partition is picked.
//
// This is the single entry point for "I have a wire task_list_name from
// the network, give me an Identifier I can route on" on the read side —
// the alternative (callers manually parse + pick + reconstruct) would
// duplicate the IsEncoded() conditional everywhere and risk re-picking
// when partition 0 was already chosen.
func (lb *LoadBalancer) ResolveReadIdentifier(namespace, wireName string) (*Identifier, error) {
	return lb.resolveIdentifier(namespace, wireName, lb.PickReadPartition)
}

// ResolveWriteIdentifier is the write-path counterpart to
// ResolveReadIdentifier. Used by DispatchRun handlers.
func (lb *LoadBalancer) ResolveWriteIdentifier(namespace, wireName string) (*Identifier, error) {
	return lb.resolveIdentifier(namespace, wireName, lb.PickWritePartition)
}

// resolveIdentifier is the shared implementation behind ResolveRead /
// ResolveWriteIdentifier. The picker parameter is a method value bound
// to lb (PickReadPartition or PickWritePartition); it is NOT exposed
// in the public API to avoid callers passing arbitrary functions.
//
// The decision to re-pick or not is gated on Identifier.IsEncoded(),
// NOT IsRoot(): the load balancer can legitimately pick partition 0,
// and the wire-name encoding ("/__dex_sys/<base>/0") is what
// reliably distinguishes "already picked" from a bare user base name.
// If we used IsRoot() (a check on partition number) here, ~25% of
// forwards (whenever partition 0 was picked) would hit the receiver
// with an unencoded wire name and the receiver would re-pick, losing
// the original LB decision.
func (lb *LoadBalancer) resolveIdentifier(
	namespace, wireName string,
	pick func(ns, base string) int32,
) (*Identifier, error) {
	id, err := ParseTasklistName(namespace, wireName)
	if err != nil {
		return nil, err
	}
	if id.IsEncoded() {
		// Wire name already encodes a partition (e.g. forwarded between
		// matching nodes): respect it; do not re-pick.
		return id, nil
	}
	partition := pick(namespace, id.BaseName())
	return NewIdentifier(namespace, id.BaseName(), partition)
}

// PickWritePartition returns a partition ID in [0, NumWritePartitions)
// for the given (namespace, baseName). Used by taskprocessor when
// dispatching a run via DispatchRun.
//
// Round-robin: each call increments the per-(ns, base, write) counter
// and returns counter % NumWritePartitions. Threadsafe via atomic.
func (lb *LoadBalancer) PickWritePartition(namespace, baseName string) int32 {
	n := lb.numWrite(namespace, baseName)
	if n <= 1 {
		return 0
	}
	counter := lb.getCounter(namespace, baseName, "write")
	v := counter.Add(1)
	return int32(v % uint64(n))
}

// PickReadPartition returns a partition ID in [0, NumReadPartitions)
// for the given (namespace, baseName). Used by MatchingServiceHandler
// when receiving PollForRun.
//
// Round-robin: each call increments the per-(ns, base, read) counter
// and returns counter % NumReadPartitions.
func (lb *LoadBalancer) PickReadPartition(namespace, baseName string) int32 {
	n := lb.numRead(namespace, baseName)
	if n <= 1 {
		return 0
	}
	counter := lb.getCounter(namespace, baseName, "read")
	v := counter.Add(1)
	return int32(v % uint64(n))
}

// PickReadPartitionForKey returns a deterministic partition ID for the
// given key (e.g. WorkerID). Useful when stickiness is desired —
// repeated polls from the same worker tend to land on the same
// partition. Only used by special paths (currently none); the default
// PollForRun handler uses PickReadPartition.
func (lb *LoadBalancer) PickReadPartitionForKey(namespace, baseName, key string) int32 {
	n := lb.numRead(namespace, baseName)
	if n <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int32(h.Sum32() % uint32(n))
}

// NumWritePartitions returns the configured write partition count for
// the given tasklist, applying per-tasklist overrides if any.
func (lb *LoadBalancer) NumWritePartitions(namespace, baseName string) int32 {
	return lb.numWrite(namespace, baseName)
}

// NumReadPartitions returns the configured read partition count for the
// given tasklist, applying per-tasklist overrides if any.
func (lb *LoadBalancer) NumReadPartitions(namespace, baseName string) int32 {
	return lb.numRead(namespace, baseName)
}

func (lb *LoadBalancer) numWrite(namespace, baseName string) int32 {
	if override, ok := lb.lookupOverride(namespace, baseName); ok && override.NumWritePartitions > 0 {
		return int32(override.NumWritePartitions)
	}
	if lb.cfg.NumWritePartitions > 0 {
		return int32(lb.cfg.NumWritePartitions)
	}
	return 1
}

func (lb *LoadBalancer) numRead(namespace, baseName string) int32 {
	if override, ok := lb.lookupOverride(namespace, baseName); ok && override.NumReadPartitions > 0 {
		return int32(override.NumReadPartitions)
	}
	if lb.cfg.NumReadPartitions > 0 {
		return int32(lb.cfg.NumReadPartitions)
	}
	return 1
}

func (lb *LoadBalancer) lookupOverride(namespace, baseName string) (config.TasklistPartitionOverride, bool) {
	nsMap, ok := lb.cfg.PerTasklistOverrides[namespace]
	if !ok {
		return config.TasklistPartitionOverride{}, false
	}
	override, ok := nsMap[baseName]
	return override, ok
}

func (lb *LoadBalancer) getCounter(namespace, baseName, role string) *atomic.Uint64 {
	key := namespace + "/" + baseName + "/" + role
	lb.mu.RLock()
	c, ok := lb.counters[key]
	lb.mu.RUnlock()
	if ok {
		return c
	}
	lb.mu.Lock()
	c, ok = lb.counters[key]
	if !ok {
		c = new(atomic.Uint64)
		lb.counters[key] = c
	}
	lb.mu.Unlock()
	return c
}

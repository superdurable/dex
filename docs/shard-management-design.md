# Shard Management Design

This document describes how dex partitions work across server instances
using logical shards, including shard mapping, lease-based ownership, graceful
shutdown, context timeout management, and cluster membership.

## 1. Shard Mapping

### Per-Namespace numShards

Each namespace has an **immutable** `numShards` value configured at creation
time. A run's shard is determined by:

```
shardID = crc32(runID) % numShards
```

This is a pure function — given the same `runID` and `numShards`, the result
is always the same. No external lookup is needed.

`numShards` is immutable because changing it would remap existing runs to
different shards, breaking ownership invariants.

### Cluster MaxShards

The cluster-wide `MaxShards` (in `ShardConfig`) is the total number of logical
shards the cluster manages. Each shard is the unit of ownership — exactly one
server instance owns a shard at any time and processes its tasks.

**MaxShards can be increased but never decreased.** Increasing it makes new
shard IDs available; the hash ring distributes them to members on the next
rebalance. Decreasing would orphan existing runs mapped to the removed shard
IDs with no owner to process them.

The invariant `MaxShards >= max(namespace.numShards)` must always hold for all
registered namespaces.

More shards = finer-grained load balancing across members, but more lease
renewal overhead and more DB polling. A good starting point is 10x the
expected max number of members.

### Registration

Namespace shard counts are configured via `ShardConfig`:

- `DefaultShardsForNewNamespaces` — the `numShards` used for any namespace not
  explicitly listed. This is the common case; most namespaces share the same
  shard count.
- `NamespaceShardsRegistry` — a `map[string]int` that overrides `numShards` for
  specific namespaces.

The `ShardMapper` is constructed from this config at startup and is a read-only
lookup table — it does not manage ownership, only computes the mapping.

> **WARNING**: When adding a namespace to `NamespaceShardsRegistry`, you must
> ensure that namespace has **never been used before**. If runs already exist
> under that namespace, they were sharded using `DefaultShardsForNewNamespaces`.
> Changing the effective `numShards` would remap those existing runs to different
> shards, breaking ownership invariants.

## 2. Shard Claim and Leasing

### Lease-Based Mutual Exclusion

Shard ownership is managed through leases persisted in the `shards` MongoDB
collection. At any time, each shard has exactly one owner (or is unclaimed).
The owner must periodically renew its lease to maintain ownership.

### Shard Row Schema

```
Shard {
    ShardID        int32      // primary key
    Version        int64      // CAS version for all updates
    MemberID       string     // current owner
    ClaimedAt      time.Time
    LeaseExpiresAt time.Time
    ReleasedAt     *time.Time // set on graceful release, nil while active
    Metadata       ShardMetadata {
        ImmediateTaskDeleteOffset
        TimerTaskDeleteOffsetSortKey
        TimerTaskDeleteOffsetID
    }
}
```

### ShardStore Interface

```go
type ShardStore interface {
    ClaimShard(ctx, shardID, memberID, leaseDuration) (*Shard, CategorizedError)
    RenewShardLease(ctx, shardID, memberID, expectedVersion, leaseDuration) (leaseExpiresAt, CategorizedError)
    ReleaseShard(ctx, shardID, memberID, expectedVersion) CategorizedError
}
```

All operations use `Version` for CAS to prevent split-brain scenarios.

### Claim Protocol

When a member wants to claim a shard:

1. **No existing row**: insert a new row with `Version=1`, the member as owner,
   and `LeaseExpiresAt = now + LeaseDuration`. On duplicate key (race with
   another member), retry by reading the existing row.

2. **Existing row, same member**: re-claim directly (the member is resuming
   after a restart). CAS update with version increment.

3. **Existing row, different member, released**: the previous owner set
   `ReleasedAt` (graceful handoff). CAS update to take ownership.

4. **Existing row, different member, lease expired**: the previous owner
   crashed or is unresponsive. `LeaseExpiresAt < now` means the lease has
   lapsed. CAS update to take ownership.

5. **Existing row, different member, lease still active**: return
   `LeaseNotExpiredError`. The new owner must retry after `ClaimRetryInterval`
   (with jitter) until the lease expires or the previous owner releases.

### Lease Renewal

A background goroutine per owned shard calls `RenewShardLease` at
`LeaseRenewInterval + random(LeaseRenewJitter)`:

```
RenewShardLease(shardID, memberID, expectedVersion, LeaseDuration)
  -> returns new leaseExpiresAt
```

The `expectedVersion` check ensures only the legitimate owner can renew.
Each `ClaimShard` / `RenewShardLease` call is retried with bounded exponential
backoff on transient store errors (`cluster.ownershipOpsMaxAttempts`, default 3).
CAS/version mismatch or conflict (e.g. lease held by another member) is not
retried. If renewal still fails after retries, the shard manager signals the
shard as lost and stops all components for it.

The renewed `leaseExpiresAt` is stored in an atomic value so that
`GetCappedContext` reads it lock-free on every operation.

### Timing Configuration

| Parameter | Default | Purpose |
|---|---|---|
| `LeaseDuration` | 30s | How long a lease is valid |
| `LeaseRenewInterval` | 10s | How often to renew (< LeaseDuration) |
| `LeaseRenewJitter` | 2s | Random jitter on renewal to avoid thundering herd |
| `LeaseExpiryBuffer` | 5s | Safety margin subtracted from lease for context deadline |
| `ShutdownGracefulPeriod` | 5s | Time to wait for in-flight work after signaling shutdown |

The renewal cycle ensures `LeaseDuration - LeaseRenewInterval - LeaseRenewJitter > LeaseExpiryBuffer`,
so operations always have time to complete before the lease actually expires.

## 3. Graceful Shutdown and Context Timeout Management

### Graceful Shutdown Flow

When a shard is released (either due to rebalance or server shutdown):

1. **Signal shutdown**: close the `ShardHandle.shutdownCh` channel. All
   per-shard components (task processors, batch readers/deleters) monitor this
   channel and begin draining.

2. **Stop lease renewal**: cancel the per-shard renewal goroutine so no
   further renewals are attempted.

3. **Stop components**: call `ComponentFactory.StopComponents(shardID)` to
   stop task processors, matching engine components, etc.

4. **Wait graceful period**: sleep for `ShutdownGracefulPeriod` to allow
   in-flight operations to complete. These operations have capped contexts
   that will expire before the lease does.

5. **Release in DB**: set `ReleasedAt` on the shard row. This signals to
   the new owner that the previous owner has cleanly released, enabling
   immediate claim without waiting for lease expiry.

6. **Remove from owned map**: delete the shard from the in-memory
   `ownedShards` map.

### Context Timeout Management (GetCappedContext)

Every server-side operation on a shard's data (RunEngine calls, store
operations) must use a context derived from `GetCappedContext`:

```go
func GetCappedContext(parentCtx, shardID) (context.Context, context.CancelFunc) {
    leaseExp := state.leaseExpiresAt.Load().(time.Time)
    deadline := leaseExp - LeaseExpiryBuffer
    return context.WithDeadline(parentCtx, deadline)
}
```

This ensures:

- **No stale writes**: if the lease is about to expire, operations fail fast
  with `DeadlineExceeded` rather than completing after another member has
  claimed the shard.
- **Safety margin**: the `LeaseExpiryBuffer` (5s default) gives enough time
  for the operation to fail, be logged, and for the shard to be released
  before the lease actually expires.
- **Lock-free**: `leaseExpiresAt` is stored in an `atomic.Value`, so reading
  it on every operation has no contention.

If the shard is not owned (e.g., was just released), `GetCappedContext` returns
an already-cancelled context, causing any operation to fail immediately.

### Per-Shard Component Lifecycle

When a shard is claimed, the `ComponentFactory` creates per-shard components:

- **BatchReader** (ITP/TTP): polls tasks from the runs collection
- **BatchDeleter**: cleans up processed tasks
- **WorkerPool tasks**: processes tasks via RunEngine

Each component receives a `ShardHandle` with a `shutdownCh`. When the channel
is closed, components drain their in-progress work and exit.

## 4. Membership Implementation (memberlist)

### Overview

dex is always a cluster (a single-node deployment is just a 1-member cluster).
It uses [hashicorp/memberlist](https://github.com/hashicorp/memberlist) for peer
discovery via gossip protocol, and [serialx/hashring](https://github.com/serialx/hashring)
for consistent hashing to map shards to members. With one member, the hash ring
trivially maps every shard to that member.

### Gossip Configuration

```go
memberlist.DefaultLANConfig() with:
    Name           = memberID (unique per instance)
    BindAddr/Port  = ClusterConfig.BindAddress (e.g., "0.0.0.0:7946")
    AdvertiseAddr  = ClusterConfig.AdvertiseAddress (for containerized envs)
```

On startup, the node first attempts to join seed nodes listed in
`ClusterConfig.StaticAddresses`.

For Kubernetes deployments, `ClusterConfig.Discovery` can additionally enable
DNS-based peer discovery from a headless Service:

```yaml
discovery:
  mode: dns
  serviceDns: dex-headless.default.svc.cluster.local
  port: 7946
  refreshInterval: 10s
```

In that mode, the node periodically resolves the headless Service, converts the
returned pod IPs into `host:port` gossip targets, and calls `Join(...)` again.
This keeps startup and scale-out simple without replacing memberlist itself.

If initial joining fails (e.g., first node in cluster), the node continues and
waits for other members to join via gossip.

### Kubernetes Identity and Addresses

In Kubernetes, the bind address is typically `0.0.0.0:<port>` while the
advertised gossip and gRPC addresses use the pod IP:

```yaml
advertiseAddress: ${POD_IP}:7946
advertiseGrpcAddress: ${POD_IP}:7233
memberID: ${POD_NAME}
```

The server config loader expands `${...}` placeholders from the environment
after reading the YAML file, so Helm can mount a static config file while still
injecting pod-specific addresses at runtime.

The production Helm chart defaults to `StatefulSet` so pods get stable names
and predictable DNS. Even though DNS-based discovery works with ordinary
Deployments too, the stable identity from `StatefulSet` reduces churn in the
hash ring and lease ownership during rollouts and restarts.

### Node Metadata

Each node broadcasts its internal gRPC address as memberlist metadata via
`metaDelegate.NodeMeta()`. When a node joins, other members store this address
for potential internal routing.

### MinMembersBeforeReady

`MinMembersBeforeReady` delays shard claiming until at least N members are in
the cluster. This prevents a single early-starting node from claiming all
shards only to release most of them when other nodes join seconds later.

The node polls `memberlist.NumMembers()` every second until the threshold is
reached.

### Consistent Hash Ring

The hash ring maps shard IDs to members:

```
ring = hashring.NewWithWeights({memberID: NumberOfVNodes})
owner = ring.GetNode(strconv.Itoa(shardID))
```

`NumberOfVNodes` controls the ring resolution. Each member gets N virtual nodes
placed at different positions around the ring. To determine the owner of a
shard, the shard ID is hashed and the nearest member vnode is found.

Without vnodes (just one physical point per member), one member could end up
owning 60%+ of shards. With 128 vnodes per member, distribution is nearly
uniform. 128 is a good default for clusters up to ~50 members.

**NumberOfVNodes is immutable after cluster creation.** Changing it repositions
all vnodes, reshuffling every shard-to-member mapping simultaneously. If
members update at different times, they would see inconsistent rings, causing
split-brain.

### Rebalance on Membership Change

When a node joins or leaves (detected via `eventDelegate`):

1. Update the hash ring: `AddWeightedNode` on join, `RemoveNode` on leave.
2. Store the joining node's internal gRPC address from its metadata.
3. Call `onRebalance()` which triggers `ShardManager.rebalanceShards()`.

Rebalance computes which shards should now be owned by this member:

```go
func rebalanceShards() {
    desiredShards = membership.GetShardsForMember(memberID, MaxShards)

    // Release shards no longer assigned to us
    for shardID in ownedShards {
        if shardID not in desiredShards: releaseShardLocked(shardID)
    }

    // Claim shards newly assigned to us
    for shardID in desiredShards {
        if shardID not in ownedShards: claimShardLocked(shardID)
    }
}
```

Claim may fail with `LeaseNotExpiredError` if the previous owner hasn't
released yet. In this case, the shard manager retries at
`ClaimRetryInterval + random(ClaimRetryIntervalJitter)` until the lease
expires or `ReleasedAt` is set.

### Graceful Handoff

When a node leaves the cluster gracefully:

1. `memberlist.Leave()` notifies all peers (5s timeout).
2. Other nodes receive `NotifyLeave`, remove the node from the hash ring,
   and trigger rebalance.
3. The leaving node calls `ShardManager.Stop()`, which releases all owned
   shards (sets `ReleasedAt`).
4. New owners see `ReleasedAt` and can claim immediately without waiting
   for lease expiry.

When a node crashes (ungraceful):

1. Other nodes detect the failure via memberlist's failure detector.
2. `NotifyLeave` fires, triggering rebalance.
3. New owners must wait for the crashed node's lease to expire (up to
   `LeaseDuration`) before claiming.

### Full Server Shutdown Sequence

```
ShardManager.Stop()
  |
  +-- for each owned shard:
  |     close(shutdownCh)        // signal components
  |     cancel renewCancel       // stop lease renewal
  |     factory.StopComponents() // stop task processors
  |     sleep(ShutdownGracefulPeriod)
  |     store.ReleaseShard()     // set ReleasedAt in DB
  |     delete from ownedShards
  |
  +-- membership.stop()
  |     memberlist.Leave(5s)     // notify peers
  |     memberlist.Shutdown()
  |
  +-- cancel(ctx)               // cancel root context
  +-- wg.Wait()                 // wait for all goroutines
```

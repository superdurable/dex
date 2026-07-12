package shardmanager

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/backoff"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// ShardManager manages shard ownership using lease-based mutual exclusion.
type ShardManager interface {
	Start(ctx context.Context) error
	Stop()
	GetOwnedShards() []int32
	IsLocalShard(shardID int32) bool
	SignalShardLost(shardID int32)
	// GetCappedContext returns a context derived from parentCtx with deadline
	// capped at leaseExpiresAt - LeaseExpiryBuffer. Called per-operation.
	GetCappedContext(parentCtx context.Context, shardID int32) (context.Context, context.CancelFunc)
	// GetShardOwnerAddress returns the gRPC address of the instance that owns
	// the given shard. Returns "" if the shard is local (a 1-member cluster
	// owns every shard locally). Used by RunsServiceHandler.tryForward to
	// route StartRun / StopRun / PublishToChannel to the shard owner.
	GetShardOwnerAddress(shardID int32) string
	// AcquireImmediateTaskSeqLock acquires a per-shard mutex that serializes
	// immediate task creation. The returned unlock function MUST be called
	// (typically via defer) after the DB write completes. This prevents
	// out-of-order MongoDB commits from causing the batch reader to skip tasks.
	AcquireImmediateTaskSeqLock(shardID int32) (func(), errors.CategorizedError)
	// GetNextImmediateTaskSeq allocates the next monotonically increasing
	// TaskSeq for an immediate task on the given shard. TaskSeq = (RangeID << 32) | LocalSeq.
	// Panics if LocalSeq overflows int32 max (restart needed).
	GetNextImmediateTaskSeq(shardID int32) (int64, error)
	// AcquireOpsFIFOTaskSeqLock is the per-shard OpsFIFO counterpart of
	// AcquireImmediateTaskSeqLock. The two queues use INDEPENDENT mutexes so
	// the OpsFIFO outbox writer (called from every UpdateRunWithNewTasks
	// path) does not contend with the immediate-task seq path. Same correctness
	// requirement: the lock MUST span allocation AND the underlying DB write.
	AcquireOpsFIFOTaskSeqLock(shardID int32) (func(), errors.CategorizedError)
	// GetNextOpsFIFOTaskSeq allocates the next monotonically increasing
	// OpsFIFO TaskSeq on the given shard. Same RangeID<<32|LocalSeq encoding
	// as immediate but uses an INDEPENDENT LocalSeq counter so the two
	// outboxes never collide.
	GetNextOpsFIFOTaskSeq(shardID int32) (int64, error)
	// GetShardVersion returns the current version of the shard.
	GetShardVersion(shardID int32) int64
	// AwaitShardReady blocks until the shard has finished its post-claim
	// readiness wait (clock-skew gate from the previous owner's committed
	// timer watermark). Returns nil immediately if the shard is already
	// ready, or a retriable error if ctx fires first / the shard is not
	// owned. Called by ShardedRunStore on every write so engine code does
	// not have to think about it.
	AwaitShardReady(ctx context.Context, shardID int32) errors.CategorizedError
	// SetMetadataCallback sets the callback used during lease renewal to collect
	// watermark offsets from the batch deleters. Must be called before Start().
	SetMetadataCallback(cb MetadataCallback)
}

// MetadataCallback is called during lease renewal to collect current watermarks
// from the batch deleters. The shard manager persists the returned metadata
// alongside the lease renewal to save a separate DB round-trip.
type MetadataCallback func(shardID int32) *p.ShardMetadata

// ShardHandle is the per-shard handle given to components.
type ShardHandle struct {
	ShardID    int32
	shutdownCh chan struct{}
	metadata   p.ShardMetadata
}

func (h *ShardHandle) ShutdownCh() <-chan struct{} { return h.shutdownCh }

// Metadata returns the shard metadata at the time of claiming, containing
// committed offsets for batch readers to resume from.
func (h *ShardHandle) Metadata() p.ShardMetadata { return h.metadata }

// ComponentFactory creates per-shard components when a shard is claimed.
type ComponentFactory interface {
	StartComponents(shardID int32, handle *ShardHandle, sm ShardManager) error
	StopComponents(shardID int32)
}

type shardState struct {
	version            int64
	rangeID            int32
	immediateLocalSeq  atomic.Int32 // immediate task LocalSeq: monotonically increasing per claim, starts at 0
	opsFIFOLocalSeq    atomic.Int32 // OpsFIFO task LocalSeq: independent of immediate so the two outboxes never collide
	immediateTaskSeqMu sync.Mutex   // serializes immediate seq allocation + DB write to prevent out-of-order commits
	opsFIFOTaskSeqMu   sync.Mutex   // separate mutex for OpsFIFO seq allocation + DB write (same correctness reasoning, decoupled contention)
	leaseExpiresAt     atomic.Value // stores time.Time
	handle             *ShardHandle
	renewCancel        context.CancelFunc

	// readyCh closes after the post-claim clock-skew wait has elapsed and
	// StartComponents has been invoked. AwaitShardReady blocks on this so the
	// engine cannot write tasks (and the wrapper cannot allocate seqs) before
	// the shard is "active". On a no-skew claim, readyCh is closed before the
	// shardState is published into ownedShards.
	readyCh chan struct{}
	// readyAt is the wall-clock time at which the shard becomes ready.
	// time.Time{} (zero) means "ready immediately on claim". Used for
	// observability and the deferred-start goroutine's wait deadline.
	readyAt time.Time
}

// rebalanceDebounceInterval controls how long to wait after a membership event
// before executing a rebalance. This coalesces rapid leave/join events during
// rolling restarts into a single rebalance, reducing shard churn.
const rebalanceDebounceInterval = 5 * time.Second

type shardManagerImpl struct {
	cfg        config.ShardConfig
	store      p.ShardStore
	logger     log.Logger
	memberID   string
	factory    ComponentFactory
	membership *membership

	mu               sync.RWMutex
	ownedShards      map[int32]*shardState
	ready            atomic.Bool      // true after Start() completes; rebalance is suppressed until then
	metadataCallback MetadataCallback // set by task processor to provide watermarks on lease renewal

	rebalanceCh chan struct{} // buffered(1); membership events send here for debounced rebalance
	rebalanceMu sync.Mutex    // serializes rebalance operations so phases don't interleave

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewShardManager(
	cfg config.ShardConfig,
	store p.ShardStore,
	logger log.Logger,
	memberID string,
	factory ComponentFactory,
	internalAddress string,
	onAddressRemoved func(addr string),
) ShardManager {
	sm := &shardManagerImpl{
		cfg:         cfg,
		store:       store,
		logger:      logger,
		memberID:    memberID,
		factory:     factory,
		ownedShards: make(map[int32]*shardState),
		rebalanceCh: make(chan struct{}, 1),
	}
	sm.membership = newMembership(cfg.Cluster, logger, memberID, internalAddress, sm.triggerRebalance, onAddressRemoved)
	return sm
}

func (sm *shardManagerImpl) Start(ctx context.Context) error {
	sm.ctx, sm.cancel = context.WithCancel(ctx)

	sm.logger.Info("Starting shard manager",
		tag.MemberId(sm.memberID),
		tag.NumberOfShards(sm.cfg.MaxShards))

	if err := sm.membership.start(); err != nil {
		return err
	}
	sm.ready.Store(true)
	sm.wg.Add(1)
	go sm.rebalanceLoop()
	sm.rebalanceShards()
	return nil
}

func (sm *shardManagerImpl) Stop() {
	sm.logger.Info("Stopping shard manager")

	// Phase 1: Under lock — signal shutdown, cancel renewals, stop components,
	// collect release info, and clear the owned map.
	type releaseInfo struct {
		shardID int32
		version int64
	}

	sm.mu.Lock()
	var toRelease []releaseInfo
	for shardID, state := range sm.ownedShards {
		sm.logger.Info("Releasing shard", tag.Shard(shardID))
		select {
		case <-state.handle.shutdownCh:
		default:
			close(state.handle.shutdownCh)
		}
		if state.renewCancel != nil {
			state.renewCancel()
		}
		if sm.factory != nil {
			sm.factory.StopComponents(shardID)
		}
		toRelease = append(toRelease, releaseInfo{shardID: shardID, version: state.version})
	}
	sm.ownedShards = make(map[int32]*shardState)
	sm.mu.Unlock()

	// Phase 2: Wait graceful period once for all shards (no lock held).
	if len(toRelease) > 0 {
		time.Sleep(sm.cfg.ShutdownGracefulPeriod)
	}

	// Phase 3: Batch release in DB (no lock held).
	var releaseEntries []p.ShardReleaseEntry
	for _, info := range toRelease {
		releaseEntries = append(releaseEntries, p.ShardReleaseEntry{
			ShardID:         info.shardID,
			ExpectedVersion: info.version,
		})
	}
	if len(releaseEntries) > 0 {
		if err := sm.store.BatchReleaseShards(sm.ctx, sm.memberID, releaseEntries); err != nil {
			sm.logger.Warn("Batch release shards partially failed during stop", tag.Error(err))
		}
	}
	for _, info := range toRelease {
		sm.logger.Info("Shard released", tag.Shard(info.shardID))
		metrics.CounterShardReleased.Inc()
	}

	sm.membership.stop()

	sm.cancel()
	sm.wg.Wait()
	sm.logger.Info("Shard manager stopped")
}

func (sm *shardManagerImpl) GetOwnedShards() []int32 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	shards := make([]int32, 0, len(sm.ownedShards))
	for id := range sm.ownedShards {
		shards = append(shards, id)
	}
	return shards
}

func (sm *shardManagerImpl) IsLocalShard(shardID int32) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.ownedShards[shardID]
	return ok
}

func (sm *shardManagerImpl) GetCappedContext(parentCtx context.Context, shardID int32) (context.Context, context.CancelFunc) {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()

	if !ok {
		// Shard not owned: return an already-cancelled context
		ctx, cancel := context.WithCancel(parentCtx)
		cancel()
		return ctx, cancel
	}

	leaseExp := state.leaseExpiresAt.Load().(time.Time)
	deadline := leaseExp.Add(-sm.cfg.LeaseExpiryBuffer)
	return context.WithDeadline(parentCtx, deadline)
}

func (sm *shardManagerImpl) SignalShardLost(shardID int32) {
	sm.logger.Warn("Shard ownership lost signal", tag.Shard(shardID))
	metrics.CounterShardLost.Inc()

	// Phase 1: Under lock — signal shutdown, cancel renewal, stop components,
	// save version for DB release, and remove from owned map immediately.
	// This must NOT sleep while holding the lock; the old code held the write
	// lock for ShutdownGracefulPeriod (10s), which blocked ALL lease renewals
	// for other shards and caused cascading failures.
	sm.mu.Lock()
	state, ok := sm.ownedShards[shardID]
	if !ok {
		sm.mu.Unlock()
		return
	}

	sm.logger.Info("Releasing shard", tag.Shard(shardID))
	select {
	case <-state.handle.shutdownCh:
	default:
		close(state.handle.shutdownCh)
	}
	if state.renewCancel != nil {
		state.renewCancel()
	}
	if sm.factory != nil {
		sm.factory.StopComponents(shardID)
	}
	version := state.version
	delete(sm.ownedShards, shardID)
	metrics.GaugeShardOwnedCount.Record(int64(len(sm.ownedShards)))
	sm.mu.Unlock()

	// Phase 2: Wait graceful period for in-flight work (no lock held).
	// Respects context cancellation so Stop() doesn't block unnecessarily.
	select {
	case <-sm.ctx.Done():
	case <-time.After(sm.cfg.ShutdownGracefulPeriod):
	}

	// Phase 3: Best-effort release in DB (no lock held).
	if err := sm.store.ReleaseShard(sm.ctx, shardID, sm.memberID, version); err != nil {
		sm.logger.Warn("Failed to release shard in DB (may already be taken)", tag.Shard(shardID), tag.Error(err))
	}

	sm.logger.Info("Shard released", tag.Shard(shardID))
	metrics.CounterShardReleased.Inc()

	// After releasing a lost shard, trigger a debounced recompute of ownership.
	// During cluster startup two members can transiently race to claim the same
	// shard. If lease renewal later detects that we lost the race, we still need
	// to re-check the current ring assignment or the shard can remain released
	// indefinitely even when it still belongs to us.
	sm.triggerRebalance()
}

func (sm *shardManagerImpl) GetShardOwnerAddress(shardID int32) string {
	owner := sm.membership.GetMemberForShard(shardID)
	if owner == sm.memberID {
		return ""
	}
	return sm.membership.GetInternalAddress(owner)
}

// SetMetadataCallback sets the callback used during lease renewal to collect
// watermark offsets from the batch deleters. Must be called before Start().
func (sm *shardManagerImpl) SetMetadataCallback(cb MetadataCallback) {
	sm.metadataCallback = cb
}

func (sm *shardManagerImpl) AcquireImmediateTaskSeqLock(shardID int32) (func(), errors.CategorizedError) {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return nil, errors.NewInternalError("shard not owned: cannot acquire immediate task seq lock", nil)
	}
	state.immediateTaskSeqMu.Lock()
	return state.immediateTaskSeqMu.Unlock, nil
}

func (sm *shardManagerImpl) GetNextImmediateTaskSeq(shardID int32) (int64, error) {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return 0, p.NewInternalError("shard not owned: cannot allocate immediate TaskSeq", nil)
	}
	seq := state.immediateLocalSeq.Add(1)
	if seq == math.MaxInt32 {
		panic("immediate task LocalSeq overflow: restart instance to reset")
	}
	taskSeq := (int64(state.rangeID) << 32) | int64(seq)
	metrics.CounterTaskSeqAllocated.Inc()
	return taskSeq, nil
}

func (sm *shardManagerImpl) AcquireOpsFIFOTaskSeqLock(shardID int32) (func(), errors.CategorizedError) {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return nil, errors.NewInternalError("shard not owned: cannot acquire OpsFIFO task seq lock", nil)
	}
	state.opsFIFOTaskSeqMu.Lock()
	return state.opsFIFOTaskSeqMu.Unlock, nil
}

func (sm *shardManagerImpl) GetNextOpsFIFOTaskSeq(shardID int32) (int64, error) {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return 0, p.NewInternalError("shard not owned: cannot allocate OpsFIFO TaskSeq", nil)
	}
	seq := state.opsFIFOLocalSeq.Add(1)
	if seq == math.MaxInt32 {
		panic("OpsFIFO task LocalSeq overflow: restart instance to reset")
	}
	taskSeq := (int64(state.rangeID) << 32) | int64(seq)
	metrics.CounterTaskSeqAllocated.Inc()
	return taskSeq, nil
}

func (sm *shardManagerImpl) GetShardVersion(shardID int32) int64 {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return 0
	}
	return state.version
}

// AwaitShardReady is the readiness gate consumed by ShardedRunStore on every
// write. Blocks until the shard's post-claim clock-skew wait has elapsed and
// StartComponents has been invoked (state.readyCh closed). Returns a
// retriable Unavailable error if the shard is not owned (caller forwarder
// will retry to a new owner) or if ctx fires before the readyCh closes.
//
// Why this matters: when ownership transfers, the new owner's local clock may
// still be behind the previous owner's committed timer watermark T_committed.
// If we let the engine write timer tasks with fire_at = now + delta where
// now < T_committed, then fire_at can land at-or-below the deleter's
// committed offset and the timer would be silently deleted before the reader
// could see it. By blocking writes until our local time exceeds T_committed,
// every newly-written timer is guaranteed to land above the watermark.
func (sm *shardManagerImpl) AwaitShardReady(ctx context.Context, shardID int32) errors.CategorizedError {
	sm.mu.RLock()
	state, ok := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !ok {
		return errors.NewUnavailableError("shard not owned: cannot accept write", nil)
	}
	// Fast path: already ready.
	select {
	case <-state.readyCh:
		return nil
	default:
	}
	// Slow path: block until ready, ctx cancel, or shard shutdown.
	select {
	case <-state.readyCh:
		return nil
	case <-state.handle.shutdownCh:
		return errors.NewUnavailableError("shard shutting down before readiness", nil)
	case <-ctx.Done():
		return errors.NewUnavailableError("shard not ready before deadline", ctx.Err())
	}
}

// --- Internal ---

// triggerRebalance enqueues a debounced rebalance. Multiple rapid calls
// (e.g., from membership events during rolling restart) coalesce into a
// single rebalance after rebalanceDebounceInterval.
func (sm *shardManagerImpl) triggerRebalance() {
	select {
	case sm.rebalanceCh <- struct{}{}:
	default:
	}
}

// rebalanceLoop runs as a goroutine, consuming debounced rebalance triggers.
func (sm *shardManagerImpl) rebalanceLoop() {
	defer sm.wg.Done()
	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-sm.rebalanceCh:
		}

		// Debounce: wait for the interval, then drain any additional signals
		// that arrived during the wait.
		select {
		case <-sm.ctx.Done():
			return
		case <-time.After(rebalanceDebounceInterval):
		}

	drain:
		for {
			select {
			case <-sm.rebalanceCh:
			default:
				break drain
			}
		}

		sm.rebalanceShards()
	}
}

func (sm *shardManagerImpl) rebalanceShards() {
	if !sm.ready.Load() {
		return
	}

	// Serialize rebalance operations so phases from concurrent calls
	// don't interleave (e.g., two membership events arriving close together).
	sm.rebalanceMu.Lock()
	defer sm.rebalanceMu.Unlock()

	metrics.CounterShardRebalance.Inc()

	desiredShards := sm.membership.GetShardsForMember(sm.memberID, sm.cfg.MaxShards)
	desiredSet := make(map[int32]bool, len(desiredShards))
	for _, id := range desiredShards {
		desiredSet[id] = true
	}

	sm.mu.Lock()

	// Collect shards to release
	var toRelease []int32
	for shardID := range sm.ownedShards {
		if !desiredSet[shardID] {
			toRelease = append(toRelease, shardID)
		}
	}

	// Collect shards to claim
	var toClaim []int32
	for _, shardID := range desiredShards {
		if _, exists := sm.ownedShards[shardID]; !exists {
			toClaim = append(toClaim, shardID)
		}
	}

	sm.logger.Info("Shard rebalance started",
		tag.OwnedCount(len(sm.ownedShards)),
		tag.DesiredCount(len(desiredShards)),
		tag.ReleaseCount(len(toRelease)),
		tag.ClaimCount(len(toClaim)))

	// Phase 1: Signal shutdown and stop components for all releasing shards
	for _, shardID := range toRelease {
		state, ok := sm.ownedShards[shardID]
		if !ok {
			continue
		}
		sm.logger.Info("Releasing shard", tag.Shard(shardID))
		select {
		case <-state.handle.shutdownCh:
		default:
			close(state.handle.shutdownCh)
		}
		if state.renewCancel != nil {
			state.renewCancel()
		}
		if sm.factory != nil {
			sm.factory.StopComponents(shardID)
		}
	}

	sm.mu.Unlock()

	// Phase 2: Wait graceful period once for all shards (not per-shard)
	if len(toRelease) > 0 {
		time.Sleep(sm.cfg.ShutdownGracefulPeriod)
	}

	// Phase 3: Batch release in DB and remove from owned map
	sm.mu.Lock()
	var releaseEntries []p.ShardReleaseEntry
	for _, shardID := range toRelease {
		state, ok := sm.ownedShards[shardID]
		if !ok {
			continue
		}
		releaseEntries = append(releaseEntries, p.ShardReleaseEntry{
			ShardID:         shardID,
			ExpectedVersion: state.version,
		})
	}

	if len(releaseEntries) > 0 {
		if err := sm.store.BatchReleaseShards(sm.ctx, sm.memberID, releaseEntries); err != nil {
			sm.logger.Warn("Batch release shards partially failed (some may already be taken)", tag.Error(err))
		}
	}

	for _, shardID := range toRelease {
		if _, ok := sm.ownedShards[shardID]; ok {
			delete(sm.ownedShards, shardID)
			sm.logger.Info("Shard released", tag.Shard(shardID))
			metrics.CounterShardReleased.Inc()
		}
	}

	// Claim new shards
	for _, shardID := range desiredShards {
		if _, exists := sm.ownedShards[shardID]; !exists {
			sm.claimShardLocked(shardID)
		}
	}

	// Self-heal: if we couldn't claim every desired shard this pass (the
	// previous owner may not have released it yet — a release/claim race
	// during convergence), schedule another debounced rebalance so we keep
	// retrying until owned == desired. Self-terminating: once converged there
	// is nothing left to claim, so no further trigger fires.
	unclaimed := len(desiredShards) - len(sm.ownedShards)
	metrics.GaugeShardOwnedCount.Record(int64(len(sm.ownedShards)))
	sm.mu.Unlock()

	if unclaimed > 0 {
		sm.triggerRebalance()
	}
}

func (sm *shardManagerImpl) claimShardLocked(shardID int32) {
	sm.logger.Info("Claiming shard", tag.Shard(shardID))

	shard, err := sm.claimShardWithRetry(sm.ctx, shardID)
	if err != nil {
		sm.logger.Error("Failed to claim shard", tag.Shard(shardID), tag.Error(err))
		metrics.CounterShardClaimFailed.Inc()
		return
	}
	metrics.CounterShardClaimed.Inc()

	handle := &ShardHandle{
		ShardID:    shardID,
		shutdownCh: make(chan struct{}),
		metadata:   shard.Metadata,
	}

	state := &shardState{
		version: shard.Version,
		rangeID: shard.Metadata.RangeID,
		handle:  handle,
		readyCh: make(chan struct{}),
	}
	state.leaseExpiresAt.Store(shard.LeaseExpiresAt)

	// Compute clock-skew gate: if the previous owner's committed timer
	// watermark is in our future, we must not let the engine write timer
	// tasks with fire_at ≤ that watermark — the deleter would consider them
	// already-committed and silently delete them. Wait until our local clock
	// has surpassed that watermark before opening the gate.
	committedTs := time.UnixMilli(shard.Metadata.TimerTaskCommittedSortKey)
	if committedTs.After(time.Now()) {
		state.readyAt = committedTs
		waitDur := time.Until(committedTs)
		sm.logger.Warn("Shard claim deferred: local clock behind previous owner watermark",
			tag.Shard(shardID), tag.Duration(waitDur))
		metrics.LatencyShardClaimSkewWait.Record(waitDur)
	}

	sm.ownedShards[shardID] = state

	// Start lease renewal immediately. Renewal must run during the skew wait
	// to keep the lease alive — otherwise the lease would expire and another
	// node would steal the shard before we became ready to use it.
	renewCtx, renewCancel := context.WithCancel(sm.ctx)
	state.renewCancel = renewCancel
	sm.wg.Add(1)
	go sm.leaseRenewalLoop(renewCtx, shardID)

	// Start per-shard components: synchronously if no skew wait, otherwise
	// in a deferred goroutine that opens the readyCh gate after the wait.
	if state.readyAt.IsZero() {
		sm.startComponentsAndOpenGate(shardID, state, handle)
	} else {
		sm.wg.Add(1)
		go sm.startWhenReady(shardID, state, handle)
	}

	sm.logger.Info("Shard claimed", tag.Shard(shardID), tag.Version(shard.Version),
		tag.RangeID(int(shard.Metadata.RangeID)))
}

// startComponentsAndOpenGate launches per-shard batch readers/deleters via
// the factory, then closes state.readyCh so any goroutine blocked in
// AwaitShardReady proceeds. Order matters: components must be live before
// any engine write lands so the wake-up notifier has somewhere to deliver.
func (sm *shardManagerImpl) startComponentsAndOpenGate(shardID int32, state *shardState, handle *ShardHandle) {
	if sm.factory != nil {
		if err := sm.factory.StartComponents(shardID, handle, sm); err != nil {
			sm.logger.Error("Failed to start components for shard", tag.Shard(shardID), tag.Error(err))
		}
	}
	close(state.readyCh)
}

// startWhenReady runs in a goroutine after a deferred claim. It waits for
// the skew gate to elapse (or for shutdown), then starts components and
// opens the readyCh gate. If shutdown fires first, the gate stays closed
// and AwaitShardReady will return Unavailable for any in-flight requests
// targeting this shard.
func (sm *shardManagerImpl) startWhenReady(shardID int32, state *shardState, handle *ShardHandle) {
	defer sm.wg.Done()

	waitDur := time.Until(state.readyAt)
	if waitDur > 0 {
		select {
		case <-time.After(waitDur):
		case <-handle.shutdownCh:
			sm.logger.Info("Shard skew wait aborted by shutdown", tag.Shard(shardID))
			return
		case <-sm.ctx.Done():
			return
		}
	}
	// Re-check ownership: the shard could have been released during the
	// wait (rebalance, signal lost). If so, factory.StopComponents was
	// already called and we must not re-start.
	sm.mu.RLock()
	_, owned := sm.ownedShards[shardID]
	sm.mu.RUnlock()
	if !owned {
		return
	}
	sm.logger.Info("Shard components started after skew wait", tag.Shard(shardID))
	sm.startComponentsAndOpenGate(shardID, state, handle)
}

func (sm *shardManagerImpl) leaseRenewalLoop(ctx context.Context, shardID int32) {
	defer sm.wg.Done()

	for {
		jitter := time.Duration(rand.Int63n(int64(sm.cfg.LeaseRenewJitter)))
		interval := sm.cfg.LeaseRenewInterval + jitter

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		sm.mu.RLock()
		state, ok := sm.ownedShards[shardID]
		sm.mu.RUnlock()
		if !ok {
			return
		}

		var metadata *p.ShardMetadata
		if sm.metadataCallback != nil {
			metadata = sm.metadataCallback(shardID)
		}

		newLeaseExp, err := sm.renewShardLeaseWithRetry(ctx, shardID, state, metadata)
		if err != nil {
			sm.logger.Warn("Lease renewal failed, signaling shard lost", tag.Shard(shardID), tag.Error(err))
			sm.SignalShardLost(shardID)
			return
		}

		state.leaseExpiresAt.Store(newLeaseExp)
	}
}

func (sm *shardManagerImpl) claimShardWithRetry(ctx context.Context, shardID int32) (*p.Shard, errors.CategorizedError) {
	r := backoff.NewRetry(
		backoff.WithRetryPolicy(backoff.OwnershipStoreRetryPolicy(sm.cfg.Cluster.OwnershipOpsMaxAttempts)),
		backoff.WithRetryableError(errors.IsRetriableExcludingCASError),
	)
	var shard *p.Shard
	plainErr := r.Do(ctx, func(ctx context.Context) error {
		var err errors.CategorizedError
		shard, err = sm.store.ClaimShard(ctx, shardID, sm.memberID, sm.cfg.LeaseDuration)
		return err
	})
	if plainErr == nil {
		return shard, nil
	}
	cat, ok := plainErr.(errors.CategorizedError)
	if !ok {
		return nil, errors.NewInternalError("claim shard: unexpected error type", plainErr)
	}
	return nil, cat
}

func (sm *shardManagerImpl) renewShardLeaseWithRetry(ctx context.Context, shardID int32, state *shardState, metadata *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	r := backoff.NewRetry(
		backoff.WithRetryPolicy(backoff.OwnershipStoreRetryPolicy(sm.cfg.Cluster.OwnershipOpsMaxAttempts)),
		backoff.WithRetryableError(errors.IsRetriableExcludingCASError),
	)
	var newExp time.Time
	plainErr := r.Do(ctx, func(ctx context.Context) error {
		var err errors.CategorizedError
		newExp, err = sm.store.RenewShardLease(ctx, shardID, sm.memberID, state.version, sm.cfg.LeaseDuration, metadata)
		return err
	})
	if plainErr == nil {
		return newExp, nil
	}
	cat, ok := plainErr.(errors.CategorizedError)
	if !ok {
		return time.Time{}, errors.NewInternalError("lease renewal: unexpected error type", plainErr)
	}
	return time.Time{}, cat
}

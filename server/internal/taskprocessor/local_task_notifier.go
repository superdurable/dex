package taskprocessor

import (
	"sync"
	"sync/atomic"
)

// ShardTaskNotifier holds the per-shard wake-up state for both batch readers
// (immediate and timer). Exported so integration tests outside this package
// can construct one and pass it into NewTimerBatchReader.
//
// pendingEarliestFireAt is the fire-time-aware companion to newTimerCh. When
// the engine writes a timer task with fire_at=T, the notifier:
//  1. CompareAndSwap(pendingEarliestFireAt, T) only if T < current pending
//     (zero means "no pending hint, no advance needed").
//  2. Rings the doorbell on newTimerCh (non-blocking).
//
// The timer batch reader drains pendingEarliestFireAt at the top of every
// loop iteration and pulls its nextWakeupTime back to that earlier value.
// This prevents the reader from sitting on a stale "now + MaxLookAhead"
// wakeup when a sooner timer was just written but lies past MinLookAhead.
type ShardTaskNotifier struct {
	newTaskCh             chan struct{}
	newTimerCh            chan struct{}
	newOpsFIFOCh          chan struct{} // capacity-1 doorbell for OpsFIFO batch reader
	pendingEarliestFireAt atomic.Int64
}

// NewShardTaskNotifier constructs a ShardTaskNotifier with capacity-1
// doorbells. Used by both the factory wiring and by integration tests that
// build a reader directly.
func NewShardTaskNotifier() *ShardTaskNotifier {
	return &ShardTaskNotifier{
		newTaskCh:    make(chan struct{}, 1),
		newTimerCh:   make(chan struct{}, 1),
		newOpsFIFOCh: make(chan struct{}, 1),
	}
}

// OpsFIFOCh returns the OpsFIFO doorbell channel. Exposed so the
// OpsFIFOBatchReader can <-receive on it. Capacity-1 + non-blocking sends in
// NotifyNewOpsFIFOTask coalesce bursts (the reader's loop drains it once and
// re-reads, so dropped duplicates are harmless).
func (c *ShardTaskNotifier) OpsFIFOCh() <-chan struct{} { return c.newOpsFIFOCh }

// NotifyTimer is the test-and-wiring helper used to push a fire-time-aware
// timer wake-up onto the reader's wait loop without going through the full
// LocalTaskNotifier shard registry. Same atomic min-swap + non-blocking
// doorbell semantics as LocalTaskNotifier.NotifyNewTimerTask.
func (c *ShardTaskNotifier) NotifyTimer(fireAtUnixMs int64) {
	if fireAtUnixMs <= 0 {
		// Doorbell-only wake (legacy callers): ring the channel without
		// touching pendingEarliestFireAt so the reader re-evaluates without
		// being asked to advance to a specific time.
		select {
		case c.newTimerCh <- struct{}{}:
		default:
		}
		return
	}
	for {
		cur := c.pendingEarliestFireAt.Load()
		if cur != 0 && cur <= fireAtUnixMs {
			return
		}
		if c.pendingEarliestFireAt.CompareAndSwap(cur, fireAtUnixMs) {
			break
		}
	}
	select {
	case c.newTimerCh <- struct{}{}:
	default:
	}
}

// LocalTaskNotifier manages per-shard ShardTaskNotifier registrations and
// implements shardmanager.TaskNotifier for in-process task wake-up signals.
// Engine writes only happen on the shard owner (the wrapping ShardedRunStore
// enforces this via AwaitShardReady + the per-shard ImmediateTaskSeq lock),
// so a process-local notifier is sufficient — there is no remote routing
// layer.
type LocalTaskNotifier struct {
	mu       sync.RWMutex
	channels map[int32]*ShardTaskNotifier
}

func NewLocalTaskNotifier() *LocalTaskNotifier {
	return &LocalTaskNotifier{
		channels: make(map[int32]*ShardTaskNotifier),
	}
}

// Register adds the shard's notifier handle.
// Called by ShardTaskProcessorFactory.StartComponents when a shard is claimed.
func (n *LocalTaskNotifier) Register(shardID int32, ch *ShardTaskNotifier) {
	n.mu.Lock()
	n.channels[shardID] = ch
	n.mu.Unlock()
}

// Unregister removes notification channels for a shard.
// Called by ShardTaskProcessorFactory.StopComponents when a shard is released.
func (n *LocalTaskNotifier) Unregister(shardID int32) {
	n.mu.Lock()
	delete(n.channels, shardID)
	n.mu.Unlock()
}

// NotifyNewImmediateTask sends a non-blocking hint to the shard's immediate
// batch reader so it skips the poll interval. No-op if the shard is not local.
func (n *LocalTaskNotifier) NotifyNewImmediateTask(shardID int32) {
	n.mu.RLock()
	ch, ok := n.channels[shardID]
	n.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case ch.newTaskCh <- struct{}{}:
	default:
	}
}

// NotifyNewTimerTask wakes the shard's timer batch reader and asks it to
// advance its next-wakeup time to fireAtUnixMs if that would be sooner than
// what it had planned. The reader drains pendingEarliestFireAt at the top of
// each loop iteration and pulls its nextWakeupTime back. Best-effort: no-op
// if the shard is not local to this node.
func (n *LocalTaskNotifier) NotifyNewTimerTask(shardID int32, fireAtUnixMs int64) {
	if fireAtUnixMs <= 0 {
		return
	}
	n.mu.RLock()
	ch, ok := n.channels[shardID]
	n.mu.RUnlock()
	if !ok {
		return
	}
	ch.NotifyTimer(fireAtUnixMs)
}

// NotifyNewOpsFIFOTask sends a non-blocking hint to the shard's OpsFIFO
// batch reader so it skips its (long, ~30s) idle-poll interval. No-op if
// the shard is not local to this node. The reader's debounce delay
// (OpsBatchReadDelay, default 100ms) starts only after this signal is
// consumed, so multiple rapid notifications still coalesce into a single
// batch read.
func (n *LocalTaskNotifier) NotifyNewOpsFIFOTask(shardID int32) {
	n.mu.RLock()
	ch, ok := n.channels[shardID]
	n.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case ch.newOpsFIFOCh <- struct{}{}:
	default:
	}
}

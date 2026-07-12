package taskprocessor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Test doubles
// ============================================================================

// fakeTimerStore is a minimal RunStore stub: it tracks an in-memory slice of
// timer tasks ordered by (SortKey, ID) and serves RangeReadTimerTasks. All
// other RunStore methods panic if called (we don't exercise them here).
type fakeTimerStore struct {
	p.RunStore // promote unused methods (panics if called)

	mu    sync.Mutex
	tasks []*p.TimerTaskRow
}

func (f *fakeTimerStore) AddTask(task *p.TimerTaskRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
}

func (f *fakeTimerStore) RangeReadTimerTasks(_ context.Context, _ int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.TaskID, limit int) ([]*p.TimerTaskRow, errors.CategorizedError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*p.TimerTaskRow
	for _, t := range f.tasks {
		if t.SortKey > sortKeyUpTo {
			continue
		}
		// Cursor: tasks must be > (afterSortKey, afterID); afterID="" is the
		// inclusive-on-afterSortKey case (matches Mongo store semantics).
		if t.SortKey < afterSortKey {
			continue
		}
		if t.SortKey == afterSortKey && !afterID.IsZero() && t.ID.String() <= afterID.String() {
			continue
		}
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeTimerStore) RangeDeleteTimerTasks(_ context.Context, _ int32, _ int64, _ ids.TaskID) errors.CategorizedError {
	return nil
}

func (f *fakeTimerStore) DeleteTimerTasksByIDBatch(_ context.Context, _ int32, _ []ids.TaskID) errors.CategorizedError {
	return nil
}

// noopShardManagerForTimer satisfies the ShardManager surface used by the
// reader (just GetCappedContext) and the deleter.
type noopShardManagerForTimer struct {
	shardmanager.ShardManager
}

func (noopShardManagerForTimer) GetCappedContext(parentCtx context.Context, _ int32) (context.Context, context.CancelFunc) {
	return context.WithCancel(parentCtx)
}

// trackingTimerHandler counts HandleTimerTask invocations (and never returns
// a real handler error so the worker pool doesn't retry).
type trackingTimerHandler struct {
	processed atomic.Int32
}

func (h *trackingTimerHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	return nil
}
func (h *trackingTimerHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	return nil
}

type noopDLQStoreForTest struct{}

func (n *noopDLQStoreForTest) WriteDLQ(_ context.Context, _ *p.DLQEntry) errors.CategorizedError {
	return nil
}
func (n *noopDLQStoreForTest) Close() error { return nil }

// ============================================================================
// Tests
// ============================================================================

// TestTimerReader_NotifyAdvancesWakeupBeyondMinLookAhead is the headline test
// for Phase C. With MaxLookAhead=30s and MinLookAhead=100ms, a timer that
// fires 800ms in the future (well beyond MinLookAhead) would normally be
// missed by the first read, sending the reader back to sleep for the full
// MaxLookAhead. The fire-time-aware notify must pull nextWakeupTime back so
// the timer fires within ~1s.
func TestTimerReader_NotifyAdvancesWakeupBeyondMinLookAhead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.DefaultTaskProcessorConfig()
	cfg.TimerMinLookAheadDuration = 100 * time.Millisecond
	cfg.TimerMaxLookAheadDuration = 30 * time.Second // headline: must not wait this long
	cfg.TimerBatchReadLimit = 100
	cfg.TimerDeleteInterval = 1 * time.Second
	cfg.TimerDeleteIntervalJitter = 1 * time.Millisecond

	store := &fakeTimerStore{}
	sm := noopShardManagerForTimer{}
	logger := log.NewNoop()
	shutdownCh := make(chan struct{})
	defer close(shutdownCh)
	channels := NewShardTaskNotifier()

	handler := &trackingTimerHandler{}
	wp := NewWorkerPool(2, 5*time.Second, cfg.ImmediateTaskRetryPolicy, cfg.TimerTaskRetryPolicy, handler, &noopDLQStoreForTest{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := NewTimerBatchDeleter(0, cfg, store, sm, logger, shutdownCh, 0, ids.TaskID{})
	reader := NewTimerBatchReader(0, cfg, store, wp, deleter, shutdownCh, sm, logger, 0, ids.TaskID{}, channels)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Let the reader enter its idle wait (empty store → nextWakeupTime sits
	// at now + MaxLookAhead = now + 30s). This is the bug surface.
	time.Sleep(300 * time.Millisecond)

	// Now write a task that fires 800ms in the future and notify. Without
	// the Phase C plumbing, the reader is parked for ~30s and the task does
	// NOT fire. With Phase C, the notify pulls nextWakeupTime back to T and
	// the read at T sees the task (T ≤ T + MinLookAhead).
	fireAt := time.Now().Add(800 * time.Millisecond).UnixMilli()
	store.AddTask(&p.TimerTaskRow{ShardID: 0, ID: ids.NewTaskID(), SortKey: fireAt})
	channels.NotifyTimer(fireAt)

	require.Eventually(t, func() bool {
		return handler.processed.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond,
		"timer at fire+800ms must be processed within ~1s, NOT after MaxLookAhead=30s")
}

// TestTimerReader_NotifyEarlierWins schedules a far-future timer first
// (sets nextWakeupTime), then notifies with a sooner fire time. The reader
// must wake at the sooner time and process the second timer first.
func TestTimerReader_NotifyEarlierWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.DefaultTaskProcessorConfig()
	cfg.TimerMinLookAheadDuration = 50 * time.Millisecond
	cfg.TimerMaxLookAheadDuration = 30 * time.Second
	cfg.TimerBatchReadLimit = 100
	cfg.TimerDeleteInterval = 1 * time.Second
	cfg.TimerDeleteIntervalJitter = 1 * time.Millisecond

	store := &fakeTimerStore{}
	sm := noopShardManagerForTimer{}
	logger := log.NewNoop()
	shutdownCh := make(chan struct{})
	defer close(shutdownCh)
	channels := NewShardTaskNotifier()

	handler := &trackingTimerHandler{}
	wp := NewWorkerPool(2, 5*time.Second, cfg.ImmediateTaskRetryPolicy, cfg.TimerTaskRetryPolicy, handler, &noopDLQStoreForTest{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := NewTimerBatchDeleter(0, cfg, store, sm, logger, shutdownCh, 0, ids.TaskID{})
	reader := NewTimerBatchReader(0, cfg, store, wp, deleter, shutdownCh, sm, logger, 0, ids.TaskID{}, channels)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Write a far-future timer first and notify. Reader will set
	// nextWakeupTime to that far time.
	farFireAt := time.Now().Add(10 * time.Second).UnixMilli()
	store.AddTask(&p.TimerTaskRow{ShardID: 0, ID: ids.NewTaskID(), SortKey: farFireAt})
	channels.NotifyTimer(farFireAt)
	time.Sleep(300 * time.Millisecond) // let reader observe and park at far time

	// Now write a sooner timer and notify with its earlier fire time.
	soonFireAt := time.Now().Add(500 * time.Millisecond).UnixMilli()
	store.AddTask(&p.TimerTaskRow{ShardID: 0, ID: ids.NewTaskID(), SortKey: soonFireAt})
	channels.NotifyTimer(soonFireAt)

	require.Eventually(t, func() bool {
		return handler.processed.Load() >= 1
	}, 1500*time.Millisecond, 20*time.Millisecond,
		"sooner timer must fire within ~700ms despite the earlier far-future scheduled wakeup")
}

// TestTimerReader_NotifyLaterIsIgnored verifies a notify with fire_at >
// current pending earliest does NOT pull the wakeup forward. Concretely:
// a sooner timer T1 is queued first; a later notify for T2 (>T1) must not
// reset the pending hint to T2 (which would lose T1's wake-up advance).
func TestTimerReader_NotifyLaterIsIgnored(t *testing.T) {
	ch := NewShardTaskNotifier()
	t1 := int64(1000)
	t2 := int64(5000)

	ch.NotifyTimer(t1)
	assert.Equal(t, t1, ch.pendingEarliestFireAt.Load(),
		"first notify sets pending to t1")

	ch.NotifyTimer(t2)
	assert.Equal(t, t1, ch.pendingEarliestFireAt.Load(),
		"later fire_at must not overwrite earlier pending hint")

	// And a still-earlier hint does win.
	t0 := int64(500)
	ch.NotifyTimer(t0)
	assert.Equal(t, t0, ch.pendingEarliestFireAt.Load(),
		"strictly earlier fire_at must overwrite the pending hint")
}

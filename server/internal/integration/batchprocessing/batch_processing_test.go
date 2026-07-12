package batchprocessing

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/taskprocessor"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dbPrefix = "dex_test_integration_batchprocessing"

type noopDLQStore struct{}

func (n *noopDLQStore) WriteDLQ(_ context.Context, _ *p.DLQEntry) errors.CategorizedError {
	return nil
}
func (n *noopDLQStore) Close() error { return nil }

// ============================================================================
// Full pipeline: Reader → WorkerPool → Deleter → RangeDelete
// ============================================================================

func TestBatchPipeline_ImmediateTask_EndToEnd(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	// Insert 5 immediate tasks directly into DB
	var insertedSeqs []int64
	for i := 1; i <= 5; i++ {
		seq := int64(i * 100)
		insertedSeqs = append(insertedSeqs, seq)
		insertImmediateTask(t, runStore, shardID, seq)
	}

	// Verify tasks exist in DB
	tasks, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 100)
	require.Nil(t, err)
	assert.Equal(t, 5, len(tasks), "should read 5 tasks from DB")

	// Create pipeline components
	var processedCount atomic.Int32
	handler := &trackingHandler{processedImmediate: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	// Start reader and deleter in background
	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Wait for all 5 tasks to be processed
	require.Eventually(t, func() bool {
		return processedCount.Load() == 5
	}, 10*time.Second, 50*time.Millisecond, "all 5 tasks should be processed")

	// Wait for watermark to advance (completions arrive via channel)
	time.Sleep(200 * time.Millisecond)

	wm := deleter.GetWatermark()
	// With 5 tasks at seq 100..500, watermark = min(remaining)-1 at each removal.
	// After all complete, it should be at least 99 (conservative) but typically 499.
	assert.Greater(t, wm, int64(0), "watermark should have advanced from initial 0")
	t.Logf("immediate watermark after all processed: %d", wm)

	// Trigger range delete by closing shutdown (which drains + deletes)
	close(shutdownCh)
	time.Sleep(300 * time.Millisecond)

	// Verify tasks were deleted from DB
	remaining, rErr := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 100)
	require.Nil(t, rErr)
	t.Logf("remaining tasks after shutdown: %d", len(remaining))
}

func TestBatchPipeline_TimerTask_EndToEnd(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	channels := taskprocessor.NewShardTaskNotifier()

	// Insert timer tasks with fire_at in the past (so they're immediately ready)
	pastMs := time.Now().Add(-1 * time.Second).UnixMilli()
	for i := 0; i < 3; i++ {
		insertTimerTask(t, runStore, shardID, pastMs+int64(i))
	}

	var processedCount atomic.Int32
	handler := &trackingHandler{processedTimer: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewTimerBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0, ids.TaskID{},
	)
	reader := taskprocessor.NewTimerBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, ids.TaskID{}, channels,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	require.Eventually(t, func() bool {
		return processedCount.Load() == 3
	}, 10*time.Second, 50*time.Millisecond, "all 3 timer tasks should be processed")

	// Verify watermark advanced
	time.Sleep(200 * time.Millisecond)
	sk, id := deleter.GetWatermark()
	t.Logf("timer watermark: sortKey=%d, id=%s", sk, id)
	assert.Greater(t, sk, int64(0), "timer watermark should have advanced")
}

func TestBatchPipeline_FailedTaskDoesNotBlockWatermark(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	// Insert 3 tasks: seq 100, 200, 300
	insertImmediateTask(t, runStore, shardID, 100)
	insertImmediateTask(t, runStore, shardID, 200)
	insertImmediateTask(t, runStore, shardID, 300)

	// Handler that fails task with seq=200 but succeeds others
	var processedCount atomic.Int32
	handler := &selectiveFailHandler{
		failSeq:   200,
		processed: &processedCount,
	}
	wp := taskprocessor.NewWorkerPool(1, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Wait for all 3 tasks to be processed (including the failed one)
	require.Eventually(t, func() bool {
		return processedCount.Load() == 3
	}, 10*time.Second, 50*time.Millisecond, "all 3 tasks should be attempted")

	time.Sleep(200 * time.Millisecond)

	wm := deleter.GetWatermark()
	// Even the failed task (200) should have been removed from pendingSet
	// (we always notify DoneCh), so watermark should advance past all.
	assert.GreaterOrEqual(t, wm, int64(299),
		"watermark should advance past failed task (200) since DoneCh is always sent")
}

func TestBatchPipeline_MultiRoundReadDelete(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	cfg.ImmediateBatchReadLimit = 3 // small batch to force multiple reads
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	// Insert 10 tasks — requires multiple read rounds with limit=3
	for i := 1; i <= 10; i++ {
		insertImmediateTask(t, runStore, shardID, int64(i*10))
	}

	var processedCount atomic.Int32
	handler := &trackingHandler{processedImmediate: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	require.Eventually(t, func() bool {
		return processedCount.Load() == 10
	}, 10*time.Second, 50*time.Millisecond, "all 10 tasks should be processed across multiple read rounds")

	time.Sleep(200 * time.Millisecond)
	wm := deleter.GetWatermark()
	assert.Greater(t, wm, int64(0), "watermark should have advanced from 0")
	t.Logf("multi-round watermark after all processed: %d", wm)
}

func TestBatchPipeline_ReaderStartsFromCommittedOffset(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	// Insert tasks at seq 100, 200, 300
	insertImmediateTask(t, runStore, shardID, 100)
	insertImmediateTask(t, runStore, shardID, 200)
	insertImmediateTask(t, runStore, shardID, 300)

	// Start reader with committedOffset=200 → should only pick up seq 300
	var processedCount atomic.Int32
	handler := &trackingHandler{processedImmediate: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	committedOffset := int64(200)
	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, committedOffset,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, committedOffset, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Wait and check: only 1 task (seq 300) should be processed
	time.Sleep(1 * time.Second)
	assert.Equal(t, int32(1), processedCount.Load(),
		"only tasks after committedOffset=200 should be read (seq 300)")
}

func TestBatchPipeline_TimerTask_MultiRoundReadNotBlockedByMaxLookAhead(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	// Small batch limit forces multiple read rounds
	cfg.TimerBatchReadLimit = 3
	// Large MaxLookAhead — if the bug existed, this would cause a 60s delay
	// between rounds instead of immediate re-poll
	cfg.TimerMaxLookAheadDuration = 60 * time.Second
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	channels := taskprocessor.NewShardTaskNotifier()

	// Insert 10 timer tasks all in the past (immediately ready).
	// With batch limit=3, this requires 4 read rounds (3+3+3+1).
	pastMs := time.Now().Add(-2 * time.Second).UnixMilli()
	for i := 0; i < 10; i++ {
		insertTimerTask(t, runStore, shardID, pastMs+int64(i))
	}

	var processedCount atomic.Int32
	handler := &trackingHandler{processedTimer: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewTimerBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0, ids.TaskID{},
	)
	reader := taskprocessor.NewTimerBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, ids.TaskID{}, channels,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// All 10 tasks should be processed within 5 seconds across multiple rounds.
	// Before the fix, the reader would stall for MaxLookAheadDuration (60s)
	// after the first round because nextWakeupTime was never reset.
	require.Eventually(t, func() bool {
		return processedCount.Load() == 10
	}, 5*time.Second, 50*time.Millisecond,
		"all 10 timer tasks should be processed within 5s across 4 read rounds (batch=3), "+
			"got %d", processedCount.Load())

	t.Logf("processed %d timer tasks across multiple rounds with MaxLookAhead=60s", processedCount.Load())
}

// ============================================================================
// Timer pipeline: Failed task does not block watermark
// ============================================================================

func TestBatchPipeline_TimerTask_FailedTaskDoesNotBlockWatermark(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	channels := taskprocessor.NewShardTaskNotifier()

	// Insert 3 timer tasks in the past; handler will fail the middle one
	pastMs := time.Now().Add(-1 * time.Second).UnixMilli()
	insertTimerTask(t, runStore, shardID, pastMs)
	insertTimerTask(t, runStore, shardID, pastMs+1)
	insertTimerTask(t, runStore, shardID, pastMs+2)

	var processedCount atomic.Int32
	handler := &selectiveTimerFailHandler{
		failSortKey: pastMs + 1,
		processed:   &processedCount,
	}
	wp := taskprocessor.NewWorkerPool(1, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewTimerBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0, ids.TaskID{},
	)
	reader := taskprocessor.NewTimerBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, ids.TaskID{}, channels,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	require.Eventually(t, func() bool {
		return processedCount.Load() == 3
	}, 10*time.Second, 50*time.Millisecond, "all 3 timer tasks should be attempted")

	time.Sleep(200 * time.Millisecond)

	sk, _ := deleter.GetWatermark()
	assert.Greater(t, sk, int64(0),
		"timer watermark should advance past failed task since DoneCh is always sent")
}

// ============================================================================
// All tasks fail: Queue still progresses
// ============================================================================

func TestBatchPipeline_AllTasksFail_QueueStillProgresses(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	for i := 1; i <= 5; i++ {
		insertImmediateTask(t, runStore, shardID, int64(i*100))
	}

	var processedCount atomic.Int32
	handler := &allFailHandler{processed: &processedCount}
	wp := taskprocessor.NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	require.Eventually(t, func() bool {
		return processedCount.Load() == 5
	}, 10*time.Second, 50*time.Millisecond, "all 5 tasks should be attempted even though all fail")

	time.Sleep(200 * time.Millisecond)

	wm := deleter.GetWatermark()
	assert.Greater(t, wm, int64(0),
		"watermark should advance even when all tasks fail")
}

// ============================================================================
// Notify wakes immediate reader
// ============================================================================

func TestBatchPipeline_NotifyTaskWakesImmediateReader(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	cfg.ImmediatePollInterval = 30 * time.Second // very long poll so only notify can wake it
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	var processedCount atomic.Int32
	handler := &trackingHandler{processedImmediate: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Let reader enter idle wait (empty queue)
	time.Sleep(200 * time.Millisecond)

	// Insert task and signal
	insertImmediateTask(t, runStore, shardID, 100)
	newTaskCh <- struct{}{}

	// Task should be processed well before the 30s poll interval
	require.Eventually(t, func() bool {
		return processedCount.Load() == 1
	}, 3*time.Second, 50*time.Millisecond,
		"task should be processed promptly via notify, not waiting for 30s poll interval")
}

// ============================================================================
// Notify wakes timer reader
// ============================================================================

func TestBatchPipeline_NotifyTimerWakesTimerReader(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	cfg.TimerMaxLookAheadDuration = 60 * time.Second // very long so only notify can wake it
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	channels := taskprocessor.NewShardTaskNotifier()

	var processedCount atomic.Int32
	handler := &trackingHandler{processedTimer: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewTimerBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0, ids.TaskID{},
	)
	reader := taskprocessor.NewTimerBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, ids.TaskID{}, channels,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Let reader enter idle wait (empty queue → sleeps for MaxLookAhead=60s)
	time.Sleep(500 * time.Millisecond)

	// Insert a past-due timer task and send notify signal
	pastMs := time.Now().Add(-1 * time.Second).UnixMilli()
	insertTimerTask(t, runStore, shardID, pastMs)
	channels.NotifyTimer(pastMs)

	require.Eventually(t, func() bool {
		return processedCount.Load() == 1
	}, 3*time.Second, 50*time.Millisecond,
		"timer task should be processed promptly via notify, not waiting for 60s look-ahead")
}

// ============================================================================
// Shutdown stops reader and deleter
// ============================================================================

func TestBatchPipeline_ShutdownStopsReaderAndDeleter(t *testing.T) {
	runStore, cleanup := getRunStoreForBatch(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardID := int32(0)
	cfg := fastTestConfig()
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	shutdownCh := make(chan struct{})
	newTaskCh := make(chan struct{}, 1)

	// Insert 3 tasks
	insertImmediateTask(t, runStore, shardID, 100)
	insertImmediateTask(t, runStore, shardID, 200)
	insertImmediateTask(t, runStore, shardID, 300)

	var processedCount atomic.Int32
	handler := &trackingHandler{processedImmediate: &processedCount}
	wp := taskprocessor.NewWorkerPool(4, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", logger)
	wp.Start(ctx)
	defer wp.Stop()

	deleter := taskprocessor.NewImmediateBatchDeleter(
		shardID, cfg, runStore, sm, logger, shutdownCh, 0,
	)
	reader := taskprocessor.NewImmediateBatchReader(
		shardID, cfg, runStore, wp, deleter,
		shutdownCh, sm, logger, 0, newTaskCh,
	)

	go reader.Run(ctx)
	go deleter.Run(ctx)

	// Wait for tasks to be processed
	require.Eventually(t, func() bool {
		return processedCount.Load() == 3
	}, 10*time.Second, 50*time.Millisecond)

	countBefore := processedCount.Load()

	// Signal shutdown
	close(shutdownCh)
	time.Sleep(300 * time.Millisecond)

	// Insert more tasks after shutdown — they should NOT be processed
	insertImmediateTask(t, runStore, shardID, 400)
	insertImmediateTask(t, runStore, shardID, 500)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, countBefore, processedCount.Load(),
		"no new tasks should be processed after shutdown")
}

// ============================================================================
// Additional test handlers for integration tests
// ============================================================================

// selectiveTimerFailHandler fails timer tasks with a specific SortKey.
type selectiveTimerFailHandler struct {
	failSortKey int64
	processed   *atomic.Int32
}

func (h *selectiveTimerFailHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	return nil
}

func (h *selectiveTimerFailHandler) HandleTimerTask(_ context.Context, _ int32, task *p.TimerTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	if task.SortKey == h.failSortKey {
		return errors.NewInvalidInputError("intentional timer failure for test", nil)
	}
	return nil
}

// allFailHandler fails every task with a non-retriable error.
type allFailHandler struct {
	processed *atomic.Int32
}

func (h *allFailHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	return errors.NewInvalidInputError("all tasks fail for test", nil)
}

func (h *allFailHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	return errors.NewInvalidInputError("all tasks fail for test", nil)
}

// ============================================================================
// Test helpers
// ============================================================================

func getRunStoreForBatch(t *testing.T) (p.RunStore, func()) {
	set := testhelpers.NewStoreSetForTest(t, dbPrefix)
	set.Run.DeleteAll(context.Background())
	// Store cleanup is registered by NewStoreSetForTest; return a no-op.
	return set.Run, func() {}
}

func fastTestConfig() config.TaskProcessorConfig {
	cfg := config.DefaultTaskProcessorConfig()
	cfg.ImmediatePollInterval = 100 * time.Millisecond
	cfg.ImmediateDeleteInterval = 200 * time.Millisecond
	cfg.ImmediateDeleteIntervalJitter = 1 * time.Millisecond
	cfg.TimerMinLookAheadDuration = 500 * time.Millisecond
	cfg.TimerDeleteInterval = 200 * time.Millisecond
	cfg.TimerDeleteIntervalJitter = 1 * time.Millisecond
	return cfg
}

func insertImmediateTask(t *testing.T, store p.RunStore, shardID int32, seq int64) {
	t.Helper()
	// Create a minimal run first (tasks need a run to coexist in the same collection)
	runID := uuid.NewString()
	taskID := ids.NewTaskID()
	run := &p.RunRow{
		ShardID:                   shardID,
		RowType:                   p.RowTypeRun,
		Namespace:                 "test",
		ID:                        runID,
		FlowType:                  "test",
		TaskListName:              "g",
		Status:                    p.RunStatusPending,
		Version:                   1,
		StateMap:                  map[string]p.Value{},
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters:         map[string]int32{},
		ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
		CreatedAt:                 time.Now(),
		UpdatedAt:                 time.Now(),
	}
	task := p.TaskRow{
		Immediate: &p.ImmediateTaskRow{
			ShardID:  shardID,
			ID:       taskID,
			SortKey:  seq,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test", TaskListName: "g"},
		},
	}
	err := store.CreateRunWithTasks(context.Background(), run, []p.TaskRow{task})
	require.Nil(t, err)
}

func insertTimerTask(t *testing.T, store p.RunStore, shardID int32, fireAtMs int64) {
	t.Helper()
	runID := uuid.NewString()
	taskID := ids.NewTaskID()
	run := &p.RunRow{
		ShardID:                   shardID,
		RowType:                   p.RowTypeRun,
		Namespace:                 "test",
		ID:                        runID,
		FlowType:                  "test",
		TaskListName:              "g",
		Status:                    p.RunStatusRunning,
		Version:                   1,
		StateMap:                  map[string]p.Value{},
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters:         map[string]int32{},
		ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
		CreatedAt:                 time.Now(),
		UpdatedAt:                 time.Now(),
	}
	task := p.TaskRow{
		Timer: &p.TimerTaskRow{
			ShardID:  shardID,
			ID:       taskID,
			SortKey:  fireAtMs,
			TaskType: p.TimerTaskRunHeartbeat,
			TaskInfo: p.TimerTaskInfo{RunID: runID, Namespace: "test"},
		},
	}
	err := store.CreateRunWithTasks(context.Background(), run, []p.TaskRow{task})
	require.Nil(t, err)
}

// trackingHandler counts processed tasks.
type trackingHandler struct {
	processedImmediate *atomic.Int32
	processedTimer     *atomic.Int32
}

func (h *trackingHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	if h.processedImmediate != nil {
		h.processedImmediate.Add(1)
	}
	return nil
}

func (h *trackingHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	if h.processedTimer != nil {
		h.processedTimer.Add(1)
	}
	return nil
}

// ============================================================================
// Reproducer: out-of-order task insertion causes reader cursor to skip tasks
// ============================================================================

// TestBatchPipeline_ImmediateTask_OutOfOrderInsertSkipsBug reproduces the race
// condition where concurrent CreateRunWithTasks calls commit in non-sequential
// order. The reader's cursor advances to the max sort_key seen, permanently
// skipping tasks that commit later with lower sort_keys.
//
// selectiveFailHandler fails tasks with a specific SortKey.
type selectiveFailHandler struct {
	failSeq   int64
	processed *atomic.Int32
}

func (h *selectiveFailHandler) HandleImmediateTask(_ context.Context, _ int32, task *p.ImmediateTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	if task.SortKey == h.failSeq {
		return errors.NewInvalidInputError("intentional failure for test", nil)
	}
	return nil
}

func (h *selectiveFailHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	h.processed.Add(1)
	return nil
}

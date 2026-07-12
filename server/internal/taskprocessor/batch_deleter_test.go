package taskprocessor

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/backoff"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tid maps a readable label to a deterministic TaskID by copying the label's
// raw bytes, so byte-order equals the label's string order. This lets the
// ordering assertions below keep their readable labels after the id type
// changed from string to ids.TaskID.
func tid(label string) ids.TaskID {
	var b [16]byte
	copy(b[:], label)
	return ids.TaskID(b)
}

// noopDLQStore is a test stub that discards DLQ writes.
type noopDLQStore struct{}

func (n *noopDLQStore) WriteDLQ(_ context.Context, _ *p.DLQEntry) errors.CategorizedError {
	return nil
}
func (n *noopDLQStore) Close() error { return nil }

// ============================================================================
// Immediate Task Key ordering
// ============================================================================

func TestImmTaskLess_OrdersBySeq(t *testing.T) {
	a := immediateTaskKey{seq: 1, id: tid("aaa")}
	b := immediateTaskKey{seq: 2, id: tid("aaa")}
	assert.True(t, immTaskLess(a, b))
	assert.False(t, immTaskLess(b, a))
}

func TestImmTaskLess_SameSeqNotLess(t *testing.T) {
	a := immediateTaskKey{seq: 5, id: tid("aaa")}
	b := immediateTaskKey{seq: 5, id: tid("bbb")}
	assert.False(t, immTaskLess(a, b), "same seq should not be less regardless of id")
}

// ============================================================================
// Timer Task Key ordering
// ============================================================================

func TestTimerTaskLess_OrdersBySortKeyThenID(t *testing.T) {
	cases := []struct {
		name string
		a, b timerTaskKey
		want bool
	}{
		{"lower sortKey", timerTaskKey{100, tid("bbb")}, timerTaskKey{200, tid("aaa")}, true},
		{"higher sortKey", timerTaskKey{200, tid("aaa")}, timerTaskKey{100, tid("bbb")}, false},
		{"same sortKey lower id", timerTaskKey{100, tid("aaa")}, timerTaskKey{100, tid("bbb")}, true},
		{"same sortKey higher id", timerTaskKey{100, tid("bbb")}, timerTaskKey{100, tid("aaa")}, false},
		{"same sortKey same id", timerTaskKey{100, tid("aaa")}, timerTaskKey{100, tid("aaa")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, timerTaskLess(tc.a, tc.b))
		})
	}
}

// ============================================================================
// Immediate Batch Deleter: Watermark advancement
// ============================================================================

func TestImmediateDeleter_WatermarkAdvancesOnCompletion(t *testing.T) {
	d := newTestImmediateDeleter(0)

	d.InsertPending(10, tid("t10"))
	d.InsertPending(20, tid("t20"))
	d.InsertPending(30, tid("t30"))

	assert.Equal(t, int64(0), d.GetWatermark(), "watermark should start at committed offset")

	d.removePending(10, tid("t10"))
	assert.Equal(t, int64(19), d.GetWatermark(), "watermark should advance to min-1 after removing lowest")

	d.removePending(30, tid("t30"))
	assert.Equal(t, int64(19), d.GetWatermark(), "watermark should not advance past still-pending task 20")

	d.removePending(20, tid("t20"))
	assert.Equal(t, int64(19), d.GetWatermark(), "watermark stays when pending set is empty (no min to compute from)")
}

func TestImmediateDeleter_OutOfOrderCompletion(t *testing.T) {
	d := newTestImmediateDeleter(0)

	d.InsertPending(5, tid("t5"))
	d.InsertPending(10, tid("t10"))
	d.InsertPending(15, tid("t15"))
	d.InsertPending(20, tid("t20"))

	d.removePending(15, tid("t15"))
	assert.Equal(t, int64(4), d.GetWatermark(), "watermark advances to min(pending)-1 = 5-1 = 4")

	d.removePending(5, tid("t5"))
	assert.Equal(t, int64(9), d.GetWatermark(), "watermark advances to 9 (min=10, so 10-1)")

	d.removePending(10, tid("t10"))
	assert.Equal(t, int64(19), d.GetWatermark(), "watermark advances to 19 (min=20, so 20-1)")
}

func TestImmediateDeleter_EmptyPendingSet_WatermarkStaysAtCommitted(t *testing.T) {
	d := newTestImmediateDeleter(100)
	assert.Equal(t, int64(100), d.GetWatermark())

	d.InsertPending(200, tid("t200"))
	d.removePending(200, tid("t200"))
	assert.Equal(t, int64(100), d.GetWatermark(), "with empty set, watermark stays at last value")
}

func TestImmediateDeleter_CompletedAboveWatermark_Tracked(t *testing.T) {
	d := newTestImmediateDeleter(0)

	d.InsertPending(10, tid("t10"))
	d.InsertPending(20, tid("t20"))
	d.InsertPending(30, tid("t30"))

	d.removePending(30, tid("t30"))
	d.removePending(20, tid("t20"))

	d.mu.Lock()
	assert.Contains(t, d.completedAbove, tid("t30"), "task 30 completed above watermark (0)")
	assert.Contains(t, d.completedAbove, tid("t20"), "task 20 completed above watermark (0)")
	d.mu.Unlock()
}

// ============================================================================
// Timer Batch Deleter: Watermark advancement
// ============================================================================

func TestTimerDeleter_WatermarkAdvancesOnCompletion(t *testing.T) {
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(100, tid("a"))
	d.InsertPending(100, tid("b"))
	d.InsertPending(200, tid("c"))

	sk, id := d.GetWatermark()
	assert.Equal(t, int64(0), sk)
	assert.Equal(t, ids.TaskID{}, id)

	d.removePending(100, tid("a"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(100), sk, "watermark sortKey should advance to min pending")
	assert.Equal(t, tid("b"), id, "watermark id should be min pending")

	d.removePending(100, tid("b"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(200), sk)
	assert.Equal(t, tid("c"), id)
}

func TestTimerDeleter_SameSortKeyDifferentIDs(t *testing.T) {
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(500, tid("alpha"))
	d.InsertPending(500, tid("beta"))
	d.InsertPending(500, tid("gamma"))

	d.removePending(500, tid("alpha"))
	sk, id := d.GetWatermark()
	assert.Equal(t, int64(500), sk)
	assert.Equal(t, tid("beta"), id, "should advance to next id at same sortKey")
}

// ============================================================================
// Worker Pool → DoneCh → Deleter integration
// ============================================================================

func TestWorkerPool_SuccessNotifiesDeleter(t *testing.T) {
	handler := &countingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	d.InsertPending(100, tid("t100"))

	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 100,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 100, ID: tid("t100")},
	})

	// Wait for completion to arrive at deleter
	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 0, d.pendingSet.Len(), "pending set should be empty after completion")
	assert.Equal(t, 1, handler.immediateCount(), "handler should have been called once")
}

func TestWorkerPool_FailureStillNotifiesDeleter(t *testing.T) {
	handler := &failingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	d.InsertPending(200, tid("t200"))
	d.InsertPending(300, tid("t300"))

	// Submit a task that will fail (retry exhausted)
	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 200,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 200, ID: tid("t200")},
	})

	// Even though the task failed, DoneCh should still receive completion
	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 1, d.pendingSet.Len(), "t300 should still be pending")
	assert.Equal(t, int64(299), d.GetWatermark(), "watermark should advance past failed task to min(pending)-1")
}

func TestWorkerPool_TimerTaskCompletionNotifiesDeleter(t *testing.T) {
	handler := &countingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestTimerDeleter(0, ids.TaskID{})
	d.InsertPending(1000, tid("timer1"))

	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Timer: &p.TimerTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 1000,
			TaskType: p.TimerTaskRunHeartbeat,
			TaskInfo: p.TimerTaskInfo{RunID: "r1", Namespace: "ns"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 1000, ID: tid("timer1")},
	})

	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 0, d.pendingSet.Len(), "pending set should be empty")
	assert.Equal(t, 1, handler.timerCount(), "timer handler should have been called once")
}

func TestWorkerPool_MultipleTasksWatermarkAdvancesCorrectly(t *testing.T) {
	handler := &countingTaskHandler{}
	wp := NewWorkerPool(1, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	d.InsertPending(10, tid("t10"))
	d.InsertPending(20, tid("t20"))
	d.InsertPending(30, tid("t30"))

	for _, seq := range []int64{10, 20, 30} {
		id := tid(fmt.Sprintf("t%d", seq))
		wp.Submit(&TaskItem{
			ShardID: 0,
			Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
				ShardID: 0, ID: ids.TaskID{}, SortKey: seq,
				TaskType: p.ImmediateTaskRunInitialDispatch,
				TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
			}},
			DoneCh:  d.DoneCh(),
			TaskKey: TaskCompletion{SortKey: seq, ID: id},
		})
	}

	// Drain all completions
	for i := 0; i < 3; i++ {
		tc := <-d.doneCh
		d.removePending(tc.SortKey, tc.ID)
	}

	assert.Equal(t, 0, d.pendingSet.Len(), "all tasks should be completed")
	assert.Equal(t, 3, handler.immediateCount())
}

// countingTaskHandler counts calls for verification. Uses atomic counters
// since worker pool dispatches calls on multiple goroutines.
type countingTaskHandler struct {
	immediateCountAtomic atomic.Int32
	timerCountAtomic     atomic.Int32
}

func (h *countingTaskHandler) immediateCount() int { return int(h.immediateCountAtomic.Load()) }
func (h *countingTaskHandler) timerCount() int     { return int(h.timerCountAtomic.Load()) }

func (h *countingTaskHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	h.immediateCountAtomic.Add(1)
	return nil
}

func (h *countingTaskHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	h.timerCountAtomic.Add(1)
	return nil
}

// failingTaskHandler always returns a non-retriable error.
type failingTaskHandler struct{}

func (h *failingTaskHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	return errors.NewInvalidInputError("permanent failure for test", nil)
}

func (h *failingTaskHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	return errors.NewInvalidInputError("permanent failure for test", nil)
}

// ============================================================================
// Bug-fix verification: Timer watermark must not delete in-flight tasks
// ============================================================================

func TestTimerDeleter_WatermarkIsExclusive_RangeDeleteDoesNotDeleteMinPending(t *testing.T) {
	// After removing the lowest pending task, the watermark advances to
	// min(pendingSet). RangeDeleteTimerTasks uses exclusive upper bound
	// (< watermark), so the task AT the watermark position must NOT be deleted.
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(100, tid("a"))
	d.InsertPending(200, tid("b"))
	d.InsertPending(300, tid("c"))

	d.removePending(100, tid("a"))
	sk, id := d.GetWatermark()
	assert.Equal(t, int64(200), sk, "watermark sortKey should be min(pending)")
	assert.Equal(t, tid("b"), id, "watermark id should be min(pending)")

	// The range delete query in Mongo uses (sort_key, id) < (wmSortKey, wmID).
	// Task (200,"b") should NOT be matched by this query because it equals
	// the watermark, and the comparison is strict less-than.
	// We can't test the Mongo query directly in unit tests, but we verify the
	// watermark value is set to min(pending) and trust that the exclusive
	// query semantics are correct (tested via integration).
	assert.Equal(t, 2, d.pendingSet.Len(), "both tasks at and above watermark should still be in pending set")
}

func TestTimerDeleter_CompletedAtWatermark_TrackedInCompletedAbove(t *testing.T) {
	// When a task at the watermark position completes, it must be tracked
	// in completedAbove for shutdown cleanup, because the exclusive range
	// delete (< watermark) does not cover it.
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(100, tid("a"))
	d.InsertPending(200, tid("b"))

	// Remove lowest → watermark advances to (200,"b")
	d.removePending(100, tid("a"))
	sk, id := d.GetWatermark()
	assert.Equal(t, int64(200), sk)
	assert.Equal(t, tid("b"), id)

	// Now remove the task AT the watermark position
	d.removePending(200, tid("b"))

	d.mu.Lock()
	assert.Contains(t, d.completedAbove, tid("b"),
		"task at watermark must be tracked in completedAbove for shutdown cleanup")
	d.mu.Unlock()
}

func TestTimerDeleter_RangeDeleteSkipCondition_UsesCompoundKeyOrdering(t *testing.T) {
	// The skip condition must use compound key ordering, not component-wise <=.
	// Scenario: committed=(200,"aaa"), watermark=(100,"zzz")
	// Compound order: (100,"zzz") < (200,"aaa"), so watermark has NOT advanced.
	// Component-wise: 100<=200 && "zzz"<="aaa" = true && false = false → would
	// incorrectly proceed with deletion.
	d := newTestTimerDeleter(200, tid("aaa"))

	// Manually set watermark below committed in compound order
	d.mu.Lock()
	d.watermarkSortKey = 100
	d.watermarkID = tid("zzz")
	d.mu.Unlock()

	// rangeDelete should be a no-op (watermark hasn't advanced beyond committed).
	// Since runStore and sm are nil, if rangeDelete proceeds it will panic.
	// No panic = correctly skipped.
	assert.NotPanics(t, func() {
		d.rangeDelete(context.Background())
	}, "rangeDelete should skip when watermark < committed in compound key order")
}

// ============================================================================
// Worker Pool: Retriable error exhaustion
// ============================================================================

func TestWorkerPool_RetriableErrorExhaustsRetries_StillNotifiesDoneCh(t *testing.T) {
	handler := &retriableFailingTaskHandler{}
	shortRetry := backoff.RetryPolicy{
		InitialInterval: 1 * time.Millisecond,
		MaximumAttempts: 3,
	}
	wp := NewWorkerPool(2, 10*time.Second, shortRetry, shortRetry, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	d.InsertPending(100, tid("t100"))

	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 100,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 100, ID: tid("t100")},
	})

	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 0, d.pendingSet.Len(), "pending set should be empty even after retry exhaustion")
}

// ============================================================================
// Worker Pool: Transient failure then success
// ============================================================================

func TestWorkerPool_TransientFailureThenSuccess_NotifiesDoneCh(t *testing.T) {
	handler := &transientFailHandler{failCount: 2}
	shortRetry := backoff.RetryPolicy{
		InitialInterval: 1 * time.Millisecond,
		MaximumAttempts: 5,
	}
	wp := NewWorkerPool(2, 10*time.Second, shortRetry, shortRetry, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	d.InsertPending(100, tid("t100"))

	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 100,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 100, ID: tid("t100")},
	})

	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 0, d.pendingSet.Len())
	assert.Equal(t, int32(3), handler.calls.Load(), "handler should be called 3 times (2 failures + 1 success)")
}

// ============================================================================
// Worker Pool: Nil DoneCh
// ============================================================================

func TestWorkerPool_NilDoneCh_DoesNotPanic(t *testing.T) {
	handler := &countingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	done := make(chan struct{})
	go func() {
		wp.Submit(&TaskItem{
			ShardID: 0,
			Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
				ShardID: 0, ID: ids.TaskID{}, SortKey: 1,
				TaskType: p.ImmediateTaskRunInitialDispatch,
				TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
			}},
			DoneCh:  nil,
			TaskKey: TaskCompletion{SortKey: 1, ID: tid("t1")},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("submit with nil DoneCh should not hang")
	}

	// Give the worker time to process
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, handler.immediateCount(), "handler should still be called with nil DoneCh")
}

// ============================================================================
// Worker Pool: Empty TaskRow (neither Immediate nor Timer)
// ============================================================================

func TestWorkerPool_EmptyTaskRow_StillNotifiesDoneCh(t *testing.T) {
	handler := &countingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	doneCh := make(chan TaskCompletion, 1)
	wp.Submit(&TaskItem{
		ShardID: 0,
		Task:    p.TaskRow{},
		DoneCh:  doneCh,
		TaskKey: TaskCompletion{SortKey: 42, ID: tid("empty")},
	})

	select {
	case tc := <-doneCh:
		assert.Equal(t, int64(42), tc.SortKey)
		assert.Equal(t, tid("empty"), tc.ID)
	case <-time.After(5 * time.Second):
		t.Fatal("DoneCh should receive completion even for empty TaskRow")
	}

	assert.Equal(t, 0, handler.immediateCount(), "immediate handler should not be called")
	assert.Equal(t, 0, handler.timerCount(), "timer handler should not be called")
}

// ============================================================================
// Worker Pool: Mixed success/failure across multiple tasks
// ============================================================================

func TestWorkerPool_MultipleTasksMixedSuccessFailure_WatermarkAdvances(t *testing.T) {
	handler := &selectiveSeqFailHandler{failSeqs: map[int64]bool{20: true, 40: true}}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestImmediateDeleter(0)
	seqs := []int64{10, 20, 30, 40, 50}
	for _, seq := range seqs {
		d.InsertPending(seq, tid(fmt.Sprintf("t%d", seq)))
	}

	for _, seq := range seqs {
		id := tid(fmt.Sprintf("t%d", seq))
		wp.Submit(&TaskItem{
			ShardID: 0,
			Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
				ShardID: 0, ID: ids.TaskID{}, SortKey: seq,
				TaskType: p.ImmediateTaskRunInitialDispatch,
				TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
			}},
			DoneCh:  d.DoneCh(),
			TaskKey: TaskCompletion{SortKey: seq, ID: id},
		})
	}

	for i := 0; i < 5; i++ {
		tc := <-d.doneCh
		d.removePending(tc.SortKey, tc.ID)
	}

	assert.Equal(t, 0, d.pendingSet.Len(), "all tasks should be completed regardless of success/failure")
}

// ============================================================================
// Worker Pool: Timer task failure
// ============================================================================

func TestWorkerPool_TimerTaskFailure_StillNotifiesDoneCh(t *testing.T) {
	handler := &failingTaskHandler{}
	wp := NewWorkerPool(2, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wp.Start(ctx)
	defer wp.Stop()

	d := newTestTimerDeleter(0, ids.TaskID{})
	d.InsertPending(1000, tid("timer1"))

	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Timer: &p.TimerTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 1000,
			TaskType: p.TimerTaskRunHeartbeat,
			TaskInfo: p.TimerTaskInfo{RunID: "r1", Namespace: "ns"},
		}},
		DoneCh:  d.DoneCh(),
		TaskKey: TaskCompletion{SortKey: 1000, ID: tid("timer1")},
	})

	tc := <-d.doneCh
	d.removePending(tc.SortKey, tc.ID)

	assert.Equal(t, 0, d.pendingSet.Len(), "pending set should be empty after failed timer task")
}

// ============================================================================
// Timer Batch Deleter: Out-of-order completion
// ============================================================================

func TestTimerDeleter_OutOfOrderCompletion(t *testing.T) {
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(100, tid("a"))
	d.InsertPending(200, tid("b"))
	d.InsertPending(300, tid("c"))

	// Remove middle first
	d.removePending(200, tid("b"))
	sk, id := d.GetWatermark()
	assert.Equal(t, int64(100), sk, "watermark stays at min(pending)=(100,'a')")
	assert.Equal(t, tid("a"), id)

	// Remove lowest
	d.removePending(100, tid("a"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(300), sk, "watermark advances to remaining min=(300,'c')")
	assert.Equal(t, tid("c"), id)

	// Remove last
	d.removePending(300, tid("c"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(300), sk, "watermark stays when pending set is empty")
	assert.Equal(t, tid("c"), id)
}

// ============================================================================
// Timer Batch Deleter: completedAbove tracking
// ============================================================================

func TestTimerDeleter_CompletedAboveWatermark_Tracked(t *testing.T) {
	d := newTestTimerDeleter(0, ids.TaskID{})

	d.InsertPending(100, tid("a"))
	d.InsertPending(200, tid("b"))
	d.InsertPending(300, tid("c"))

	// Remove highest first → should be tracked as above watermark
	d.removePending(300, tid("c"))
	d.removePending(200, tid("b"))

	d.mu.Lock()
	assert.Contains(t, d.completedAbove, tid("c"), "task (300,'c') completed above watermark (0,'')")
	assert.Contains(t, d.completedAbove, tid("b"), "task (200,'b') completed above watermark (0,'')")
	d.mu.Unlock()
}

// ============================================================================
// Immediate Batch Deleter: Interleaved insert and remove
// ============================================================================

func TestImmediateDeleter_InterleavedInsertAndRemove(t *testing.T) {
	d := newTestImmediateDeleter(0)

	// First batch
	d.InsertPending(10, tid("t10"))
	d.InsertPending(20, tid("t20"))

	d.removePending(10, tid("t10"))
	assert.Equal(t, int64(19), d.GetWatermark())

	// Second batch arrives while first is still processing
	d.InsertPending(30, tid("t30"))
	d.InsertPending(40, tid("t40"))

	d.removePending(20, tid("t20"))
	assert.Equal(t, int64(29), d.GetWatermark(), "watermark advances to new min-1 after old tasks complete")

	d.removePending(30, tid("t30"))
	assert.Equal(t, int64(39), d.GetWatermark())

	d.removePending(40, tid("t40"))
	assert.Equal(t, int64(39), d.GetWatermark(), "watermark stays when pending is empty")
	assert.Equal(t, 0, d.pendingSet.Len())
}

// ============================================================================
// Worker Pool: Context cancellation during retry
// ============================================================================

func TestWorkerPool_ContextCancelled_DuringRetry(t *testing.T) {
	handler := &retriableFailingTaskHandler{}
	longRetry := backoff.RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		TotalTimeout:    1 * time.Hour,
	}
	wp := NewWorkerPool(1, 10*time.Second, longRetry, longRetry, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	wp.Start(ctx)

	doneCh := make(chan TaskCompletion, 1)
	wp.Submit(&TaskItem{
		ShardID: 0,
		Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: ids.TaskID{}, SortKey: 1,
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
		}},
		DoneCh:  doneCh,
		TaskKey: TaskCompletion{SortKey: 1, ID: tid("t1")},
	})

	// Let a few retries happen, then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Pool should stop without hanging
	stopped := make(chan struct{})
	go func() {
		wp.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker pool should stop promptly after context cancellation")
	}
}

// ============================================================================
// Worker Pool: Shutdown drains in-flight tasks
// ============================================================================

func TestWorkerPool_ShutdownDrainsInFlightTasks(t *testing.T) {
	var processed atomic.Int32
	handler := &atomicCountingTaskHandler{count: &processed}
	wp := NewWorkerPool(1, 10*time.Second, config.DefaultTaskProcessorConfig().ImmediateTaskRetryPolicy, config.DefaultTaskProcessorConfig().TimerTaskRetryPolicy, handler, &noopDLQStore{}, "test", log.NewNoop())
	ctx, cancel := context.WithCancel(context.Background())
	wp.Start(ctx)

	doneCh := make(chan TaskCompletion, 3)
	for i := int64(1); i <= 3; i++ {
		id := tid(fmt.Sprintf("t%d", i))
		wp.Submit(&TaskItem{
			ShardID: 0,
			Task: p.TaskRow{Immediate: &p.ImmediateTaskRow{
				ShardID: 0, ID: ids.TaskID{}, SortKey: i,
				TaskType: p.ImmediateTaskRunInitialDispatch,
				TaskInfo: p.ImmediateTaskInfo{RunID: "r1", Namespace: "ns", TaskListName: "g"},
			}},
			DoneCh:  doneCh,
			TaskKey: TaskCompletion{SortKey: i, ID: id},
		})
	}

	// Wait for at least 1 task to complete
	<-doneCh

	cancel()
	stopped := make(chan struct{})
	go func() {
		wp.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("pool should stop without hanging")
	}

	require.GreaterOrEqual(t, processed.Load(), int32(1), "at least 1 task should have been processed")
}

// ============================================================================
// Timer Batch Deleter: Empty pending set watermark stays at committed
// ============================================================================

func TestTimerDeleter_EmptyPendingSet_WatermarkStaysAtCommitted(t *testing.T) {
	d := newTestTimerDeleter(100, tid("committed-id"))
	sk, id := d.GetWatermark()
	assert.Equal(t, int64(100), sk)
	assert.Equal(t, tid("committed-id"), id)

	// Insert a single task and remove it. Watermark only advances on
	// removePending (not InsertPending). When the pending set becomes empty
	// after removal, advanceWatermarkLocked returns early, so watermark stays.
	d.InsertPending(200, tid("t200"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(100), sk, "InsertPending does not advance watermark")
	assert.Equal(t, tid("committed-id"), id)

	d.removePending(200, tid("t200"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(100), sk, "watermark stays when pending set becomes empty")
	assert.Equal(t, tid("committed-id"), id)

	// Insert two tasks. Remove the lower one — watermark advances to new min.
	// Remove the higher one — pending empty, watermark stays.
	d.InsertPending(300, tid("t300"))
	d.InsertPending(400, tid("t400"))
	d.removePending(300, tid("t300"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(400), sk, "watermark advances to remaining min(pending)")
	assert.Equal(t, tid("t400"), id)

	d.removePending(400, tid("t400"))
	sk, id = d.GetWatermark()
	assert.Equal(t, int64(400), sk, "watermark should not regress when pending empties")
	assert.Equal(t, tid("t400"), id)
}

// ============================================================================
// Additional handlers for edge-case tests
// ============================================================================

// retriableFailingTaskHandler always returns a retriable TimeoutError.
type retriableFailingTaskHandler struct{}

func (h *retriableFailingTaskHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	return errors.NewTimeoutError("retriable timeout for test", nil)
}

func (h *retriableFailingTaskHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	return errors.NewTimeoutError("retriable timeout for test", nil)
}

// transientFailHandler fails the first N calls with a retriable error, then succeeds.
type transientFailHandler struct {
	failCount int32
	calls     atomic.Int32
}

func (h *transientFailHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	n := h.calls.Add(1)
	if n <= h.failCount {
		return errors.NewTimeoutError("transient failure for test", nil)
	}
	return nil
}

func (h *transientFailHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	n := h.calls.Add(1)
	if n <= h.failCount {
		return errors.NewTimeoutError("transient failure for test", nil)
	}
	return nil
}

// selectiveSeqFailHandler fails tasks whose SortKey is in failSeqs.
type selectiveSeqFailHandler struct {
	mu       sync.Mutex
	failSeqs map[int64]bool
}

func (h *selectiveSeqFailHandler) HandleImmediateTask(_ context.Context, _ int32, task *p.ImmediateTaskRow) errors.CategorizedError {
	h.mu.Lock()
	shouldFail := h.failSeqs[task.SortKey]
	h.mu.Unlock()
	if shouldFail {
		return errors.NewInvalidInputError("selective failure for test", nil)
	}
	return nil
}

func (h *selectiveSeqFailHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	return nil
}

// atomicCountingTaskHandler counts calls using an atomic counter (thread-safe).
type atomicCountingTaskHandler struct {
	count *atomic.Int32
}

func (h *atomicCountingTaskHandler) HandleImmediateTask(_ context.Context, _ int32, _ *p.ImmediateTaskRow) errors.CategorizedError {
	h.count.Add(1)
	return nil
}

func (h *atomicCountingTaskHandler) HandleTimerTask(_ context.Context, _ int32, _ *p.TimerTaskRow) errors.CategorizedError {
	h.count.Add(1)
	return nil
}

// ============================================================================
// TaskSeq composition
// ============================================================================

func TestTaskSeq_Composition(t *testing.T) {
	rangeID := int32(1)
	localSeq := int32(1)
	seq := (int64(rangeID) << 32) | int64(localSeq)
	assert.Equal(t, int64(4294967297), seq) // (1<<32)+1

	rangeID2 := int32(2)
	localSeq2 := int32(0)
	seq2 := (int64(rangeID2) << 32) | int64(localSeq2)
	assert.Greater(t, seq2, seq, "higher rangeID should produce higher seq")
}

func TestTaskSeq_MonotonicAcrossRangeChange(t *testing.T) {
	// Range 1: last task
	lastSeq := (int64(1) << 32) | int64(math.MaxInt32-1)
	// Range 2: first task
	firstSeq := (int64(2) << 32) | int64(1)
	assert.Greater(t, firstSeq, lastSeq, "new range's first seq must exceed old range's last seq")
}

// ============================================================================
// Config validation
// ============================================================================

func TestConfigValidation_AttemptTimeoutVsLeaseExpiryBuffer(t *testing.T) {
	cfg := config.DefaultTaskProcessorConfig()

	assert.Nil(t, cfg.Validate(5*time.Second), "AttemptTimeout 4s <= LeaseExpiryBuffer 5s should pass")
	assert.Nil(t, cfg.Validate(4*time.Second), "AttemptTimeout 4s <= LeaseExpiryBuffer 4s should pass (equal is ok)")
	assert.NotNil(t, cfg.Validate(3*time.Second), "AttemptTimeout 4s > LeaseExpiryBuffer 3s should fail")
}

// ============================================================================
// Helpers
// ============================================================================

func newTestImmediateDeleter(committedOffset int64) *ImmediateBatchDeleter {
	return NewImmediateBatchDeleter(
		0, config.DefaultTaskProcessorConfig(), nil, nil,
		log.NewNoop(), make(chan struct{}), committedOffset,
	)
}

func newTestTimerDeleter(committedSortKey int64, committedID ids.TaskID) *TimerBatchDeleter {
	return NewTimerBatchDeleter(
		0, config.DefaultTaskProcessorConfig(), nil, nil,
		log.NewNoop(), make(chan struct{}), committedSortKey, committedID,
	)
}

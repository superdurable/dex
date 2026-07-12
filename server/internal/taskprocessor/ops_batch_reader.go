package taskprocessor

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/backoff"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/historynotify"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// OpsBatchReader is the per-shard FIFO reader that drains the OpsFIFOTaskRow
// outbox into the visibility + history downstream stores. Unlike the
// immediate / timer readers, the OpsBatchReader executes batches INLINE in
// its own goroutine — there is no worker pool. This preserves strict FIFO
// per shard (and therefore per run, since a run is always on a single
// shard), which is required because skipping a task would let later tasks
// for the same run be applied out of order (e.g. a RunStop event landing
// before a step-completed event, or a stale visibility row overwriting a
// fresh one).
//
// Loop summary:
//  1. Read a batch (up to OpsBatchReadLimit). If non-empty, process inline
//     and immediately loop back to read again — this drains any backlog
//     without artificial delay.
//  2. If the read was empty, wait for a NotifyNewOpsFIFOTask signal or the
//     OpsPollInterval safety-net fallback.
//  3. After receiving a signal (NOT after the fallback poll), sleep
//     OpsBatchReadDelay so concurrent writers have time to land their
//     OpsTasks in the SAME upcoming read — this is the deliberate
//     coalescing the user spec'd. The fallback poll path skips the
//     delay since the lost-signal case it covers is already past
//     deserving more latency.
//
// Failure handling: see "No DLQ + skip" in docs/ops-fifo-queue-design.md.
// The retry loop is bounded only by shard-lease cancellation.
type OpsBatchReader struct {
	shardID         int32
	cfg             config.TaskProcessorConfig
	runStore        p.RunStore
	historyStore    p.HistoryStore
	visibilityStore p.VisibilityStore
	historyNotifier historynotify.NotifierManager
	deleter         *OpsBatchDeleter
	sm              shardmanager.ShardManager
	logger          log.Logger
	shutdownCh      <-chan struct{}
	newOpsFIFOCh    <-chan struct{}
	lastSeq         int64
}

func NewOpsBatchReader(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	historyStore p.HistoryStore,
	visibilityStore p.VisibilityStore,
	historyNotifier historynotify.NotifierManager,
	deleter *OpsBatchDeleter,
	sm shardmanager.ShardManager,
	logger log.Logger,
	shutdownCh <-chan struct{},
	initialSeq int64,
	newOpsFIFOCh <-chan struct{},
) *OpsBatchReader {
	return &OpsBatchReader{
		shardID:         shardID,
		cfg:             cfg,
		runStore:        runStore,
		historyStore:    historyStore,
		visibilityStore: visibilityStore,
		historyNotifier: historyNotifier,
		deleter:         deleter,
		sm:              sm,
		logger:          logger,
		shutdownCh:      shutdownCh,
		newOpsFIFOCh:    newOpsFIFOCh,
		lastSeq:         initialSeq,
	}
}

func (r *OpsBatchReader) Run(ctx context.Context) {
	r.logger.Info("Starting OpsFIFO batch reader", tag.Shard(r.shardID))
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			r.logger.Info("OpsFIFO batch reader shutting down", tag.Shard(r.shardID))
			return
		default:
		}

		tasks := r.readBatch(ctx)
		if len(tasks) > 0 {
			// Process immediately, then loop back to read again — this
			// drains any backlog without artificial delay.
			r.observeFIFOLag(tasks)
			r.processBatch(ctx, tasks)
			continue
		}
		// Idle: wait for a new-task signal (then debounce so we coalesce
		// follow-up writes) or fall back to the safety-net poll.
		if !r.waitForWork(ctx) {
			return
		}
	}
}

// waitForWork blocks until either:
//   - a new-task signal arrives, after which we sleep OpsBatchReadDelay so
//     concurrent writers can land more OpsTasks for the next read (this is
//     the deliberate coalescing optimization); or
//   - OpsPollInterval elapses (safety-net fallback for lost signals — see
//     ops_batch_reader.go config comment); or
//   - ctx / shutdownCh fires.
//
// Returns false iff ctx / shutdownCh fired during the wait so the caller
// breaks out of the main loop.
func (r *OpsBatchReader) waitForWork(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-r.shutdownCh:
		return false
	case <-r.newOpsFIFOCh:
		// Got a signal. Sleep the debounce so any concurrent / follow-up
		// OpsTask writes have time to land in the same upcoming read.
		select {
		case <-ctx.Done():
			return false
		case <-r.shutdownCh:
			return false
		case <-time.After(r.cfg.OpsBatchReadDelay):
		}
	case <-time.After(r.cfg.OpsPollInterval):
		// Fallback poll. Don't add the debounce on top: this path only
		// fires when a signal was lost, and we don't want to penalize
		// the recovery latency further.
	}
	return true
}

// readBatch issues a single RangeReadOpsFIFOTasks. Returns nil on read
// error after sleeping briefly so a persistent failure doesn't spin the
// CPU; the main loop's next iteration will retry the read.
func (r *OpsBatchReader) readBatch(ctx context.Context) []*p.OpsFIFOTaskRow {
	cappedCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
	defer cancel()

	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueOpsFIFO)
	tasks, err := r.runStore.RangeReadOpsFIFOTasks(cappedCtx, r.shardID, r.lastSeq, r.cfg.OpsBatchReadLimit)
	if err != nil {
		metrics.CounterBatchReadFailed.Inc(kindTag)
		r.logger.Error("Failed to read OpsFIFO tasks", tag.Shard(r.shardID), tag.Error(err))
		select {
		case <-ctx.Done():
		case <-r.shutdownCh:
		case <-time.After(r.cfg.OpsBatchReadDelay):
		}
		return nil
	}
	metrics.CounterBatchReadSuccess.Inc(kindTag)
	if len(tasks) > 0 {
		metrics.HistogramOpsTaskBatchSize.Record(float64(len(tasks)))
		metrics.HistogramBatchReadCount.Record(float64(len(tasks)), kindTag)
	}
	return tasks
}

// processBatch splits the batch by TaskType, merges visibility entries by
// run_id, and runs the two downstream batch writes INDEFINITELY until both
// succeed (via the backoff package). On success advances lastSeq and the
// deleter's committed offset.
//
// Returns when (a) the batch fully succeeds, or (b) ctx / shutdownCh fires
// during a retry — in case (b) the offset is NOT advanced so the next
// owner (or this owner on restart) replays the same batch. Both batch APIs
// are idempotent on replay (visibility upsert by run_id; history insert
// with continue-on-duplicate-key).
func (r *OpsBatchReader) processBatch(ctx context.Context, tasks []*p.OpsFIFOTaskRow) {
	historyEvents, visibilityEntries := splitOpsBatch(tasks)
	visibilityEntries = mergeVisibilityByRunID(visibilityEntries)

	// Merge shutdownCh into ctx so backoff.Do's internal select exits on
	// shard-lease loss as well as parent-ctx cancellation.
	retryCtx, cancel := shutdownAwareContext(ctx, r.shutdownCh)
	defer cancel()

	// Use the standard backoff retrier with AlwaysRetry: any error from
	// the operation triggers another attempt. The OpsTaskRetryPolicy
	// ships TotalTimeout=0 + MaximumAttempts=0 (infinite), so the loop
	// only exits on success or retryCtx cancellation.
	retrier := backoff.NewRetry(
		backoff.WithRetryPolicy(&r.cfg.OpsTaskRetryPolicy),
		backoff.WithRetryableError(backoff.AlwaysRetry),
	)
	attempt := 0
	doErr := retrier.Do(retryCtx, func(opCtx context.Context) error {
		if r.tryWriteBatch(opCtx, historyEvents, visibilityEntries) {
			return nil
		}
		attempt++
		metrics.CounterOpsTaskBatchStuck.Inc()
		if r.cfg.OpsBatchStuckWarnEvery > 0 && attempt%r.cfg.OpsBatchStuckWarnEvery == 0 {
			r.logger.Warn("OpsFIFO batch stuck, retrying",
				tag.Shard(r.shardID),
				tag.Attempt(attempt),
				tag.Count(len(tasks)))
		}
		return errOpsBatchTransient
	})
	if doErr != nil {
		// retryCtx was canceled (parent ctx done OR shard shutdown). Do
		// NOT advance the offset — the next owner replays this batch
		// from OpsFIFOTaskCommittedSeq.
		return
	}
	// Success: advance committed offset to max SortKey of the batch.
	// tasks is ordered ASC by SortKey (RangeReadOpsFIFOTasks contract),
	// so the last element has the max.
	maxSeq := tasks[len(tasks)-1].SortKey
	r.deleter.SetCommittedSeq(maxSeq)
	r.lastSeq = maxSeq
}

// errOpsBatchTransient is a sentinel value returned to backoff.Do after a
// failed batch attempt so AlwaysRetry triggers another iteration. The
// underlying per-store errors are already logged inside writeHistoryBatch /
// writeVisibilityBatch, so we don't need to propagate them here.
var errOpsBatchTransient = stderrors.New("ops batch transient failure")

// tryWriteBatch issues the two independent downstream batch writes. Returns
// true iff both succeeded. On any failure, the caller retries the WHOLE
// batch (both sub-batches) — the writes are idempotent so retrying after
// a partial success is safe (visibility upsert is a no-op if state
// matches; history insert is dedup-on-unique-key).
func (r *OpsBatchReader) tryWriteBatch(ctx context.Context, history []p.HistoryEvent, visibility []p.VisibilityEntry) bool {
	cappedCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
	defer cancel()

	historyOK := r.writeHistoryBatch(cappedCtx, history)
	visibilityOK := r.writeVisibilityBatch(cappedCtx, visibility)
	return historyOK && visibilityOK
}

func (r *OpsBatchReader) writeHistoryBatch(ctx context.Context, events []p.HistoryEvent) bool {
	if len(events) == 0 {
		return true
	}
	target := metrics.TagOpsFIFOTaskTarget(metrics.OpsFIFOTaskTargetHistory)
	since := time.Now()
	if err := r.historyStore.BatchInsertHistory(ctx, events); err != nil {
		r.logger.Error("OpsFIFO history batch failed", tag.Shard(r.shardID), tag.Error(err))
		return false
	}
	metrics.CounterOpsTaskBatchExecuted.Inc(target)
	metrics.LatencyOpsTaskBatchExecution.Record(time.Since(since), target)
	// Hand the just-inserted events to the notifier; it derives each run's tip
	// and closed/terminal status and wakes WaitForHistoryEvent waiters.
	if r.historyNotifier != nil {
		r.historyNotifier.NotifyEventsWritten(events)
	}
	return true
}

func (r *OpsBatchReader) writeVisibilityBatch(ctx context.Context, entries []p.VisibilityEntry) bool {
	if len(entries) == 0 {
		return true
	}
	target := metrics.TagOpsFIFOTaskTarget(metrics.OpsFIFOTaskTargetVisibility)
	since := time.Now()
	if err := r.visibilityStore.BatchUpsertVisibility(ctx, entries); err != nil {
		r.logger.Error("OpsFIFO visibility batch failed", tag.Shard(r.shardID), tag.Error(err))
		return false
	}
	metrics.CounterOpsTaskBatchExecuted.Inc(target)
	metrics.LatencyOpsTaskBatchExecution.Record(time.Since(since), target)
	return true
}

// observeFIFOLag samples the lag between now and the earliest pending task's
// created_at. Recorded once per batch (cheap; one Time.Sub call). When the
// OpsFIFO falls behind run state this number climbs, and the operator dash
// alert fires from this metric.
func (r *OpsBatchReader) observeFIFOLag(tasks []*p.OpsFIFOTaskRow) {
	if len(tasks) == 0 {
		return
	}
	earliest := tasks[0].CreatedAt
	if earliest.IsZero() {
		return
	}
	metrics.LatencyOpsTaskFIFOLag.Record(time.Since(earliest))
}

// splitOpsBatch separates the tasks by TaskType. Preserves SortKey order
// within each group (the input is already sorted ASC).
func splitOpsBatch(tasks []*p.OpsFIFOTaskRow) ([]p.HistoryEvent, []p.VisibilityEntry) {
	var history []p.HistoryEvent
	var visibility []p.VisibilityEntry
	for _, t := range tasks {
		switch t.TaskType {
		case p.OpsFIFOTaskHistoryWrite:
			if t.HistoryPayload != nil {
				history = append(history, *t.HistoryPayload)
			}
		case p.OpsFIFOTaskVisibilityWrite:
			if t.VisibilityPayload != nil {
				visibility = append(visibility, *t.VisibilityPayload)
			}
		}
	}
	return history, visibility
}

// mergeVisibilityByRunID folds visibility entries for the same run_id into a
// single upsert. Mutable fields (status, updated_at, task_list_name, flow_type)
// come from the LATEST entry in the input order; start_time comes from the
// EARLIEST entry that has it set, since the run's start time is immutable.
//
// This is a write-side optimization that turns N status updates per run
// into a single Mongo UpdateOne and avoids upsert ordering hazards for the
// rare case of two entries within one batch landing for the same run.
func mergeVisibilityByRunID(entries []p.VisibilityEntry) []p.VisibilityEntry {
	if len(entries) <= 1 {
		return entries
	}
	type slot struct {
		merged p.VisibilityEntry
		index  int // first-occurrence index in the output slice
	}
	byRun := make(map[string]*slot, len(entries))
	out := make([]p.VisibilityEntry, 0, len(entries))
	for i := range entries {
		e := entries[i]
		key := e.Namespace + "/" + e.RunID
		if existing, ok := byRun[key]; ok {
			// Latest wins for mutable fields. e is later than existing
			// because we iterate in input order.
			merged := existing.merged
			merged.Status = e.Status
			merged.UpdatedAt = e.UpdatedAt
			if e.FlowType != "" {
				merged.FlowType = e.FlowType
			}
			if e.TaskListName != "" {
				merged.TaskListName = e.TaskListName
			}
			// start_time: keep the earliest non-zero (existing was first
			// in the input, so it wins unless it's zero and e isn't).
			if existing.merged.StartTime.IsZero() && !e.StartTime.IsZero() {
				merged.StartTime = e.StartTime
			}
			existing.merged = merged
			out[existing.index] = merged
			continue
		}
		out = append(out, e)
		byRun[key] = &slot{merged: e, index: len(out) - 1}
	}
	return out
}

// shutdownAwareContext returns a context that cancels when EITHER the parent
// ctx is canceled OR shutdownCh is closed. Used to merge shard-lease
// cancellation into the context passed to backoff.Do so the retry loop
// breaks out cleanly on shard release. Caller MUST call the returned
// cancel to reap the helper goroutine.
func shutdownAwareContext(parent context.Context, shutdownCh <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-shutdownCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

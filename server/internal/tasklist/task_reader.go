package tasklist

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
)

// taskReader fetches DB-persisted tasks and pushes them into the matcher's
// bufferedCh. The matcher owns the buffer and serves tasks to its single
// Poll consumer; the reader is just the producer side.
//
// Design:
//   - Single goroutine drains tasks from DB into matcherBufferCh.
//   - Triggered by Signal() (called by writer after a successful CreateTasks)
//     or by an internal periodic timer (UpdateAckInterval) for safety net.
//   - Uses writer.GetMaxReadableTaskID() as the upper bound so it never
//     reads past committed data — avoids zero-result DB calls when the
//     tasklist is idle.
//   - Tracks readLevel (cursor of last task fetched) and ackLevel (which
//     persists periodically via db.UpdateAckLevel).
//   - On startup, initializes readLevel from the persisted ackLevel.
//
// Push-back protocol:
//   - The PollForRun handler pushes failed-delivery tasks back into
//     matcherBufferCh via PushBack (blocks until buffered or reader
//     stops). The pendingSet entry stays so the watermark won't advance
//     past the failed task until it's successfully delivered.
//
// Ownership-loss detection:
//   - If UpdateAckLevel fails with a Conflict (rangeID fence violation),
//     the reader calls onFatalErr to trigger manager self-eviction. This
//     covers the read-heavy case where the writer would never naturally
//     hit a fence failure.
type taskReader struct {
	id     *Identifier
	db     *tasklistDB
	writer *taskWriter
	cfg    config.MatchingServiceConfig
	logger log.Logger

	// matcherBufferCh is the channel owned by the matcher; the reader
	// pushes async-pickup tasks here. Set via constructor; never nil.
	matcherBufferCh chan<- *Task

	// pendingSet receives Insert(taskID) for every task pushed into the
	// matcher's buffer (so the watermark can be computed as min - 1).
	// Set via constructor; never nil.
	pendingSet *pendingSet

	// notifyCh wakes the reader loop. Buffered(1); senders never block —
	// if a notification is already queued, additional sends are coalesced
	// (the reader will consume the latest state when it next runs).
	notifyCh chan struct{}

	stopCh  chan struct{}
	doneCh  chan struct{}
	stopped atomic.Bool

	// readLevel is the cursor: tasks with task_id <= readLevel have been
	// fetched into matcherBufferCh (or are no longer available, e.g. after
	// a new owner skipped over a previous owner's range gap).
	readLevel atomic.Int64

	// onFatalErr is invoked when a fence error is observed during ack
	// persistence. The manager wires this up to manager.Stop(). May be
	// nil during testing.
	onFatalErr func(errors.CategorizedError)
}

func newTaskReader(
	id *Identifier,
	db *tasklistDB,
	writer *taskWriter,
	pendingSet *pendingSet,
	cfg config.MatchingServiceConfig,
	logger log.Logger,
	matcherBufferCh chan<- *Task,
	onFatalErr func(errors.CategorizedError),
) *taskReader {
	return &taskReader{
		id:              id,
		db:              db,
		writer:          writer,
		pendingSet:      pendingSet,
		cfg:             cfg,
		logger:          logger.WithTags(tag.Namespace(id.Namespace()), tag.TaskListName(id.FullName())),
		matcherBufferCh: matcherBufferCh,
		notifyCh:        make(chan struct{}, 1),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		onFatalErr:      onFatalErr,
	}
}

// Start initializes readLevel from the persisted ackLevel and launches
// the reader goroutine.
func (r *taskReader) Start(_ context.Context) {
	r.readLevel.Store(r.db.AckLevel())
	r.logger.Debugf("taskReader.Start: initial readLevel=%d (from persisted ackLevel)", r.db.AckLevel())
	go r.loop()
}

// Stop signals the reader goroutine to exit and waits for it. Idempotent.
func (r *taskReader) Stop() {
	if !r.stopped.CompareAndSwap(false, true) {
		return
	}
	close(r.stopCh)
	<-r.doneCh
}

// Signal wakes the reader loop to check for new tasks. Non-blocking and
// coalescing: if a signal is already queued, additional Signal() calls
// are no-ops until the reader processes the queued one. Called by the
// writer after a successful CreateTasks (so the reader picks up newly
// committed tasks promptly).
func (r *taskReader) Signal() {
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

// PushBack re-enqueues a task that failed delivery. Blocks until the task
// is in matcherBufferCh or stopCh fires (reader shutdown). readLevel
// already covers this taskID, so abandoning the push would lose it.
func (r *taskReader) PushBack(task *Task) {
	select {
	case r.matcherBufferCh <- task:
	case <-r.stopCh:
		r.logger.Debug("PushBack abandoned on reader stop", tag.TaskID(strconv.FormatInt(task.taskID, 10)))
	}
}

// loop is the reader goroutine. Wakes on notifyCh signals or the periodic
// UpdateAckInterval timer. Each cycle: fetch new tasks from DB (bounded
// by maxReadableTaskID), push to matcherBufferCh, advance readLevel.
func (r *taskReader) loop() {
	defer close(r.doneCh)

	updateAckInterval := r.cfg.UpdateAckInterval
	if updateAckInterval <= 0 {
		updateAckInterval = 10 * time.Second
	}
	ackTicker := time.NewTicker(updateAckInterval)
	defer ackTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-r.notifyCh:
			r.fetchAndBuffer()
		case <-ackTicker.C:
			// Periodic safety-net fetch (catches missed signals) +
			// triggers ackLevel persist + GC.
			r.fetchAndBuffer()
			r.persistAckLevelAndGC()
		}
	}
}

// fetchAndBuffer reads tasks from DB up to the writer's current
// maxReadableTaskID and pushes them into matcherBufferCh. If readLevel
// is already at maxReadable, it returns immediately (no DB call) — this
// is the key optimization that avoids spinning on idle tasklists.
func (r *taskReader) fetchAndBuffer() {
	maxReadable := r.writer.GetMaxReadableTaskID()
	current := r.readLevel.Load()
	if current >= maxReadable {
		r.logger.Debugf("taskReader.fetchAndBuffer: caught up, readLevel=%d >= maxReadable=%d (no fetch)", current, maxReadable)
		return
	}
	r.logger.Debugf("taskReader.fetchAndBuffer: fetching (readLevel=%d, maxReadable=%d]", current, maxReadable)

	batchSize := r.cfg.MaxTaskReadBatchSize
	if batchSize < 1 {
		batchSize = 100
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.OperationTimeout)
	defer cancel()

	rows, err := r.db.GetTasks(ctx, current, maxReadable, batchSize)
	if err != nil {
		r.logger.Warn("taskReader: GetTasks failed", tag.Error(err))
		return
	}
	if len(rows) == 0 {
		// No new rows in (current, maxReadable]. Advance readLevel to
		// avoid re-scanning this empty range next time. Safe because
		// any newly committed task (after this call) will have
		// task_id > maxReadable.
		r.readLevel.Store(maxReadable)
		return
	}

	r.logger.Debugf("taskReader: fetched batch size=%d first_id=%d last_id=%d", len(rows), rows[0].TaskID, rows[len(rows)-1].TaskID)

	for _, row := range rows {
		task := newAsyncPickupTask(row.RunID, r.id.Namespace(), row.ShardID, row.TaskID)
		// Track in pendingSet BEFORE pushing so the watermark always
		// reflects this task's existence.
		r.pendingSet.Insert(task.taskID)
		// Blocking send: if the buffer is full, this provides natural
		// backpressure — the reader stops fetching until the matcher's
		// Poll consumer drains.
		select {
		case r.matcherBufferCh <- task:
			r.logger.Debug("taskReader: buffered async task", tag.RunID(row.RunID), tag.Shard(row.ShardID))
		case <-r.stopCh:
			return
		}
	}

	r.readLevel.Store(rows[len(rows)-1].TaskID)

	// If the batch was full, more tasks may exist. Re-signal ourselves
	// so we run another fetch cycle without waiting for an external Signal.
	if len(rows) == batchSize {
		r.Signal()
	}
}

// persistAckLevelAndGC writes the current watermark to the DB metadata
// row (fenced via cached rangeID) and triggers GC if size/time thresholds
// are met. On a Conflict (rangeID fence violation), triggers onFatalErr
// so the manager self-evicts even on a read-heavy tasklist where the
// writer would never naturally fail.
func (r *taskReader) persistAckLevelAndGC() {
	wm := r.pendingSet.Watermark()
	currentAck := r.db.AckLevel()
	r.logger.Debugf("taskReader.persistAckLevelAndGC: watermark=%d currentAck=%d willPersist=%v", wm, currentAck, wm > currentAck)
	if wm > currentAck {
		ctx, cancel := context.WithTimeout(context.Background(), r.cfg.OperationTimeout)
		err := r.db.UpdateAckLevel(ctx, wm)
		cancel()
		if err != nil {
			r.logger.Warn("taskReader: UpdateAckLevel failed", tag.Error(err), tag.AckLevel(wm))
			if err.IsConflictError() && r.onFatalErr != nil {
				r.onFatalErr(err)
				return
			}
		}
	}
	r.pendingSet.MaybeGC(context.Background())
}

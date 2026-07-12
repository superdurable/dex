package tasklist

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// writeTaskRequest is enqueued to the writer's appendCh by AppendTask.
// The writer goroutine drains the channel and batches multiple requests
// into a single CreateTasks DB call. The response channel returns either
// the assigned taskID or an error.
type writeTaskRequest struct {
	runID   string
	shardID int32
	respCh  chan writeTaskResponse
}

type writeTaskResponse struct {
	taskID int64
	err    errors.CategorizedError
}

// taskWriter performs single-goroutine batched INSERTs of dispatch tasks.
//
// Design (mirrors Cadence taskWriter):
//   - All AppendTask calls enqueue to a buffered appendCh; one writer
//     goroutine drains opportunistically (non-blocking, no time-based
//     batching window — DB latency itself creates the natural batch
//     window).
//   - Each batch:
//     1. Allocates contiguous taskIDs by incrementing localSeq atomically
//     within the goroutine.
//     2. Calls db.CreateTasks (fenced multi-doc transaction).
//     3. On success: atomic.Store(maxReadableTaskID, lastTaskID).
//     4. Sends per-request responses with their assigned taskIDs.
//
// Concurrency:
//   - Writer goroutine is the only writer to localSeq and maxReadableTaskID
//     (atomic.Store after batch commits so reader sees committed values).
//   - Callers can invoke AppendTask concurrently; appendCh serializes them.
//
// Failure model:
//   - Fence error (rangeID mismatch) → fatal: writer calls onFatalErr
//     which triggers manager.Stop(). All pending requests fail with the
//     fence error.
//   - localSeq overflow (int32 > MaxInt32) → panic. Same pattern as
//     shard_manager.go (assumes int32 is enough; restart resets).
type taskWriter struct {
	id     *Identifier
	db     *tasklistDB
	cfg    config.MatchingServiceConfig
	logger log.Logger

	appendCh chan *writeTaskRequest
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopped  atomic.Bool

	// localSeq is the per-owner monotonic counter (lower 32 bits of taskID).
	// Reset on each ClaimTasklist (because rangeID changed). Only written
	// by the writer goroutine.
	localSeq atomic.Int32

	// maxReadableTaskID is the largest taskID committed to DB. Reader uses
	// this as the upper bound for GetTasks to avoid wasted DB calls.
	// Atomic for cross-goroutine visibility (reader runs in its own goroutine).
	maxReadableTaskID atomic.Int64

	// onFatalErr is invoked when a fence error occurs. The manager wires
	// this up to manager.Stop(). May be nil during testing.
	onFatalErr func(errors.CategorizedError)
}

func newTaskWriter(
	id *Identifier,
	db *tasklistDB,
	cfg config.MatchingServiceConfig,
	logger log.Logger,
	onFatalErr func(errors.CategorizedError),
) *taskWriter {
	bufSize := cfg.MaxTaskWriteBatchSize
	if bufSize < 1 {
		bufSize = 100
	}
	// The appendCh buffer should be at least as large as MaxTaskWriteBatchSize
	// so a full batch can drain in one cycle without losing the slack.
	return &taskWriter{
		id:         id,
		db:         db,
		cfg:        cfg,
		logger:     logger.WithTags(tag.Namespace(id.Namespace()), tag.TaskListName(id.FullName())),
		appendCh:   make(chan *writeTaskRequest, bufSize*2),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		onFatalErr: onFatalErr,
	}
}

// Start initializes the writer's state from the current rangeID and
// launches the writer goroutine. Must be called after db.ClaimTasklist.
//
// Initial maxReadableTaskID is set to the start of the new range with
// localSeq=0; the first allocated taskID will have localSeq=1, so any
// readLevel <= encodeTaskID(rangeID, 0) safely covers "no tasks yet".
func (w *taskWriter) Start(_ context.Context) errors.CategorizedError {
	rangeID := w.db.RangeID()
	if rangeID <= 0 {
		return errors.NewInternalError(
			fmt.Sprintf("taskWriter.Start: tasklist not claimed yet (rangeID=%d)", rangeID),
			nil,
		)
	}
	w.localSeq.Store(0)
	w.maxReadableTaskID.Store(encodeTaskID(rangeID, 0))
	go w.loop()
	return nil
}

// Stop signals the writer goroutine to exit and waits for it to drain.
// All pending requests in appendCh are failed with errShutdown.
// Idempotent.
func (w *taskWriter) Stop() {
	if !w.stopped.CompareAndSwap(false, true) {
		return
	}
	close(w.stopCh)
	<-w.doneCh
}

// AppendTask enqueues a write request and blocks until the writer goroutine
// either commits the task (returns taskID) or fails (returns error).
// The caller's ctx provides cancellation; if the writer is stopped while
// the request is queued, returns errShutdown.
func (w *taskWriter) AppendTask(ctx context.Context, runID string, shardID int32) (int64, errors.CategorizedError) {
	if w.stopped.Load() {
		return 0, errors.NewUnavailableError("task writer stopped", nil)
	}
	req := &writeTaskRequest{
		runID:   runID,
		shardID: shardID,
		respCh:  make(chan writeTaskResponse, 1),
	}
	select {
	case w.appendCh <- req:
	case <-ctx.Done():
		return 0, errors.NewUnavailableError("AppendTask: context cancelled before enqueue", ctx.Err())
	case <-w.stopCh:
		return 0, errors.NewUnavailableError("AppendTask: writer stopped", nil)
	}
	select {
	case resp := <-req.respCh:
		return resp.taskID, resp.err
	case <-ctx.Done():
		return 0, errors.NewUnavailableError("AppendTask: context cancelled while waiting for write", ctx.Err())
	}
}

// GetMaxReadableTaskID returns the largest committed taskID. Used by
// the reader as the upper bound for GetTasks scans.
func (w *taskWriter) GetMaxReadableTaskID() int64 {
	return w.maxReadableTaskID.Load()
}

// loop is the writer goroutine. Drains appendCh, batches up to
// MaxTaskWriteBatchSize requests, and commits via db.CreateTasks.
func (w *taskWriter) loop() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			w.failPendingRequests()
			return
		case first := <-w.appendCh:
			w.processBatch(first)
		}
	}
}

// processBatch drains up to MaxTaskWriteBatchSize-1 additional requests
// non-blockingly, allocates taskIDs, calls CreateTasks, and dispatches
// responses.
func (w *taskWriter) processBatch(first *writeTaskRequest) {
	batch := []*writeTaskRequest{first}
	maxBatch := w.cfg.MaxTaskWriteBatchSize
	if maxBatch < 1 {
		maxBatch = 100
	}
drain:
	for len(batch) < maxBatch {
		select {
		case req := <-w.appendCh:
			batch = append(batch, req)
		default:
			break drain
		}
	}

	rangeID := w.db.RangeID()
	taskIDs, err := w.allocTaskIDs(rangeID, len(batch))
	if err != nil {
		w.failBatch(batch, err)
		return
	}

	rows := make([]*p.TasklistTaskRow, len(batch))
	now := time.Now()
	for i, req := range batch {
		rows[i] = &p.TasklistTaskRow{
			Namespace:    w.id.Namespace(),
			TasklistName: w.id.BaseName(),
			PartitionID:  w.id.Partition(),
			TaskID:       taskIDs[i],
			RunID:        req.runID,
			ShardID:      req.shardID,
			CreatedAt:    now,
		}
	}

	w.logger.Debugf("taskWriter: committing batch size=%d first_id=%d last_id=%d", len(batch), taskIDs[0], taskIDs[len(taskIDs)-1])
	dbCtx, dbCancel := context.WithTimeout(context.Background(), w.cfg.OperationTimeout)
	dbErr := w.db.CreateTasks(dbCtx, rows)
	dbCancel()
	if dbErr != nil {
		// Fence error → fatal. Trigger onFatalErr to stop the manager.
		// All pending requests in this batch fail with the same error.
		w.logger.Error("CreateTasks failed", tag.Error(dbErr))
		w.failBatch(batch, dbErr)
		if dbErr.IsConflictError() && w.onFatalErr != nil {
			w.onFatalErr(dbErr)
		}
		return
	}

	// DB write succeeded. Advance maxReadableTaskID atomically so the
	// reader sees the committed batch. Only after this Store can the
	// reader safely read these taskIDs.
	lastID := taskIDs[len(taskIDs)-1]
	w.maxReadableTaskID.Store(lastID)

	for i, req := range batch {
		w.logger.Debug("taskWriter: committed async task", tag.RunID(req.runID), tag.Shard(req.shardID))
		req.respCh <- writeTaskResponse{taskID: taskIDs[i]}
	}
}

// allocTaskIDs increments localSeq by N and returns the contiguous
// taskID slice. Panics on int32 overflow (same pattern as shard_manager).
func (w *taskWriter) allocTaskIDs(rangeID int32, n int) ([]int64, errors.CategorizedError) {
	taskIDs := make([]int64, n)
	for i := 0; i < n; i++ {
		seq := w.localSeq.Add(1)
		if seq == math.MaxInt32 {
			panic(fmt.Sprintf("taskWriter: localSeq overflow for tasklist %s — restart instance to reset", w.id.String()))
		}
		taskIDs[i] = encodeTaskID(rangeID, seq)
	}
	return taskIDs, nil
}

// failBatch sends an error response to every request in the batch.
func (w *taskWriter) failBatch(batch []*writeTaskRequest, err errors.CategorizedError) {
	for _, req := range batch {
		req.respCh <- writeTaskResponse{err: err}
	}
}

// failPendingRequests drains any remaining requests in appendCh after
// stopCh fires, failing them with errShutdown so callers don't block
// forever on respCh.
func (w *taskWriter) failPendingRequests() {
	shutdownErr := errors.NewUnavailableError("task writer stopped", nil)
	for {
		select {
		case req := <-w.appendCh:
			req.respCh <- writeTaskResponse{err: shutdownErr}
		default:
			return
		}
	}
}

// encodeTaskID combines a rangeID and localSeq into a globally-unique,
// monotonically increasing int64 taskID. Higher 32 bits = rangeID,
// lower 32 bits = localSeq.
func encodeTaskID(rangeID int32, localSeq int32) int64 {
	return (int64(rangeID) << 32) | int64(localSeq)
}

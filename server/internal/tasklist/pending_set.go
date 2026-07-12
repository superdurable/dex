package tasklist

import (
	"context"
	"sync"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/google/btree"
)

// pendingSet tracks taskIDs that have been read from DB but not yet
// successfully delivered to a worker. Backed by a BTree for O(log N)
// Min() — used to compute the watermark = min(pending) - 1.
//
// The watermark anchors GC: tasks with task_id <= watermark have all
// been delivered (no pending taskID is at or below this value), so
// they're safe to DELETE from DB.
//
// Lifecycle:
//   - Insert: called by reader when a task is pushed into taskBuffer.
//   - Ack: called by matcher after successful delivery (PollForRun returns).
//   - Watermark: computed each call as Min() - 1 (or last-known max
//     if the BTree is empty, since "all caught up").
//   - MaybeGC: invoked periodically; if size or time threshold met,
//     calls db.DeleteTasksLessThan(watermark, batchSize).
//   - ShutdownDrain: on shutdown, deletes any taskIDs that were acked
//     above the final watermark (matcher acked task X but task X-1 is
//     still pending; X is in completedAbove).
//
// Concurrency: safe for concurrent Insert/Ack/Watermark calls via
// internal mutex. MaybeGC is safe for concurrent invocation but only
// one GC pass runs at a time (atomic gate).
type pendingSet struct {
	id     *Identifier
	db     *tasklistDB
	cfg    config.MatchingServiceConfig
	logger log.Logger

	mu             sync.Mutex
	tree           *btree.BTreeG[int64]
	maxObserved    int64   // largest taskID ever inserted (for watermark when tree is empty)
	completedAbove []int64 // acked taskIDs > current watermark (still need explicit delete)
	gcRunning      bool
	lastGCTime     time.Time
	gcAckLevel     int64 // last ackLevel passed to GC (avoid duplicate calls)
}

func newPendingSet(
	id *Identifier,
	db *tasklistDB,
	cfg config.MatchingServiceConfig,
	logger log.Logger,
) *pendingSet {
	return &pendingSet{
		id:     id,
		db:     db,
		cfg:    cfg,
		logger: logger.WithTags(tag.Namespace(id.Namespace()), tag.TaskListName(id.FullName())),
		// Degree 32 is a reasonable default for our expected sizes (a few
		// hundred to a few thousand pending tasks per partition).
		tree: btree.NewG[int64](32, func(a, b int64) bool { return a < b }),
	}
}

// Insert adds a taskID to the pending set. Called by the reader when a
// task is pushed into the matcher's buffer. Must be called BEFORE the
// task becomes visible to consumers, so the watermark always reflects
// the correct lower bound.
func (p *pendingSet) Insert(taskID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tree.ReplaceOrInsert(taskID)
	if taskID > p.maxObserved {
		p.maxObserved = taskID
	}
}

// Ack marks a taskID as successfully delivered. The taskID is removed
// from the pending set; if it was the minimum, the watermark advances.
//
// If the acked taskID is above the current watermark (e.g. matcher acks
// task 5 but tasks 2 and 3 are still pending), it's added to
// completedAbove for shutdown cleanup.
func (p *pendingSet) Ack(taskID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tree.Has(taskID) {
		// Already acked or never inserted — log and return.
		p.logger.Debugf("pendingSet: Ack of unknown taskID %d", taskID)
		return
	}
	p.tree.Delete(taskID)

	// If this taskID is above the new watermark, track it for shutdown
	// cleanup. (Watermark = new min - 1, so any pending entry below
	// taskID would mean taskID is "above the watermark.")
	min, hasMin := p.tree.Min()
	if hasMin && taskID > min {
		p.completedAbove = append(p.completedAbove, taskID)
	}
	wm := p.maxObserved
	if hasMin {
		wm = min - 1
	}
	p.logger.Debugf("pendingSet.Ack task_id=%d new_min=%d watermark=%d size=%d", taskID, min, wm, p.tree.Len())
}

// Watermark returns the largest taskID such that ALL tasks with id <= this
// value have been acked (or were never pending). Computed as min(tree)-1,
// or maxObserved if the tree is empty (all acked / never had any).
func (p *pendingSet) Watermark() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if min, ok := p.tree.Min(); ok {
		return min - 1
	}
	return p.maxObserved
}

// Size returns the number of currently pending taskIDs. Used by GC
// threshold logic.
func (p *pendingSet) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tree.Len()
}

// MaybeGC runs DeleteTasksLessThan(watermark, batchSize) if size or
// time threshold is met. Single-flight: concurrent calls return
// immediately if a GC pass is already running.
func (p *pendingSet) MaybeGC(ctx context.Context) {
	p.mu.Lock()
	if p.gcRunning {
		p.mu.Unlock()
		return
	}

	wm := int64(0)
	if min, ok := p.tree.Min(); ok {
		wm = min - 1
	} else {
		wm = p.maxObserved
	}
	if wm <= p.gcAckLevel {
		p.logger.Debugf("pendingSet.MaybeGC skip: watermark=%d <= gcAckLevel=%d (pinned by oldest pending task=%d)", wm, p.gcAckLevel, wm+1)
		p.mu.Unlock()
		return // nothing new to GC
	}

	maxBatch := p.cfg.MaxTaskDeleteBatchSize
	if maxBatch < 1 {
		maxBatch = 100
	}
	maxTimeBetween := p.cfg.MaxTimeBetweenTaskDeletes
	if maxTimeBetween <= 0 {
		maxTimeBetween = 1 * time.Minute
	}

	backlog := wm - p.gcAckLevel
	hitSize := backlog >= int64(maxBatch)
	hitTime := time.Since(p.lastGCTime) >= maxTimeBetween

	if !hitSize && !hitTime {
		p.logger.Debugf("pendingSet.MaybeGC skip: backlog=%d hitSize=%v hitTime=%v", backlog, hitSize, hitTime)
		p.mu.Unlock()
		return
	}
	p.logger.Debugf("pendingSet.MaybeGC running: watermark=%d gcAckLevel=%d backlog=%d", wm, p.gcAckLevel, backlog)

	p.gcRunning = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.gcRunning = false
		p.lastGCTime = time.Now()
		p.gcAckLevel = wm
		p.mu.Unlock()
	}()

	gcCtx, cancel := context.WithTimeout(ctx, p.cfg.OperationTimeout)
	defer cancel()

	deleted, err := p.db.DeleteTasksLessThan(gcCtx, wm, maxBatch)
	if err != nil {
		p.logger.Warn("pendingSet: DeleteTasksLessThan failed", tag.Error(err), tag.AckLevel(wm))
		return
	}
	if deleted > 0 {
		p.logger.Info("pendingSet: GC deleted tasks",
			tag.Count(deleted), tag.AckLevel(wm))
	} else {
		p.logger.Debugf("pendingSet.MaybeGC: DeleteTasksLessThan(<=%d) deleted 0 rows", wm)
	}
}

// ShutdownDrain deletes the completedAbove taskIDs explicitly — these
// were acked but their IDs are above the final watermark, so range-delete
// won't reach them. Best-effort: failures are logged but not retried
// (those tasks become "stale" rows in DB, picked up on next claim).
func (p *pendingSet) ShutdownDrain(ctx context.Context) {
	p.mu.Lock()
	ids := p.completedAbove
	p.completedAbove = nil
	p.mu.Unlock()

	if len(ids) == 0 {
		return
	}

	maxBatch := p.cfg.MaxTaskDeleteBatchSize
	if maxBatch < 1 {
		maxBatch = 100
	}

	for i := 0; i < len(ids); i += maxBatch {
		end := i + maxBatch
		if end > len(ids) {
			end = len(ids)
		}
		page := ids[i:end]
		dctx, cancel := context.WithTimeout(ctx, p.cfg.OperationTimeout)
		err := p.db.DeleteTasksByIDBatch(dctx, page)
		cancel()
		if err != nil {
			p.logger.Warn("pendingSet: ShutdownDrain DeleteTasksByIDBatch failed",
				tag.Error(err), tag.Count(len(page)))
			return // stop on first error; remaining ids become stale rows
		}
		p.logger.Info("pendingSet: ShutdownDrain deleted tasks", tag.Count(len(page)))
	}
}

package taskprocessor

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/google/btree"
)

// immediateTaskKey is a BTree item ordered by TaskSeq.
type immediateTaskKey struct {
	seq int64
	id  ids.TaskID // needed for shutdown DeleteByIDBatch
}

func immTaskLess(a, b immediateTaskKey) bool { return a.seq < b.seq }

// ImmediateBatchDeleter tracks in-flight immediate tasks using a BTree-based
// pending set. The watermark advances to min(pendingSet)-1 as tasks complete.
// Range deletes happen periodically below the watermark. On shutdown,
// completed-above-watermark tasks are cleaned up via DeleteByIDBatch.
type ImmediateBatchDeleter struct {
	shardID         int32
	cfg             config.TaskProcessorConfig
	runStore        p.RunStore
	sm              shardmanager.ShardManager
	logger          log.Logger
	shutdownCh      <-chan struct{}
	doneCh          chan TaskCompletion // receives completions from worker pool
	committedOffset int64

	mu             sync.Mutex
	pendingSet     *btree.BTreeG[immediateTaskKey]
	completedAbove []ids.TaskID // task IDs completed above watermark (for shutdown)
	watermark      int64
}

func NewImmediateBatchDeleter(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	sm shardmanager.ShardManager,
	logger log.Logger,
	shutdownCh <-chan struct{},
	committedOffset int64,
) *ImmediateBatchDeleter {
	return &ImmediateBatchDeleter{
		shardID:         shardID,
		cfg:             cfg,
		runStore:        runStore,
		sm:              sm,
		logger:          logger,
		shutdownCh:      shutdownCh,
		doneCh:          make(chan TaskCompletion, cfg.ImmediateBatchReadLimit),
		committedOffset: committedOffset,
		pendingSet:      btree.NewG[immediateTaskKey](16, immTaskLess),
		watermark:       committedOffset,
	}
}

// DoneCh returns the channel that the batch reader should pass to TaskItem
// so the worker pool can send completions.
func (d *ImmediateBatchDeleter) DoneCh() chan<- TaskCompletion {
	return d.doneCh
}

// InsertPending adds a task to the pending set. Called by the batch reader
// before submitting the task to the worker pool.
func (d *ImmediateBatchDeleter) InsertPending(seq int64, taskID ids.TaskID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pendingSet.ReplaceOrInsert(immediateTaskKey{seq: seq, id: taskID})
}

func (d *ImmediateBatchDeleter) removePending(seq int64, taskID ids.TaskID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pendingSet.Delete(immediateTaskKey{seq: seq, id: taskID})
	if seq > d.watermark {
		d.completedAbove = append(d.completedAbove, taskID)
	}
	d.logger.Debugf("ImmediateBatchDeleter shard=%d: removePending seq=%d taskID=%s pendingSize=%d watermark=%d",
		d.shardID, seq, taskID.String(), d.pendingSet.Len(), d.watermark)
	d.advanceWatermarkLocked()
}

// GetWatermark returns the current watermark for offset commit.
func (d *ImmediateBatchDeleter) GetWatermark() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermark
}

func (d *ImmediateBatchDeleter) advanceWatermarkLocked() {
	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueImmediate)
	metrics.GaugePendingSetSize.Record(int64(d.pendingSet.Len()), kindTag)

	min, ok := d.pendingSet.Min()
	if !ok {
		return
	}
	newWatermark := min.seq - 1
	if newWatermark > d.watermark {
		d.logger.Debugf("ImmediateBatchDeleter shard=%d: watermark advanced %d -> %d", d.shardID, d.watermark, newWatermark)
		d.watermark = newWatermark
		metrics.GaugeWatermark.Record(newWatermark, kindTag)
	}
}

// Run is the main loop: receives completions and periodically range-deletes.
func (d *ImmediateBatchDeleter) Run(ctx context.Context) {
	d.logger.Info("Starting immediate batch deleter", tag.Shard(d.shardID))

	deleteInterval := d.cfg.ImmediateDeleteInterval + time.Duration(rand.Int63n(int64(d.cfg.ImmediateDeleteIntervalJitter)))
	deleteTimer := time.NewTimer(deleteInterval)
	defer deleteTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.shutdownCh:
			d.logger.Info("Immediate batch deleter shutting down", tag.Shard(d.shardID))
			d.drainAndShutdown(ctx)
			return
		case tc := <-d.doneCh:
			d.removePending(tc.SortKey, tc.ID)
		case <-deleteTimer.C:
			d.rangeDelete(ctx)
			jitter := time.Duration(rand.Int63n(int64(d.cfg.ImmediateDeleteIntervalJitter)))
			deleteTimer.Reset(d.cfg.ImmediateDeleteInterval + jitter)
		}
	}
}

func (d *ImmediateBatchDeleter) rangeDelete(ctx context.Context) {
	d.mu.Lock()
	wm := d.watermark
	d.mu.Unlock()

	if wm <= d.committedOffset {
		return
	}

	cappedCtx, cancel := d.sm.GetCappedContext(ctx, d.shardID)
	defer cancel()

	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueImmediate)
	err := d.runStore.RangeDeleteImmediateTasks(cappedCtx, d.shardID, wm)
	if err != nil {
		metrics.CounterRangeDeleteFailed.Inc(kindTag)
		d.logger.Error("Failed to range-delete immediate tasks", tag.Shard(d.shardID), tag.Error(err))
		return
	}
	metrics.CounterRangeDeleteSuccess.Inc(kindTag)
	d.logger.Info("Range deleted immediate tasks", tag.Shard(d.shardID),
		tag.Watermark(wm))
}

// drainAndShutdown drains remaining completions from the channel, then deletes
// tasks that completed above the watermark using DeleteByIDBatch in pages.
func (d *ImmediateBatchDeleter) drainAndShutdown(ctx context.Context) {
	// Drain any remaining completions from the channel
	for {
		select {
		case tc := <-d.doneCh:
			d.removePending(tc.SortKey, tc.ID)
		default:
			goto drained
		}
	}
drained:

	d.mu.Lock()
	completedIDs := d.completedAbove
	d.completedAbove = nil
	d.mu.Unlock()

	if len(completedIDs) == 0 {
		return
	}

	batchSize := d.cfg.ShutdownDeleteBatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	for i := 0; i < len(completedIDs); i += batchSize {
		end := i + batchSize
		if end > len(completedIDs) {
			end = len(completedIDs)
		}
		page := completedIDs[i:end]

		cappedCtx, cancel := d.sm.GetCappedContext(ctx, d.shardID)
		err := d.runStore.DeleteImmediateTasksByIDBatch(cappedCtx, d.shardID, page)
		cancel()

		if err != nil {
			d.logger.Error("Shutdown: failed to delete immediate tasks by ID batch",
				tag.Shard(d.shardID), tag.Error(err))
			return
		}
		d.logger.Info("Shutdown batch delete immediate tasks",
			tag.Shard(d.shardID), tag.Count(len(page)))
	}
}

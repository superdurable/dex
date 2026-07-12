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

// timerTaskKey is a BTree item ordered by (SortKey, ID).
type timerTaskKey struct {
	sortKey int64
	id      ids.TaskID
}

func timerTaskLess(a, b timerTaskKey) bool {
	if a.sortKey != b.sortKey {
		return a.sortKey < b.sortKey
	}
	return a.id.Compare(b.id) < 0
}

// TimerBatchDeleter tracks in-flight timer tasks using a BTree-based pending set.
type TimerBatchDeleter struct {
	shardID          int32
	cfg              config.TaskProcessorConfig
	runStore         p.RunStore
	sm               shardmanager.ShardManager
	logger           log.Logger
	shutdownCh       <-chan struct{}
	doneCh           chan TaskCompletion
	committedSortKey int64
	committedID      ids.TaskID

	mu               sync.Mutex
	pendingSet       *btree.BTreeG[timerTaskKey]
	completedAbove   []ids.TaskID // task IDs completed at or above watermark (not covered by range delete)
	watermarkSortKey int64
	watermarkID      ids.TaskID
}

func NewTimerBatchDeleter(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	sm shardmanager.ShardManager,
	logger log.Logger,
	shutdownCh <-chan struct{},
	committedSortKey int64,
	committedID ids.TaskID,
) *TimerBatchDeleter {
	return &TimerBatchDeleter{
		shardID:          shardID,
		cfg:              cfg,
		runStore:         runStore,
		sm:               sm,
		logger:           logger,
		shutdownCh:       shutdownCh,
		doneCh:           make(chan TaskCompletion, cfg.TimerBatchReadLimit),
		committedSortKey: committedSortKey,
		committedID:      committedID,
		pendingSet:       btree.NewG[timerTaskKey](16, timerTaskLess),
		watermarkSortKey: committedSortKey,
		watermarkID:      committedID,
	}
}

// DoneCh returns the channel for the batch reader to pass to TaskItem.
func (d *TimerBatchDeleter) DoneCh() chan<- TaskCompletion {
	return d.doneCh
}

// InsertPending adds a task to the pending set.
func (d *TimerBatchDeleter) InsertPending(sortKey int64, id ids.TaskID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pendingSet.ReplaceOrInsert(timerTaskKey{sortKey: sortKey, id: id})
}

func (d *TimerBatchDeleter) removePending(sortKey int64, id ids.TaskID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pendingSet.Delete(timerTaskKey{sortKey: sortKey, id: id})
	// Track tasks completed at or above the watermark for shutdown cleanup.
	// Range delete uses exclusive upper bound (< watermark), so tasks AT the
	// watermark position are not covered and must be individually deleted.
	removedKey := timerTaskKey{sortKey: sortKey, id: id}
	wmKey := timerTaskKey{sortKey: d.watermarkSortKey, id: d.watermarkID}
	if !timerTaskLess(removedKey, wmKey) {
		d.completedAbove = append(d.completedAbove, id)
	}
	d.advanceWatermarkLocked()
}

// GetWatermark returns the current watermark for offset commit.
func (d *TimerBatchDeleter) GetWatermark() (sortKey int64, id ids.TaskID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermarkSortKey, d.watermarkID
}

func (d *TimerBatchDeleter) advanceWatermarkLocked() {
	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueTimer)
	metrics.GaugePendingSetSize.Record(int64(d.pendingSet.Len()), kindTag)

	min, ok := d.pendingSet.Min()
	if !ok {
		return
	}
	if timerTaskLess(timerTaskKey{sortKey: d.watermarkSortKey, id: d.watermarkID}, min) {
		d.logger.Debugf("TimerBatchDeleter shard=%d: watermark advanced (%d,%s) -> below (%d,%s)",
			d.shardID, d.watermarkSortKey, d.watermarkID, min.sortKey, min.id)
		d.watermarkSortKey = min.sortKey
		d.watermarkID = min.id
		metrics.GaugeWatermark.Record(min.sortKey, kindTag)
	}
}

// Run is the main loop: receives completions and periodically range-deletes.
func (d *TimerBatchDeleter) Run(ctx context.Context) {
	d.logger.Info("Starting timer batch deleter", tag.Shard(d.shardID))

	deleteInterval := d.cfg.TimerDeleteInterval + time.Duration(rand.Int63n(int64(d.cfg.TimerDeleteIntervalJitter)))
	deleteTimer := time.NewTimer(deleteInterval)
	defer deleteTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.shutdownCh:
			d.logger.Info("Timer batch deleter shutting down", tag.Shard(d.shardID))
			d.drainAndShutdown(ctx)
			return
		case tc := <-d.doneCh:
			d.removePending(tc.SortKey, tc.ID)
		case <-deleteTimer.C:
			d.rangeDelete(ctx)
			jitter := time.Duration(rand.Int63n(int64(d.cfg.TimerDeleteIntervalJitter)))
			deleteTimer.Reset(d.cfg.TimerDeleteInterval + jitter)
		}
	}
}

func (d *TimerBatchDeleter) rangeDelete(ctx context.Context) {
	d.mu.Lock()
	wmSortKey := d.watermarkSortKey
	wmID := d.watermarkID
	d.mu.Unlock()

	// Skip if watermark hasn't advanced beyond the committed position.
	wmKey := timerTaskKey{sortKey: wmSortKey, id: wmID}
	committedKey := timerTaskKey{sortKey: d.committedSortKey, id: d.committedID}
	if !timerTaskLess(committedKey, wmKey) {
		return
	}
	if wmID.IsZero() {
		return
	}

	cappedCtx, cancel := d.sm.GetCappedContext(ctx, d.shardID)
	defer cancel()

	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueTimer)
	err := d.runStore.RangeDeleteTimerTasks(cappedCtx, d.shardID, wmSortKey, wmID)
	if err != nil {
		metrics.CounterRangeDeleteFailed.Inc(kindTag)
		d.logger.Error("Failed to range-delete timer tasks", tag.Shard(d.shardID), tag.Error(err))
		return
	}
	metrics.CounterRangeDeleteSuccess.Inc(kindTag)
	d.logger.Info("Range deleted timer tasks", tag.Shard(d.shardID),
		tag.WatermarkSortKey(wmSortKey))
}

func (d *TimerBatchDeleter) drainAndShutdown(ctx context.Context) {
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
		err := d.runStore.DeleteTimerTasksByIDBatch(cappedCtx, d.shardID, page)
		cancel()

		if err != nil {
			d.logger.Error("Shutdown: failed to delete timer tasks by ID batch",
				tag.Shard(d.shardID), tag.Error(err))
			return
		}
		d.logger.Info("Shutdown batch delete timer tasks",
			tag.Shard(d.shardID), tag.Count(len(page)))
	}
}

package taskprocessor

import (
	"context"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// OpsBatchDeleter is the OpsFIFO counterpart to ImmediateBatchDeleter. It is
// dramatically simpler because the OpsFIFO batch executor runs INLINE in the
// reader goroutine and a whole batch completes atomically — there are never
// out-of-order completions to track. So instead of a B-tree pending set +
// min-watermark machinery, the deleter keeps a single atomic int64 that the
// reader bumps to max(SortKey) after each successful batch.
//
// Responsibilities:
//   - Expose CommittedSeq() so the OpsBatchReader can publish the new
//     committed offset after a successful batch.
//   - Periodically run RangeDeleteOpsFIFOTasks(committedSeq) to reclaim disk
//     space (with jitter, mirroring the immediate / timer deleters).
//   - Expose GetWatermark() so ShardTaskProcessorFactory.GetMetadataForShard
//     can persist OpsFIFOTaskCommittedSeq via lease renewal, letting the next
//     shard owner resume from this offset.
type OpsBatchDeleter struct {
	shardID    int32
	cfg        config.TaskProcessorConfig
	runStore   p.RunStore
	sm         shardmanager.ShardManager
	logger     log.Logger
	shutdownCh <-chan struct{}

	// committedSeq is the highest TaskSeq through which the OpsFIFO has been
	// successfully processed. The reader only advances it after both the
	// history AND visibility batch writes have succeeded (the OpsBatchReader
	// retries indefinitely until that happens, so once advanced it never
	// regresses). Seeded from ShardMetadata.OpsFIFOTaskCommittedSeq at
	// claim time so the new owner resumes deleting from the previous
	// owner's high-water mark.
	committedSeq atomic.Int64
}

func NewOpsBatchDeleter(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	sm shardmanager.ShardManager,
	logger log.Logger,
	shutdownCh <-chan struct{},
	committedOffset int64,
) *OpsBatchDeleter {
	d := &OpsBatchDeleter{
		shardID:    shardID,
		cfg:        cfg,
		runStore:   runStore,
		sm:         sm,
		logger:     logger,
		shutdownCh: shutdownCh,
	}
	d.committedSeq.Store(committedOffset)
	return d
}

// SetCommittedSeq is called by the OpsBatchReader after a successful batch.
// Monotonic — newSeq must be >= the current value (the reader processes
// batches in order so this invariant holds naturally).
func (d *OpsBatchDeleter) SetCommittedSeq(newSeq int64) {
	for {
		cur := d.committedSeq.Load()
		if newSeq <= cur {
			return
		}
		if d.committedSeq.CompareAndSwap(cur, newSeq) {
			return
		}
	}
}

// GetWatermark returns the current committed offset. Used both by the
// periodic rangeDelete and by ShardTaskProcessorFactory.GetMetadataForShard
// for lease-renewal piggyback.
func (d *OpsBatchDeleter) GetWatermark() int64 { return d.committedSeq.Load() }

func (d *OpsBatchDeleter) Run(ctx context.Context) {
	d.logger.Info("Starting OpsFIFO batch deleter", tag.Shard(d.shardID))
	deleteInterval := d.cfg.OpsDeleteInterval + jitterDuration(d.cfg.OpsDeleteIntervalJitter)
	timer := time.NewTimer(deleteInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.shutdownCh:
			d.logger.Info("OpsFIFO batch deleter shutting down", tag.Shard(d.shardID))
			return
		case <-timer.C:
			d.rangeDelete(ctx)
			timer.Reset(d.cfg.OpsDeleteInterval + jitterDuration(d.cfg.OpsDeleteIntervalJitter))
		}
	}
}

func (d *OpsBatchDeleter) rangeDelete(ctx context.Context) {
	wm := d.committedSeq.Load()
	if wm == 0 {
		// Never processed any batch on this owner AND no resume offset to
		// chase down — nothing to delete. Skip the round trip.
		return
	}

	cappedCtx, cancel := d.sm.GetCappedContext(ctx, d.shardID)
	defer cancel()

	// We do NOT skip when wm hasn't moved since the last delete; deleting
	// the same range twice is a cheap 0-row no-op (the previous owner /
	// previous tick already cleaned it up) and saving the round trip would
	// require tracking yet another field. Per-shard cost is one DeleteMany
	// every OpsDeleteInterval (~30 s) — trivial.
	kindTag := metrics.TagTaskQueueType(metrics.TaskQueueOpsFIFO)
	if err := d.runStore.RangeDeleteOpsFIFOTasks(cappedCtx, d.shardID, wm); err != nil {
		metrics.CounterRangeDeleteFailed.Inc(kindTag)
		d.logger.Error("Failed to range-delete OpsFIFO tasks", tag.Shard(d.shardID), tag.Error(err))
		return
	}
	metrics.CounterRangeDeleteSuccess.Inc(kindTag)
	d.logger.Debug("Range deleted OpsFIFO tasks", tag.Shard(d.shardID), tag.Watermark(wm))
}

// jitterDuration returns a non-negative random duration up to max. Returns 0
// when max is 0 (no jitter configured).
func jitterDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

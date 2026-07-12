package taskprocessor

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// ImmediateBatchReader reads immediate tasks for a shard and sends them to the worker pool.
// Tasks are ordered by SortKey (TaskSeq = RangeID<<32 | LocalSeq) which guarantees
// monotonic ordering within and across shard ownership changes.
type ImmediateBatchReader struct {
	shardID    int32
	cfg        config.TaskProcessorConfig
	runStore   p.RunStore
	workerPool *WorkerPool
	deleter    *ImmediateBatchDeleter
	shutdownCh <-chan struct{}
	sm         shardmanager.ShardManager
	logger     log.Logger
	lastSeq    int64 // cursor: read tasks with sort_key > lastSeq
	newTaskCh  <-chan struct{}
}

func NewImmediateBatchReader(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	workerPool *WorkerPool,
	deleter *ImmediateBatchDeleter,
	shutdownCh <-chan struct{},
	sm shardmanager.ShardManager,
	logger log.Logger,
	initialSeq int64,
	newTaskCh <-chan struct{},
) *ImmediateBatchReader {
	return &ImmediateBatchReader{
		shardID:    shardID,
		cfg:        cfg,
		runStore:   runStore,
		workerPool: workerPool,
		deleter:    deleter,
		shutdownCh: shutdownCh,
		sm:         sm,
		logger:     logger,
		lastSeq:    initialSeq,
		newTaskCh:  newTaskCh,
	}
}

func (r *ImmediateBatchReader) Run(ctx context.Context) {
	r.logger.Info("Starting immediate batch reader", tag.Shard(r.shardID))
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			r.logger.Info("Immediate batch reader shutting down", tag.Shard(r.shardID))
			return
		default:
		}

		cappedCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
		tasks, err := r.runStore.RangeReadImmediateTasks(cappedCtx, r.shardID, r.lastSeq, r.cfg.ImmediateBatchReadLimit)
		cancel()

		kindTag := metrics.TagTaskQueueType(metrics.TaskQueueImmediate)
		if err != nil {
			metrics.CounterBatchReadFailed.Inc(kindTag)
			r.logger.Error("Failed to read immediate tasks", tag.Shard(r.shardID), tag.Error(err))
			time.Sleep(r.cfg.ImmediatePollInterval)
			continue
		}
		metrics.CounterBatchReadSuccess.Inc(kindTag)

		if len(tasks) == 0 {
			r.logger.Debugf("ImmediateBatchReader shard=%d: no tasks (afterSeq=%d), waiting", r.shardID, r.lastSeq)
			select {
			case <-ctx.Done():
				return
			case <-r.shutdownCh:
				return
			case <-r.newTaskCh:
				r.logger.Debugf("ImmediateBatchReader shard=%d: new task signal received", r.shardID)
			case <-time.After(r.cfg.ImmediatePollInterval):
			}
			continue
		}

		metrics.HistogramBatchReadCount.Record(float64(len(tasks)), kindTag)
		for _, t := range tasks {
			r.logger.Debugf("ImmediateBatchReader shard=%d: task id=%s sortKey=%d type=%d runID=%s",
				r.shardID, t.ID, t.SortKey, t.TaskType, t.TaskInfo.RunID)
		}

		doneCh := r.deleter.DoneCh()
		for _, task := range tasks {
			r.deleter.InsertPending(task.SortKey, task.ID)

			r.workerPool.Submit(&TaskItem{
				ShardID: r.shardID,
				Task:    p.TaskRow{Immediate: task},
				DoneCh:  doneCh,
				TaskKey: TaskCompletion{SortKey: task.SortKey, ID: task.ID},
			})
			r.lastSeq = task.SortKey
		}
	}
}

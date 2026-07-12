package taskprocessor

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

// TimerBatchReader reads timer tasks for a shard and sends them to the worker pool.
// Uses a TimerGate pattern to sleep until the next timer fires.
type TimerBatchReader struct {
	shardID        int32
	cfg            config.TaskProcessorConfig
	runStore       p.RunStore
	workerPool     *WorkerPool
	deleter        *TimerBatchDeleter
	shutdownCh     <-chan struct{}
	sm             shardmanager.ShardManager
	logger         log.Logger
	lastSortKey    int64
	lastID         ids.TaskID
	nextWakeupTime time.Time
	notifier       *ShardTaskNotifier
}

func NewTimerBatchReader(
	shardID int32,
	cfg config.TaskProcessorConfig,
	runStore p.RunStore,
	workerPool *WorkerPool,
	deleter *TimerBatchDeleter,
	shutdownCh <-chan struct{},
	sm shardmanager.ShardManager,
	logger log.Logger,
	initialSortKey int64,
	initialID ids.TaskID,
	notifier *ShardTaskNotifier,
) *TimerBatchReader {
	// Use zero lastID so the first RangeReadTimerTasks call is inclusive on
	// initialSortKey. The watermark points AT the min pending task (not below
	// it), so the next owner must re-read that position. With a zero afterID,
	// RangeReadTimerTasks skips the $or cursor filter and reads all tasks at
	// or above initialSortKey. Tasks below the watermark were already
	// range-deleted, so only relevant tasks remain.
	return &TimerBatchReader{
		shardID:     shardID,
		cfg:         cfg,
		runStore:    runStore,
		workerPool:  workerPool,
		deleter:     deleter,
		shutdownCh:  shutdownCh,
		sm:          sm,
		logger:      logger,
		lastSortKey: initialSortKey,
		lastID:      ids.TaskID{},
		notifier:    notifier,
	}
}

func (r *TimerBatchReader) Run(ctx context.Context) {
	r.logger.Info("Starting timer batch reader", tag.Shard(r.shardID))
	r.nextWakeupTime = time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			r.logger.Info("Timer batch reader shutting down", tag.Shard(r.shardID))
			return
		default:
		}

		// Drain any pending fire-time hint and pull our next-wakeup back if
		// the hint would wake us sooner than what we had planned. Notifier
		// only stores hints earlier than its current value; we never advance
		// nextWakeupTime past what the previous read already chose.
		if pending := r.notifier.pendingEarliestFireAt.Swap(0); pending != 0 {
			if t := time.UnixMilli(pending); t.Before(r.nextWakeupTime) {
				r.nextWakeupTime = t
				r.logger.Debug("Timer reader advanced nextWakeupTime via notify",
					tag.Shard(r.shardID), tag.Timestamp(t))
			}
		}

		waitDur := time.Until(r.nextWakeupTime)
		if waitDur > 0 {
			select {
			case <-ctx.Done():
				return
			case <-r.shutdownCh:
				return
			case <-r.notifier.newTimerCh:
				// Re-evaluate pending hint and waitDur; do NOT poll yet.
				// A pure timer-rich poll would otherwise return 0 rows and
				// reset us to now+MaxLookAhead, undoing the advance.
				continue
			case <-time.After(waitDur):
			}
		}

		nowMs := time.Now().UnixMilli()
		lookAheadMs := time.Now().Add(r.cfg.TimerMinLookAheadDuration).UnixMilli()

		cappedCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
		tasks, err := r.runStore.RangeReadTimerTasks(cappedCtx, r.shardID, lookAheadMs, r.lastSortKey, r.lastID, r.cfg.TimerBatchReadLimit)
		cancel()

		timerKind := metrics.TagTaskQueueType(metrics.TaskQueueTimer)
		if err != nil {
			metrics.CounterBatchReadFailed.Inc(timerKind)
			r.logger.Error("Failed to read timer tasks", tag.Shard(r.shardID), tag.Error(err))
			r.nextWakeupTime = time.Now().Add(r.cfg.TimerMinLookAheadDuration)
			continue
		}
		metrics.CounterBatchReadSuccess.Inc(timerKind)

		readyTasks := make([]*p.TimerTaskRow, 0)
		for _, task := range tasks {
			if task.SortKey <= nowMs {
				readyTasks = append(readyTasks, task)
				r.lastSortKey = task.SortKey
				r.lastID = task.ID
			} else {
				r.nextWakeupTime = time.UnixMilli(task.SortKey)
				break
			}
		}

		if len(readyTasks) > 0 {
			metrics.HistogramBatchReadCount.Record(float64(len(readyTasks)), timerKind)
			// Ensure immediate re-poll if the batch might be full (more tasks waiting).
			// Without this, a stale nextWakeupTime from a previous empty-queue
			// iteration (now + MaxLookAhead) could delay processing.
			r.nextWakeupTime = time.Now()
		} else if len(tasks) == 0 {
			// No tasks at all — extend look-ahead to avoid busy-polling.
			r.nextWakeupTime = time.Now().Add(r.cfg.TimerMaxLookAheadDuration)
		}
		// else: len(readyTasks)==0 but len(tasks)>0 means all tasks are future.
		// nextWakeupTime was already set to the earliest future task's fire_at
		// at line 109.

		doneCh := r.deleter.DoneCh()
		for _, task := range readyTasks {
			r.deleter.InsertPending(task.SortKey, task.ID)

			r.workerPool.Submit(&TaskItem{
				ShardID: r.shardID,
				Task:    p.TaskRow{Timer: task},
				DoneCh:  doneCh,
				TaskKey: TaskCompletion{SortKey: task.SortKey, ID: task.ID},
			})
		}
	}
}

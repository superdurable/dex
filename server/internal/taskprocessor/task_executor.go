// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package taskprocessor

import (
	"context"
	"sync"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/backoff"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// TaskExecutor runs tasks through a worker pool.
// TODO: add DLQ support
type TaskExecutor interface {
	Start(ctx context.Context)
	Stop()
	// Submit enqueues a task, blocking while the queue is full. Returns false
	// without enqueuing once Stop has run so producers are never stuck forever.
	Submit(item *taskItem) bool
	TaskChan() chan<- *taskItem
}

type taskExecutorImpl struct {
	cfg     *config.TaskProcessorConfig
	handler TaskHandler
	logger  log.Logger

	taskChan    chan *taskItem
	retryPolicy *backoff.RetryPolicy
	wg          sync.WaitGroup
	cancel      context.CancelFunc
	done        chan struct{} // closed by Stop to release a blocked Submit
}

var _ TaskExecutor = (*taskExecutorImpl)(nil)

// taskItem is one unit of work. Exactly one of immediate / timer is set.
type taskItem struct {
	shardID   int32
	immediate *p.ImmediateTaskRow
	timer     *p.TimerTaskRow
	// doneCh reports completion to the task deleter. Optional.
	doneCh chan<- TaskCompletion
	// genCtx is the shard-generation context at submit time. The handler runs
	// under it, so ownership loss fences execution and the task is not completed.
	genCtx context.Context
}

func NewTaskExecutor(
	cfg *config.TaskProcessorConfig,
	handler TaskHandler,
	logger log.Logger,
) TaskExecutor {
	if cfg == nil {
		panic("TaskProcessorConfig must not be nil")
	}
	if handler == nil {
		panic("TaskHandler must not be nil")
	}
	if logger == nil {
		panic("Logger must not be nil")
	}
	if cfg.NumWorkers <= 0 {
		panic("TaskProcessorConfig.NumWorkers must be > 0")
	}
	if cfg.HandleAttemptTimeout <= 0 {
		panic("TaskProcessorConfig.HandleAttemptTimeout must be > 0")
	}

	return &taskExecutorImpl{
		cfg:         cfg,
		handler:     handler,
		logger:      logger,
		taskChan:    make(chan *taskItem, cfg.NumWorkers*2),
		retryPolicy: &cfg.HandleRetryPolicy,
	}
}

func newImmediateTaskItem(shardID int32, task *p.ImmediateTaskRow, doneCh chan<- TaskCompletion, genCtx context.Context) *taskItem {
	return &taskItem{shardID: shardID, immediate: task, doneCh: doneCh, genCtx: genCtx}
}

func newTimerTaskItem(shardID int32, task *p.TimerTaskRow, doneCh chan<- TaskCompletion, genCtx context.Context) *taskItem {
	return &taskItem{shardID: shardID, timer: task, doneCh: doneCh, genCtx: genCtx}
}

func (e *taskExecutorImpl) Start(ctx context.Context) {
	if e.cancel != nil {
		panic("TaskExecutor already started")
	}

	ctx, e.cancel = context.WithCancel(ctx)
	e.done = make(chan struct{})
	for i := 0; i < e.cfg.NumWorkers; i++ {
		e.wg.Add(1)
		go e.worker(ctx)
	}
}

func (e *taskExecutorImpl) Stop() {
	if e.cancel == nil {
		return
	}
	close(e.done)
	e.cancel()
	e.wg.Wait()
	e.cancel = nil

	// Queued-but-unexecuted items are dropped, NOT completed: completing them
	// would delete un-run tasks. They stay in the deleter's pending (watermark
	// blocked) and in the DB, and are re-read after failover / restart.
}

func (e *taskExecutorImpl) Submit(item *taskItem) bool {
	if e.done == nil {
		return false
	}
	select {
	case e.taskChan <- item:
		return true
	case <-e.done:
		return false
	}
}

func (e *taskExecutorImpl) worker(ctx context.Context) {
	defer e.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case item := <-e.taskChan:
			e.execute(ctx, item)
		}
	}
}

func (e *taskExecutorImpl) execute(parent context.Context, item *taskItem) {
	if item.immediate == nil && item.timer == nil {
		e.logger.Error("task item has neither immediate nor timer task",
			tag.ShardId(item.shardID),
		)
		return
	}

	// Fence to the shard generation: the handler aborts the moment this claim is
	// detached, so a stale owner cannot keep executing after reclaim.
	handlerCtx := parent
	if item.genCtx != nil {
		var cancel context.CancelFunc
		handlerCtx, cancel = context.WithCancel(parent)
		stop := context.AfterFunc(item.genCtx, cancel)
		defer stop()
		defer cancel()
	}

	retry := backoff.NewRetry(backoff.WithRetryPolicy(e.retryPolicy))
	err := retry.DoCategorized(handlerCtx, func(ctx context.Context) errors.CategorizedError {
		attemptCtx, cancel := context.WithTimeout(ctx, e.cfg.HandleAttemptTimeout)
		defer cancel()
		return e.handleOnce(attemptCtx, item)
	})

	// Success: commit for deletion.
	if err == nil {
		e.notifyDone(item)
		return
	}

	// Ownership fenced: never complete — the new owner re-processes.
	if item.genCtx != nil && item.genCtx.Err() != nil {
		return
	}

	tags := []tag.Tag{tag.ShardId(item.shardID), tag.Error(err)}
	// Retriable exhausted: retain (no DLQ yet). Do NOT complete; the watermark
	// blocks on this task until it succeeds or is sidelined.
	if err.IsRetriable() {
		e.logger.Error("task handle failed after retries; retained, watermark blocked", tags...)
		return
	}

	// Non-retriable (business) outcome: definitive, commit for deletion.
	e.logger.Debug("task handle failed (non-retriable)", tags...)
	e.notifyDone(item)
}

// notifyDone is non-blocking so a stopped deleter cannot stall the shared pool.
func (e *taskExecutorImpl) notifyDone(item *taskItem) {
	if item.doneCh == nil {
		return
	}
	var completion TaskCompletion
	switch {
	case item.timer != nil:
		completion = TaskCompletion{SortKey: item.timer.SortKey, ID: item.timer.ID}
	case item.immediate != nil:
		completion = TaskCompletion{SortKey: item.immediate.SortKey, ID: item.immediate.ID}
	default:
		return
	}
	select {
	case item.doneCh <- completion:
	default:
		e.logger.Error("task completion dropped; DoneCh full or consumer stopped",
			tag.ShardId(item.shardID),
		)
	}
}

func (e *taskExecutorImpl) handleOnce(ctx context.Context, item *taskItem) errors.CategorizedError {
	if item.immediate != nil {
		return e.handler.HandleImmediateTask(ctx, item.shardID, item.immediate)
	}
	return e.handler.HandleTimerTask(ctx, item.shardID, item.timer)
}

func (e *taskExecutorImpl) TaskChan() chan<- *taskItem {
	return e.taskChan
}

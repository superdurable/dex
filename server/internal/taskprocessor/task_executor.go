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
	// Submit enqueues a task, blocking while the queue is full. Returns without
	// enqueuing once Stop has run, so a producer is never blocked forever on a
	// stopped pool; the task stays in the DB and is re-read after failover.
	Submit(item *taskItem)
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
	// A zero/negative per-attempt timeout makes every attempt context expire
	// immediately, so every task would fail instantly.
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

func newImmediateTaskItem(shardID int32, task *p.ImmediateTaskRow, doneCh chan<- TaskCompletion) *taskItem {
	return &taskItem{shardID: shardID, immediate: task, doneCh: doneCh}
}

func newTimerTaskItem(shardID int32, task *p.TimerTaskRow, doneCh chan<- TaskCompletion) *taskItem {
	return &taskItem{shardID: shardID, timer: task, doneCh: doneCh}
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
	close(e.done) // release any producer blocked in Submit before workers exit
	e.cancel()
	e.wg.Wait()
	e.cancel = nil
}

func (e *taskExecutorImpl) Submit(item *taskItem) {
	select {
	case e.taskChan <- item:
	case <-e.done:
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
	defer e.notifyDone(item)

	// Retry only retriable (infra/transient) errors; DoCategorized inspects
	// IsRetriable, so business errors fail after one attempt. Each attempt gets
	// its own HandleAttemptTimeout. parent cancellation aborts the retry loop.
	retry := backoff.NewRetry(backoff.WithRetryPolicy(e.retryPolicy))
	err := retry.DoCategorized(parent, func(ctx context.Context) errors.CategorizedError {
		attemptCtx, cancel := context.WithTimeout(ctx, e.cfg.HandleAttemptTimeout)
		defer cancel()
		return e.handleOnce(attemptCtx, item)
	})
	if err == nil {
		return
	}

	tags := []tag.Tag{tag.ShardId(item.shardID), tag.Error(err)}
	switch err.GetCategory() {
	case errors.ErrorCategoryInternal, errors.ErrorCategoryUnavailable:
		e.logger.Error("task handle failed after retries", tags...)
	default:
		e.logger.Debug("task handle failed", tags...)
	}
}

// notifyDone advances the deleter even when handling failed.
func (e *taskExecutorImpl) notifyDone(item *taskItem) {
	if item.doneCh == nil {
		return
	}
	switch {
	case item.timer != nil:
		item.doneCh <- TaskCompletion{SortKey: item.timer.SortKey, ID: item.timer.ID}
	case item.immediate != nil:
		item.doneCh <- TaskCompletion{SortKey: item.immediate.SortKey, ID: item.immediate.ID}
	}
}

// handleOnce dispatches one attempt. execute guarantees exactly one of
// immediate / timer is set before this runs.
func (e *taskExecutorImpl) handleOnce(ctx context.Context, item *taskItem) errors.CategorizedError {
	if item.immediate != nil {
		return e.handler.HandleImmediateTask(ctx, item.shardID, item.immediate)
	}
	return e.handler.HandleTimerTask(ctx, item.shardID, item.timer)
}

func (e *taskExecutorImpl) TaskChan() chan<- *taskItem {
	return e.taskChan
}

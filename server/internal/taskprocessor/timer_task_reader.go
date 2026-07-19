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
	"time"

	"common-go/ids"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// TimerTaskReader polls due timer tasks for one shard and submits them.
type TimerTaskReader interface {
	Start(ctx context.Context)
	Stop()
}

type timerTaskReaderImpl struct {
	cfg      *config.TaskProcessorConfig
	store    p.RunStore
	sm       shards.ShardManager
	executor TaskExecutor
	deleter  TimerTaskDeleter
	notifier *perShardNotifier
	shardID  int32
	logger   log.Logger

	lastSortKey int64
	lastID      ids.UID

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ TimerTaskReader = (*timerTaskReaderImpl)(nil)

// NewTimerTaskReader starts the read cursor after the committed watermark.
func NewTimerTaskReader(
	cfg *config.TaskProcessorConfig,
	store p.RunStore,
	sm shards.ShardManager,
	executor TaskExecutor,
	deleter TimerTaskDeleter,
	notifier *perShardNotifier,
	shardID int32,
	initialSortKey int64,
	initialID ids.UID,
	logger log.Logger,
) TimerTaskReader {
	if cfg == nil {
		panic("TaskProcessorConfig must not be nil")
	}
	if store == nil {
		panic("RunStore must not be nil")
	}
	if sm == nil {
		panic("ShardManager must not be nil")
	}
	if executor == nil {
		panic("TaskExecutor must not be nil")
	}
	if deleter == nil {
		panic("TimerTaskDeleter must not be nil")
	}
	if notifier == nil {
		panic("perShardNotifier must not be nil")
	}
	if logger == nil {
		panic("Logger must not be nil")
	}
	if cfg.TimerBatchReadLimit <= 0 {
		panic("TaskProcessorConfig.TimerBatchReadLimit must be > 0")
	}
	if cfg.TimerMinLookAheadDuration <= 0 {
		panic("TaskProcessorConfig.TimerMinLookAheadDuration must be > 0")
	}
	if cfg.TimerMaxLookAheadDuration <= 0 {
		panic("TaskProcessorConfig.TimerMaxLookAheadDuration must be > 0")
	}

	return &timerTaskReaderImpl{
		cfg:         cfg,
		store:       store,
		sm:          sm,
		executor:    executor,
		deleter:     deleter,
		notifier:    notifier,
		shardID:     shardID,
		logger:      logger,
		lastSortKey: initialSortKey,
		lastID:      initialID,
	}
}

func (r *timerTaskReaderImpl) Start(ctx context.Context) {
	if r.cancel != nil {
		panic("TimerTaskReader already started")
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.run(ctx)
}

func (r *timerTaskReaderImpl) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
	r.cancel = nil
}

func (r *timerTaskReaderImpl) run(ctx context.Context) {
	defer r.wg.Done()

	nextWakeup := time.Now()
	for {
		if ctx.Err() != nil {
			return
		}

		nextWakeup = r.mergeNotifierHint(nextWakeup)

		// TimerGate: sleep until nextWakeup, or recalculate on newTimerCh (no poll).
		wokenByNotify, err := r.awaitWakeup(ctx, nextWakeup)
		if err != nil {
			return
		}
		if wokenByNotify {
			continue
		}

		nextWakeup = r.pollAndSubmit(ctx)
	}
}

func (r *timerTaskReaderImpl) mergeNotifierHint(nextWakeup time.Time) time.Time {
	hint := r.notifier.pendingEarliestFireAt.Swap(0)
	if hint <= 0 {
		return nextWakeup
	}
	hintTime := sortKeyToTime(hint)
	if hintTime.Before(nextWakeup) {
		return hintTime
	}
	return nextWakeup
}

// awaitWakeup sleeps until nextWakeup. newTimerCh wakes early without polling.
func (r *timerTaskReaderImpl) awaitWakeup(ctx context.Context, nextWakeup time.Time) (wokenByNotify bool, err error) {
	delay := time.Until(nextWakeup)
	if delay <= 0 {
		return false, nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return false, nil
	case <-r.notifier.newTimerCh:
		return true, nil
	}
}

func (r *timerTaskReaderImpl) pollAndSubmit(ctx context.Context) time.Time {
	now := time.Now()
	nowSortKey := now.UnixNano()
	sortKeyUpTo := nowSortKey + r.cfg.TimerMinLookAheadDuration.Nanoseconds()

	// Publish the read watermark (this poll's due threshold) BEFORE reading, so a
	// concurrent timer write floors any fire time at or below it above it. Then a
	// timer can never be committed below where this cursor is about to advance.
	if err := r.sm.AdvanceTimerReadLevel(r.shardID, nowSortKey); err != nil {
		return now.Add(r.cfg.TimerMaxLookAheadDuration)
	}

	// Generation context for the tasks we submit this poll; fences the handler on
	// ownership loss.
	genCtx, err := r.sm.ShardContext(r.shardID)
	if err != nil {
		return now.Add(r.cfg.TimerMaxLookAheadDuration)
	}

	readCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
	tasks, err := r.store.RangeReadTimerTasks(
		readCtx,
		r.shardID,
		sortKeyUpTo,
		r.lastSortKey,
		r.lastID,
		r.cfg.TimerBatchReadLimit,
	)
	cancel()
	if err != nil {
		r.logger.Error("range read timer tasks failed",
			tag.ShardId(r.shardID),
			tag.Error(err),
		)
		return now.Add(r.cfg.TimerMaxLookAheadDuration)
	}

	if len(tasks) == 0 {
		return now.Add(r.cfg.TimerMaxLookAheadDuration)
	}

	ready := make([]*p.TimerTaskRow, 0, len(tasks))
	var nextWakeup time.Time
	sawFuture := false

	for _, task := range tasks {
		if task.SortKey > nowSortKey {
			nextWakeup = sortKeyToTime(task.SortKey)
			sawFuture = true
			break
		}
		ready = append(ready, task)
	}

	for _, task := range ready {
		if ctx.Err() != nil {
			return time.Now()
		}
		r.deleter.InsertPending(task.SortKey, task.ID)
		if !r.executor.Submit(newTimerTaskItem(r.shardID, task, r.deleter.DoneCh(), genCtx)) {
			r.deleter.RemovePending(task.SortKey, task.ID)
			return time.Now()
		}
		r.lastSortKey = task.SortKey
		r.lastID = task.ID
	}

	if sawFuture {
		return nextWakeup
	}
	if len(tasks) >= r.cfg.TimerBatchReadLimit {
		// More due tasks may remain; poll again immediately.
		return time.Now()
	}
	// Look-ahead window exhausted with no future task seen.
	return now.Add(r.cfg.TimerMaxLookAheadDuration)
}

func sortKeyToTime(sortKey int64) time.Time {
	return time.Unix(0, sortKey)
}

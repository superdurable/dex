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

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// ImmediateTaskReader polls immediate tasks for one shard and submits them.
type ImmediateTaskReader interface {
	Start(ctx context.Context)
	Stop()
}

type immediateTaskReaderImpl struct {
	cfg      *config.TaskProcessorConfig
	store    p.RunStore
	sm       shards.ShardManager
	executor TaskExecutor
	deleter  ImmediateTaskDeleter
	notifier *perShardNotifier
	shardID  int32
	logger   log.Logger

	lastSeq int64

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ ImmediateTaskReader = (*immediateTaskReaderImpl)(nil)

// NewImmediateTaskReader starts the read cursor after the committed watermark.
func NewImmediateTaskReader(
	cfg *config.TaskProcessorConfig,
	store p.RunStore,
	sm shards.ShardManager,
	executor TaskExecutor,
	deleter ImmediateTaskDeleter,
	notifier *perShardNotifier,
	shardID int32,
	initialSeq int64,
	logger log.Logger,
) ImmediateTaskReader {
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
		panic("ImmediateTaskDeleter must not be nil")
	}
	if notifier == nil {
		panic("perShardNotifier must not be nil")
	}
	if logger == nil {
		panic("Logger must not be nil")
	}
	if cfg.ImmediateBatchReadLimit <= 0 {
		panic("TaskProcessorConfig.ImmediateBatchReadLimit must be > 0")
	}
	if cfg.ImmediateMaxPollInterval <= 0 {
		panic("TaskProcessorConfig.ImmediateMaxPollInterval must be > 0")
	}

	return &immediateTaskReaderImpl{
		cfg:      cfg,
		store:    store,
		sm:       sm,
		executor: executor,
		deleter:  deleter,
		notifier: notifier,
		shardID:  shardID,
		logger:   logger,
		lastSeq:  initialSeq,
	}
}

func (r *immediateTaskReaderImpl) Start(ctx context.Context) {
	if r.cancel != nil {
		panic("ImmediateTaskReader already started")
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.wg.Add(1)
	go r.run(ctx)
}

func (r *immediateTaskReaderImpl) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
	r.cancel = nil
}

func (r *immediateTaskReaderImpl) run(ctx context.Context) {
	defer r.wg.Done()

	for {
		if ctx.Err() != nil {
			return
		}

		n := r.pollAndSubmit(ctx)
		if n >= r.cfg.ImmediateBatchReadLimit {
			// More tasks may remain; poll again immediately.
			continue
		}

		if err := r.awaitNotifyOrInterval(ctx); err != nil {
			return
		}
	}
}

func (r *immediateTaskReaderImpl) awaitNotifyOrInterval(ctx context.Context) error {
	timer := time.NewTimer(r.cfg.ImmediateMaxPollInterval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.notifier.newTaskCh:
		return nil
	case <-timer.C:
		return nil
	}
}

func (r *immediateTaskReaderImpl) pollAndSubmit(ctx context.Context) int {
	readCtx, cancel := r.sm.GetCappedContext(ctx, r.shardID)
	tasks, err := r.store.RangeReadImmediateTasks(
		readCtx,
		r.shardID,
		r.lastSeq,
		r.cfg.ImmediateBatchReadLimit,
	)
	cancel()
	if err != nil {
		r.logger.Error("range read immediate tasks failed",
			tag.ShardId(r.shardID),
			tag.Error(err),
		)
		return 0
	}

	for _, task := range tasks {
		if ctx.Err() != nil {
			return len(tasks)
		}
		r.deleter.InsertPending(task.SortKey)
		r.executor.Submit(newImmediateTaskItem(r.shardID, task, r.deleter.DoneCh()))
		r.lastSeq = task.SortKey
	}
	return len(tasks)
}

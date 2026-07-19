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
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

// taskProcessorManagerImpl owns the per-shard immediate/timer readers and
// deleters. The TaskExecutor (worker pool) and TaskNotifier are instance-shared
// and injected; their lifecycle is managed by the caller, not per shard.
type taskProcessorManagerImpl struct {
	cfg      *config.TaskProcessorConfig
	shardCfg *config.ShardConfig
	store    p.RunStore
	sm       shards.ShardManager
	executor TaskExecutor
	notifier TaskNotifier
	logger   log.Logger

	mu     sync.Mutex
	shards map[int32]*shardProcessors
}

// shardProcessors bundles the four per-shard components for one shard.
type shardProcessors struct {
	immediateReader  ImmediateTaskReader
	immediateDeleter ImmediateTaskDeleter
	timerReader      TimerTaskReader
	timerDeleter     TimerTaskDeleter
}

var _ shards.TaskProcessorsManager = (*taskProcessorManagerImpl)(nil)

func NewTaskProcessorManagerImpl(
	cfg *config.TaskProcessorConfig,
	shardCfg *config.ShardConfig,
	store p.RunStore,
	sm shards.ShardManager,
	executor TaskExecutor,
	notifier TaskNotifier,
	logger log.Logger,
) shards.TaskProcessorsManager {
	if cfg == nil {
		panic("TaskProcessorConfig must not be nil")
	}
	if shardCfg == nil {
		panic("ShardConfig must not be nil")
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
	if notifier == nil {
		panic("TaskNotifier must not be nil")
	}
	if logger == nil {
		panic("Logger must not be nil")
	}
	return &taskProcessorManagerImpl{
		cfg:      cfg,
		shardCfg: shardCfg,
		store:    store,
		sm:       sm,
		executor: executor,
		notifier: notifier,
		logger:   logger,
		shards:   make(map[int32]*shardProcessors),
	}
}

// StartShard launches the shard's readers and deleters, resuming from the
// committed watermarks in metadata. Called by ShardManager once per claim.
func (t *taskProcessorManagerImpl) StartShard(shardID int32, initMetadata p.ShardMetadata) {
	perShard := t.notifier.AddShard(shardID)

	immediateDeleter := NewImmediateTaskDeleter(
		t.cfg, t.shardCfg, t.store, t.sm, shardID, initMetadata.ImmediateTaskCommittedSeq, t.logger)
	timerDeleter := NewTimerTaskDeleter(
		t.cfg, t.shardCfg, t.store, t.sm, shardID,
		initMetadata.TimerTaskCommittedSortKey, initMetadata.TimerTaskCommittedID, t.logger)

	immediateReader := NewImmediateTaskReader(
		t.cfg, t.store, t.sm, t.executor, immediateDeleter, perShard, shardID,
		initMetadata.ImmediateTaskCommittedSeq, t.logger)
	timerReader := NewTimerTaskReader(
		t.cfg, t.store, t.sm, t.executor, timerDeleter, perShard, shardID,
		initMetadata.TimerTaskCommittedSortKey, initMetadata.TimerTaskCommittedID, t.logger)

	t.mu.Lock()
	t.shards[shardID] = &shardProcessors{
		immediateReader:  immediateReader,
		immediateDeleter: immediateDeleter,
		timerReader:      timerReader,
		timerDeleter:     timerDeleter,
	}
	t.mu.Unlock()

	// Start deleters before readers so DoneCh is consumed before any task is submitted.
	ctx := context.Background()
	immediateDeleter.Start(ctx)
	timerDeleter.Start(ctx)
	immediateReader.Start(ctx)
	timerReader.Start(ctx)

	t.logger.Info("started task processors for shard", tag.ShardId(shardID))
}

// StopShard stops the shard's components and drops its notifier. Readers stop
// first so no new task is submitted or pending inserted, then the deleters.
// forced=true (ownership lost) skips the deleters' unfenced store cleanup so a
// stale owner cannot delete the new owner's tasks; forced=false flushes.
func (t *taskProcessorManagerImpl) StopShard(shardID int32, forced bool) {
	t.mu.Lock()
	procs, ok := t.shards[shardID]
	if ok {
		delete(t.shards, shardID)
	}
	t.mu.Unlock()
	if !ok {
		return
	}

	procs.immediateReader.Stop()
	procs.timerReader.Stop()
	procs.immediateDeleter.Stop(forced)
	procs.timerDeleter.Stop(forced)

	t.notifier.RemoveShard(shardID)
	t.logger.Info("stopped task processors for shard", tag.ShardId(shardID))
}

// GetShardMetadata returns the shard's current committed watermarks, used by
// ShardManager as the renew-time MetadataCallback. Returns nil if not started.
func (t *taskProcessorManagerImpl) GetShardMetadata(shardID int32) *p.ShardMetadata {
	t.mu.Lock()
	procs, ok := t.shards[shardID]
	t.mu.Unlock()
	if !ok {
		return nil
	}

	timerSortKey, timerID := procs.timerDeleter.GetWatermark()
	return &p.ShardMetadata{
		ImmediateTaskCommittedSeq: procs.immediateDeleter.GetWatermark(),
		TimerTaskCommittedSortKey: timerSortKey,
		TimerTaskCommittedID:      timerID,
	}
}

func (t *taskProcessorManagerImpl) NotifyNewImmediateTask(shardID int32) {
	t.notifier.NotifyNewImmediateTask(shardID)
}

func (t *taskProcessorManagerImpl) NotifyNewTimerTask(shardID int32, fireAtUnixNano int64) {
	t.notifier.NotifyNewTimerTask(shardID, fireAtUnixNano)
}

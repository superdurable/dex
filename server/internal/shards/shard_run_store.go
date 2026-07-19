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

package shards

import (
	"context"

	"github.com/superdurable/dex/server/internal/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// ShardRunStore is RunStore plus shard ready-gate, seq allocation, and notify.
type ShardRunStore interface {
	// GetRun is a read-only pass-through; it does not await shard ready.
	GetRun(ctx context.Context, shardID int32, namespace, runID string) (*p.RunRow, errors.CategorizedError)

	// CreateRunWithTasks inserts run+tasks using run.ShardID.
	// Allocates SortKey for immediate tasks still at the zero sentinel.
	CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError

	// UpdateRunWithNewTasks CAS-updates the run and inserts tasks.
	// Same SortKey allocation and notify behavior as CreateRunWithTasks.
	UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
		expectedVersion int64, update *p.RunRowUpdate, tasks []p.TaskRow) errors.CategorizedError
}

func NewShardRunStore(runStore p.RunStore, sm ShardManager, processorMgr TaskProcessorsManager) ShardRunStore {
	if runStore == nil {
		panic("runStore must not be nil")
	}
	if sm == nil {
		panic("ShardManager must not be nil")
	}
	if processorMgr == nil {
		panic("ShardTaskProcessorManager must not be nil")
	}
	return &shardRunStoreImpl{
		runStore:     runStore,
		sm:           sm,
		processorMgr: processorMgr,
	}
}

type shardRunStoreImpl struct {
	runStore     p.RunStore
	sm           ShardManager
	processorMgr TaskProcessorsManager
}

func (s *shardRunStoreImpl) CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError {
	if run == nil {
		return errors.NewInvalidInputError("run must not be nil", nil)
	}
	return s.writeWithShard(ctx, run.ShardID, tasks, func(writeCtx context.Context) errors.CategorizedError {
		return s.runStore.CreateRunWithTasks(writeCtx, run, tasks)
	})
}

func (s *shardRunStoreImpl) UpdateRunWithNewTasks(
	ctx context.Context,
	shardID int32,
	namespace, runID string,
	expectedVersion int64,
	update *p.RunRowUpdate,
	tasks []p.TaskRow,
) errors.CategorizedError {
	return s.writeWithShard(ctx, shardID, tasks, func(writeCtx context.Context) errors.CategorizedError {
		return s.runStore.UpdateRunWithNewTasks(writeCtx, shardID, namespace, runID, expectedVersion, update, tasks)
	})
}

func (s *shardRunStoreImpl) GetRun(ctx context.Context, shardID int32, namespace, runID string) (*p.RunRow, errors.CategorizedError) {
	return s.runStore.GetRun(ctx, shardID, namespace, runID)
}

// writeWithShard: await ready → (lock + alloc if immediate) → write → unlock → notify.
func (s *shardRunStoreImpl) writeWithShard(
	ctx context.Context,
	shardID int32,
	tasks []p.TaskRow,
	write func(context.Context) errors.CategorizedError,
) errors.CategorizedError {
	if err := s.sm.AwaitShardReady(ctx, shardID); err != nil {
		return err
	}

	writeCtx, cancel := s.sm.GetCappedContext(ctx, shardID)
	defer cancel()

	if err := s.lockedWrite(shardID, tasks, writeCtx, write); err != nil {
		return err
	}

	s.notifyNewTasks(shardID, tasks)
	return nil
}

// lockedWrite assigns immediate seqs and floors timer fire times under their
// per-shard locks, held across the DB write, then releases before returning so
// notify never runs under a lock. Locks are taken in a fixed order (immediate,
// then timer) so a mixed batch cannot deadlock.
//
// Both queues share the same invariant: the reader advances a monotonic cursor
// and never re-reads below it, so a task must never be committed below that
// cursor. Holding the lock across the write forces commit order to respect the
// cursor — immediate via seq allocation, timer via the read-watermark floor.
func (s *shardRunStoreImpl) lockedWrite(
	shardID int32,
	tasks []p.TaskRow,
	writeCtx context.Context,
	write func(context.Context) errors.CategorizedError,
) errors.CategorizedError {
	if hasImmediateTask(tasks) {
		seqLock, err := s.sm.AcquireImmediateTaskSeqLock(shardID)
		if err != nil {
			return err
		}
		defer seqLock.Unlock()
		allocateImmediateSortKeys(seqLock, tasks)
	}

	if hasTimerTask(tasks) {
		timerLock, err := s.sm.AcquireTimerTaskWriteLock(shardID)
		if err != nil {
			return err
		}
		defer timerLock.Unlock()
		floorTimerFireTimes(timerLock, tasks)
	}

	return write(writeCtx)
}

func allocateImmediateSortKeys(seqLock ImmediateTaskSeqLock, tasks []p.TaskRow) {
	for i := range tasks {
		imm := tasks[i].Immediate
		if imm == nil || imm.SortKey != 0 {
			continue
		}
		imm.SortKey = seqLock.Next()
	}
}

func floorTimerFireTimes(timerLock TimerTaskWriteLock, tasks []p.TaskRow) {
	for i := range tasks {
		if timer := tasks[i].Timer; timer != nil {
			timer.SortKey = timerLock.FloorFireTime(timer.SortKey)
		}
	}
}

func (s *shardRunStoreImpl) notifyNewTasks(shardID int32, tasks []p.TaskRow) {
	hasImmediate := false
	for i := range tasks {
		if tasks[i].Immediate != nil {
			hasImmediate = true
		}
		if timer := tasks[i].Timer; timer != nil {
			s.processorMgr.NotifyNewTimerTask(shardID, timer.SortKey)
		}
	}
	if hasImmediate {
		s.processorMgr.NotifyNewImmediateTask(shardID)
	}
}

func hasImmediateTask(tasks []p.TaskRow) bool {
	for i := range tasks {
		if tasks[i].Immediate != nil {
			return true
		}
	}
	return false
}

func hasTimerTask(tasks []p.TaskRow) bool {
	for i := range tasks {
		if tasks[i].Timer != nil {
			return true
		}
	}
	return false
}

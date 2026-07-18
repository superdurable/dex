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
	stderrors "errors"

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

// NewShardedRunStore constructs a ShardRunStore over the given dependencies.
func NewShardedRunStore(runStore p.RunStore, sm ShardManager, processorMgr TaskProcessorsManager) ShardRunStore {
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

	if hasImmediateTask(tasks) {
		if err := s.writeImmediateLocked(shardID, tasks, writeCtx, write); err != nil {
			return err
		}
	} else if err := write(writeCtx); err != nil {
		return err
	}

	s.notifyNewTasks(shardID, tasks)
	return nil
}

// writeImmediateLocked holds the seq lock across allocation AND the DB write.
//
// The batch reader treats a visible seq=k as proof every seq<=k is committed.
//
// Commits must therefore land in seq-allocation order.
//
// Unlocking before the write would let a later seq commit first, so the reader
// would skip the earlier, still-uncommitted one.
//
// Unlock is deferred, running before notify so wake-ups never fire under lock.
func (s *shardRunStoreImpl) writeImmediateLocked(
	shardID int32,
	tasks []p.TaskRow,
	writeCtx context.Context,
	write func(context.Context) errors.CategorizedError,
) errors.CategorizedError {
	unlock, err := s.sm.AcquireImmediateTaskSeqLock(shardID)
	if err != nil {
		return err
	}
	defer unlock()

	if err := s.allocateImmediateSortKeys(shardID, tasks); err != nil {
		return err
	}
	return write(writeCtx)
}

func (s *shardRunStoreImpl) allocateImmediateSortKeys(shardID int32, tasks []p.TaskRow) errors.CategorizedError {
	for i := range tasks {
		imm := tasks[i].Immediate
		if imm == nil || imm.SortKey != 0 {
			continue
		}
		seq, err := s.sm.GetNextImmediateTaskSeq(shardID)
		if err != nil {
			return asCategorized(err, "allocate immediate task seq")
		}
		imm.SortKey = seq
	}
	return nil
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

func asCategorized(err error, msg string) errors.CategorizedError {
	if catErr, ok := stderrors.AsType[errors.CategorizedError](err); ok {
		return catErr
	}
	return errors.NewInternalError(msg, err)
}

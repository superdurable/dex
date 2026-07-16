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

package process

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/server/common/log/tag"
	extensions2 "github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) BackoffImmediateTask(
	ctx context.Context, request data_models2.BackoffImmediateTaskRequest,
) error {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return err
	}

	err = p.doBackoffImmediateTaskTx(ctx, tx, request)
	if err != nil {
		err2 := tx.Rollback()
		if err2 != nil {
			p.logger.Error("error on rollback transaction", tag.Error(err2))
		}
	} else {
		err = tx.Commit()
		if err != nil {
			p.logger.Error("error on committing transaction", tag.Error(err))
			return err
		}
	}
	return err
}

func (p sqlProcessStoreImpl) doBackoffImmediateTaskTx(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.BackoffImmediateTaskRequest,
) error {
	task := request.Task
	prep := request.Prep

	if task.ImmediateTaskInfo.WorkerTaskBackoffInfo == nil {
		return fmt.Errorf("WorkerTaskBackoffInfo cannot be nil")
	}
	failureBytes, err := data_models2.CreateStateExecutionFailureBytesForBackoff(
		request.LastFailureStatus, request.LastFailureDetails, task.ImmediateTaskInfo.WorkerTaskBackoffInfo.CompletedAttempts)

	if err != nil {
		return err
	}
	err = tx.UpdateAsyncStateExecutionWithoutCommands(ctx, extensions2.AsyncStateExecutionRowForUpdateWithoutCommands{
		ProcessExecutionId: task.ProcessExecutionId,
		StateId:            task.StateId,
		StateIdSequence:    task.StateIdSequence,
		Status:             prep.Status,
		PreviousVersion:    prep.PreviousVersion,
		LastFailure:        failureBytes,
	})
	if err != nil {
		return err
	}
	timerInfoBytes, err := data_models2.CreateTimerTaskInfoBytes(task.ImmediateTaskInfo.WorkerTaskBackoffInfo, &task.TaskType)
	if err != nil {
		return err
	}
	err = tx.InsertTimerTask(ctx, extensions2.TimerTaskRowForInsert{
		ShardId:             task.ShardId,
		FireTimeUnixSeconds: request.FireTimestampSeconds,
		TaskType:            data_models2.TimerTaskTypeWorkerTaskBackoff,
		ProcessExecutionId:  task.ProcessExecutionId,
		StateId:             task.StateId,
		StateIdSequence:     task.StateIdSequence,
		Info:                timerInfoBytes,
	})
	if err != nil {
		return err
	}
	return tx.DeleteImmediateTask(ctx, extensions2.ImmediateTaskRowDeleteFilter{
		ShardId:      task.ShardId,
		TaskSequence: task.GetTaskSequence(),
		OptionalPartitionKey: &data_models2.PartitionKey{
			Namespace: prep.Info.Namespace,
			ProcessId: prep.Info.ProcessId,
		},
	})
}

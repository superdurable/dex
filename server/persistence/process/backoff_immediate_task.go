// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"
	"fmt"

	"github.com/xcherryio/xcherry/server/common/log/tag"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
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

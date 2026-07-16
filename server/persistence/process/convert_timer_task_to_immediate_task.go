// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	"github.com/xcherryio/xcherry/server/common/log/tag"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) ConvertTimerTaskToImmediateTask(
	ctx context.Context, request data_models2.ProcessTimerTaskRequest,
) (*data_models2.ProcessTimerTaskResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	err = p.doConvertTimerTaskToImmediateTaskTx(ctx, tx, request)
	if err != nil {
		err2 := tx.Rollback()
		if err2 != nil {
			p.logger.Error("error on rollback transaction", tag.Error(err2))
		}
	} else {
		err = tx.Commit()
		if err != nil {
			p.logger.Error("error on committing transaction", tag.Error(err))
			return nil, err
		}
	}

	return &data_models2.ProcessTimerTaskResponse{
		HasNewImmediateTask: true,
	}, err
}

func (p sqlProcessStoreImpl) doConvertTimerTaskToImmediateTaskTx(
	ctx context.Context, tx extensions2.SQLTransaction,
	request data_models2.ProcessTimerTaskRequest,
) error {
	currentTask := request.Task
	timerInfo := currentTask.TimerTaskInfo
	taskInfoBytes, err := data_models2.FromImmediateTaskInfoIntoBytes(data_models2.ImmediateTaskInfoJson{
		WorkerTaskBackoffInfo: timerInfo.WorkerTaskBackoffInfo,
	})
	if err != nil {
		return err
	}

	err = tx.InsertImmediateTask(ctx, extensions2.ImmediateTaskRowForInsert{
		ShardId:            currentTask.ShardId,
		TaskType:           *timerInfo.WorkerTaskType,
		ProcessExecutionId: currentTask.ProcessExecutionId,
		StateId:            currentTask.StateId,
		StateIdSequence:    currentTask.StateIdSequence,
		Info:               taskInfoBytes,
	})
	if err != nil {
		return err
	}
	return tx.DeleteTimerTask(ctx, extensions2.TimerTaskRowDeleteFilter{
		ShardId:              currentTask.ShardId,
		FireTimeUnixSeconds:  currentTask.FireTimestampSeconds,
		TaskSequence:         *currentTask.TaskSequence,
		OptionalPartitionKey: currentTask.OptionalPartitionKey,
	})
}

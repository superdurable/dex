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

	"github.com/xcherryio/xcherry/server/common/log/tag"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) ProcessTimerTaskForTimerCommand(
	ctx context.Context, request data_models2.ProcessTimerTaskRequest,
) (*data_models2.ProcessTimerTaskResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doProcessTimerTaskForTimerCommandTx(ctx, tx, request)
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

	return resp, err
}

func (p sqlProcessStoreImpl) doProcessTimerTaskForTimerCommandTx(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.ProcessTimerTaskRequest,
) (*data_models2.ProcessTimerTaskResponse, error) {
	task := request.Task
	timerCommandIndex := task.TimerTaskInfo.TimerCommandIndex

	// Step 1: get localQueues from the process execution row
	prcRow, err := tx.SelectProcessExecutionForUpdate(ctx, task.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	localQueues, err := data_models2.NewStateExecutionLocalQueuesFromBytes(prcRow.StateExecutionLocalQueues)
	if err != nil {
		return nil, err
	}

	// Step 2: update the state execution row
	stateRow, err := tx.SelectAsyncStateExecutionForUpdate(ctx, extensions2.AsyncStateExecutionSelectFilter{
		ProcessExecutionId: task.ProcessExecutionId,
		StateId:            task.StateId,
		StateIdSequence:    task.StateIdSequence,
	})
	if err != nil {
		return nil, err
	}

	// early stop if the state is not waiting commands
	if stateRow.Status != data_models2.StateExecutionStatusWaitUntilWaiting {
		return &data_models2.ProcessTimerTaskResponse{
			HasNewImmediateTask: false,
		}, nil
	}

	stateRow.LastFailure = nil

	commandRequest, err := data_models2.BytesToCommandRequest(stateRow.WaitUntilCommands)
	if err != nil {
		return nil, err
	}

	commandResults, err := data_models2.BytesToCommandResultsJson(stateRow.WaitUntilCommandResults)
	if err != nil {
		return nil, err
	}

	p.updateCommandResultsWithFiredTimerCommand(&commandResults, timerCommandIndex)

	hasNewImmediateTask := false

	if p.hasCompletedWaitUntilWaiting(commandRequest, commandResults) {
		hasNewImmediateTask = true

		err = p.updateWhenCompletedWaitUntilWaiting(ctx, tx, task.ShardId, &localQueues, stateRow)
		if err != nil {
			return nil, err
		}
	}

	stateRow.WaitUntilCommandResults, err = data_models2.FromCommandResultsJsonToBytes(commandResults)
	if err != nil {
		return nil, err
	}

	err = tx.UpdateAsyncStateExecution(ctx, *stateRow)
	if err != nil {
		return nil, err
	}

	// Step 3: update process execution row, and submit
	prcRow.StateExecutionLocalQueues, err = localQueues.ToBytes()
	if err != nil {
		return nil, err
	}

	err = tx.UpdateProcessExecution(ctx, *prcRow)
	if err != nil {
		return nil, err
	}

	// step 4: delete timer task
	err = tx.DeleteTimerTask(ctx, extensions2.TimerTaskRowDeleteFilter{
		ShardId:              task.ShardId,
		FireTimeUnixSeconds:  task.FireTimestampSeconds,
		TaskSequence:         *task.TaskSequence,
		OptionalPartitionKey: task.OptionalPartitionKey,
	})
	if err != nil {
		return nil, err
	}

	return &data_models2.ProcessTimerTaskResponse{
		HasNewImmediateTask: hasNewImmediateTask,
	}, nil
}

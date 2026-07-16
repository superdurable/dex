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
	"time"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/log/tag"
	extensions2 "github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) ProcessWaitUntilExecution(
	ctx context.Context, request data_models2.ProcessWaitUntilExecutionRequest,
) (*data_models2.ProcessWaitUntilExecutionResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doProcessWaitUntilExecutionTx(ctx, tx, request)
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

func (p sqlProcessStoreImpl) doProcessWaitUntilExecutionTx(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.ProcessWaitUntilExecutionRequest,
) (*data_models2.ProcessWaitUntilExecutionResponse, error) {
	hasNewImmediateTask := false
	var fireTimestamps []int64

	if request.CommandRequest.GetWaitingType() == xcapi.EMPTY_COMMAND {
		hasNewImmediateTask = true
		err := p.completeWaitUntilExecution(ctx, tx, data_models2.CompleteWaitUntilExecutionRequest{
			TaskShardId:        request.TaskShardId,
			ProcessExecutionId: request.ProcessExecutionId,
			StateExecutionId:   request.StateExecutionId,
			PreviousVersion:    request.Prepare.PreviousVersion,
		})
		if err != nil {
			return nil, err
		}
	} else {
		resp, err := p.updateWaitUntilExecution(ctx, tx, request)
		if err != nil {
			return nil, err
		}

		if resp.HasNewImmediateTask {
			hasNewImmediateTask = true
		}
		fireTimestamps = resp.FireTimestamps
	}

	hasNewImmediateTask2, err := p.publishToLocalQueue(ctx, tx, request.ProcessExecutionId, request.TaskShardId, request.PublishToLocalQueue)
	if err != nil {
		return nil, err
	}
	if hasNewImmediateTask2 {
		hasNewImmediateTask = true
	}

	err = tx.DeleteImmediateTask(ctx, extensions2.ImmediateTaskRowDeleteFilter{
		ShardId:      request.TaskShardId,
		TaskSequence: request.TaskSequence,
	})
	if err != nil {
		return nil, err
	}

	return &data_models2.ProcessWaitUntilExecutionResponse{
		HasNewImmediateTask: hasNewImmediateTask,
		FireTimestamps:      fireTimestamps,
	}, nil
}

func (p sqlProcessStoreImpl) completeWaitUntilExecution(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.CompleteWaitUntilExecutionRequest,
) error {
	stateRow := extensions2.AsyncStateExecutionRowForUpdateWithoutCommands{
		ProcessExecutionId: request.ProcessExecutionId,
		StateId:            request.StateId,
		StateIdSequence:    request.StateIdSequence,
		Status:             data_models2.StateExecutionStatusExecuteRunning,
		PreviousVersion:    request.PreviousVersion,
		LastFailure:        nil,
	}

	err := tx.UpdateAsyncStateExecutionWithoutCommands(ctx, stateRow)
	if err != nil {
		if p.session.IsConditionalUpdateFailure(err) {
			p.logger.Warn("UpdateAsyncStateExecutionWithoutCommands failed at conditional update")
		}
		return err
	}

	return tx.InsertImmediateTask(ctx, extensions2.ImmediateTaskRowForInsert{
		ShardId:            request.TaskShardId,
		TaskType:           data_models2.ImmediateTaskTypeExecute,
		ProcessExecutionId: request.ProcessExecutionId,
		StateId:            request.StateId,
		StateIdSequence:    request.StateIdSequence,
	})
}

func (p sqlProcessStoreImpl) updateWaitUntilExecution(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.ProcessWaitUntilExecutionRequest,
) (*data_models2.ProcessWaitUntilExecutionResponse, error) {
	hasLocalQueueCommands := len(request.CommandRequest.GetLocalQueueCommands()) > 0

	var prcRow *extensions2.ProcessExecutionRowForUpdate
	var localQueues data_models2.StateExecutionLocalQueuesJson
	var consumedMessagesMap map[int][]data_models2.InternalLocalQueueMessage

	// Step 1: get localQueues from the process execution row,
	// update it with commands, and try to consume for the state execution
	if hasLocalQueueCommands {
		prcRow2, err := tx.SelectProcessExecutionForUpdate(ctx, request.ProcessExecutionId)
		if err != nil {
			return nil, err
		}

		prcRow = prcRow2

		localQueues, err = data_models2.NewStateExecutionLocalQueuesFromBytes(prcRow.StateExecutionLocalQueues)
		if err != nil {
			return nil, err
		}

		localQueues.AddNewLocalQueueCommands(request.StateExecutionId, request.CommandRequest.GetLocalQueueCommands())

		consumedMessagesMap = localQueues.TryConsumeForStateExecution(
			request.StateExecutionId, request.CommandRequest.GetWaitingType())
	}

	// Step 2: update the state execution row
	stateRow, err := tx.SelectAsyncStateExecutionForUpdate(ctx, extensions2.AsyncStateExecutionSelectFilter{
		ProcessExecutionId: request.ProcessExecutionId,
		StateId:            request.StateId,
		StateIdSequence:    request.StateIdSequence,
	})
	if err != nil {
		return nil, err
	}

	stateRow.Status = data_models2.StateExecutionStatusWaitUntilWaiting
	stateRow.LastFailure = nil

	stateRow.WaitUntilCommands, err = data_models2.FromCommandRequestToBytes(request.CommandRequest)
	if err != nil {
		return nil, err
	}

	commandResults, err := data_models2.BytesToCommandResultsJson(stateRow.WaitUntilCommandResults)
	if err != nil {
		return nil, err
	}

	// Step 2 - 1: update local queue command results
	var allConsumedMessages []data_models2.InternalLocalQueueMessage
	for _, consumedMessages := range consumedMessagesMap {
		allConsumedMessages = append(allConsumedMessages, consumedMessages...)
	}

	dedupIdToLocalQueueMessageMap, err := p.getDedupIdToLocalQueueMessageMap(ctx, request.ProcessExecutionId, allConsumedMessages)
	if err != nil {
		return nil, err
	}

	err = p.updateCommandResultsWithNewlyConsumedLocalQueueMessages(&commandResults, consumedMessagesMap, dedupIdToLocalQueueMessageMap)
	if err != nil {
		return nil, err
	}

	hasNewImmediateTask := false

	if hasLocalQueueCommands && p.hasCompletedWaitUntilWaiting(request.CommandRequest, commandResults) {
		hasNewImmediateTask = true

		err = p.updateWhenCompletedWaitUntilWaiting(ctx, tx, request.TaskShardId, &localQueues, stateRow)
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

	// Step 2 - 2: create timer command tasks
	var fireTimestamps []int64

	for idx, timerCommand := range request.CommandRequest.TimerCommands {
		if timerCommand.DelayInSeconds < 0 {
			timerCommand.DelayInSeconds = 0
		}

		timerTaskInfoJson := data_models2.TimerTaskInfoJson{
			TimerCommandIndex: idx,
		}
		timerInfoBytes, err := timerTaskInfoJson.ToBytes()
		if err != nil {
			return nil, err
		}

		fireTimestamp := time.Now().Add(time.Second * time.Duration(timerCommand.DelayInSeconds)).Unix()
		err = tx.InsertTimerTask(ctx, extensions2.TimerTaskRowForInsert{
			ShardId:             request.TaskShardId,
			FireTimeUnixSeconds: fireTimestamp,
			TaskType:            data_models2.TimerTaskTypeTimerCommand,
			ProcessExecutionId:  request.ProcessExecutionId,
			StateId:             request.StateId,
			StateIdSequence:     request.StateIdSequence,
			Info:                timerInfoBytes,
		})
		if err != nil {
			return nil, err
		}

		fireTimestamps = append(fireTimestamps, fireTimestamp)
	}

	// Step 3: update process execution row, and submit
	if hasLocalQueueCommands {
		prcRow.StateExecutionLocalQueues, err = localQueues.ToBytes()
		if err != nil {
			return nil, err
		}

		err = tx.UpdateProcessExecution(ctx, *prcRow)
		if err != nil {
			return nil, err
		}
	}

	return &data_models2.ProcessWaitUntilExecutionResponse{
		HasNewImmediateTask: hasNewImmediateTask,
		FireTimestamps:      fireTimestamps,
	}, nil
}

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

func (p sqlProcessStoreImpl) ProcessLocalQueueMessages(
	ctx context.Context, request data_models2.ProcessLocalQueueMessagesRequest,
) (*data_models2.ProcessLocalQueueMessagesResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doProcessLocalQueueMessagesTx(ctx, tx, request)
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

func (p sqlProcessStoreImpl) doProcessLocalQueueMessagesTx(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.ProcessLocalQueueMessagesRequest,
) (*data_models2.ProcessLocalQueueMessagesResponse, error) {
	assignedStateExecutionIdToMessagesMap := map[string]map[int][]data_models2.InternalLocalQueueMessage{}

	// Step 1: get localQueues from the process execution row, and update it with messages
	prcRow, err := tx.SelectProcessExecutionForUpdate(ctx, request.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	localQueues, err := data_models2.NewStateExecutionLocalQueuesFromBytes(prcRow.StateExecutionLocalQueues)
	if err != nil {
		return nil, err
	}

	for _, message := range request.Messages {
		assignedStateExecutionIdString, idx, consumedMessages := localQueues.AddMessageAndTryConsume(message)

		if assignedStateExecutionIdString == "" {
			continue
		}

		_, ok := assignedStateExecutionIdToMessagesMap[assignedStateExecutionIdString]
		if !ok {
			assignedStateExecutionIdToMessagesMap[assignedStateExecutionIdString] = map[int][]data_models2.InternalLocalQueueMessage{}
		}
		assignedStateExecutionIdToMessagesMap[assignedStateExecutionIdString][idx] = consumedMessages
	}

	// Step 2: update assigned state execution rows
	hasNewImmediateTask := false

	if len(assignedStateExecutionIdToMessagesMap) > 0 {
		var allConsumedMessages []data_models2.InternalLocalQueueMessage
		for _, consumedMessagesMap := range assignedStateExecutionIdToMessagesMap {
			for _, consumedMessages := range consumedMessagesMap {
				allConsumedMessages = append(allConsumedMessages, consumedMessages...)
			}
		}

		dedupIdToLocalQueueMessageMap, err := p.getDedupIdToLocalQueueMessageMap(ctx, prcRow.ProcessExecutionId, allConsumedMessages)
		if err != nil {
			return nil, err
		}

		for assignedStateExecutionIdString, consumedMessagesMap := range assignedStateExecutionIdToMessagesMap {
			stateExecutionId, err := data_models2.NewStateExecutionIdFromString(assignedStateExecutionIdString)
			if err != nil {
				return nil, err
			}

			stateRow, err := tx.SelectAsyncStateExecutionForUpdate(ctx, extensions2.AsyncStateExecutionSelectFilter{
				ProcessExecutionId: prcRow.ProcessExecutionId,
				StateId:            stateExecutionId.StateId,
				StateIdSequence:    stateExecutionId.StateIdSequence,
			})
			if err != nil {
				return nil, err
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

			err = p.updateCommandResultsWithNewlyConsumedLocalQueueMessages(&commandResults, consumedMessagesMap, dedupIdToLocalQueueMessageMap)
			if err != nil {
				return nil, err
			}

			if p.hasCompletedWaitUntilWaiting(commandRequest, commandResults) {
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
		}
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

	// Step 4: delete the task row
	err = tx.DeleteImmediateTask(ctx, extensions2.ImmediateTaskRowDeleteFilter{
		ShardId:      request.TaskShardId,
		TaskSequence: request.TaskSequence,
	})
	if err != nil {
		return nil, err
	}

	return &data_models2.ProcessLocalQueueMessagesResponse{
		HasNewImmediateTask: hasNewImmediateTask,
	}, nil
}

// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) PrepareStateExecution(
	ctx context.Context, request data_models2.PrepareStateExecutionRequest,
) (*data_models2.PrepareStateExecutionResponse, error) {
	stateRow, err := p.session.SelectAsyncStateExecution(
		ctx, extensions.AsyncStateExecutionSelectFilter{
			ProcessExecutionId: request.ProcessExecutionId,
			StateId:            request.StateId,
			StateIdSequence:    request.StateIdSequence,
		})
	if err != nil {
		return nil, err
	}

	info, err := data_models2.BytesToAsyncStateExecutionInfo(stateRow.Info)
	if err != nil {
		return nil, err
	}

	input, err := data_models2.BytesToEncodedObject(stateRow.Input)
	if err != nil {
		return nil, err
	}

	commandResultsJson, err := data_models2.BytesToCommandResultsJson(stateRow.WaitUntilCommandResults)
	if err != nil {
		return nil, err
	}

	commandRequest, err := data_models2.BytesToCommandRequest(stateRow.WaitUntilCommands)
	if err != nil {
		return nil, err
	}

	commandResults := p.prepareWaitUntilCommandResults(commandResultsJson, commandRequest)

	return &data_models2.PrepareStateExecutionResponse{
		Status:                  stateRow.Status,
		WaitUntilCommandResults: commandResults,
		PreviousVersion:         stateRow.PreviousVersion,
		Info:                    info,
		Input:                   input,
	}, nil
}

func (p sqlProcessStoreImpl) prepareWaitUntilCommandResults(
	commandResultsJson data_models2.CommandResultsJson, commandRequest xcapi.CommandRequest,
) xcapi.CommandResults {
	commandResults := xcapi.CommandResults{}

	for idx := range commandRequest.TimerCommands {
		timerResult := xcapi.TimerResult{
			Status: xcapi.WAITING_COMMAND,
		}

		fired, ok := commandResultsJson.TimerResults[idx]
		if ok {
			if fired {
				timerResult.Status = xcapi.COMPLETED_COMMAND
			} else {
				timerResult.Status = xcapi.SKIPPED_COMMAND
			}
		}

		commandResults.TimerResults = append(commandResults.TimerResults, timerResult)
	}

	for idx, localQueueCommand := range commandRequest.LocalQueueCommands {
		localQueueResult := xcapi.LocalQueueResult{
			Status:    xcapi.WAITING_COMMAND,
			QueueName: localQueueCommand.GetQueueName(),
			Messages:  nil,
		}

		result, ok := commandResultsJson.LocalQueueResults[idx]
		if ok {
			localQueueResult.Status = xcapi.COMPLETED_COMMAND
			localQueueResult.Messages = result
		}

		commandResults.LocalQueueResults = append(commandResults.LocalQueueResults, localQueueResult)
	}

	return commandResults
}

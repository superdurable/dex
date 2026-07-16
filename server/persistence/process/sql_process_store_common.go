// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"
	"fmt"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/uuid"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func insertAsyncStateExecution(
	ctx context.Context,
	tx extensions2.SQLTransaction,
	processExecutionId uuid.UUID,
	stateId string,
	stateIdSeq int,
	stateConfig *xcapi.AsyncStateConfig,
	stateInput []byte,
	stateInfo []byte,
) error {
	commandResultsBytes, err := data_models2.FromCommandResultsJsonToBytes(data_models2.NewCommandResultsJson())
	if err != nil {
		return err
	}

	// set this as default value for https://github.com/xcherryio/xcherry/issues/100
	emptyCmdReq := xcapi.NewCommandRequest(xcapi.EMPTY_COMMAND)
	commandRequestBytes, err := data_models2.FromCommandRequestToBytes(*emptyCmdReq)
	if err != nil {
		return err
	}

	stateRow := extensions2.AsyncStateExecutionRow{
		ProcessExecutionId: processExecutionId,
		StateId:            stateId,
		StateIdSequence:    int32(stateIdSeq),
		// the waitUntil/execute status will be set later

		WaitUntilCommands:       commandRequestBytes,
		WaitUntilCommandResults: commandResultsBytes,

		LastFailure:     nil,
		PreviousVersion: 1,
		Input:           stateInput,
		Info:            stateInfo,
	}

	if stateConfig.GetSkipWaitUntil() {
		stateRow.Status = data_models2.StateExecutionStatusExecuteRunning
	} else {
		stateRow.Status = data_models2.StateExecutionStatusWaitUntilRunning
	}

	return tx.InsertAsyncStateExecution(ctx, stateRow)
}

func insertImmediateTask(
	ctx context.Context,
	tx extensions2.SQLTransaction,
	processExecutionId uuid.UUID,
	stateId string,
	stateIdSeq int,
	stateConfig *xcapi.AsyncStateConfig,
	shardId int32,
) error {
	immediateTaskRow := extensions2.ImmediateTaskRowForInsert{
		ShardId:            shardId,
		ProcessExecutionId: processExecutionId,
		StateId:            stateId,
		StateIdSequence:    int32(stateIdSeq),
	}
	if stateConfig.GetSkipWaitUntil() {
		immediateTaskRow.TaskType = data_models2.ImmediateTaskTypeExecute
	} else {
		immediateTaskRow.TaskType = data_models2.ImmediateTaskTypeWaitUntil
	}

	return tx.InsertImmediateTask(ctx, immediateTaskRow)
}

// publishToLocalQueue inserts len(valid_messages) rows into xcherry_sys_local_queue_messages,
// and inserts only one row into xcherry_sys_immediate_tasks with all the dedupIds for these messages.
// publishToLocalQueue returns (HasNewImmediateTask, error).
func (p sqlProcessStoreImpl) publishToLocalQueue(
	ctx context.Context, tx extensions2.SQLTransaction, processExecutionId uuid.UUID, shardId int32,
	messages []xcapi.LocalQueueMessage,
) (bool, error) {

	var localQueueMessageInfo []data_models2.LocalQueueMessageInfoJson

	for _, message := range messages {
		dedupId := uuid.MustNewUUID()

		// dealing with user-customized dedupId
		if message.GetDedupId() != "" {
			dedupId2, err := uuid.ParseUUID(message.GetDedupId())
			if err != nil {
				return false, err
			}
			dedupId = dedupId2
		}

		// insert a row into xcherry_sys_local_queue_messages

		payloadBytes, err := data_models2.FromEncodedObjectIntoBytes(message.Payload)
		if err != nil {
			return false, err
		}

		insertSuccessfully, err := tx.InsertLocalQueueMessage(ctx, extensions2.LocalQueueMessageRow{
			ProcessExecutionId: processExecutionId,
			QueueName:          message.GetQueueName(),
			DedupId:            dedupId,
			Payload:            payloadBytes,
		})
		if err != nil {
			return false, err
		}
		if !insertSuccessfully {
			continue
		}

		localQueueMessageInfo = append(localQueueMessageInfo, data_models2.LocalQueueMessageInfoJson{
			QueueName: message.GetQueueName(),
			DedupId:   dedupId,
		})
	}

	// insert a row into xcherry_sys_immediate_tasks

	if len(localQueueMessageInfo) == 0 {
		return false, nil
	}

	taskInfoBytes, err := data_models2.FromImmediateTaskInfoIntoBytes(
		data_models2.ImmediateTaskInfoJson{
			LocalQueueMessageInfo: localQueueMessageInfo,
		})
	if err != nil {
		return false, err
	}

	err = tx.InsertImmediateTask(ctx, extensions2.ImmediateTaskRowForInsert{
		ShardId:  shardId,
		TaskType: data_models2.ImmediateTaskTypeNewLocalQueueMessages,

		ProcessExecutionId: processExecutionId,
		StateId:            "",
		StateIdSequence:    0,
		Info:               taskInfoBytes,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func (p sqlProcessStoreImpl) getDedupIdToLocalQueueMessageMap(
	ctx context.Context, processExecutionId uuid.UUID,
	consumedMessages []data_models2.InternalLocalQueueMessage,
) (map[string]extensions2.LocalQueueMessageRow, error) {
	if len(consumedMessages) == 0 {
		return map[string]extensions2.LocalQueueMessageRow{}, nil
	}

	var allConsumedDedupIdStrings []string
	for _, consumedMessage := range consumedMessages {
		allConsumedDedupIdStrings = append(allConsumedDedupIdStrings, consumedMessage.DedupId)
	}

	allConsumedLocalQueueMessages, err := p.session.SelectLocalQueueMessages(ctx, processExecutionId, allConsumedDedupIdStrings)
	if err != nil {
		return nil, err
	}

	dedupIdToLocalQueueMessageMap := map[string]extensions2.LocalQueueMessageRow{}
	for _, message := range allConsumedLocalQueueMessages {
		dedupIdToLocalQueueMessageMap[message.DedupId.String()] = message
	}

	return dedupIdToLocalQueueMessageMap, nil
}

func (p sqlProcessStoreImpl) updateCommandResultsWithNewlyConsumedLocalQueueMessages(
	commandResults *data_models2.CommandResultsJson,
	newlyConsumedMessagesMap map[int][]data_models2.InternalLocalQueueMessage,
	dedupIdToLocalQueueMessageMap map[string]extensions2.LocalQueueMessageRow,
) error {

	for idx, consumedMessages := range newlyConsumedMessagesMap {
		var localQueueMessageResults []xcapi.LocalQueueMessageResult
		for _, consumedMessage := range consumedMessages {
			message, ok := dedupIdToLocalQueueMessageMap[consumedMessage.DedupId]
			if !ok {
				panic(fmt.Sprintf("Something wrong with the dedupIdToLocalQueueMessageMap, key: %v", consumedMessage.DedupId))
			}

			dedupIdString := message.DedupId.String()
			payload, err := data_models2.BytesToEncodedObject(message.Payload)
			if err != nil {
				return err
			}

			localQueueMessageResults = append(localQueueMessageResults, xcapi.LocalQueueMessageResult{
				DedupId: dedupIdString,
				Payload: &payload,
			})
		}

		commandResults.LocalQueueResults[idx] = localQueueMessageResults
	}

	return nil
}

func (p sqlProcessStoreImpl) updateCommandResultsWithFiredTimerCommand(
	commandResults *data_models2.CommandResultsJson, timerCommandIndex int,
) {
	commandResults.TimerResults[timerCommandIndex] = true
}

func (p sqlProcessStoreImpl) hasCompletedWaitUntilWaiting(
	commandRequest xcapi.CommandRequest, commandResults data_models2.CommandResultsJson,
) bool {
	switch commandRequest.GetWaitingType() {
	case xcapi.ANY_OF_COMPLETION:
		return len(commandResults.LocalQueueResults)+len(commandResults.TimerResults) > 0
	case xcapi.ALL_OF_COMPLETION:
		return len(commandResults.LocalQueueResults)+len(commandResults.TimerResults) ==
			len(commandRequest.LocalQueueCommands)+len(commandRequest.TimerCommands)
	case xcapi.EMPTY_COMMAND:
		return true
	default:
		panic("this is not supported")
	}
}

func (p sqlProcessStoreImpl) updateWhenCompletedWaitUntilWaiting(
	ctx context.Context, tx extensions2.SQLTransaction, shardId int32,
	localQueues *data_models2.StateExecutionLocalQueuesJson, stateRow *extensions2.AsyncStateExecutionRowForUpdate,
) error {
	localQueues.CleanupFor(data_models2.StateExecutionId{
		StateId:         stateRow.StateId,
		StateIdSequence: stateRow.StateIdSequence,
	})

	stateRow.Status = data_models2.StateExecutionStatusExecuteRunning

	return tx.InsertImmediateTask(ctx, extensions2.ImmediateTaskRowForInsert{
		ShardId:            shardId,
		TaskType:           data_models2.ImmediateTaskTypeExecute,
		ProcessExecutionId: stateRow.ProcessExecutionId,
		StateId:            stateRow.StateId,
		StateIdSequence:    stateRow.StateIdSequence,
	})
}

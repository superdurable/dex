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
	"math"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/ptr"
	"github.com/xcherryio/xcherry/server/common/uuid"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func createGetTimerTaskResponse(
	shardId int32, dbTimerTasks []extensions2.TimerTaskRow, reqPageSize *int32,
) (*data_models2.GetTimerTasksResponse, error) {
	var tasks []data_models2.TimerTask
	for _, t := range dbTimerTasks {
		info, err := data_models2.BytesToTimerTaskInfo(t.Info)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, data_models2.TimerTask{
			ShardId:              shardId,
			FireTimestampSeconds: t.FireTimeUnixSeconds,
			TaskSequence:         ptr.Any(t.TaskSequence),

			TaskType:           t.TaskType,
			ProcessExecutionId: t.ProcessExecutionId,
			StateExecutionId: data_models2.StateExecutionId{
				StateId:         t.StateId,
				StateIdSequence: t.StateIdSequence,
			},
			TimerTaskInfo: info,
		})
	}
	resp := &data_models2.GetTimerTasksResponse{
		Tasks: tasks,
	}
	if len(dbTimerTasks) > 0 {
		firstTask := dbTimerTasks[0]
		lastTask := dbTimerTasks[len(dbTimerTasks)-1]
		resp.MinFireTimestampSecondsInclusive = firstTask.FireTimeUnixSeconds
		resp.MaxFireTimestampSecondsInclusive = lastTask.FireTimeUnixSeconds

		resp.MinSequenceInclusive = math.MaxInt64
		resp.MaxSequenceInclusive = math.MinInt64
		for _, t := range dbTimerTasks {
			if t.TaskSequence < resp.MinSequenceInclusive {
				resp.MinSequenceInclusive = t.TaskSequence
			}
			if t.TaskSequence > resp.MaxSequenceInclusive {
				resp.MaxSequenceInclusive = t.TaskSequence
			}
		}
	}
	if reqPageSize != nil {
		if len(dbTimerTasks) == int(*reqPageSize) {
			resp.FullPage = true
		}
	}
	return resp, nil
}

type (
	HandleStateDecisionRequest struct {
		Namespace          string
		ProcessId          string
		ProcessType        string
		ProcessExecutionId uuid.UUID
		StateDecision      xcapi.StateDecision
		AppDatabaseConfig  *data_models2.InternalAppDatabaseConfig
		WorkerUrl          string

		// for ProcessExecutionRowForUpdate
		ProcessExecutionRowStateExecutionSequenceMaps *data_models2.StateExecutionSequenceMapsJson
		ProcessExecutionRowGracefulCompleteRequested  bool
		ProcessExecutionRowStatus                     data_models2.ProcessExecutionStatus

		TaskShardId int32
	}

	HandleStateDecisionResponse struct {
		HasNewImmediateTask bool

		// for ProcessExecutionRowForUpdate to update
		ProcessExecutionRowNewStateExecutionSequenceMaps *data_models2.StateExecutionSequenceMapsJson
		ProcessExecutionRowNewGracefulCompleteRequested  bool
		ProcessExecutionRowNewStatus                     data_models2.ProcessExecutionStatus
	}
)

func (p sqlProcessStoreImpl) handleStateDecision(
	ctx context.Context, tx extensions2.SQLTransaction,
	request HandleStateDecisionRequest,
) (*HandleStateDecisionResponse, error) {
	hasNewImmediateTask := false

	// these fields will be updated and returned back in response for ProcessExecutionRowForUpdate
	sequenceMaps := request.ProcessExecutionRowStateExecutionSequenceMaps
	procExecGracefulCompleteRequested := request.ProcessExecutionRowGracefulCompleteRequested
	procExecStatus := request.ProcessExecutionRowStatus

	if len(request.StateDecision.GetNextStates()) > 0 {
		hasNewImmediateTask = true

		for _, next := range request.StateDecision.GetNextStates() {
			stateIdSeq := sequenceMaps.StartNewStateExecution(next.StateId)

			stateInputBytes, err := data_models2.FromEncodedObjectIntoBytes(next.StateInput)
			if err != nil {
				return nil, err
			}

			stateInfo := data_models2.AsyncStateExecutionInfoJson{
				Namespace:         request.Namespace,
				ProcessId:         request.ProcessId,
				ProcessType:       request.ProcessType,
				WorkerURL:         request.WorkerUrl,
				StateConfig:       next.StateConfig,
				AppDatabaseConfig: request.AppDatabaseConfig,
			}

			stateInfoBytes, err := stateInfo.ToBytes()
			if err != nil {
				return nil, err
			}

			err = insertAsyncStateExecution(ctx, tx, request.ProcessExecutionId, next.StateId, stateIdSeq, next.StateConfig, stateInputBytes, stateInfoBytes)
			if err != nil {
				return nil, err
			}

			err = insertImmediateTask(ctx, tx, request.ProcessExecutionId, next.StateId, stateIdSeq, next.StateConfig, request.TaskShardId)
			if err != nil {
				return nil, err
			}
		}
	}

	// If the process was previously configured to gracefully complete and there are no states running,
	// then gracefully complete the process regardless of the thread close type set in this state.
	// Otherwise, handle the thread close type set in this state.

	shouldGracefulComplete := procExecGracefulCompleteRequested && len(sequenceMaps.PendingExecutionMap) == 0

	toAbortRunningAsyncStates := false

	threadDecision := request.StateDecision.GetThreadCloseDecision()
	if !shouldGracefulComplete && request.StateDecision.HasThreadCloseDecision() {
		switch threadDecision.GetCloseType() {
		case xcapi.GRACEFUL_COMPLETE_PROCESS:
			procExecGracefulCompleteRequested = true
			shouldGracefulComplete = len(sequenceMaps.PendingExecutionMap) == 0
		case xcapi.FORCE_COMPLETE_PROCESS:
			toAbortRunningAsyncStates = len(sequenceMaps.PendingExecutionMap) > 0

			procExecStatus = data_models2.ProcessExecutionStatusCompleted
			sequenceMaps.PendingExecutionMap = map[string]map[int]bool{}
		case xcapi.FORCE_FAIL_PROCESS:
			toAbortRunningAsyncStates = len(sequenceMaps.PendingExecutionMap) > 0

			procExecStatus = data_models2.ProcessExecutionStatusFailed
			sequenceMaps.PendingExecutionMap = map[string]map[int]bool{}
		case xcapi.DEAD_END:
			// do nothing
		}
	}

	if shouldGracefulComplete {
		procExecStatus = data_models2.ProcessExecutionStatusCompleted
	}

	if toAbortRunningAsyncStates {
		// handle xcherry_sys_async_state_executions
		// find all related rows with the processExecutionId, and
		// modify the wait_until/execute status from running to aborted
		err := tx.BatchUpdateAsyncStateExecutionsToAbortRunning(ctx, request.ProcessExecutionId)
		if err != nil {
			return nil, err
		}
	}

	return &HandleStateDecisionResponse{
		HasNewImmediateTask: hasNewImmediateTask,
		ProcessExecutionRowNewStateExecutionSequenceMaps: sequenceMaps,
		ProcessExecutionRowNewGracefulCompleteRequested:  procExecGracefulCompleteRequested,
		ProcessExecutionRowNewStatus:                     procExecStatus,
	}, nil
}

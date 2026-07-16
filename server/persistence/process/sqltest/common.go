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

package sqltest

import (
	"context"
	"encoding/json"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/ptr"
	"github.com/superdurable/dex/server/common/uuid"
	"github.com/superdurable/dex/server/persistence"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProcessType = "test-type"
const testWorkerUrl = "test-url"
const stateId1 = "state1"
const stateId2 = "state2"
const namespace = "test-ns"
const defaultShardId = 0

func createTestInput() xcapi.EncodedObject {
	return xcapi.EncodedObject{
		Encoding: "test-encoding",
		Data:     "test-data",
	}
}

func createEmptyEncodedObject() xcapi.EncodedObject {
	return xcapi.EncodedObject{
		Encoding: "",
		Data:     "",
	}
}

func startProcessWithConfigs(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore,
	namespace, processId string,
	input xcapi.EncodedObject, appDatabaseConfig *xcapi.AppDatabaseConfig, stateCfg *xcapi.AsyncStateConfig,
) uuid.UUID {
	startReq := createStartRequest(namespace, processId, input, appDatabaseConfig, stateCfg)
	startResp, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})

	require.NoError(t, err)
	require.NoError(t, startResp.AppDatabaseWritingError)
	ass.False(startResp.AlreadyStarted)
	ass.True(startResp.HasNewImmediateTask)
	ass.True(len(startResp.ProcessExecutionId.String()) > 0)
	return startResp.ProcessExecutionId
}

func startProcess(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) uuid.UUID {
	return startProcessWithConfigs(ctx, t, ass, store, namespace, processId, input, nil, nil)
}

func terminateProcess(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string,
) {
	resp, err := store.StopProcess(ctx, data_models2.StopProcessRequest{
		Namespace:       namespace,
		ProcessId:       processId,
		ProcessStopType: xcapi.TERMINATE,
	})

	require.NoError(t, err)
	ass.False(resp.NotExists)
}

func startProcessWithAllowIfPreviousExitAbnormally(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) uuid.UUID {
	startReq := createStartRequestWithAllowIfPreviousExitAbnormallyPolicy(namespace, processId, input)
	startResp, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})

	require.NoError(t, err)
	ass.False(startResp.AlreadyStarted)
	ass.True(startResp.HasNewImmediateTask)
	ass.True(len(startResp.ProcessExecutionId.String()) > 0)
	return startResp.ProcessExecutionId
}

func startProcessWithTerminateIfRunningPolicy(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) uuid.UUID {
	startReq := createStartRequestWithTerminateIfRunningPolicy(namespace, processId, input)
	startResp, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})

	require.NoError(t, err)
	ass.False(startResp.AlreadyStarted)
	ass.True(startResp.HasNewImmediateTask)
	ass.True(len(startResp.ProcessExecutionId.String()) > 0)
	return startResp.ProcessExecutionId
}

func startProcessWithAllowIfNoRunningPolicy(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) uuid.UUID {
	startReq := createStartRequestWithAllowIfNoRunningPolicy(namespace, processId, input)
	startResp, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})

	require.NoError(t, err)
	ass.False(startResp.AlreadyStarted)
	ass.True(startResp.HasNewImmediateTask)
	ass.True(len(startResp.ProcessExecutionId.String()) > 0)
	return startResp.ProcessExecutionId
}

func startProcessWithDisallowReusePolicy(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) uuid.UUID {
	startReq := createStartRequestWithDisallowReusePolicy(namespace, processId, input)
	startResp, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})

	require.NoError(t, err)
	ass.True(startResp.AlreadyStarted)
	return startResp.ProcessExecutionId
}

func createStartRequestWithAllowIfPreviousExitAbnormallyPolicy(
	namespace, processId string, input xcapi.EncodedObject,
) xcapi.ProcessExecutionStartRequest {
	// Other values like processType, workerUrl etc. are kept constants for simplicity
	return xcapi.ProcessExecutionStartRequest{
		Namespace:        namespace,
		ProcessId:        processId,
		ProcessType:      "test-type",
		WorkerUrl:        "test-url",
		StartStateId:     ptr.Any(stateId1),
		StartStateInput:  &input,
		StartStateConfig: nil,
		ProcessStartConfig: &xcapi.ProcessStartConfig{
			TimeoutSeconds: ptr.Any(int32(100)),
			IdReusePolicy:  xcapi.ALLOW_IF_PREVIOUS_EXIT_ABNORMALLY.Ptr().Ptr(),
		},
	}
}

func createStartRequestWithDisallowReusePolicy(
	namespace, processId string, input xcapi.EncodedObject,
) xcapi.ProcessExecutionStartRequest {
	// Other values like processType, workerUrl etc. are kept constants for simplicity
	return xcapi.ProcessExecutionStartRequest{
		Namespace:        namespace,
		ProcessId:        processId,
		ProcessType:      "test-type",
		WorkerUrl:        "test-url",
		StartStateId:     ptr.Any(stateId1),
		StartStateInput:  &input,
		StartStateConfig: nil,
		ProcessStartConfig: &xcapi.ProcessStartConfig{
			TimeoutSeconds: ptr.Any(int32(100)),
			IdReusePolicy:  xcapi.DISALLOW_REUSE.Ptr(),
		},
	}
}

func createStartRequestWithAllowIfNoRunningPolicy(
	namespace, processId string, input xcapi.EncodedObject,
) xcapi.ProcessExecutionStartRequest {
	// Other values like processType, workerUrl etc. are kept constants for simplicity
	return xcapi.ProcessExecutionStartRequest{
		Namespace:        namespace,
		ProcessId:        processId,
		ProcessType:      "test-type",
		WorkerUrl:        "test-url",
		StartStateId:     ptr.Any(stateId1),
		StartStateInput:  &input,
		StartStateConfig: nil,
		ProcessStartConfig: &xcapi.ProcessStartConfig{
			TimeoutSeconds: ptr.Any(int32(100)),
			IdReusePolicy:  xcapi.ALLOW_IF_NO_RUNNING.Ptr(),
		},
	}
}

func createStartRequestWithTerminateIfRunningPolicy(
	namespace, processId string, input xcapi.EncodedObject,
) xcapi.ProcessExecutionStartRequest {
	// Other values like processType, workerUrl etc. are kept constants for simplicity
	return xcapi.ProcessExecutionStartRequest{
		Namespace:        namespace,
		ProcessId:        processId,
		ProcessType:      "test-type",
		WorkerUrl:        "test-url",
		StartStateId:     ptr.Any(stateId1),
		StartStateInput:  &input,
		StartStateConfig: nil,
		ProcessStartConfig: &xcapi.ProcessStartConfig{
			TimeoutSeconds: ptr.Any(int32(100)),
			IdReusePolicy:  xcapi.TERMINATE_IF_RUNNING.Ptr(),
		},
	}
}

func createStartRequest(
	namespace, processId string, input xcapi.EncodedObject,
	appDatabaseConfig *xcapi.AppDatabaseConfig, stateCfg *xcapi.AsyncStateConfig,
) xcapi.ProcessExecutionStartRequest {
	// Other values like processType, workerUrl etc. are kept constants for simplicity
	return xcapi.ProcessExecutionStartRequest{
		Namespace:        namespace,
		ProcessId:        processId,
		ProcessType:      "test-type",
		WorkerUrl:        "test-url",
		StartStateId:     ptr.Any(stateId1),
		StartStateInput:  &input,
		StartStateConfig: stateCfg,
		ProcessStartConfig: &xcapi.ProcessStartConfig{
			TimeoutSeconds:    ptr.Any(int32(100)),
			AppDatabaseConfig: appDatabaseConfig,
		},
	}
}

func retryStartProcessForFailure(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, namespace, processId string, input xcapi.EncodedObject,
) {
	startReq := createStartRequest(namespace, processId, input, nil, nil)
	startResp2, err := store.StartProcess(ctx, data_models2.StartProcessRequest{
		Request:        startReq,
		NewTaskShardId: defaultShardId,
	})
	require.NoError(t, err)
	ass.True(startResp2.AlreadyStarted)
	ass.False(startResp2.HasNewImmediateTask)
}

func describeProcess(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore,
	namespace, processId string, processStatus xcapi.ProcessStatus,
) {
	// Incorrect process id description
	descResp, err := store.DescribeLatestProcess(ctx, data_models2.DescribeLatestProcessRequest{
		Namespace: namespace,
		ProcessId: "some-wrong-id",
	})
	require.NoError(t, err)
	ass.True(descResp.NotExists)

	// Correct process id description
	descResp, err = store.DescribeLatestProcess(ctx, data_models2.DescribeLatestProcessRequest{
		Namespace: namespace,
		ProcessId: processId,
	})
	require.NoError(t, err)
	ass.False(descResp.NotExists)
	ass.Equal(testProcessType, descResp.Response.GetProcessType())
	ass.Equal(testWorkerUrl, descResp.Response.GetWorkerUrl())
	ass.Equal(processStatus, descResp.Response.GetStatus())
}

func checkAndGetImmediateTasks(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore, expectedLength int,
) (int64, int64, []data_models2.ImmediateTask) {
	getTasksResp, err := store.GetImmediateTasks(ctx, data_models2.GetImmediateTasksRequest{
		ShardId:                defaultShardId,
		StartSequenceInclusive: 0,
		PageSize:               10,
	})
	require.NoError(t, err)
	ass.Equal(expectedLength, len(getTasksResp.Tasks))
	return getTasksResp.MinSequenceInclusive, getTasksResp.MaxSequenceInclusive, getTasksResp.Tasks
}

func getAndCheckTimerTasksUpToTs(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore, expectedLength int,
	upToTimestamp int64,
) (int64, int64, []data_models2.TimerTask) {
	getTasksResp, err := store.GetTimerTasksUpToTimestamp(ctx, data_models2.GetTimerTasksRequest{
		ShardId:                          defaultShardId,
		MaxFireTimestampSecondsInclusive: upToTimestamp,
		PageSize:                         10,
	})
	require.NoError(t, err)
	ass.Equal(expectedLength, len(getTasksResp.Tasks))
	return getTasksResp.MinSequenceInclusive, getTasksResp.MaxSequenceInclusive, getTasksResp.Tasks
}

func getAndCheckTimerTasksUpForTimestamps(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore, expectedLength int,
	forTimestamps []int64, minTaskSeq int64,
) (int64, int64, []data_models2.TimerTask) {
	getTasksResp, err := store.GetTimerTasksForTimestamps(ctx, data_models2.GetTimerTasksForTimestampsRequest{
		ShardId:              defaultShardId,
		MinSequenceInclusive: minTaskSeq,
		DetailedRequests: []xcapi.NotifyTimerTasksRequest{
			{
				FireTimestamps: forTimestamps,
			},
		},
	})
	require.NoError(t, err)
	ass.Equal(expectedLength, len(getTasksResp.Tasks))
	return getTasksResp.MinSequenceInclusive, getTasksResp.MaxSequenceInclusive, getTasksResp.Tasks
}

func verifyImmediateTaskNoInfo(
	ass *assert.Assertions, task data_models2.ImmediateTask,
	taskType data_models2.ImmediateTaskType, stateExeId string,
) {
	verifyImmediateTask(ass, task, taskType, stateExeId, data_models2.ImmediateTaskInfoJson{})
}

func verifyImmediateTask(
	ass *assert.Assertions, task data_models2.ImmediateTask,
	taskType data_models2.ImmediateTaskType, stateExeId string,
	info data_models2.ImmediateTaskInfoJson,
) {
	ass.NotNil(task.StateIdSequence)
	ass.Equal(defaultShardId, int(task.ShardId))
	ass.Equal(taskType, task.TaskType)
	ass.Equal(stateExeId, task.GetStateExecutionId())
	ass.True(task.TaskSequence != nil)
	verifyImmediateTaskInfo(ass, task, info)
}

func verifyImmediateTaskInfo(
	assert *assert.Assertions,
	task data_models2.ImmediateTask,
	expectedInfo data_models2.ImmediateTaskInfoJson) {
	if expectedInfo.VisibilityInfo != nil {
		assert.Equal(expectedInfo.VisibilityInfo.Namespace, task.ImmediateTaskInfo.VisibilityInfo.Namespace)
		assert.Equal(expectedInfo.VisibilityInfo.ProcessId, task.ImmediateTaskInfo.VisibilityInfo.ProcessId)
		assert.Equal(expectedInfo.VisibilityInfo.ProcessType, task.ImmediateTaskInfo.VisibilityInfo.ProcessType)
		assert.Equal(expectedInfo.VisibilityInfo.Status, task.ImmediateTaskInfo.VisibilityInfo.Status)
	}
}

func verifyTimerTask(
	ass *assert.Assertions, task data_models2.TimerTask,
	taskType data_models2.TimerTaskType, stateExeId string,
	taskInfo data_models2.TimerTaskInfoJson,
) {
	ass.NotNil(task.StateIdSequence)
	ass.Equal(defaultShardId, int(task.ShardId))
	ass.Equal(taskType, task.TaskType)
	ass.Equal(stateExeId, task.GetStateExecutionId())
	ass.True(task.TaskSequence != nil)
	ass.Equal(taskInfo, task.TimerTaskInfo)
}

func deleteAndVerifyImmediateTasksDeleted(
	ctx context.Context, t *testing.T, ass *assert.Assertions, store persistence.ProcessStore, minSeq, maxSeq int64,
) {
	err := store.DeleteImmediateTasks(ctx, data_models2.DeleteImmediateTasksRequest{
		ShardId:                  defaultShardId,
		MinTaskSequenceInclusive: minSeq,
		MaxTaskSequenceInclusive: maxSeq,
	})
	require.NoError(t, err)
	checkAndGetImmediateTasks(ctx, t, ass, store, 0) // Expect no tasks
}

func prepareStateExecution(
	ctx context.Context, t *testing.T,
	store persistence.ProcessStore, prcExeId uuid.UUID, stateId string, stateIdSeq int32,
) *data_models2.PrepareStateExecutionResponse {
	stateExeId := data_models2.StateExecutionId{
		StateId:         stateId,
		StateIdSequence: stateIdSeq,
	}
	prep, err := store.PrepareStateExecution(ctx, data_models2.PrepareStateExecutionRequest{
		ProcessExecutionId: prcExeId,
		StateExecutionId:   stateExeId,
	})
	require.NoError(t, err)
	return prep
}

func verifyStateExecution(
	ass *assert.Assertions,
	prep *data_models2.PrepareStateExecutionResponse,
	processId string, input xcapi.EncodedObject,
	expectedStatus data_models2.StateExecutionStatus,
) {
	ass.Equal(testWorkerUrl, prep.Info.WorkerURL)
	ass.Equal(testProcessType, prep.Info.ProcessType)
	ass.Equal(processId, prep.Info.ProcessId)
	ass.Equal(input, prep.Input)
	ass.Equal(expectedStatus, prep.Status)
}

func completeWaitUntilExecution(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, prcExeId uuid.UUID, immediateTask data_models2.ImmediateTask,
	prep *data_models2.PrepareStateExecutionResponse,
) {
	stateExeId := data_models2.StateExecutionId{
		StateId:         immediateTask.StateId,
		StateIdSequence: immediateTask.StateIdSequence,
	}
	compWaitResp, err := store.ProcessWaitUntilExecution(ctx, data_models2.ProcessWaitUntilExecutionRequest{
		ProcessExecutionId: prcExeId,
		StateExecutionId:   stateExeId,
		Prepare:            *prep,
		CommandRequest: xcapi.CommandRequest{
			WaitingType: xcapi.EMPTY_COMMAND,
		},
		TaskShardId: defaultShardId,
	})
	require.NoError(t, err)
	ass.True(compWaitResp.HasNewImmediateTask)
}

func completeExecuteExecution(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, prcExeId uuid.UUID, immediateTask data_models2.ImmediateTask,
	prep *data_models2.PrepareStateExecutionResponse,
	stateDecision xcapi.StateDecision, hasNewImmediateTask bool,
) {
	completeExecuteExecutionWithAppDatabase(
		ctx, t, ass, store, prcExeId, immediateTask, prep,
		stateDecision, hasNewImmediateTask, nil, nil,
	)
}
func completeExecuteExecutionWithAppDatabase(
	ctx context.Context, t *testing.T, ass *assert.Assertions,
	store persistence.ProcessStore, prcExeId uuid.UUID, immediateTask data_models2.ImmediateTask,
	prep *data_models2.PrepareStateExecutionResponse,
	stateDecision xcapi.StateDecision, hasNewImmediateTask bool,
	appDatabaseConfig *data_models2.InternalAppDatabaseConfig,
	appDatabaseWrite *xcapi.AppDatabaseWrite,
) {
	stateExeId := data_models2.StateExecutionId{
		StateId:         immediateTask.StateId,
		StateIdSequence: immediateTask.StateIdSequence,
	}
	compResp, err := store.CompleteExecuteExecution(ctx, data_models2.CompleteExecuteExecutionRequest{
		ProcessExecutionId: prcExeId,
		StateExecutionId:   stateExeId,
		Prepare:            *prep,
		StateDecision:      stateDecision,
		TaskShardId:        defaultShardId,
		AppDatabaseConfig:  appDatabaseConfig,
		WriteAppDatabase:   appDatabaseWrite,
	})
	require.NoError(t, err)
	require.NoError(t, compResp.AppDatabaseWritingError)
	ass.Equal(hasNewImmediateTask, compResp.HasNewImmediateTask)
}

func recoverFromFailure(
	t *testing.T,
	ctx context.Context,
	assert *assert.Assertions,
	store persistence.ProcessStore,
	namespace string,
	prcExeId uuid.UUID,
	prep data_models2.PrepareStateExecutionResponse,
	sourceStateExecId data_models2.StateExecutionId,
	sourceFailedStateApi xcapi.WorkerApiType,
	destinationStateId string,
	destiantionStateConfig *xcapi.AsyncStateConfig,
	destinationStateInput xcapi.EncodedObject,
) {
	request := data_models2.RecoverFromStateExecutionFailureRequest{
		Namespace:              namespace,
		ProcessExecutionId:     prcExeId,
		Prepare:                prep,
		SourceStateExecutionId: sourceStateExecId,
		SourceFailedStateApi:   sourceFailedStateApi,
		DestinationStateId:     destinationStateId,
		DestinationStateConfig: destiantionStateConfig,
		DestinationStateInput:  destinationStateInput,
		ShardId:                defaultShardId,
	}

	err := store.RecoverFromStateExecutionFailure(ctx, request)
	require.NoError(t, err)

	// verify process execution
	descResp, err := store.DescribeLatestProcess(ctx, data_models2.DescribeLatestProcessRequest{
		Namespace: namespace,
		ProcessId: prep.Info.ProcessId,
	})
	require.NoError(t, err)
	assert.Equal(xcapi.RUNNING, *descResp.Response.Status)
}

// this won't guarantee the actual equality, but it's good enough for our test
// when the objects are large, it's hard to use assert.Equal or assert.ElementsMatch
func assertProbablyEqualForIgnoringOrderByJsonEncoder(
	t *testing.T, ass *assert.Assertions, obj1, obj2 interface{},
) {
	str1, err1 := json.Marshal(obj1)
	str2, err2 := json.Marshal(obj2)
	require.NoError(t, err1)
	require.NoError(t, err2)
	ass.Equal(len(str1), len(str2))
}

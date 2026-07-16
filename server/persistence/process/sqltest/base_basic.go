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
	"fmt"
	"testing"
	"time"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/ptr"
	"github.com/superdurable/dex/server/persistence"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"

	"github.com/stretchr/testify/assert"
)

func CleanupEnv(ass *assert.Assertions, store persistence.ProcessStore) {
	err := store.CleanUpTasksForTest(context.Background(), defaultShardId)
	ass.Nil(err)
}

func SQLBasicTest(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()
	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	// Test waitUntil API execution
	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks := checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task := immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeWaitUntil, stateId1+"-1")
	taskVisibility := immediateTasks[1]
	verifyImmediateTask(
		ass,
		taskVisibility,
		data_models2.ImmediateTaskTypeVisibility,
		"-0",
		data_models2.ImmediateTaskInfoJson{
			VisibilityInfo: &data_models2.VisibilityInfoJson{
				Namespace:          namespace,
				ProcessId:          processId,
				ProcessType:        testProcessType,
				ProcessExecutionId: prcExeId,
				Status:             data_models2.ProcessExecutionStatusRunning,
				StartTime:          nil,
				CloseTime:          nil,
			},
		})

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution.
	prep := prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusWaitUntilRunning)

	// Complete 'WaitUntil' execution.
	completeWaitUntilExecution(ctx, t, ass, store, prcExeId, task, prep)

	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)

	decision1 := xcapi.StateDecision{
		NextStates: []xcapi.StateMovement{
			{
				StateId: stateId2,
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
			{
				StateId: stateId1, // use the same stateId
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
				StateInput:  &input,
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")
	task = immediateTasks[1]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-2")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)
	decision2 := xcapi.StateDecision{
		ThreadCloseDecision: &xcapi.ThreadCloseDecision{
			CloseType: xcapi.FORCE_COMPLETE_PROCESS,
		},
	}
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision2, true)

	// Verify stateId2 was aborted and process has completed
	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusAborted)
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.COMPLETED)
}

func SQLProcessIdReusePolicyAllowIfPreviousExitAbnormally(
	t *testing.T, ass *assert.Assertions, store persistence.ProcessStore,
) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	// stop the process with temerminated
	terminateProcess(ctx, t, ass, store, namespace, processId)

	// Describe the process, verify it's terminated
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.TERMINATED)

	// start the process with allow if previous exit abnormally
	startProcessWithAllowIfPreviousExitAbnormally(ctx, t, ass, store, namespace, processId, input)
}

func SQLProcessIdReusePolicyDefault(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	// stop the process with temerminated
	terminateProcess(ctx, t, ass, store, namespace, processId)

	// Describe the process, verify it's terminated
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.TERMINATED)

	// start with default process id reuse policy and it should start correctly
	prcExeID2 := startProcess(ctx, t, ass, store, namespace, processId, input)

	ass.NotEqual(prcExeId.String(), prcExeID2.String())
}

func SQLProcessIdReusePolicyTerminateIfRunning(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	prcExeID2 := startProcessWithTerminateIfRunningPolicy(ctx, t, ass, store, namespace, processId, input)

	ass.NotEqual(prcExeId.String(), prcExeID2.String())
}

func SQLProcessIdReusePolicyDisallowReuseTest(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	// Test waitUntil API execution
	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks := checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task := immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeWaitUntil, stateId1+"-1")
	visibilityTask := immediateTasks[1]
	ass.Equal(data_models2.ImmediateTaskTypeVisibility, visibilityTask.TaskType)

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution.
	prep := prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusWaitUntilRunning)

	// Complete 'WaitUntil' execution.
	completeWaitUntilExecution(ctx, t, ass, store, prcExeId, task, prep)

	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)

	decision1 := xcapi.StateDecision{
		NextStates: []xcapi.StateMovement{
			{
				StateId: stateId2,
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
			{
				StateId: stateId1, // use the same stateId
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
				StateInput:  &input,
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")
	task = immediateTasks[1]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-2")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)
	decision2 := xcapi.StateDecision{
		ThreadCloseDecision: &xcapi.ThreadCloseDecision{
			CloseType: xcapi.FORCE_COMPLETE_PROCESS,
		},
	}
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision2, true)

	// Verify stateId2 was aborted and process has completed
	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusAborted)
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.COMPLETED)

	// try to start with disallow_reuse policy, and verify it's returning already started
	startProcessWithDisallowReusePolicy(ctx, t, ass, store, namespace, processId, input)
}

func SQLProcessIdReusePolicyAllowIfNoRunning(
	t *testing.T, ass *assert.Assertions, store persistence.ProcessStore,
) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Try to start the process again and verify the behavior.
	retryStartProcessForFailure(ctx, t, ass, store, namespace, processId, input)

	// Describe the process.
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)

	// Test waitUntil API execution
	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks := checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task := immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeWaitUntil, stateId1+"-1")
	visibilityTask := immediateTasks[1]
	ass.Equal(data_models2.ImmediateTaskTypeVisibility, visibilityTask.TaskType)

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution.
	prep := prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusWaitUntilRunning)

	// Complete 'WaitUntil' execution.
	completeWaitUntilExecution(ctx, t, ass, store, prcExeId, task, prep)

	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)

	decision1 := xcapi.StateDecision{
		NextStates: []xcapi.StateMovement{
			{
				StateId: stateId2,
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
			{
				StateId: stateId1, // use the same stateId
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
				StateInput:  &input,
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")
	task = immediateTasks[1]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-2")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)
	decision2 := xcapi.StateDecision{
		ThreadCloseDecision: &xcapi.ThreadCloseDecision{
			CloseType: xcapi.FORCE_COMPLETE_PROCESS,
		},
	}
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision2, true)

	// Verify stateId2 was aborted and process has completed
	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusAborted)
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.COMPLETED)

	// start with allow if no running,
	startProcessWithAllowIfNoRunningPolicy(ctx, t, ass, store, namespace, processId, input)
}

func SQLGracefulCompleteTest(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()
	namespace := "test-ns-2"
	processId := fmt.Sprintf("test-graceful-complete-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Get the task
	minSeq, maxSeq, immediateTasks := checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task := immediateTasks[0]
	visibilityTask := immediateTasks[1]
	ass.Equal(data_models2.ImmediateTaskTypeVisibility, visibilityTask.TaskType)

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution.
	prep := prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusWaitUntilRunning)

	// Complete 'WaitUntil' execution.
	completeWaitUntilExecution(ctx, t, ass, store, prcExeId, task, prep)

	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)

	decision1 := xcapi.StateDecision{
		NextStates: []xcapi.StateMovement{
			{
				StateId: stateId2,
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
			{
				StateId: stateId1, // use the same stateId
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")
	task = immediateTasks[1]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-2")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusExecuteRunning)
	decision2 := xcapi.StateDecision{
		ThreadCloseDecision: &xcapi.ThreadCloseDecision{
			CloseType: xcapi.GRACEFUL_COMPLETE_PROCESS,
		},
	}
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision2, false)

	// Verify both stateId2 and process are still running
	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusExecuteRunning)
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.RUNNING)
}

func SQLForceFailTest(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-force-fail-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

	// Get the task
	minSeq, maxSeq, immediateTasks := checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task := immediateTasks[0]

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution.
	prep := prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusWaitUntilRunning)

	// Complete 'WaitUntil' execution.
	completeWaitUntilExecution(ctx, t, ass, store, prcExeId, task, prep)

	// Check initial immediate tasks.
	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, input, data_models2.StateExecutionStatusExecuteRunning)

	decision1 := xcapi.StateDecision{
		NextStates: []xcapi.StateMovement{
			{
				StateId: stateId2,
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
			{
				StateId: stateId1, // use the same stateId
				// no input, skip waitUntil
				StateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 2)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")
	task = immediateTasks[1]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId1+"-2")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusExecuteRunning)
	decision2 := xcapi.StateDecision{
		ThreadCloseDecision: &xcapi.ThreadCloseDecision{
			CloseType: xcapi.FORCE_FAIL_PROCESS,
		},
	}
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision2, true)

	// Verify stateId2 was aborted and process has failed
	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, createEmptyEncodedObject(), data_models2.StateExecutionStatusAborted)
	describeProcess(ctx, t, ass, store, namespace, processId, xcapi.FAILED)
}

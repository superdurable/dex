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
	"github.com/xcherryio/xcherry/server/common/ptr"
	"github.com/xcherryio/xcherry/server/persistence"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"

	"github.com/stretchr/testify/assert"
)

func SQLStateFailureRecoveryTest(t *testing.T, ass *assert.Assertions, store persistence.ProcessStore) {
	ctx := context.Background()

	processId := fmt.Sprintf("test-prcid-%v", time.Now().String())
	input := createTestInput()

	// Start the process and verify it started correctly.
	prcExeId := startProcess(ctx, t, ass, store, namespace, processId, input)

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
				StateId:    stateId2,
				StateInput: xcapi.NewEncodedObject(input.Encoding, input.Data+"-"+stateId1+"-1"),
				StateConfig: &xcapi.AsyncStateConfig{
					SkipWaitUntil: ptr.Any(true),
					StateFailureRecoveryOptions: &xcapi.StateFailureRecoveryOptions{
						Policy:                         xcapi.PROCEED_TO_CONFIGURED_STATE,
						StateFailureProceedStateId:     ptr.Any(stateId1),
						StateFailureProceedStateConfig: &xcapi.AsyncStateConfig{SkipWaitUntil: ptr.Any(true)},
					},
				},
			},
		},
	}
	// Complete 'Execute' execution.
	completeExecuteExecution(ctx, t, ass, store, prcExeId, task, prep, decision1, true)

	minSeq, maxSeq, immediateTasks = checkAndGetImmediateTasks(ctx, t, ass, store, 1)
	task = immediateTasks[0]
	verifyImmediateTaskNoInfo(ass, task, data_models2.ImmediateTaskTypeExecute, stateId2+"-1")

	// Delete and verify immediate tasks are deleted.
	deleteAndVerifyImmediateTasksDeleted(ctx, t, ass, store, minSeq, maxSeq)

	// Prepare state execution for Execute API again
	prep = prepareStateExecution(ctx, t, store, prcExeId, task.StateId, task.StateIdSequence)
	verifyStateExecution(
		ass,
		prep,
		processId,
		*xcapi.NewEncodedObject(input.Encoding, input.Data+"-"+stateId1+"-1"),
		data_models2.StateExecutionStatusExecuteRunning)

	recoverFromFailure(
		t,
		ctx,
		ass,
		store,
		namespace,
		prcExeId,
		*prep,
		data_models2.StateExecutionId{
			StateId:         stateId2,
			StateIdSequence: 1,
		},
		xcapi.EXECUTE_API,
		stateId1,
		&xcapi.AsyncStateConfig{
			SkipWaitUntil: ptr.Any(true),
		},
		*xcapi.NewEncodedObject(input.Encoding, input.Data+"-"+stateId1+"-1"+"-"+stateId2+"-1"),
	)

	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId2, 1)
	verifyStateExecution(ass, prep, processId, *xcapi.NewEncodedObject("test-encoding", input.Data+"-"+stateId1+"-1"), data_models2.StateExecutionStatusFailed)

	prep = prepareStateExecution(ctx, t, store, prcExeId, stateId1, 2)
	verifyStateExecution(
		ass,
		prep,
		processId,
		*xcapi.NewEncodedObject("test-encoding", input.Data+"-"+stateId1+"-1"+"-"+stateId2+"-1"),
		data_models2.StateExecutionStatusExecuteRunning)
}

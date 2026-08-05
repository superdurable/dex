// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package datasetdeal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type replayRepository struct {
	Repository
	execution   DealExecution
	updateCalls int
}

type replayContext struct {
	context.Context
	flowID          string
	runID           string
	stepExecutionID string
}

func TestExecuteActionStepReplaysCommittedStepWithoutMutation(t *testing.T) {
	repository := &replayRepository{execution: DealExecution{
		FlowID:                "execution-id",
		LatestRunID:           "run-id",
		TargetState:           "target",
		CurrentActionPhase:    PreActionPhase,
		CurrentActionIndex:    1,
		Status:                ExecutionProcessing,
		LastStepExecutionID:   "action-step-id",
		ProcessDefinition:     DealProcess{},
		StateData:             map[string]string{"lastAction": "already-committed"},
		PendingConditionPhase: "",
	}}
	flow := &DealFlow{repository: repository}
	step := executeActionStep{flow: flow}
	decision, err := step.Execute(replayContext{
		Context:         context.Background(),
		flowID:          "execution-id",
		runID:           "run-id",
		stepExecutionID: "action-step-id",
	}, actionStepInput{
		StateName:   "target",
		Phase:       PreActionPhase,
		ActionIndex: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.Equal(t, 0, repository.updateCalls)
	require.Equal(t, "already-committed", repository.execution.StateData["lastAction"])
}

func TestStepReplayRequiresMatchingRunID(t *testing.T) {
	ctx := replayContext{
		Context:         context.Background(),
		runID:           "new-run-id",
		stepExecutionID: "action-step-id",
	}
	require.False(t, stepAlreadyCommitted(ctx, DealExecution{
		LatestRunID:         "previous-run-id",
		LastStepExecutionID: "action-step-id",
	}))
}

func (repository *replayRepository) GetExecution(
	context.Context,
	string,
) (DealExecution, error) {
	return repository.execution, nil
}

func (repository *replayRepository) UpdateExecution(
	_ context.Context,
	execution DealExecution,
) (DealExecution, error) {
	repository.updateCalls++
	repository.execution = execution
	return execution, nil
}

func (ctx replayContext) FlowID() string {
	return ctx.flowID
}

func (ctx replayContext) RunID() string {
	return ctx.runID
}

func (replayContext) FlowStartedAt() time.Time {
	return time.Time{}
}

func (ctx replayContext) StepExecutionID() string {
	return ctx.stepExecutionID
}

func (replayContext) FromStepExecutionID() string {
	return ""
}

func (replayContext) FirstAttemptAt() time.Time {
	return time.Time{}
}

func (replayContext) Attempt() int32 {
	return 1
}

func (replayContext) HasTimerFired() bool {
	return false
}

func (replayContext) HasTimerFiredByIndex(int) bool {
	return false
}

func (replayContext) WaitForMethodFailed() bool {
	return false
}

func (replayContext) SetStepExecutionLocal(string, any) error {
	return nil
}

func (replayContext) GetStepExecutionLocal(string, any) (bool, error) {
	return false, nil
}

func (replayContext) RecordEvent(string, any) error {
	return nil
}

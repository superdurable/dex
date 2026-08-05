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
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type DealFlow struct {
	dex.FlowDefaults
	repository Repository
	actions    *actionRegistry
}

func NewDealFlow(repository Repository, logger dex.Logger) *DealFlow {
	if repository == nil {
		panic("datasetdeal.NewDealFlow requires Repository")
	}
	return &DealFlow{
		repository: repository,
		actions:    newActionRegistry(logger),
	}
}

func (flow *DealFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(triggerStep{flow: flow}),
		dex.DefineStep(advanceStateStep{flow: flow}),
		dex.DefineStep(executeActionStep{flow: flow}),
	}
}

func (*DealFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// TriggerStepExecutionID identifies the persisted trigger step.
func TriggerStepExecutionID() dex.StepExecutionID {
	return dex.StepExecutionID{StepType: dex.GetFinalStepType(triggerStep{})}
}

type triggerStep struct {
	dex.StepDefaultsNoWaitFor[TriggerInput]
	flow *DealFlow
}

func (step triggerStep) Execute(
	ctx dex.Context,
	input TriggerInput,
) (*dex.StepDecision, error) {
	switch input.Type {
	case startTriggerType:
		return step.initializeExecution(ctx, input)
	case conditionTriggerType:
		return step.applyCondition(ctx, input)
	default:
		return nil, fmt.Errorf("unknown dataset deal trigger type %q", input.Type)
	}
}

func (step triggerStep) initializeExecution(
	ctx dex.Context,
	input TriggerInput,
) (*dex.StepDecision, error) {
	process, err := step.flow.repository.GetProcess(ctx, input.ProcessID)
	if err != nil {
		return nil, err
	}
	if err := ValidateProcess(process); err != nil {
		return nil, fmt.Errorf("stored deal process is invalid: %w", err)
	}
	stateData := process.InitialStateData
	if stateData == nil {
		stateData = make(map[string]string)
	}
	execution := DealExecution{
		FlowID:              ctx.FlowID(),
		LatestRunID:         ctx.RunID(),
		ProcessID:           input.ProcessID,
		ProcessDefinition:   process,
		BuyerID:             input.BuyerID,
		TargetState:         process.InitialState,
		StateData:           stateData,
		Status:              ExecutionProcessing,
		LastStepExecutionID: ctx.StepExecutionID(),
	}
	execution, err = step.flow.repository.CreateExecution(ctx, execution)
	if errors.Is(err, ErrExecutionExists) {
		existing, getErr := step.flow.repository.GetExecution(ctx, ctx.FlowID())
		if getErr != nil {
			return nil, getErr
		}
		if existing.LatestRunID != ctx.RunID() ||
			existing.LastStepExecutionID != ctx.StepExecutionID() {
			return nil, err
		}
		execution = existing
	} else if err != nil {
		return nil, err
	}
	return step.flow.executionDecision(execution)
}

func (step triggerStep) applyCondition(
	ctx dex.Context,
	input TriggerInput,
) (*dex.StepDecision, error) {
	execution, err := step.flow.repository.GetExecution(ctx, ctx.FlowID())
	if err != nil {
		return nil, err
	}
	if stepAlreadyCommitted(ctx, execution) {
		return step.flow.conditionDecision(execution, input.ConditionName)
	}
	if execution.Status != ExecutionWaiting {
		return nil, ErrExecutionNotWaiting
	}
	if execution.PendingConditionName != input.ConditionName {
		return nil, fmt.Errorf(
			"%w: expected %q, received %q",
			ErrConditionNotPending,
			execution.PendingConditionName,
			input.ConditionName,
		)
	}
	state, phase, err := execution.ProcessDefinition.Condition(input.ConditionName)
	if err != nil {
		return nil, err
	}
	if state.Name != execution.PendingConditionState || phase != execution.PendingConditionPhase {
		return nil, fmt.Errorf("pending condition cursor does not match process definition")
	}
	if err := mergeStateData(execution.StateData, input.Data); err != nil {
		return nil, err
	}
	execution.LatestRunID = ctx.RunID()
	execution.Status = ExecutionProcessing
	execution.PendingConditionState = ""
	execution.PendingConditionName = ""
	execution.PendingConditionPhase = ""
	execution.CurrentActionPhase = ""
	execution.CurrentActionIndex = 0
	switch phase {
	case PreConditionPhase:
		execution.TargetState = state.Name
	case PostConditionPhase:
		execution.TargetState = evaluateDecision(state.PostCondition.Decision, execution.StateData)
	default:
		return nil, fmt.Errorf("unknown condition phase %q", phase)
	}
	execution, err = step.flow.commitExecution(ctx, execution)
	if err != nil {
		return nil, err
	}
	return step.flow.conditionDecision(execution, input.ConditionName)
}

func (flow *DealFlow) conditionDecision(
	execution DealExecution,
	conditionName string,
) (*dex.StepDecision, error) {
	_, phase, err := execution.ProcessDefinition.Condition(conditionName)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(
		advanceStateStep{flow: flow},
		stateStepInput{
			StateName:             execution.TargetState,
			PreConditionSatisfied: phase == PreConditionPhase,
		},
	), nil
}

type advanceStateStep struct {
	dex.StepDefaultsNoWaitFor[stateStepInput]
	flow *DealFlow
}

func (step advanceStateStep) Execute(
	ctx dex.Context,
	input stateStepInput,
) (*dex.StepDecision, error) {
	execution, err := step.flow.repository.GetExecution(ctx, ctx.FlowID())
	if err != nil {
		return nil, err
	}
	if stepAlreadyCommitted(ctx, execution) {
		return step.flow.executionDecision(execution)
	}
	if err := validateProcessingExecution(ctx, execution); err != nil {
		return nil, err
	}
	if execution.TargetState != input.StateName {
		return nil, fmt.Errorf(
			"target state %q does not match step input %q",
			execution.TargetState,
			input.StateName,
		)
	}
	state, err := execution.ProcessDefinition.State(input.StateName)
	if err != nil {
		return nil, err
	}
	if !input.PreConditionSatisfied && state.PreCondition != nil {
		execution.Status = ExecutionWaiting
		execution.PendingConditionState = state.Name
		execution.PendingConditionName = state.PreCondition.Name
		execution.PendingConditionPhase = PreConditionPhase
		execution.CurrentActionPhase = ""
		execution.CurrentActionIndex = 0
		execution, err = step.flow.commitExecution(ctx, execution)
		if err != nil {
			return nil, err
		}
		return dex.GracefulComplete(execution), nil
	}
	if len(state.PreActions) > 0 {
		execution.CurrentActionPhase = PreActionPhase
		execution.CurrentActionIndex = 0
		execution, err = step.flow.commitExecution(ctx, execution)
		if err != nil {
			return nil, err
		}
		return step.flow.executionDecision(execution)
	}
	execution.CurrentState = state.Name
	execution.CurrentActionPhase = ""
	execution.CurrentActionIndex = 0
	if len(state.PostActions) > 0 {
		execution.CurrentActionPhase = PostActionPhase
	} else if err := finishState(&execution, state); err != nil {
		return nil, err
	}
	execution, err = step.flow.commitExecution(ctx, execution)
	if err != nil {
		return nil, err
	}
	return step.flow.executionDecision(execution)
}

type executeActionStep struct {
	dex.StepDefaultsNoWaitFor[actionStepInput]
	flow *DealFlow
}

func (step executeActionStep) Execute(
	ctx dex.Context,
	input actionStepInput,
) (*dex.StepDecision, error) {
	execution, err := step.flow.repository.GetExecution(ctx, ctx.FlowID())
	if err != nil {
		return nil, err
	}
	if stepAlreadyCommitted(ctx, execution) {
		return step.flow.executionDecision(execution)
	}
	if err := validateProcessingExecution(ctx, execution); err != nil {
		return nil, err
	}
	if execution.TargetState != input.StateName ||
		execution.CurrentActionPhase != input.Phase ||
		execution.CurrentActionIndex != input.ActionIndex {
		return nil, fmt.Errorf("action cursor does not match step input")
	}
	state, err := execution.ProcessDefinition.State(input.StateName)
	if err != nil {
		return nil, err
	}
	actions, err := actionsForPhase(state, input.Phase)
	if err != nil {
		return nil, err
	}
	if input.ActionIndex < 0 || input.ActionIndex >= len(actions) {
		return nil, fmt.Errorf(
			"action index %d is invalid for %s actions in state %q",
			input.ActionIndex,
			input.Phase,
			state.Name,
		)
	}
	updates, err := step.flow.actions.execute(actions[input.ActionIndex], ActionInput{
		FlowID:          execution.FlowID,
		RunID:           ctx.RunID(),
		StepExecutionID: ctx.StepExecutionID(),
		ProcessID:       execution.ProcessID,
		BuyerID:         execution.BuyerID,
		TargetState:     state.Name,
		StateData:       execution.StateData,
	})
	if err != nil {
		return nil, err
	}
	if err := mergeStateData(execution.StateData, updates); err != nil {
		return nil, err
	}
	nextActionIndex := input.ActionIndex + 1
	if nextActionIndex < len(actions) {
		execution.CurrentActionIndex = nextActionIndex
	} else {
		execution.CurrentActionPhase = ""
		execution.CurrentActionIndex = 0
		switch input.Phase {
		case PreActionPhase:
			execution.CurrentState = state.Name
			if len(state.PostActions) > 0 {
				execution.CurrentActionPhase = PostActionPhase
			} else if err := finishState(&execution, state); err != nil {
				return nil, err
			}
		case PostActionPhase:
			if err := finishState(&execution, state); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown action phase %q", input.Phase)
		}
	}
	execution, err = step.flow.commitExecution(ctx, execution)
	if err != nil {
		return nil, err
	}
	return step.flow.executionDecision(execution)
}

func (flow *DealFlow) executionDecision(
	execution DealExecution,
) (*dex.StepDecision, error) {
	switch execution.Status {
	case ExecutionWaiting, ExecutionCompleted:
		return dex.GracefulComplete(execution), nil
	case ExecutionProcessing:
	default:
		return nil, fmt.Errorf("unknown execution status %q", execution.Status)
	}
	if execution.CurrentActionPhase != "" {
		return dex.GoTo(
			executeActionStep{flow: flow},
			actionStepInput{
				StateName:   execution.TargetState,
				Phase:       execution.CurrentActionPhase,
				ActionIndex: execution.CurrentActionIndex,
			},
		), nil
	}
	if execution.TargetState == "" {
		return nil, fmt.Errorf("processing execution has no target state")
	}
	return dex.GoTo(
		advanceStateStep{flow: flow},
		stateStepInput{StateName: execution.TargetState},
	), nil
}

func (flow *DealFlow) commitExecution(
	ctx dex.Context,
	execution DealExecution,
) (DealExecution, error) {
	execution.LastStepExecutionID = ctx.StepExecutionID()
	return flow.repository.UpdateExecution(ctx, execution)
}

func stepAlreadyCommitted(ctx dex.Context, execution DealExecution) bool {
	return execution.LatestRunID == ctx.RunID() &&
		execution.LastStepExecutionID == ctx.StepExecutionID()
}

func validateProcessingExecution(ctx dex.Context, execution DealExecution) error {
	if execution.LatestRunID != ctx.RunID() {
		return fmt.Errorf(
			"execution run %q does not match trigger run %q",
			execution.LatestRunID,
			ctx.RunID(),
		)
	}
	if execution.Status != ExecutionProcessing {
		return fmt.Errorf("execution status %q is not processing", execution.Status)
	}
	return nil
}

func finishState(execution *DealExecution, state StateDefinition) error {
	execution.CurrentActionPhase = ""
	execution.CurrentActionIndex = 0
	if state.PostCondition == nil {
		execution.Status = ExecutionCompleted
		execution.TargetState = ""
		execution.PendingConditionState = ""
		execution.PendingConditionName = ""
		execution.PendingConditionPhase = ""
		return nil
	}
	if state.PostCondition.WaitFor != nil {
		execution.Status = ExecutionWaiting
		execution.PendingConditionState = state.Name
		execution.PendingConditionName = state.PostCondition.WaitFor.Name
		execution.PendingConditionPhase = PostConditionPhase
		return nil
	}
	execution.Status = ExecutionProcessing
	execution.TargetState = evaluateDecision(state.PostCondition.Decision, execution.StateData)
	return nil
}

func actionsForPhase(state StateDefinition, phase ActionPhase) ([]string, error) {
	switch phase {
	case PreActionPhase:
		return state.PreActions, nil
	case PostActionPhase:
		return state.PostActions, nil
	default:
		return nil, fmt.Errorf("unknown action phase %q", phase)
	}
}

func mergeStateData(stateData map[string]string, updates map[string]string) error {
	if stateData == nil && len(updates) > 0 {
		return fmt.Errorf("dataset deal stateData must not be nil")
	}
	for key, value := range updates {
		if key == "" {
			return fmt.Errorf("stateData update key must not be empty")
		}
		stateData[key] = value
	}
	return nil
}

func evaluateDecision(
	decision DecisionExpression,
	stateData map[string]string,
) string {
	value := stateData[decision.Key]
	for _, conditionCase := range decision.Cases {
		if value == conditionCase.Equals {
			return conditionCase.GoToState
		}
	}
	return decision.ElseState
}

var _ dex.Flow = (*DealFlow)(nil)

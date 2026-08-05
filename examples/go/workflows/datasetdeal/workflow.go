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
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	ProcessIDSearchKey                = "ProcessID"
	BuyerIDSearchKey                  = "BuyerID"
	CurrentStateSearchKey             = "CurrentState"
	PendingPreConditionStateSearchKey = "PendingPreConditionState"
	PendingPreConditionNameSearchKey  = "PendingPreConditionName"
)

var (
	StateData         = dex.DefineAttribute[map[string]string]("stateData")
	ProcessDefinition = dex.DefineAttribute[DealProcess]("processDefinition")
	ProcessID         = dex.DefineAttribute[string](
		"processID",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: ProcessIDSearchKey}),
	)
	BuyerID = dex.DefineAttribute[string](
		"buyerID",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: BuyerIDSearchKey}),
	)
	CurrentState = dex.DefineAttribute[string](
		"currentState",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: CurrentStateSearchKey}),
	)
	CurrentActionIndexToExecute = dex.DefineAttribute[int]("currentActionIndexToExecute")
	PendingPreConditionState    = dex.DefineAttribute[string](
		"pendingPreConditionState",
		dex.Indexed(dex.AttributeIndex{
			Type:     dex.IndexKeyword,
			IndexKey: PendingPreConditionStateSearchKey,
		}),
	)
	PendingPreConditionName = dex.DefineAttribute[string](
		"pendingPreConditionName",
		dex.Indexed(dex.AttributeIndex{
			Type:     dex.IndexKeyword,
			IndexKey: PendingPreConditionNameSearchKey,
		}),
	)
	ConditionMessages = dex.DefineChannelMap[map[string]string]("conditionMessages")
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
		dex.DefineStartStep(initializeStep{flow: flow}),
		dex.DefineStep(preConditionStep{flow: flow}),
		dex.DefineStep(executeActionStep{flow: flow}),
		dex.DefineStep(postConditionStep{flow: flow}),
	}
}

func (*DealFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			StateData,
			ProcessDefinition,
			ProcessID,
			BuyerID,
			CurrentState,
			CurrentActionIndexToExecute,
			PendingPreConditionState,
			PendingPreConditionName,
		},
		Channels: []dex.ChannelDef{ConditionMessages},
	}
}

type initializeStep struct {
	dex.StepDefaultsNoWaitFor[string]
	flow *DealFlow
}

func (step initializeStep) Execute(
	ctx dex.Context,
	processID string,
) (dex.StepDecision, error) {
	process, err := step.flow.repository.GetProcess(ctx, processID)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if err := ValidateProcess(process); err != nil {
		return dex.StepDecision{}, fmt.Errorf("stored deal process is invalid: %w", err)
	}
	_, found, err := BuyerID.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		return dex.StepDecision{}, fmt.Errorf("dataset deal buyer ID is missing")
	}
	stateData := copyStateData(process.InitialStateData)
	if err := ProcessID.Set(ctx, processID); err != nil {
		return dex.StepDecision{}, err
	}
	if err := ProcessDefinition.Set(ctx, process); err != nil {
		return dex.StepDecision{}, err
	}
	if err := StateData.Set(ctx, stateData); err != nil {
		return dex.StepDecision{}, err
	}
	if err := CurrentActionIndexToExecute.Set(ctx, 0); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoTo(
		preConditionStep{flow: step.flow},
		stateStepInput{StateName: process.InitialState},
	), nil
}

type preConditionStep struct {
	dex.StepDefaults
	flow *DealFlow
}

func (step preConditionStep) WaitFor(
	ctx dex.Context,
	input stateStepInput,
) (dex.Wait, error) {
	state, err := step.flow.loadState(ctx, input.StateName)
	if err != nil {
		return dex.Wait{}, err
	}
	if state.PreCondition == nil {
		return dex.SkipWaitImmediately(), nil
	}
	if err := PendingPreConditionState.Set(ctx, state.Name); err != nil {
		return dex.Wait{}, err
	}
	if err := PendingPreConditionName.Set(ctx, state.PreCondition.Name); err != nil {
		return dex.Wait{}, err
	}
	return dex.AllOf(ConditionMessages.ForOne(state.PreCondition.Name)), nil
}

func (step preConditionStep) Execute(
	ctx dex.Context,
	input stateStepInput,
) (dex.StepDecision, error) {
	state, err := step.flow.loadState(ctx, input.StateName)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if state.PreCondition != nil {
		updates, resultErr := conditionUpdates(ctx, state.PreCondition.Name)
		if resultErr != nil {
			return dex.StepDecision{}, resultErr
		}
		if err := mergeStateData(ctx, updates); err != nil {
			return dex.StepDecision{}, err
		}
		if err := PendingPreConditionState.Delete(ctx); err != nil {
			return dex.StepDecision{}, err
		}
		if err := PendingPreConditionName.Delete(ctx); err != nil {
			return dex.StepDecision{}, err
		}
	}
	if err := CurrentActionIndexToExecute.Set(ctx, 0); err != nil {
		return dex.StepDecision{}, err
	}
	if len(state.PreActions) > 0 {
		return dex.GoTo(
			executeActionStep{flow: step.flow},
			actionStepInput{StateName: state.Name, Phase: preActionPhase},
		), nil
	}
	return step.flow.enterState(ctx, state)
}

type executeActionStep struct {
	dex.StepDefaultsNoWaitFor[actionStepInput]
	flow *DealFlow
}

func (step executeActionStep) Execute(
	ctx dex.Context,
	input actionStepInput,
) (dex.StepDecision, error) {
	state, err := step.flow.loadState(ctx, input.StateName)
	if err != nil {
		return dex.StepDecision{}, err
	}
	actions, err := actionsForPhase(state, input.Phase)
	if err != nil {
		return dex.StepDecision{}, err
	}
	actionIndex, found, err := CurrentActionIndexToExecute.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found || actionIndex < 0 || actionIndex >= len(actions) {
		return dex.StepDecision{}, fmt.Errorf(
			"action index %d is invalid for %s actions in state %q",
			actionIndex,
			input.Phase,
			state.Name,
		)
	}
	runtimeState, err := readRuntimeState(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	updates, err := step.flow.actions.execute(actions[actionIndex], ActionInput{
		FlowID:       ctx.FlowID(),
		ProcessID:    runtimeState.ProcessID,
		BuyerID:      runtimeState.BuyerID,
		CurrentState: runtimeState.CurrentState,
		TargetState:  state.Name,
		StateData:    runtimeState.StateData,
	})
	if err != nil {
		return dex.StepDecision{}, err
	}
	if err := mergeStateData(ctx, updates); err != nil {
		return dex.StepDecision{}, err
	}
	nextActionIndex := actionIndex + 1
	if err := CurrentActionIndexToExecute.Set(ctx, nextActionIndex); err != nil {
		return dex.StepDecision{}, err
	}
	if nextActionIndex < len(actions) {
		return dex.GoTo(executeActionStep{flow: step.flow}, input), nil
	}
	if err := CurrentActionIndexToExecute.Set(ctx, 0); err != nil {
		return dex.StepDecision{}, err
	}
	if input.Phase == preActionPhase {
		return step.flow.enterState(ctx, state)
	}
	return dex.GoTo(
		postConditionStep{flow: step.flow},
		stateStepInput{StateName: state.Name},
	), nil
}

type postConditionStep struct {
	dex.StepDefaults
	flow *DealFlow
}

func (step postConditionStep) WaitFor(
	ctx dex.Context,
	input stateStepInput,
) (dex.Wait, error) {
	state, err := step.flow.loadState(ctx, input.StateName)
	if err != nil {
		return dex.Wait{}, err
	}
	if state.PostCondition == nil || state.PostCondition.WaitFor == nil {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.AllOf(ConditionMessages.ForOne(state.PostCondition.WaitFor.Name)), nil
}

func (step postConditionStep) Execute(
	ctx dex.Context,
	input stateStepInput,
) (dex.StepDecision, error) {
	state, err := step.flow.loadState(ctx, input.StateName)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if state.PostCondition == nil {
		stateData, _, err := StateData.Get(ctx)
		if err != nil {
			return dex.StepDecision{}, err
		}
		return dex.GracefulComplete(stateData), nil
	}
	if state.PostCondition.WaitFor != nil {
		updates, resultErr := conditionUpdates(ctx, state.PostCondition.WaitFor.Name)
		if resultErr != nil {
			return dex.StepDecision{}, resultErr
		}
		if err := mergeStateData(ctx, updates); err != nil {
			return dex.StepDecision{}, err
		}
	}
	stateData, found, err := StateData.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		return dex.StepDecision{}, fmt.Errorf("dataset deal stateData is missing")
	}
	nextState := evaluateDecision(state.PostCondition.Decision, stateData)
	return dex.GoTo(
		preConditionStep{flow: step.flow},
		stateStepInput{StateName: nextState},
	), nil
}

func (flow *DealFlow) loadState(
	ctx dex.Context,
	stateName string,
) (StateDefinition, error) {
	process, found, err := ProcessDefinition.Get(ctx)
	if err != nil {
		return StateDefinition{}, err
	}
	if !found {
		return StateDefinition{}, fmt.Errorf("dataset deal process definition is missing")
	}
	state, found := process.State(stateName)
	if !found {
		return StateDefinition{}, fmt.Errorf("state %q is not defined", stateName)
	}
	return state, nil
}

func (flow *DealFlow) enterState(
	ctx dex.Context,
	state StateDefinition,
) (dex.StepDecision, error) {
	if err := CurrentState.Set(ctx, state.Name); err != nil {
		return dex.StepDecision{}, err
	}
	if err := CurrentActionIndexToExecute.Set(ctx, 0); err != nil {
		return dex.StepDecision{}, err
	}
	if len(state.PostActions) > 0 {
		return dex.GoTo(
			executeActionStep{flow: flow},
			actionStepInput{StateName: state.Name, Phase: postActionPhase},
		), nil
	}
	return dex.GoTo(
		postConditionStep{flow: flow},
		stateStepInput{StateName: state.Name},
	), nil
}

type runtimeState struct {
	ProcessID    string
	BuyerID      string
	CurrentState string
	StateData    map[string]string
}

func readRuntimeState(ctx dex.Context) (runtimeState, error) {
	processID, found, err := ProcessID.Get(ctx)
	if err != nil {
		return runtimeState{}, err
	}
	if !found {
		return runtimeState{}, fmt.Errorf("dataset deal process ID is missing")
	}
	buyerID, found, err := BuyerID.Get(ctx)
	if err != nil {
		return runtimeState{}, err
	}
	if !found {
		return runtimeState{}, fmt.Errorf("dataset deal buyer ID is missing")
	}
	stateData, found, err := StateData.Get(ctx)
	if err != nil {
		return runtimeState{}, err
	}
	if !found {
		return runtimeState{}, fmt.Errorf("dataset deal stateData is missing")
	}
	currentState, _, err := CurrentState.Get(ctx)
	if err != nil {
		return runtimeState{}, err
	}
	return runtimeState{
		ProcessID:    processID,
		BuyerID:      buyerID,
		CurrentState: currentState,
		StateData:    stateData,
	}, nil
}

func actionsForPhase(state StateDefinition, phase actionPhase) ([]string, error) {
	switch phase {
	case preActionPhase:
		return state.PreActions, nil
	case postActionPhase:
		return state.PostActions, nil
	default:
		return nil, fmt.Errorf("unknown action phase %q", phase)
	}
}

func conditionUpdates(
	ctx dex.Context,
	conditionName string,
) (map[string]string, error) {
	results, err := ConditionMessages.GetConditionResults(ctx, conditionName)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf(
			"condition %q expected one message, received %d",
			conditionName,
			len(results),
		)
	}
	return results[0], nil
}

func mergeStateData(ctx dex.Context, updates map[string]string) error {
	stateData, found, err := StateData.Get(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("dataset deal stateData is missing")
	}
	merged := copyStateData(stateData)
	for key, value := range updates {
		if key == "" {
			return fmt.Errorf("stateData update key must not be empty")
		}
		merged[key] = value
	}
	return StateData.Set(ctx, merged)
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

func copyStateData(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

var _ dex.Flow = (*DealFlow)(nil)

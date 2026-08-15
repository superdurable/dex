// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func mapRegisteredWait(
	registry *Registry,
	flow *registeredFlow,
	wait *Wait,
) (*dexpb.WaitingCondition, error) {
	if wait == nil {
		return nil, fmt.Errorf("dex: WaitFor returned nil")
	}
	if err := validateRegisteredWait(flow, wait); err != nil {
		return nil, err
	}
	return mapWaitWithRegistry(registry, flow, wait)
}

func validateRegisteredWait(flow *registeredFlow, wait *Wait) error {
	for _, condition := range wait.conditions {
		if err := validateRegisteredCondition(flow, condition); err != nil {
			return err
		}
	}
	for _, combination := range wait.combinations {
		for _, condition := range combination.conditions {
			if err := validateRegisteredCondition(flow, condition); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRegisteredCondition(flow *registeredFlow, condition Condition) error {
	concrete, ok := condition.(*conditionImpl)
	if !ok || concrete == nil {
		return fmt.Errorf("dex: invalid condition %T", condition)
	}
	if concrete.kind != conditionChannel {
		return nil
	}
	channel, found := flow.channels[concrete.channelName]
	if !found {
		return fmt.Errorf("dex: channel %q is not declared", concrete.channelName)
	}
	if channel.isMap != concrete.isMap {
		return fmt.Errorf(
			"dex: channel %q static/map kind does not match its condition",
			concrete.channelName,
		)
	}
	_, err := physicalName(concrete.channelName, concrete.instance, concrete.isMap)
	return err
}

func flowRegistryTarget(condition *conditionImpl, registry *Registry) (*registeredFlow, error) {
	if registry == nil {
		return nil, nil
	}
	target, err := registry.resolveFlow(condition.subFlow)
	if err != nil {
		return nil, err
	}
	if target.startingStep == nil {
		return nil, fmt.Errorf("dex: SubFlow %q has no starting Step", target.flowType)
	}
	if !assignableValue(condition.subFlowInput, target.startingStep.inputType) {
		return nil, fmt.Errorf(
			"dex: SubFlow %q input %T does not match %s",
			target.flowType,
			condition.subFlowInput,
			target.startingStep.inputType,
		)
	}
	return target, nil
}

func mapRegisteredDecision(
	flow *registeredFlow,
	decision *StepDecision,
) (*dexpb.StepDecision, error) {
	if decision == nil {
		return nil, fmt.Errorf("dex: Execute returned nil")
	}
	var mapped *dexpb.StepDecision
	switch decision.kind {
	case decisionNext:
		if len(decision.movements) == 0 {
			return nil, fmt.Errorf("dex: GoToMulti requires at least one movement")
		}
		movements, err := mapRegisteredMovements(flow, decision.movements)
		if err != nil {
			return nil, err
		}
		mapped = &dexpb.StepDecision{NextSteps: movements}
	case decisionClose:
		if len(decision.movements) != 0 {
			return nil, fmt.Errorf("dex: close decision cannot have next steps")
		}
		closeDecision, err := mapCloseDecision(decision.close, false)
		if err != nil {
			return nil, err
		}
		mapped = &dexpb.StepDecision{CloseDecision: closeDecision}
	case decisionConditionalClose:
		if len(decision.movements) == 0 {
			return nil, fmt.Errorf("dex: conditional close requires fallback movements")
		}
		if err := validateRegisteredCloseChannels(flow, decision.close.channels); err != nil {
			return nil, err
		}
		movements, err := mapRegisteredMovements(flow, decision.movements)
		if err != nil {
			return nil, err
		}
		closeDecision, err := mapCloseDecision(decision.close, true)
		if err != nil {
			return nil, err
		}
		mapped = &dexpb.StepDecision{
			NextSteps:     movements,
			CloseDecision: closeDecision,
		}
	default:
		return nil, fmt.Errorf("dex: Execute returned an empty decision")
	}
	cancelingSteps, cancelingSiblings, err := mapCancellationSelectors(
		flow,
		decision.cancelingSteps,
		decision.cancelingSiblingSteps,
	)
	if err != nil {
		return nil, err
	}
	mapped.CancelStepTypes = cancelingSteps
	mapped.CancelSiblingStepTypes = cancelingSiblings
	return mapped, nil
}

func mapCancellationSelectors(
	flow *registeredFlow,
	global []StepSelector,
	siblings []StepSelector,
) ([]string, []string, error) {
	globalTypes, err := mapStepSelectors(flow, global, nil)
	if err != nil {
		return nil, nil, err
	}
	selected := make(map[string]struct{}, len(globalTypes))
	for _, stepType := range globalTypes {
		selected[stepType] = struct{}{}
	}
	siblingTypes, err := mapStepSelectors(flow, siblings, selected)
	if err != nil {
		return nil, nil, err
	}
	return globalTypes, siblingTypes, nil
}

func mapStepSelectors(
	flow *registeredFlow,
	selectors []StepSelector,
	excluded map[string]struct{},
) ([]string, error) {
	stepTypes := make([]string, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		target, err := flow.resolveStepSelector(selector)
		if err != nil {
			return nil, err
		}
		if _, found := excluded[target.stepType]; found {
			continue
		}
		if _, found := seen[target.stepType]; found {
			continue
		}
		seen[target.stepType] = struct{}{}
		stepTypes = append(stepTypes, target.stepType)
	}
	return stepTypes, nil
}

func validateRegisteredCloseChannels(
	flow *registeredFlow,
	channels []ChannelDef,
) error {
	resolved, err := flow.resolveChannels(channels)
	if err != nil {
		return err
	}
	for _, channel := range resolved {
		if channel.isMap {
			return fmt.Errorf(
				"dex: conditional close channel %q must be static",
				channel.name,
			)
		}
	}
	return nil
}

func mapRegisteredMovements(
	flow *registeredFlow,
	movements []StepMovement,
) ([]*dexpb.StepMovement, error) {
	mapped := make([]*dexpb.StepMovement, 0, len(movements))
	for _, movement := range movements {
		value, err := mapRegisteredMovement(flow, movement)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, value)
	}
	return mapped, nil
}

func mapRegisteredMovement(
	flow *registeredFlow,
	movement StepMovement,
) (*dexpb.StepMovement, error) {
	target, err := flow.resolveMovement(movement)
	if err != nil {
		return nil, err
	}
	input, err := encodeValue(movement.input)
	if err != nil {
		return nil, err
	}
	options, err := mapRegisteredStepOptions(target, movement.options)
	if err != nil {
		return nil, err
	}
	return &dexpb.StepMovement{
		StepType:    target.stepType,
		StepInput:   input,
		StepOptions: options,
	}, nil
}

func mapRegisteredStepOptions(
	step *registeredStep,
	overrides *StepOptions,
) (*dexpb.StepOptions, error) {
	options, err := mapStepOptions(mergeStepOptions(step.options, overrides))
	if err != nil {
		return nil, err
	}
	if !step.skipWaitFor {
		return options, nil
	}
	if options == nil {
		options = &dexpb.StepOptions{}
	}
	options.SkipWaitFor = true
	return options, nil
}

func mapRegisteredRPCResult(
	flow *registeredFlow,
	result rpcResult,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if result == nil {
		return nil, fmt.Errorf("RPC returned nil")
	}
	output, err := encodeValue(result.rpcOutput())
	if err != nil {
		return nil, err
	}
	response := &dexpb.InvokeWorkerRPCResponse{Output: output}
	movements := result.rpcMovements()
	cancelingSteps, err := mapStepSelectors(flow, result.rpcCancelingSteps(), nil)
	if err != nil {
		return nil, err
	}
	if len(movements) == 0 && len(cancelingSteps) == 0 {
		return response, nil
	}
	nextSteps, err := mapRegisteredMovements(flow, movements)
	if err != nil {
		return nil, err
	}
	response.StepDecision = &dexpb.StepDecision{
		NextSteps:       nextSteps,
		CancelStepTypes: cancelingSteps,
	}
	return response, nil
}

// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func mapInitialAttributes(
	attributes []InitialAttributeDef,
) ([]*dexpb.AttributeWrite, error) {
	mapped := make([]*dexpb.AttributeWrite, 0, len(attributes))
	for _, attribute := range attributes {
		concrete, ok := attribute.(initialAttribute)
		if !ok {
			return nil, fmt.Errorf("dex: invalid initial attribute %T", attribute)
		}
		key, err := physicalName(
			concrete.name,
			concrete.instance,
			concrete.isMap,
		)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, &dexpb.AttributeWrite{
			Key:         key,
			Value:       concrete.encoded,
			IndexConfig: concrete.indexConfig,
		})
	}
	return mapped, nil
}

func mapAttributeWrite(write AttributeWrite) (*dexpb.AttributeWrite, error) {
	if write.Name == "" {
		return nil, fmt.Errorf("dex: attribute write name must not be empty")
	}
	value, indexConfig, err := encodeAttributeValue(write.Value, write.Index)
	if err != nil {
		return nil, err
	}
	return &dexpb.AttributeWrite{
		Key:         write.Name,
		Value:       value,
		IndexConfig: indexConfig,
	}, nil
}

func mapAttributeDelete(
	name string,
	index *AttributeIndex,
) (*dexpb.AttributeWrite, error) {
	if name == "" {
		return nil, fmt.Errorf("dex: attribute delete name must not be empty")
	}
	value, indexConfig, err := newDeleteValue(index)
	if err != nil {
		return nil, err
	}
	return &dexpb.AttributeWrite{
		Key:         name,
		Value:       value,
		IndexConfig: indexConfig,
	}, nil
}

func mapStartFlowOptions(
	options StartFlowOptions,
) (int32, *dexpb.FlowStartOptions, string, error) {
	timeout, err := optionalDurationSeconds32(options.Timeout)
	if err != nil {
		return 0, nil, "", fmt.Errorf("dex: flow timeout: %w", err)
	}
	startDelay, err := optionalDurationSeconds32(options.StartDelay)
	if err != nil {
		return 0, nil, "", fmt.Errorf("dex: start delay: %w", err)
	}
	idReuse, err := mapIDReusePolicy(options.IDReusePolicy)
	if err != nil {
		return 0, nil, "", err
	}
	retry, err := mapFlowRetryPolicy(options.RetryPolicy)
	if err != nil {
		return 0, nil, "", err
	}
	attributes, err := mapInitialAttributes(options.Attributes)
	if err != nil {
		return 0, nil, "", err
	}
	config, err := mapFlowConfig(options.ConfigOverride)
	if err != nil {
		return 0, nil, "", err
	}
	requestID, err := newRequestID()
	if err != nil {
		return 0, nil, "", err
	}
	return timeout, &dexpb.FlowStartOptions{
		IdReusePolicy:         idReuse,
		CronSchedule:          options.CronSchedule,
		FlowStartDelaySeconds: startDelay,
		RetryPolicy:           retry,
		Attributes:            attributes,
		FlowConfigOverride:    config,
		FlowAlreadyStartedOptions: mapAlreadyStartedOptions(
			options.AlreadyStarted,
		),
	}, requestID, nil
}

func mapAlreadyStartedOptions(
	options *AlreadyStartedOptions,
) *dexpb.FlowAlreadyStartedOptions {
	if options == nil {
		return nil
	}
	return &dexpb.FlowAlreadyStartedOptions{
		IgnoreAlreadyStartedError: options.IgnoreError,
	}
}

func mapStepOptions(options *StepOptions) (*dexpb.StepOptions, error) {
	return mapStepOptionsRecursive(options, make(map[*StepOptions]bool))
}

func mapStepOptionsRecursive(
	options *StepOptions,
	active map[*StepOptions]bool,
) (*dexpb.StepOptions, error) {
	if options == nil {
		return nil, nil
	}
	if active[options] {
		return nil, fmt.Errorf("dex: step options contain a cycle")
	}
	active[options] = true
	defer delete(active, options)

	waitForTimeout, err := durationSeconds32(options.WaitForTimeout)
	if err != nil {
		return nil, fmt.Errorf("dex: WaitFor timeout: %w", err)
	}
	executeTimeout, err := durationSeconds32(options.ExecuteTimeout)
	if err != nil {
		return nil, fmt.Errorf("dex: Execute timeout: %w", err)
	}
	waitForRetry, err := mapRetryPolicy(options.WaitForRetry)
	if err != nil {
		return nil, fmt.Errorf("dex: WaitFor retry: %w", err)
	}
	executeRetry, err := mapRetryPolicy(options.ExecuteRetry)
	if err != nil {
		return nil, fmt.Errorf("dex: Execute retry: %w", err)
	}
	waitForFailure, err := mapWaitForFailurePolicy(options.WaitForFailure)
	if err != nil {
		return nil, err
	}
	waitForDurability, err := mapStepDurability(options.WaitForDurability)
	if err != nil {
		return nil, err
	}
	executeDurability, err := mapStepDurability(options.ExecuteDurability)
	if err != nil {
		return nil, err
	}
	executeFailurePolicy, executeFailureStep, executeFailureOptions, err :=
		mapExecuteFailure(options.ExecuteFailure, active)
	if err != nil {
		return nil, err
	}
	waitForLocks, err := mapAttributeLocks(options.WaitForLockAttributes)
	if err != nil {
		return nil, err
	}
	executeLocks, err := mapAttributeLocks(options.ExecuteLockAttributes)
	if err != nil {
		return nil, err
	}
	return &dexpb.StepOptions{
		WaitForTimeoutSeconds:            waitForTimeout,
		ExecuteTimeoutSeconds:            executeTimeout,
		WaitForRetryPolicy:               waitForRetry,
		ExecuteRetryPolicy:               executeRetry,
		WaitForFailurePolicy:             waitForFailure,
		ExecuteFailurePolicy:             executeFailurePolicy,
		ExecuteFailureProceedStepType:    executeFailureStep,
		ExecuteFailureProceedStepOptions: executeFailureOptions,
		WaitForDurabilityOverride:        waitForDurability,
		ExecuteDurabilityOverride:        executeDurability,
		WaitForLockAttributeKeys:         waitForLocks,
		ExecuteLockAttributeKeys:         executeLocks,
	}, nil
}

func mapExecuteFailure(
	failure *ExecuteFailure,
	active map[*StepOptions]bool,
) (
	dexpb.ExecuteMethodFailurePolicy,
	string,
	*dexpb.StepOptions,
	error,
) {
	if failure == nil {
		return dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED,
			"", nil, nil
	}
	if !validStepReference(failure.step) {
		return dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED,
			"", nil, fmt.Errorf("dex: Execute failure target is invalid")
	}
	options, err := mapStepOptionsRecursive(failure.options, active)
	if err != nil {
		return dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED,
			"", nil, err
	}
	return dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
		failure.step.stepType(), options, nil
}

func mapRetryPolicy(policy *RetryPolicy) (*dexpb.RetryPolicy, error) {
	if policy == nil {
		return nil, nil
	}
	initial, err := durationSeconds32(policy.InitialInterval)
	if err != nil {
		return nil, err
	}
	maximum, err := durationSeconds32(policy.MaximumInterval)
	if err != nil {
		return nil, err
	}
	total, err := durationSeconds32(policy.TotalDuration)
	if err != nil {
		return nil, err
	}
	backoff, err := float32Value(policy.BackoffCoefficient)
	if err != nil {
		return nil, err
	}
	return &dexpb.RetryPolicy{
		InitialIntervalSeconds: initial,
		BackoffCoefficient:     backoff,
		MaximumIntervalSeconds: maximum,
		MaximumAttempts:        policy.MaximumAttempts,
		TotalDurationSeconds:   total,
	}, nil
}

func mapFlowRetryPolicy(
	policy *FlowRetryPolicy,
) (*dexpb.FlowRetryPolicy, error) {
	if policy == nil {
		return nil, nil
	}
	initial, err := durationSeconds32(policy.InitialInterval)
	if err != nil {
		return nil, err
	}
	maximum, err := durationSeconds32(policy.MaximumInterval)
	if err != nil {
		return nil, err
	}
	backoff, err := float32Value(policy.BackoffCoefficient)
	if err != nil {
		return nil, err
	}
	return &dexpb.FlowRetryPolicy{
		InitialIntervalSeconds: initial,
		BackoffCoefficient:     backoff,
		MaximumIntervalSeconds: maximum,
		MaximumAttempts:        policy.MaximumAttempts,
	}, nil
}

func mapFlowConfig(config *FlowConfig) (*dexpb.FlowConfig, error) {
	if config == nil {
		return nil, nil
	}
	searchMode, err := mapOptionalActiveStepSearchMode(config.ActiveStepSearchMode)
	if err != nil {
		return nil, err
	}
	durability, err := mapOptionalStepDurability(config.StepDurability)
	if err != nil {
		return nil, err
	}
	return &dexpb.FlowConfig{
		ActiveStepSearchMode:         searchMode,
		ContinueAsNewThreshold:       config.ContinueAsNewThreshold,
		ContinueAsNewPageSizeInBytes: config.ContinueAsNewPageSizeBytes,
		StepDurability:               durability,
		WorkerTarget:                 mapWorkerTarget(config.WorkerTarget),
	}, nil
}

func mapWorkerTarget(target *WorkerTarget) *dexpb.WorkerTarget {
	if target == nil {
		return nil
	}
	return &dexpb.WorkerTarget{
		Address:           target.Address,
		IsHeadlessAddress: target.Headless,
	}
}

func mapWait(wait Wait) (*dexpb.WaitingCondition, error) {
	switch wait.kind {
	case waitCompletedImmediately:
		return nil, nil
	case waitAllOf:
		return mapFlatWait(
			dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			wait.conditions,
		)
	case waitAnyOf:
		return mapFlatWait(
			dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
			wait.conditions,
		)
	case waitAnyComboOf:
		return mapCombinationWait(wait.combinations)
	default:
		return nil, fmt.Errorf("dex: invalid wait")
	}
}

func mapFlatWait(
	waitType dexpb.WaitingConditionType,
	conditions []Condition,
) (*dexpb.WaitingCondition, error) {
	if len(conditions) == 0 {
		return nil, fmt.Errorf("dex: wait requires at least one condition")
	}
	mapper := newConditionMapper()
	for _, condition := range conditions {
		if _, err := mapper.add(condition); err != nil {
			return nil, err
		}
	}
	mapped := mapper.result(waitType)
	return mapped, nil
}

func mapCombinationWait(
	combinations []ConditionCombination,
) (*dexpb.WaitingCondition, error) {
	if len(combinations) == 0 {
		return nil, fmt.Errorf("dex: AnyComboOf requires at least one combination")
	}
	mapper := newConditionMapper()
	mappedCombinations := make(
		[]*dexpb.ConditionCombination,
		0,
		len(combinations),
	)
	for _, combination := range combinations {
		if len(combination.conditions) == 0 {
			return nil, fmt.Errorf("dex: condition combination must not be empty")
		}
		ids := make([]string, 0, len(combination.conditions))
		for _, condition := range combination.conditions {
			id, err := mapper.add(condition)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		mappedCombinations = append(mappedCombinations, &dexpb.ConditionCombination{
			ConditionIds: ids,
		})
	}
	mapped := mapper.result(
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
	)
	mapped.ConditionCombinations = mappedCombinations
	return mapped, nil
}

type conditionMapper struct {
	ids      map[*conditionValue]string
	usedIDs  map[string]struct{}
	timers   []*dexpb.TimerCondition
	channels []*dexpb.ChannelCondition
	nextID   int
}

func newConditionMapper() *conditionMapper {
	return &conditionMapper{
		ids:     make(map[*conditionValue]string),
		usedIDs: make(map[string]struct{}),
	}
}

func (mapper *conditionMapper) add(condition Condition) (string, error) {
	concrete, ok := condition.(*conditionValue)
	if !ok || concrete == nil {
		return "", fmt.Errorf("dex: invalid condition %T", condition)
	}
	if id, found := mapper.ids[concrete]; found {
		return id, nil
	}
	if concrete.err != nil {
		return "", concrete.err
	}
	id, err := mapper.assignID(concrete.conditionID, concrete.idSet)
	if err != nil {
		return "", err
	}
	switch concrete.kind {
	case conditionTimer:
		duration, durationErr := durationSeconds64(concrete.duration)
		if durationErr != nil {
			return "", durationErr
		}
		mapper.timers = append(mapper.timers, &dexpb.TimerCondition{
			ConditionId:     id,
			DurationSeconds: duration,
		})
	case conditionChannel:
		name, nameErr := physicalName(
			concrete.channelName,
			concrete.instance,
			concrete.isMap,
		)
		if nameErr != nil {
			return "", nameErr
		}
		atLeast, boundsErr := optionalInt32(concrete.atLeast)
		if boundsErr != nil {
			return "", boundsErr
		}
		atMost, boundsErr := optionalInt32(concrete.atMost)
		if boundsErr != nil {
			return "", boundsErr
		}
		mapper.channels = append(mapper.channels, &dexpb.ChannelCondition{
			ConditionId: id,
			ChannelName: name,
			AtLeast:     atLeast,
			AtMost:      atMost,
		})
	default:
		return "", fmt.Errorf("dex: unsupported condition kind %d", concrete.kind)
	}
	mapper.ids[concrete] = id
	return id, nil
}

func (mapper *conditionMapper) assignID(
	explicit string,
	explicitlySet bool,
) (string, error) {
	if explicitlySet && explicit == "" {
		return "", fmt.Errorf("dex: explicit condition ID must not be empty")
	}
	id := explicit
	if id == "" {
		id = internalIDPrefix + strconv.Itoa(mapper.nextID)
		mapper.nextID++
	} else if strings.HasPrefix(id, internalIDPrefix) {
		return "", fmt.Errorf("dex: condition ID %q uses reserved prefix", id)
	}
	if _, found := mapper.usedIDs[id]; found {
		return "", fmt.Errorf("dex: duplicate condition ID %q", id)
	}
	mapper.usedIDs[id] = struct{}{}
	return id, nil
}

func (mapper *conditionMapper) result(
	waitType dexpb.WaitingConditionType,
) *dexpb.WaitingCondition {
	return &dexpb.WaitingCondition{
		WaitingConditionType: waitType,
		TimerConditions:      mapper.timers,
		ChannelConditions:    mapper.channels,
	}
}

func mapStepDecision(decision StepDecision) (*dexpb.StepDecision, error) {
	switch decision.kind {
	case decisionNext:
		if len(decision.movements) == 0 {
			return nil, fmt.Errorf("dex: GoToMulti requires at least one movement")
		}
		movements, err := mapStepMovements(decision.movements)
		if err != nil {
			return nil, err
		}
		return &dexpb.StepDecision{NextSteps: movements}, nil
	case decisionClose:
		if len(decision.movements) != 0 {
			return nil, fmt.Errorf("dex: close decision cannot have next steps")
		}
		closeDecision, err := mapCloseDecision(decision.close, false)
		if err != nil {
			return nil, err
		}
		return &dexpb.StepDecision{CloseDecision: closeDecision}, nil
	case decisionConditionalClose:
		if len(decision.movements) == 0 {
			return nil, fmt.Errorf("dex: conditional close requires fallback movements")
		}
		movements, err := mapStepMovements(decision.movements)
		if err != nil {
			return nil, err
		}
		closeDecision, err := mapCloseDecision(decision.close, true)
		if err != nil {
			return nil, err
		}
		return &dexpb.StepDecision{
			NextSteps:     movements,
			CloseDecision: closeDecision,
		}, nil
	default:
		return nil, fmt.Errorf("dex: Execute returned an empty decision")
	}
}

func mapStepMovements(
	movements []StepMovement,
) ([]*dexpb.StepMovement, error) {
	mapped := make([]*dexpb.StepMovement, 0, len(movements))
	for _, movement := range movements {
		value, err := mapStepMovement(movement)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, value)
	}
	return mapped, nil
}

func mapStepMovement(movement StepMovement) (*dexpb.StepMovement, error) {
	if !validStepReference(movement.step) {
		return nil, fmt.Errorf("dex: movement target is invalid")
	}
	input, err := encodeValue(movement.input)
	if err != nil {
		return nil, err
	}
	options, err := mapStepOptions(
		mergeStepOptions(movement.step.stepOptions(), movement.options),
	)
	if err != nil {
		return nil, err
	}
	return &dexpb.StepMovement{
		StepType:    movement.step.stepType(),
		StepInput:   input,
		StepOptions: options,
	}, nil
}

func mapCloseDecision(
	decision CloseDecision,
	conditional bool,
) (*dexpb.CloseDecision, error) {
	switch decision.kind {
	case closeGracefulComplete:
		return mapCloseWithInput(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			decision.output,
		)
	case closeForceComplete:
		return mapCloseWithInput(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
			decision.output,
		)
	case closeForceFail:
		return mapCloseWithInput(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
			decision.reason,
		)
	case closeDeadEnd:
		return &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
		}, nil
	case closeConditionalForceComplete:
		if !conditional {
			return nil, fmt.Errorf("dex: conditional close has invalid decision kind")
		}
		channels, err := mapUniqueChannels(decision.channels)
		if err != nil {
			return nil, err
		}
		closeDecision, err := mapCloseWithInput(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
			decision.output,
		)
		if err != nil {
			return nil, err
		}
		closeDecision.ConditionalChannelNames = channels
		return closeDecision, nil
	default:
		return nil, fmt.Errorf("dex: invalid close decision")
	}
}

func mapCloseWithInput(
	closeType dexpb.CloseDecisionType,
	input any,
) (*dexpb.CloseDecision, error) {
	value, err := encodeValue(input)
	if err != nil {
		return nil, err
	}
	return &dexpb.CloseDecision{
		CloseDecisionType: closeType,
		CloseInput:        value,
	}, nil
}

func mapUniqueChannels(channels []ChannelDef) ([]string, error) {
	if len(channels) == 0 {
		return nil, fmt.Errorf("dex: conditional close requires channels")
	}
	mapped := make([]string, 0, len(channels))
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if channel == nil || channel.channelName() == "" {
			return nil, fmt.Errorf("dex: conditional close channel is invalid")
		}
		name := channel.channelName()
		if _, found := seen[name]; found {
			return nil, fmt.Errorf("dex: duplicate conditional close channel %q", name)
		}
		seen[name] = struct{}{}
		mapped = append(mapped, name)
	}
	return mapped, nil
}

func mapRPCResult[OUT any](result RPCResult[OUT]) (*dexpb.InvokeWorkerRPCResponse, error) {
	output, err := encodeValue(result.Output)
	if err != nil {
		return nil, err
	}
	response := &dexpb.InvokeWorkerRPCResponse{Output: output}
	if len(result.NextSteps) == 0 {
		return response, nil
	}
	nextSteps, err := mapStepMovements(result.NextSteps)
	if err != nil {
		return nil, err
	}
	response.StepDecision = &dexpb.StepDecision{NextSteps: nextSteps}
	return response, nil
}

func mapWaitForFlowResult(
	response *dexpb.WaitForFlowResponse,
) (WaitForFlowResult, error) {
	if response == nil {
		return WaitForFlowResult{}, fmt.Errorf("dex: WaitForFlow response is nil")
	}
	status, err := mapFlowStatus(response.FlowStatus)
	if err != nil {
		return WaitForFlowResult{}, err
	}
	errorType, err := mapFlowErrorType(response.ErrorType)
	if err != nil {
		return WaitForFlowResult{}, err
	}
	completions := make([]StepCompletion, 0, len(response.Results))
	for _, completion := range response.Results {
		if completion == nil {
			return WaitForFlowResult{}, fmt.Errorf("dex: step completion is nil")
		}
		output, valueErr := newValue(completion.CompletedStepOutput)
		if valueErr != nil {
			return WaitForFlowResult{}, valueErr
		}
		completions = append(completions, StepCompletion{
			StepType:        completion.CompletedStepType,
			StepExecutionID: completion.CompletedStepExecutionId,
			Output:          output,
		})
	}
	return WaitForFlowResult{
		Status:       status,
		Completions:  completions,
		ErrorType:    errorType,
		ErrorMessage: response.ErrorMessage,
	}, nil
}

func mapSearchFlowsPage(
	response *dexpb.SearchFlowsResponse,
) (SearchFlowsPage, error) {
	if response == nil {
		return SearchFlowsPage{}, fmt.Errorf("dex: SearchFlows response is nil")
	}
	flows := make([]SearchFlowEntry, 0, len(response.FlowRuns))
	for _, flow := range response.FlowRuns {
		if flow == nil {
			return SearchFlowsPage{}, fmt.Errorf("dex: search flow entry is nil")
		}
		attributes, err := mapValues(flow.SearchAttributes)
		if err != nil {
			return SearchFlowsPage{}, err
		}
		flows = append(flows, SearchFlowEntry{
			FlowID:           flow.FlowId,
			RunID:            flow.RunId,
			SearchAttributes: attributes,
		})
	}
	return SearchFlowsPage{
		Flows:         flows,
		NextPageToken: response.NextPageToken,
	}, nil
}

func mapHealthInfo(info *dexpb.HealthInfo) (HealthInfo, error) {
	if info == nil {
		return HealthInfo{}, fmt.Errorf("dex: health info is nil")
	}
	return HealthInfo{
		Condition: info.Condition,
		Hostname:  info.Hostname,
		Duration:  info.Duration,
	}, nil
}

func mapStopOptions(options StopOptions) (dexpb.StopType, string, error) {
	var stopType dexpb.StopType
	switch options.Type {
	case CancelFlow:
		stopType = dexpb.StopType_STOP_TYPE_CANCEL
	case TerminateFlow:
		stopType = dexpb.StopType_STOP_TYPE_TERMINATE
	case FailFlow:
		stopType = dexpb.StopType_STOP_TYPE_FAIL
	default:
		return dexpb.StopType_STOP_TYPE_UNSPECIFIED, "",
			fmt.Errorf("dex: unsupported stop type %d", options.Type)
	}
	return stopType, options.Reason, nil
}

func mapResetOptions(options ResetOptions) (*dexpb.ResetFlowRequest, error) {
	resetType, err := mapResetType(options.Type)
	if err != nil {
		return nil, err
	}
	historyTime := ""
	if !options.HistoryEventTime.IsZero() {
		historyTime = options.HistoryEventTime.Format(dateTimeFormat)
	}
	return &dexpb.ResetFlowRequest{
		ResetType:                  resetType,
		HistoryEventId:             options.HistoryEventID,
		Reason:                     options.Reason,
		HistoryEventTime:           historyTime,
		StepType:                   options.StepType,
		StepExecutionId:            options.StepExecutionID,
		SkipChannelMessagesReapply: options.SkipChannelMessagesReapply,
		SkipLockingRpcReapply:      options.SkipLockingRPCReapply,
	}, nil
}

func mapWaitOptions(options WaitOptions) (int32, string, error) {
	timeout, err := durationSeconds32(options.Timeout)
	if err != nil {
		return 0, "", err
	}
	requestID, err := newRequestID()
	if err != nil {
		return 0, "", err
	}
	return timeout, requestID, nil
}

func mapWaitForFlowOptions(
	options WaitForFlowOptions,
) (bool, int32, error) {
	timeout, err := durationSeconds32(options.Timeout)
	if err != nil {
		return false, 0, err
	}
	return options.NeedsResults, timeout, nil
}

func mapInvokeOptions(
	options InvokeOptions,
) (int32, []string, string, error) {
	timeout, err := durationSeconds32(options.Timeout)
	if err != nil {
		return 0, nil, "", err
	}
	locks, err := mapAttributeLocks(options.LockAttributes)
	if err != nil {
		return 0, nil, "", err
	}
	if len(locks) == 0 {
		return timeout, locks, "", nil
	}
	requestID, err := newRequestID()
	if err != nil {
		return 0, nil, "", err
	}
	return timeout, locks, requestID, nil
}

func mapSearchFlowsOptions(
	query string,
	pageSize int32,
	nextPageToken string,
) (*dexpb.SearchFlowsRequest, error) {
	if pageSize < 0 {
		return nil, fmt.Errorf("dex: search page size must not be negative")
	}
	return &dexpb.SearchFlowsRequest{
		Query:         query,
		PageSize:      pageSize,
		NextPageToken: nextPageToken,
	}, nil
}

func mapValues(values []*dexpb.KV) (map[string]Value, error) {
	mapped := make(map[string]Value, len(values))
	for _, keyValue := range values {
		if keyValue == nil || keyValue.Key == "" {
			return nil, fmt.Errorf("dex: invalid key/value result")
		}
		if _, found := mapped[keyValue.Key]; found {
			return nil, fmt.Errorf("dex: duplicate result key %q", keyValue.Key)
		}
		value, err := newValue(keyValue.Value)
		if err != nil {
			return nil, err
		}
		mapped[keyValue.Key] = value
	}
	return mapped, nil
}

func newValue(value *dexpb.Value) (Value, error) {
	if err := validateConcreteValue(value); err != nil {
		return Value{}, err
	}
	return Value{value: value}, nil
}

func validateConcreteValue(value *dexpb.Value) error {
	if value == nil || value.Kind == nil {
		return fmt.Errorf("dex: value has no concrete kind")
	}
	switch kind := value.Kind.(type) {
	case *dexpb.Value_StringValue,
		*dexpb.Value_IntValue,
		*dexpb.Value_BoolValue:
		return nil
	case *dexpb.Value_DoubleValue:
		if math.IsNaN(kind.DoubleValue) || math.IsInf(kind.DoubleValue, 0) {
			return fmt.Errorf("dex: non-finite double result is unsupported")
		}
		return nil
	case *dexpb.Value_ObjValue:
		if kind.ObjValue == nil {
			return fmt.Errorf("dex: object value is missing")
		}
		if kind.ObjValue.Encoding != jsonEncoding {
			return fmt.Errorf(
				"dex: unsupported object encoding %q",
				kind.ObjValue.Encoding,
			)
		}
		return nil
	case *dexpb.Value_InternalBlobIdForStringValue,
		*dexpb.Value_InternalBlobIdForObjValue:
		return fmt.Errorf("dex: blob-backed value is not hydrated")
	case *dexpb.Value_NullValue:
		return fmt.Errorf("dex: attribute deletion marker is not a result value")
	default:
		return fmt.Errorf("dex: unsupported value kind %T", kind)
	}
}

func mergeStepOptions(defaults *StepOptions, overrides *StepOptions) *StepOptions {
	if defaults == nil {
		return overrides
	}
	if overrides == nil {
		return defaults
	}
	merged := *defaults
	if overrides.WaitForTimeout != 0 {
		merged.WaitForTimeout = overrides.WaitForTimeout
	}
	if overrides.ExecuteTimeout != 0 {
		merged.ExecuteTimeout = overrides.ExecuteTimeout
	}
	if overrides.WaitForRetry != nil {
		merged.WaitForRetry = overrides.WaitForRetry
	}
	if overrides.ExecuteRetry != nil {
		merged.ExecuteRetry = overrides.ExecuteRetry
	}
	if overrides.WaitForFailure != FailFlowOnWaitForFailure {
		merged.WaitForFailure = overrides.WaitForFailure
	}
	if overrides.ExecuteFailure != nil {
		merged.ExecuteFailure = overrides.ExecuteFailure
	}
	if overrides.WaitForDurability != StepDurabilityDefault {
		merged.WaitForDurability = overrides.WaitForDurability
	}
	if overrides.ExecuteDurability != StepDurabilityDefault {
		merged.ExecuteDurability = overrides.ExecuteDurability
	}
	if overrides.WaitForLockAttributes != nil {
		merged.WaitForLockAttributes = overrides.WaitForLockAttributes
	}
	if overrides.ExecuteLockAttributes != nil {
		merged.ExecuteLockAttributes = overrides.ExecuteLockAttributes
	}
	return &merged
}

func mapAttributeLocks(locks []AttributeLock) ([]string, error) {
	if locks == nil {
		return nil, nil
	}
	mapped := make([]string, 0, len(locks))
	seen := make(map[string]struct{}, len(locks))
	for _, lock := range locks {
		concrete, ok := lock.(attributeLock)
		if !ok {
			return nil, fmt.Errorf("dex: invalid attribute lock %T", lock)
		}
		name, err := physicalName(concrete.name, concrete.instance, concrete.isMap)
		if err != nil {
			return nil, err
		}
		if _, found := seen[name]; found {
			return nil, fmt.Errorf("dex: duplicate attribute lock %q", name)
		}
		seen[name] = struct{}{}
		mapped = append(mapped, name)
	}
	return mapped, nil
}

func physicalName(name string, instance string, isMap bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("dex: definition name must not be empty")
	}
	if !isMap {
		return name, nil
	}
	if instance == "" {
		return "", fmt.Errorf("dex: map instance must not be empty")
	}
	return name + "/" + url.PathEscape(instance), nil
}

func mapWaitForFailurePolicy(
	policy WaitForFailurePolicy,
) (dexpb.WaitForMethodFailurePolicy, error) {
	switch policy {
	case FailFlowOnWaitForFailure:
		return dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE,
			nil
	case ProceedOnWaitForFailure:
		return dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
			nil
	default:
		return dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_UNSPECIFIED,
			fmt.Errorf("dex: unsupported WaitFor failure policy %d", policy)
	}
}

func mapStepDurability(
	durability StepDurability,
) (dexpb.StepDurability, error) {
	switch durability {
	case StepDurabilityDefault:
		return dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED, nil
	case StepDurabilitySync:
		return dexpb.StepDurability_STEP_DURABILITY_SYNC, nil
	case StepDurabilityAsync:
		return dexpb.StepDurability_STEP_DURABILITY_ASYNC, nil
	default:
		return dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
			fmt.Errorf("dex: unsupported step durability %d", durability)
	}
}

func mapOptionalStepDurability(
	durability *StepDurability,
) (*dexpb.StepDurability, error) {
	if durability == nil {
		return nil, nil
	}
	mapped, err := mapStepDurability(*durability)
	if err != nil {
		return nil, err
	}
	return ptr.Any(mapped), nil
}

func mapOptionalActiveStepSearchMode(
	mode *ActiveStepSearchMode,
) (*dexpb.ActiveStepSearchMode, error) {
	if mode == nil {
		return nil, nil
	}
	var mapped dexpb.ActiveStepSearchMode
	switch *mode {
	case SearchActiveStepsDefault:
		mapped = dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED
	case SearchAllActiveSteps:
		mapped = dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
	case SearchActiveStepsWithWaitFor:
		mapped = dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
	case DisableActiveStepSearch:
		mapped = dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED
	default:
		return nil, fmt.Errorf("dex: unsupported active-step search mode %d", *mode)
	}
	return ptr.Any(mapped), nil
}

func mapIDReusePolicy(policy IDReusePolicy) (dexpb.IdReusePolicy, error) {
	switch policy {
	case IDReuseDefault:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED, nil
	case IDReuseAllowIfPreviousFailed:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY,
			nil
	case IDReuseAllowIfNotRunning:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING, nil
	case IDReuseDisallow:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE, nil
	case IDReuseTerminateIfRunning:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING, nil
	default:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED,
			fmt.Errorf("dex: unsupported ID reuse policy %d", policy)
	}
}

func mapResetType(resetType ResetType) (dexpb.FlowResetType, error) {
	switch resetType {
	case ResetByHistoryEventID:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_ID, nil
	case ResetToBeginning:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING, nil
	case ResetByHistoryEventTime:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_TIME, nil
	case ResetByStepType:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE, nil
	case ResetByStepExecutionID:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID, nil
	default:
		return dexpb.FlowResetType_FLOW_RESET_TYPE_UNSPECIFIED,
			fmt.Errorf("dex: unsupported reset type %d", resetType)
	}
}

func mapFlowStatus(status dexpb.FlowStatus) (FlowStatus, error) {
	switch status {
	case dexpb.FlowStatus_FLOW_STATUS_RUNNING:
		return FlowRunning, nil
	case dexpb.FlowStatus_FLOW_STATUS_COMPLETED:
		return FlowCompleted, nil
	case dexpb.FlowStatus_FLOW_STATUS_FAILED:
		return FlowFailed, nil
	case dexpb.FlowStatus_FLOW_STATUS_TIMEOUT:
		return FlowTimedOut, nil
	case dexpb.FlowStatus_FLOW_STATUS_TERMINATED:
		return FlowTerminated, nil
	case dexpb.FlowStatus_FLOW_STATUS_CANCELED:
		return FlowCanceled, nil
	case dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW:
		return FlowContinuedAsNew, nil
	default:
		return 0, fmt.Errorf("dex: unsupported flow status %d", status)
	}
}

func mapFlowErrorType(errorType dexpb.FlowErrorType) (FlowErrorType, error) {
	switch errorType {
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED:
		return 0, nil
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW:
		return FlowErrorStepDecision, nil
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW:
		return FlowErrorClientAPI, nil
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL:
		return FlowErrorWorkerMethod, nil
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE:
		return FlowErrorInvalidUserCode, nil
	case dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL:
		return FlowErrorInternal, nil
	default:
		return 0, fmt.Errorf("dex: unsupported flow error type %d", errorType)
	}
}

func optionalDurationSeconds32(duration *time.Duration) (int32, error) {
	if duration == nil {
		return 0, nil
	}
	return durationSeconds32(*duration)
}

func durationSeconds32(duration time.Duration) (int32, error) {
	seconds, err := durationSeconds64(duration)
	if err != nil {
		return 0, err
	}
	if seconds > math.MaxInt32 {
		return 0, fmt.Errorf("duration %s exceeds int32 seconds", duration)
	}
	return int32(seconds), nil
}

func durationSeconds64(duration time.Duration) (int64, error) {
	if duration < 0 {
		return 0, fmt.Errorf("duration must not be negative")
	}
	if duration == 0 {
		return 0, nil
	}
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	return int64(seconds), nil
}

func optionalInt32(value *int) (*int32, error) {
	if value == nil {
		return nil, nil
	}
	if *value < math.MinInt32 || *value > math.MaxInt32 {
		return nil, fmt.Errorf("dex: count %d exceeds int32", *value)
	}
	return ptr.Any(int32(*value)), nil
}

func float32Value(value float64) (float32, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) ||
		value > math.MaxFloat32 || value < -math.MaxFloat32 {
		return 0, fmt.Errorf("dex: backoff coefficient %v exceeds float32", value)
	}
	return float32(value), nil
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestActivityInvocationIdStableAcrossRetries(t *testing.T) {
	activityInfo := interfaces.ActivityInfo{
		ActivityID: "activity-456",
		Attempt:    1,
		WorkflowExecution: interfaces.WorkflowExecution{
			ID:    "flow-123",
			RunID: "run-123",
		},
	}
	firstInvocationId := activityInvocationId(activityInfo)
	activityInfo.Attempt = 7
	retryInvocationId := activityInvocationId(activityInfo)

	require.Equal(t, firstInvocationId, retryInvocationId)
}

func TestInvalidAnyConditionCombination(t *testing.T) {
	timers, channels := createConditions()
	waiting := &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		TimerConditions:      timers,
		ChannelConditions:    channels,
		ConditionCombinations: []*dexpb.ConditionCombination{
			{ConditionIds: []string{"timer-cmd1", "signal-cmd1"}},
			{ConditionIds: []string{"timer-cmd1", "invalid"}},
		},
	}

	err := validateWaitingCondition(waiting)
	require.Error(t, err)
	require.ErrorContains(t, err, `references undeclared condition_id "invalid"`)
}

func TestValidAnyConditionCombination(t *testing.T) {
	timers, channels := createConditions()
	waiting := &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		TimerConditions:      timers,
		ChannelConditions:    channels,
		ConditionCombinations: []*dexpb.ConditionCombination{
			{ConditionIds: []string{"timer-cmd1", "signal-cmd1"}},
			{ConditionIds: []string{"timer-cmd1", "internal-cmd1"}},
		},
	}

	require.NoError(t, validateWaitingCondition(waiting))
}

func TestValidateWaitingConditionChannelBounds(t *testing.T) {
	waiting := &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		ChannelConditions: []*dexpb.ChannelCondition{
			{ConditionId: "c1", ChannelName: "ch", AtLeast: ptr.Any(int32(2)), AtMost: ptr.Any(int32(1))},
		},
	}
	require.Error(t, validateWaitingCondition(waiting))
}

func TestValidateWaitingConditionRejections(t *testing.T) {
	duplicateId := newValidWaitingCondition()
	duplicateId.TimerConditions = []*dexpb.TimerCondition{{ConditionId: "condition"}}

	negativeTimer := newValidWaitingCondition()
	negativeTimer.TimerConditions = []*dexpb.TimerCondition{{
		ConditionId:     "timer",
		DurationSeconds: -1,
	}}

	absoluteTimer := newValidWaitingCondition()
	absoluteTimer.TimerConditions = []*dexpb.TimerCondition{{
		ConditionId:                "timer",
		FiringUnixTimestampSeconds: 10,
	}}

	combinationsOnAll := newValidWaitingCondition()
	combinationsOnAll.WaitingConditionType =
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED
	combinationsOnAll.ConditionCombinations = []*dexpb.ConditionCombination{{
		ConditionIds: []string{"condition"},
	}}

	missingCombination := newValidWaitingCondition()
	missingCombination.WaitingConditionType =
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED

	unknownType := newValidWaitingCondition()
	unknownType.WaitingConditionType =
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_UNSPECIFIED

	emptyConditionId := waitingConditionWithChannel(newChannelCondition("", "channel", nil, nil))
	emptyConditionId.WaitingConditionType =
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED
	emptyConditionId.ConditionCombinations = []*dexpb.ConditionCombination{{
		ConditionIds: []string{"condition"},
	}}

	testCases := []struct {
		name             string
		waitingCondition *dexpb.WaitingCondition
		errorContains    string
	}{
		{"nil_timer_entry", waitingConditionWithTimer(nil), "timer condition at index 0 is nil"},
		{"nil_channel_entry", waitingConditionWithChannel(nil), "channel condition at index 0 is nil"},
		{"empty_condition_id_for_combination", emptyConditionId, "empty condition_id"},
		{"duplicate_condition_id", duplicateId, `duplicate condition_id "condition"`},
		{"empty_channel_name", waitingConditionWithChannel(newChannelCondition("condition", "", nil, nil)), "empty channel_name"},
		{
			"negative_at_least",
			waitingConditionWithChannel(newChannelCondition("condition", "channel", ptr.Any(int32(-1)), nil)),
			"negative at_least",
		},
		{
			"negative_at_most",
			waitingConditionWithChannel(newChannelCondition("condition", "channel", nil, ptr.Any(int32(-1)))),
			"negative at_most",
		},
		{
			"at_most_less_than_at_least",
			waitingConditionWithChannel(
				newChannelCondition("condition", "channel", ptr.Any(int32(3)), ptr.Any(int32(2))),
			),
			"at_most 2 < at_least 3",
		},
		{
			"explicit_zero_at_most_less_than_at_least",
			waitingConditionWithChannel(
				newChannelCondition("condition", "channel", ptr.Any(int32(1)), ptr.Any(int32(0))),
			),
			"at_most 0 < at_least 1",
		},
		{"negative_timer_duration", negativeTimer, "negative duration_seconds"},
		{"worker_sets_absolute_timer", absoluteTimer, "server-owned firing_unix_timestamp_seconds"},
		{"combinations_on_all", combinationsOnAll, "only valid for ANY_COMBINATION_COMPLETED"},
		{"any_combination_requires_combination", missingCombination, "requires at least one condition_combination"},
		{
			"empty_combination",
			&dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*dexpb.ConditionCombination{
					{},
				},
			},
			"condition_combination at index 0 is empty",
		},
		{
			"combination_references_undeclared_id",
			&dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*dexpb.ConditionCombination{
					{ConditionIds: []string{"undeclared"}},
				},
			},
			`references undeclared condition_id "undeclared"`,
		},
		{
			"combination_duplicate_id",
			&dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*dexpb.ConditionCombination{
					{ConditionIds: []string{"condition", "condition"}},
				},
			},
			`duplicate condition_id "condition"`,
		},
		{"unknown_type", unknownType, "unknown waiting_condition_type"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateWaitingCondition(testCase.waitingCondition)
			require.Error(t, err)
			require.ErrorContains(t, err, testCase.errorContains)
		})
	}
	require.NoError(t, validateWaitingCondition(nil))
}

func TestValidateStepDecision(t *testing.T) {
	validConditional := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
	)
	validConditional.NextSteps = []*dexpb.StepMovement{{StepType: "next"}}
	validConditional.CloseDecision.ConditionalChannelNames = []string{"channel"}

	conditionalWithoutNext := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
	)
	conditionalWithoutNext.CloseDecision.ConditionalChannelNames = []string{"channel"}

	conditionalWithoutChannels := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
	)
	conditionalWithoutChannels.NextSteps = []*dexpb.StepMovement{{StepType: "next"}}

	conditionalWithDuplicateChannels := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
	)
	conditionalWithDuplicateChannels.NextSteps = []*dexpb.StepMovement{{StepType: "next"}}
	conditionalWithDuplicateChannels.CloseDecision.ConditionalChannelNames = []string{"channel", "channel"}

	conditionalWithEmptyChannel := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
	)
	conditionalWithEmptyChannel.NextSteps = []*dexpb.StepMovement{{StepType: "next"}}
	conditionalWithEmptyChannel.CloseDecision.ConditionalChannelNames = []string{""}

	gracefulWithNext := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
	)
	gracefulWithNext.NextSteps = []*dexpb.StepMovement{{StepType: "next"}}

	gracefulWithChannels := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
	)
	gracefulWithChannels.CloseDecision.ConditionalChannelNames = []string{"channel"}

	forceFailWithString := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
	)
	forceFailWithString.CloseDecision.CloseInput = &dexpb.Value{
		Kind: &dexpb.Value_StringValue{StringValue: "detail"},
	}

	forceFailWithInt := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
	)
	forceFailWithInt.CloseDecision.CloseInput = &dexpb.Value{
		Kind: &dexpb.Value_IntValue{IntValue: 1},
	}

	deadEndWithInput := decisionWithClose(
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
	)
	deadEndWithInput.CloseDecision.CloseInput = &dexpb.Value{
		Kind: &dexpb.Value_StringValue{StringValue: "input"},
	}

	testCases := []struct {
		name          string
		decision      *dexpb.StepDecision
		errorContains string
	}{
		{"nil", nil, "step decision is nil"},
		{"empty", &dexpb.StepDecision{}, "empty step decision"},
		{"nil_next_step", &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{nil},
		}, "next step at index 0 is invalid"},
		{"empty_next_step_type", &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{{}},
		}, "next step at index 0 is invalid"},
		{"next_step", &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{{StepType: "next"}},
		}, ""},
		{"conditional", validConditional, ""},
		{"conditional_without_next", conditionalWithoutNext, "requires at least one next step"},
		{"conditional_without_channels", conditionalWithoutChannels, "requires at least one channel"},
		{"conditional_with_duplicate_channels", conditionalWithDuplicateChannels, "duplicate conditional close channel"},
		{"conditional_with_empty_channel", conditionalWithEmptyChannel, "conditional close channel name is empty"},
		{"graceful", decisionWithClose(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		), ""},
		{"graceful_with_next", gracefulWithNext, "cannot be combined with next steps"},
		{"graceful_with_channels", gracefulWithChannels, "require a conditional close decision"},
		{"force_complete", decisionWithClose(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
		), ""},
		{"force_fail", decisionWithClose(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
		), ""},
		{"force_fail_with_string", forceFailWithString, ""},
		{"force_fail_with_int", forceFailWithInt, "must be a string"},
		{"dead_end", decisionWithClose(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
		), ""},
		{"dead_end_with_input", deadEndWithInput, "cannot have close input"},
		{"unspecified", decisionWithClose(
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_UNSPECIFIED,
		), "close decision type is unspecified"},
		{"unknown", decisionWithClose(
			dexpb.CloseDecisionType(100),
		), "close decision type is unspecified"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateStepDecision(testCase.decision)
			if testCase.errorContains == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, testCase.errorContains)
		})
	}
}

func decisionWithClose(closeType dexpb.CloseDecisionType) *dexpb.StepDecision {
	return &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: closeType,
		},
	}
}

func TestValidateTransientStepMovement(t *testing.T) {
	valid := func() *dexpb.StepMovement {
		return &dexpb.StepMovement{
			StepType:    "transient",
			StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		}
	}
	testCases := []struct {
		name          string
		movement      *dexpb.StepMovement
		errorContains string
	}{
		{"empty_step_type", &dexpb.StepMovement{}, "step type is empty"},
		{"does_not_skip_wait_for", &dexpb.StepMovement{StepType: "transient"}, "must skip WaitFor"},
		{
			"wait_for_proceed",
			&dexpb.StepMovement{
				StepType: "transient",
				StepOptions: &dexpb.StepOptions{
					SkipWaitFor:          true,
					WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
				},
			},
			"cannot proceed on WaitFor failure",
		},
		{
			"execute_proceed",
			&dexpb.StepMovement{
				StepType: "transient",
				StepOptions: &dexpb.StepOptions{
					SkipWaitFor:          true,
					ExecuteFailurePolicy: dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
				},
			},
			"cannot proceed on Execute failure",
		},
		{
			"execute_failure_step",
			&dexpb.StepMovement{
				StepType: "transient",
				StepOptions: &dexpb.StepOptions{
					SkipWaitFor:                   true,
					ExecuteFailureProceedStepType: "fallback",
				},
			},
			"cannot configure an Execute failure step",
		},
		{
			"execute_failure_step_options",
			&dexpb.StepMovement{
				StepType: "transient",
				StepOptions: &dexpb.StepOptions{
					SkipWaitFor:                      true,
					ExecuteFailureProceedStepOptions: &dexpb.StepOptions{},
				},
			},
			"cannot configure Execute failure step options",
		},
	}

	require.NoError(t, validateTransientStepMovement(nil))
	require.NoError(t, validateTransientStepMovement(valid()))
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateTransientStepMovement(testCase.movement)
			require.ErrorContains(t, err, testCase.errorContains)
		})
	}
}

func TestValidateTransientDeadEndDecision(t *testing.T) {
	valid := decisionWithClose(dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END)
	testCases := []struct {
		name     string
		decision *dexpb.StepDecision
	}{
		{"nil", nil},
		{"empty", &dexpb.StepDecision{}},
		{
			"non_dead_end",
			&dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{StepType: "next"}}},
		},
		{
			"multiple",
			&dexpb.StepDecision{
				NextSteps:     []*dexpb.StepMovement{{StepType: "next"}},
				CloseDecision: valid.GetCloseDecision(),
			},
		},
		{
			"conditional",
			decisionWithClose(
				dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
			),
		},
	}

	require.NoError(t, validateTransientDeadEndDecision(valid))
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Error(t, validateTransientDeadEndDecision(testCase.decision))
		})
	}
}

func TestValidateExecuteResponseRejectsTransientBeforeProcessingWrites(t *testing.T) {
	response := &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{{StepType: "not-dead-end"}},
		},
		UpsertAttributes: []*dexpb.AttributeWrite{
			{Key: "attribute", Value: stringValue("value")},
		},
		PublishToChannel: []*dexpb.ChannelMessage{
			{ChannelName: "channel", Value: stringValue("value")},
		},
	}

	require.ErrorContains(
		t,
		validateExecuteResponse(response, true),
		"requires a DeadEnd close decision",
	)
}

func createConditions() ([]*dexpb.TimerCondition, []*dexpb.ChannelCondition) {
	timers := []*dexpb.TimerCondition{
		{ConditionId: "timer-cmd1", DurationSeconds: 86400 * 365},
	}
	channels := []*dexpb.ChannelCondition{
		{ConditionId: "signal-cmd1", ChannelName: "test-signal-name1"},
		{ConditionId: "internal-cmd1", ChannelName: "test-internal-name1"},
	}
	return timers, channels
}

func newValidWaitingCondition() *dexpb.WaitingCondition {
	return waitingConditionWithChannel(newChannelCondition("condition", "channel", nil, nil))
}

func waitingConditionWithTimer(timerCondition *dexpb.TimerCondition) *dexpb.WaitingCondition {
	return &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		TimerConditions:      []*dexpb.TimerCondition{timerCondition},
	}
}

func TestValidateWaitingConditionAllowsEmptyIDsForAllAndAny(t *testing.T) {
	waitingConditionTypes := []dexpb.WaitingConditionType{
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
	}
	for _, waitingConditionType := range waitingConditionTypes {
		waitingCondition := &dexpb.WaitingCondition{
			WaitingConditionType: waitingConditionType,
			TimerConditions: []*dexpb.TimerCondition{
				{DurationSeconds: 1},
				{DurationSeconds: 2},
			},
			ChannelConditions: []*dexpb.ChannelCondition{
				newChannelCondition("", "first", nil, nil),
				newChannelCondition("", "second", nil, nil),
			},
		}
		require.NoError(t, validateWaitingCondition(waitingCondition))
	}
}

func waitingConditionWithChannel(channelCondition *dexpb.ChannelCondition) *dexpb.WaitingCondition {
	return &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		ChannelConditions:    []*dexpb.ChannelCondition{channelCondition},
	}
}

func newChannelCondition(
	conditionId string,
	channelName string,
	atLeast *int32,
	atMost *int32,
) *dexpb.ChannelCondition {
	return &dexpb.ChannelCondition{
		ConditionId: conditionId,
		ChannelName: channelName,
		AtLeast:     atLeast,
		AtMost:      atMost,
	}
}

func TestComposeActivityErrorUsesInternalForNonGRPCError(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	inputError := errors.New("dial failed")
	activityError := errors.New("activity error")
	var errorResponse *dexpb.ErrorResponse
	provider.EXPECT().
		NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ dexpb.FlowErrorType,
			response *dexpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeActivityError(provider, inputError), activityError)
	require.Equal(t, "dial failed", errorResponse.GetDetail())
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errorResponse.GetSubStatus(),
	)
}

func TestComposeActivityErrorPreservesWorkerDetails(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	grpcStatus, err := status.New(codes.Internal, "worker failure").WithDetails(
		&dexpb.WorkerErrorResponse{
			Detail:    "worker detail",
			ErrorType: "worker type",
		},
	)
	require.NoError(t, err)

	activityError := errors.New("activity error")
	var errorResponse *dexpb.ErrorResponse
	provider.EXPECT().
		NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ dexpb.FlowErrorType,
			response *dexpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeActivityError(provider, grpcStatus.Err()), activityError)
	require.Equal(t, "worker failure", errorResponse.GetDetail())
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		errorResponse.GetSubStatus(),
	)
	require.Equal(t, int32(codes.Internal), errorResponse.GetOriginalWorkerErrorStatus())
	require.Equal(t, "worker detail", errorResponse.GetOriginalWorkerErrorDetail())
	require.Equal(t, "worker type", errorResponse.GetOriginalWorkerErrorType())
}

func TestComposeActivityErrorFallsBackWhenMessageEmpty(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	inputError := status.Error(codes.Canceled, "")
	activityError := errors.New("activity error")
	var errorResponse *dexpb.ErrorResponse
	provider.EXPECT().
		NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ dexpb.FlowErrorType,
			response *dexpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeActivityError(provider, inputError), activityError)
	require.Equal(t, inputError.Error(), errorResponse.GetDetail())
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		errorResponse.GetSubStatus(),
	)
	require.Equal(t, int32(codes.Canceled), errorResponse.GetOriginalWorkerErrorStatus())
}

func TestComposeInternalActivityErrorKeepsGRPCErrorInternal(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	inputError := status.Error(codes.Unavailable, "internal service unavailable")
	activityError := errors.New("activity error")
	var errorResponse *dexpb.ErrorResponse
	provider.EXPECT().
		NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ dexpb.FlowErrorType,
			response *dexpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeInternalActivityError(provider, inputError), activityError)
	require.Contains(t, errorResponse.GetDetail(), "internal service unavailable")
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errorResponse.GetSubStatus(),
	)
}

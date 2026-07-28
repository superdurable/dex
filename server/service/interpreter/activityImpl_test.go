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

package interpreter

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/ptr"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInvalidAnyConditionCombination(t *testing.T) {
	timers, channels := createConditions()
	waiting := &iwfpb.WaitingCondition{
		WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		TimerConditions:      timers,
		ChannelConditions:    channels,
		ConditionCombinations: []*iwfpb.ConditionCombination{
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
	waiting := &iwfpb.WaitingCondition{
		WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		TimerConditions:      timers,
		ChannelConditions:    channels,
		ConditionCombinations: []*iwfpb.ConditionCombination{
			{ConditionIds: []string{"timer-cmd1", "signal-cmd1"}},
			{ConditionIds: []string{"timer-cmd1", "internal-cmd1"}},
		},
	}

	require.NoError(t, validateWaitingCondition(waiting))
}

func TestValidateWaitingConditionChannelBounds(t *testing.T) {
	waiting := &iwfpb.WaitingCondition{
		WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		ChannelConditions: []*iwfpb.ChannelCondition{
			{ConditionId: "c1", ChannelName: "ch", AtLeast: ptr.Any(int32(2)), AtMost: ptr.Any(int32(1))},
		},
	}
	require.Error(t, validateWaitingCondition(waiting))
}

func TestValidateWaitingConditionRejections(t *testing.T) {
	duplicateId := newValidWaitingCondition()
	duplicateId.TimerConditions = []*iwfpb.TimerCondition{{ConditionId: "condition"}}

	negativeTimer := newValidWaitingCondition()
	negativeTimer.TimerConditions = []*iwfpb.TimerCondition{{
		ConditionId:     "timer",
		DurationSeconds: -1,
	}}

	absoluteTimer := newValidWaitingCondition()
	absoluteTimer.TimerConditions = []*iwfpb.TimerCondition{{
		ConditionId:                "timer",
		FiringUnixTimestampSeconds: 10,
	}}

	combinationsOnAll := newValidWaitingCondition()
	combinationsOnAll.WaitingConditionType =
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED
	combinationsOnAll.ConditionCombinations = []*iwfpb.ConditionCombination{{
		ConditionIds: []string{"condition"},
	}}

	missingCombination := newValidWaitingCondition()
	missingCombination.WaitingConditionType =
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED

	unknownType := newValidWaitingCondition()
	unknownType.WaitingConditionType =
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_UNSPECIFIED

	emptyConditionId := waitingConditionWithChannel(newChannelCondition("", "channel", nil, nil))
	emptyConditionId.WaitingConditionType =
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED
	emptyConditionId.ConditionCombinations = []*iwfpb.ConditionCombination{{
		ConditionIds: []string{"condition"},
	}}

	testCases := []struct {
		name             string
		waitingCondition *iwfpb.WaitingCondition
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
		{"negative_timer_duration", negativeTimer, "negative duration_seconds"},
		{"worker_sets_absolute_timer", absoluteTimer, "server-owned firing_unix_timestamp_seconds"},
		{"combinations_on_all", combinationsOnAll, "only valid for ANY_COMBINATION_COMPLETED"},
		{"any_combination_requires_combination", missingCombination, "requires at least one condition_combination"},
		{
			"empty_combination",
			&iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*iwfpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*iwfpb.ConditionCombination{
					{},
				},
			},
			"condition_combination at index 0 is empty",
		},
		{
			"combination_references_undeclared_id",
			&iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*iwfpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*iwfpb.ConditionCombination{
					{ConditionIds: []string{"undeclared"}},
				},
			},
			`references undeclared condition_id "undeclared"`,
		},
		{
			"combination_duplicate_id",
			&iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				ChannelConditions:    []*iwfpb.ChannelCondition{newChannelCondition("condition", "channel", nil, nil)},
				ConditionCombinations: []*iwfpb.ConditionCombination{
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

func TestValidateStepDecisionEmpty(t *testing.T) {
	require.Error(t, validateStepDecision(nil))
	require.Error(t, validateStepDecision(&iwfpb.StepDecision{}))
	require.NoError(t, validateStepDecision(&iwfpb.StepDecision{
		NextSteps: []*iwfpb.StepMovement{{StepType: "s"}},
	}))
}

func createConditions() ([]*iwfpb.TimerCondition, []*iwfpb.ChannelCondition) {
	timers := []*iwfpb.TimerCondition{
		{ConditionId: "timer-cmd1", DurationSeconds: 86400 * 365},
	}
	channels := []*iwfpb.ChannelCondition{
		{ConditionId: "signal-cmd1", ChannelName: "test-signal-name1"},
		{ConditionId: "internal-cmd1", ChannelName: "test-internal-name1"},
	}
	return timers, channels
}

func newValidWaitingCondition() *iwfpb.WaitingCondition {
	return waitingConditionWithChannel(newChannelCondition("condition", "channel", nil, nil))
}

func waitingConditionWithTimer(timerCondition *iwfpb.TimerCondition) *iwfpb.WaitingCondition {
	return &iwfpb.WaitingCondition{
		WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		TimerConditions:      []*iwfpb.TimerCondition{timerCondition},
	}
}

func TestValidateWaitingConditionAllowsEmptyIDsForAllAndAny(t *testing.T) {
	waitingConditionTypes := []iwfpb.WaitingConditionType{
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
	}
	for _, waitingConditionType := range waitingConditionTypes {
		waitingCondition := &iwfpb.WaitingCondition{
			WaitingConditionType: waitingConditionType,
			TimerConditions: []*iwfpb.TimerCondition{
				{DurationSeconds: 1},
				{DurationSeconds: 2},
			},
			ChannelConditions: []*iwfpb.ChannelCondition{
				newChannelCondition("", "first", nil, nil),
				newChannelCondition("", "second", nil, nil),
			},
		}
		require.NoError(t, validateWaitingCondition(waitingCondition))
	}
}

func waitingConditionWithChannel(channelCondition *iwfpb.ChannelCondition) *iwfpb.WaitingCondition {
	return &iwfpb.WaitingCondition{
		WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		ChannelConditions:    []*iwfpb.ChannelCondition{channelCondition},
	}
}

func newChannelCondition(
	conditionId string,
	channelName string,
	atLeast *int32,
	atMost *int32,
) *iwfpb.ChannelCondition {
	return &iwfpb.ChannelCondition{
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
	var errorResponse *iwfpb.ErrorResponse
	provider.EXPECT().
		NewActivityError(
			iwfpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ iwfpb.FlowErrorType,
			response *iwfpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeActivityError(provider, inputError), activityError)
	require.Equal(t, "dial failed", errorResponse.GetDetail())
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errorResponse.GetSubStatus(),
	)
}

func TestComposeActivityErrorPreservesWorkerDetails(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	grpcStatus, err := status.New(codes.Internal, "worker failure").WithDetails(
		&iwfpb.WorkerErrorResponse{
			Detail:    "worker detail",
			ErrorType: "worker type",
		},
	)
	require.NoError(t, err)

	activityError := errors.New("activity error")
	var errorResponse *iwfpb.ErrorResponse
	provider.EXPECT().
		NewActivityError(
			iwfpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ iwfpb.FlowErrorType,
			response *iwfpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeActivityError(provider, grpcStatus.Err()), activityError)
	require.Equal(t, "worker failure", errorResponse.GetDetail())
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		errorResponse.GetSubStatus(),
	)
	require.Equal(t, int32(codes.Internal), errorResponse.GetOriginalWorkerErrorStatus())
	require.Equal(t, "worker detail", errorResponse.GetOriginalWorkerErrorDetail())
	require.Equal(t, "worker type", errorResponse.GetOriginalWorkerErrorType())
}

func TestComposeInternalActivityErrorKeepsGRPCErrorInternal(t *testing.T) {
	provider := interfaces.NewMockActivityProvider(gomock.NewController(t))
	inputError := status.Error(codes.Unavailable, "internal service unavailable")
	activityError := errors.New("activity error")
	var errorResponse *iwfpb.ErrorResponse
	provider.EXPECT().
		NewActivityError(
			iwfpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			gomock.Any(),
		).
		DoAndReturn(func(
			_ iwfpb.FlowErrorType,
			response *iwfpb.ErrorResponse,
		) error {
			errorResponse = response
			return activityError
		})

	require.ErrorIs(t, composeInternalActivityError(provider, inputError), activityError)
	require.Contains(t, errorResponse.GetDetail(), "internal service unavailable")
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errorResponse.GetSubStatus(),
	)
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
)

func TestClientCloneForkFlowMapsDefaults(t *testing.T) {
	client, service := newHardDeleteTestClient(t, cloneForkFlowProtocolVersion)
	result, err := client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, CloneForkFlowResult{
		RunID:            "clone-run-1",
		SourceRunID:      "source-run-1",
		SourceRunAttempt: 2,
		SourceEventID:    17,
	}, result)

	request := service.cloneForkRequest
	require.Equal(t, "source-1", request.GetSourceFlowId())
	require.Equal(t, "clone-1", request.GetFlowId())
	require.Empty(t, request.GetSourceRunId())
	_, err = uuid.Parse(request.GetRequestId())
	require.NoError(t, err, "the SDK generates a UUID request ID")
	require.Equal(t, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT, request.GetPointType())
	require.Equal(t, dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_UNSPECIFIED, request.GetStepMethod())
	require.Equal(t, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED, request.GetSubFlowResetPolicy())
	require.Equal(t, dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED, request.GetIdReusePolicy())
	require.Nil(t, request.GetFlowConfigOverride(), "no override keeps the source run's FlowConfig")
	require.Nil(t, request.GetFlowAlreadyStartedOptions())
	require.Empty(t, request.GetAttributes())
	require.Zero(t, request.GetFlowTimeoutSeconds())
	require.Zero(t, request.GetRunAttempt())
	require.Zero(t, request.GetEventId())

	_, err = client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-2", CloneForkFlowOptions{})
	require.NoError(t, err)
	require.NotEqual(t, request.GetRequestId(), service.cloneForkRequest.GetRequestId(), "each call sends a new request ID")
}

func TestClientCloneForkFlowMapsEveryOption(t *testing.T) {
	client, service := newHardDeleteTestClient(t, cloneForkFlowProtocolVersion)
	requestID := "branch-request"
	historyWindowEvents := int32(64)
	initialSecret, err := InitialAttribute(hardDeleteTestSecret, "copied")
	require.NoError(t, err)
	_, err = client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{
		SourceRunID:        "source-run-9",
		SourceRunAttempt:   3,
		Type:               TimeTravelByEventID,
		EventID:            42,
		IDReusePolicy:      IDReuseTerminateIfRunning,
		AlreadyStarted:     &AlreadyStartedOptions{IgnoreError: true},
		RequestID:          &requestID,
		ConfigOverride:     &FlowConfig{HistoryWindowEvents: &historyWindowEvents},
		TaskQueue:          "tenant-b",
		Attributes:         []InitialAttributeDef{initialSecret},
		Timeout:            ptr.Any(90 * time.Second),
		Reason:             "branch the session",
		SubFlowResetPolicy: SubFlowResetAttachElseFail,
	})
	require.NoError(t, err)

	request := service.cloneForkRequest
	require.Equal(t, "source-run-9", request.GetSourceRunId())
	require.Equal(t, int32(3), request.GetRunAttempt())
	require.Equal(t, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID, request.GetPointType())
	require.Equal(t, int64(42), request.GetEventId())
	require.Equal(t, dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING, request.GetIdReusePolicy())
	require.True(t, request.GetFlowAlreadyStartedOptions().GetIgnoreAlreadyStartedError())
	require.Equal(t, requestID, request.GetRequestId())
	require.Equal(t, historyWindowEvents, request.GetFlowConfigOverride().GetHistoryWindowEvents())
	require.Nil(t, request.GetFlowConfigOverride().StepDurability, "unset fields keep the source run's values")
	require.Equal(t, "tenant-b", request.GetFlowConfigOverride().GetWorkerTarget().GetTaskQueue())
	require.True(t, request.GetFlowConfigOverride().GetWorkerTarget().GetIsPullMode())
	require.Len(t, request.GetAttributes(), 1)
	require.Equal(t, "hard-delete-test-secret", request.GetAttributes()[0].GetKey())
	require.True(t, request.GetAttributes()[0].GetIsHardDeleteOnNull())
	require.Equal(t, int32(90), request.GetFlowTimeoutSeconds())
	require.Equal(t, "branch the session", request.GetReason())
	require.Equal(t, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL, request.GetSubFlowResetPolicy())
}

func TestCloneForkFlowOptionsMapPointTypes(t *testing.T) {
	historyEventTime := time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name          string
		options       CloneForkFlowOptions
		wantPointType dexpb.FlowCloneForkPointType
		wantMethod    dexpb.FlowResetStepMethod
	}{
		{name: "zero is current", options: CloneForkFlowOptions{}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT},
		{name: "current", options: CloneForkFlowOptions{Type: TimeTravelToCurrent}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT},
		{name: "beginning", options: CloneForkFlowOptions{Type: TimeTravelToBeginning, SourceRunAttempt: 1}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_BEGINNING},
		{name: "history event time", options: CloneForkFlowOptions{Type: TimeTravelByHistoryEventTime, HistoryEventTime: historyEventTime}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME},
		{name: "Step type", options: CloneForkFlowOptions{Type: TimeTravelByStepType, StepType: "ChargeOrder"}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE},
		{
			name:          "Step execution ID",
			options:       CloneForkFlowOptions{Type: TimeTravelByStepExecutionID, StepExecutionID: "ChargeOrder-2", StepMethod: TimeTravelStepWaitFor},
			wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID,
			wantMethod:    dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR,
		},
		{name: "event ID", options: CloneForkFlowOptions{Type: TimeTravelByEventID, EventID: 7}, wantPointType: dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := mapCloneForkFlowOptions(testCase.options)
			require.NoError(t, err)
			require.Equal(t, testCase.wantPointType, request.GetPointType())
			require.Equal(t, testCase.wantMethod, request.GetStepMethod())
			require.Equal(t, testCase.options.StepType, request.GetStepType())
			require.Equal(t, testCase.options.StepExecutionID, request.GetStepExecutionId())
			require.Equal(t, testCase.options.EventID, request.GetEventId())
			require.Equal(t, testCase.options.SourceRunAttempt, request.GetRunAttempt())
			if testCase.options.Type == TimeTravelByHistoryEventTime {
				require.Equal(t, "2026-11-15T10:30:00Z", request.GetHistoryEventTime())
			} else {
				require.Empty(t, request.GetHistoryEventTime())
			}
		})
	}
}

func TestCloneForkFlowOptionsRejectInvalidPoints(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		options   CloneForkFlowOptions
		wantError string
	}{
		{name: "current with event ID", options: CloneForkFlowOptions{EventID: 3}, wantError: "current point cannot include another selector"},
		{name: "current with run attempt", options: CloneForkFlowOptions{SourceRunAttempt: 1}, wantError: "accepts only source run attempt zero"},
		{name: "negative run attempt", options: CloneForkFlowOptions{Type: TimeTravelToBeginning, SourceRunAttempt: -1}, wantError: "must not be negative"},
		{name: "event ID zero", options: CloneForkFlowOptions{Type: TimeTravelByEventID}, wantError: "event ID must be positive"},
		{name: "event ID negative", options: CloneForkFlowOptions{Type: TimeTravelByEventID, EventID: -4}, wantError: "event ID must be positive"},
		{name: "event ID with Step type", options: CloneForkFlowOptions{Type: TimeTravelByEventID, EventID: 4, StepType: "A"}, wantError: "cannot be combined"},
		{name: "Step method outside Step execution ID", options: CloneForkFlowOptions{Type: TimeTravelByStepType, StepType: "A", StepMethod: TimeTravelStepExecute}, wantError: "cannot be combined"},
		{name: "Step execution ID without method", options: CloneForkFlowOptions{Type: TimeTravelByStepExecutionID, StepExecutionID: "A-1"}, wantError: "WaitFor or Execute"},
		{name: "unknown point type", options: CloneForkFlowOptions{Type: TimeTravelType(99)}, wantError: "unsupported clone fork type"},
		{name: "negative timeout", options: CloneForkFlowOptions{Timeout: ptr.Any(-time.Second)}, wantError: "must not be negative"},
		{name: "unknown SubFlow reset policy", options: CloneForkFlowOptions{SubFlowResetPolicy: SubFlowResetPolicy(99)}, wantError: "unsupported SubFlow reset policy"},
		{name: "reserved task queue", options: CloneForkFlowOptions{TaskQueue: "dex.other"}, wantError: "reserved"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := mapCloneForkFlowOptions(testCase.options)
			require.ErrorContains(t, err, testCase.wantError)
		})
	}
}

func TestClientCloneForkFlowValidatesFlowIDs(t *testing.T) {
	client, service := newHardDeleteTestClient(t, cloneForkFlowProtocolVersion)
	_, err := client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "", "clone-1", CloneForkFlowOptions{})
	require.ErrorContains(t, err, "source flow ID must not be empty")
	_, err = client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "same", "same", CloneForkFlowOptions{})
	require.ErrorContains(t, err, "must differ from the source flow ID")
	emptyRequestID := ""
	_, err = client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{RequestID: &emptyRequestID})
	require.ErrorContains(t, err, "request ID must not be empty")
	require.Nil(t, service.cloneForkRequest)
}

func TestClientCloneForkFlowReportsUnsupportedServer(t *testing.T) {
	client, service := newHardDeleteTestClient(t, taskQueueProtocolVersion)
	_, err := client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{})
	var unsupported *CloneForkFlowUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	require.Equal(t, taskQueueProtocolVersion, unsupported.NegotiatedServerProtocol)
	require.Equal(t, cloneForkFlowProtocolVersion, unsupported.RequiredServerProtocolVersion)
	require.ErrorContains(t, err, "server-zd 0.1.33 or later")
	require.Nil(t, service.cloneForkRequest)
}

func TestClientCloneForkFlowTranslatesErrors(t *testing.T) {
	client, service := newHardDeleteTestClient(t, cloneForkFlowProtocolVersion)
	service.cloneForkFailure = clientTestServiceError(
		codes.NotFound,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		"source Flow not found",
		nil,
	)
	_, err := client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{})
	var notFound *FlowNotFoundError
	require.ErrorAs(t, err, &notFound)
	require.Equal(t, "source-1", notFound.FlowID)

	service.cloneForkFailure = clientTestServiceError(
		codes.AlreadyExists,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
		"Flow already started",
		nil,
	)
	_, err = client.CloneForkFlow(context.Background(), hardDeleteTestFlow{}, "source-1", "clone-1", CloneForkFlowOptions{})
	var alreadyStarted *FlowAlreadyStartedError
	require.ErrorAs(t, err, &alreadyStarted)
	require.Equal(t, "clone-1", alreadyStarted.FlowID)
}

func TestClientTimeTravelSendsEventIDAndSubFlowResetPolicy(t *testing.T) {
	client, service := newHardDeleteTestClient(t, cloneForkFlowProtocolVersion)
	_, err := client.TimeTravel(context.Background(), "flow-1", TimeTravelOptions{
		Type:               TimeTravelByEventID,
		EventID:            12,
		SubFlowResetPolicy: SubFlowResetAlwaysReject,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowResetType_FLOW_RESET_TYPE_EVENT_ID, service.resetRequest.GetResetType())
	require.Equal(t, int64(12), service.resetRequest.GetEventId())
	require.Equal(t, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_REJECT, service.resetRequest.GetSubFlowResetPolicy())
	require.Equal(t, dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_UNSPECIFIED, service.resetRequest.GetStepMethod())

	_, err = client.TimeTravel(context.Background(), "flow-1", TimeTravelOptions{Type: TimeTravelToBeginning})
	require.NoError(t, err)
	require.Equal(t, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED, service.resetRequest.GetSubFlowResetPolicy())
	require.Zero(t, service.resetRequest.GetEventId())
}

func TestTimeTravelOptionsRejectInvalidEventIDAndCurrent(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		options   TimeTravelOptions
		wantError string
	}{
		{name: "current point", options: TimeTravelOptions{Type: TimeTravelToCurrent}, wantError: "valid only for CloneForkFlow"},
		{name: "event ID zero", options: TimeTravelOptions{Type: TimeTravelByEventID}, wantError: "event ID must be positive"},
		{name: "event ID with another type", options: TimeTravelOptions{Type: TimeTravelToBeginning, EventID: 5}, wantError: "requires TimeTravelByEventID"},
		{name: "event ID with history event time", options: TimeTravelOptions{Type: TimeTravelByEventID, EventID: 5, HistoryEventTime: time.Now()}, wantError: "cannot be combined"},
		{name: "event ID with Step type", options: TimeTravelOptions{Type: TimeTravelByEventID, EventID: 5, StepType: "A"}, wantError: "cannot be combined"},
		{name: "event ID with Step execution ID", options: TimeTravelOptions{Type: TimeTravelByEventID, EventID: 5, StepExecutionID: "A-1"}, wantError: "cannot be combined"},
		{name: "event ID with Step method", options: TimeTravelOptions{Type: TimeTravelByEventID, EventID: 5, StepMethod: TimeTravelStepExecute}, wantError: "cannot be combined"},
		{name: "earlier beginning with Step type", options: TimeTravelOptions{Type: TimeTravelToBeginning, StepType: "A"}, wantError: "cannot include another selector"},
		{name: "unknown SubFlow reset policy", options: TimeTravelOptions{Type: TimeTravelToBeginning, SubFlowResetPolicy: SubFlowResetPolicy(42)}, wantError: "unsupported SubFlow reset policy"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := mapTimeTravelOptions(testCase.options)
			require.ErrorContains(t, err, testCase.wantError)
		})
	}
}

func TestSubFlowResetPolicyMapsEveryValue(t *testing.T) {
	for policy, want := range map[SubFlowResetPolicy]dexpb.SubFlowResetPolicy{
		SubFlowResetDefault:           dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED,
		SubFlowResetAlwaysRestart:     dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_RESTART,
		SubFlowResetAlwaysFail:        dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_FAIL,
		SubFlowResetAlwaysReject:      dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_REJECT,
		SubFlowResetAttachElseRestart: dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART,
		SubFlowResetAttachElseFail:    dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
		SubFlowResetAttachElseReject:  dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT,
	} {
		mapped, err := mapSubFlowResetPolicy(policy)
		require.NoError(t, err)
		require.Equal(t, want, mapped)
	}
}

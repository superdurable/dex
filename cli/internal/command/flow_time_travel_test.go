// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func TestTimeTravelRequestMapsEventIDPoint(t *testing.T) {
	request, err := timeTravelRequest("flow-1", "run-1", "event-id", "42", "", "retry")
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowResetType_FLOW_RESET_TYPE_EVENT_ID, request.GetResetType())
	require.Equal(t, int64(42), request.GetEventId())

	for _, target := range []string{"", "0", "-3", "abc"} {
		_, err := timeTravelRequest("flow-1", "run-1", "event-id", target, "", "")
		require.Error(t, err, "target %q", target)
	}
	_, err = timeTravelRequest("flow-1", "run-1", "current", "", "", "")
	require.Error(t, err)
}

func TestParseSubFlowResetPolicyMapsEveryValue(t *testing.T) {
	expected := map[string]dexpb.SubFlowResetPolicy{
		"":                    dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED,
		"attach-else-restart": dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART,
		"attach-else-fail":    dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
		"attach-else-reject":  dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT,
		"always-restart":      dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_RESTART,
		"always-fail":         dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_FAIL,
		"always-reject":       dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_REJECT,
	}
	for name, policy := range expected {
		parsed, err := parseSubFlowResetPolicy(name)
		require.NoError(t, err, name)
		require.Equal(t, policy, parsed, name)
	}
	_, err := parseSubFlowResetPolicy("attach")
	require.Error(t, err)
}

func TestCloneForkFlowRequestDefaultsToCurrentPoint(t *testing.T) {
	request, err := cloneForkFlowRequest(bytes.NewReader(nil), cloneForkFlowFlags{
		sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current",
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT, request.GetPointType())
	require.Equal(t, "source-1", request.GetSourceFlowId())
	require.Equal(t, "fork-1", request.GetFlowId())
	require.NotEmpty(t, request.GetRequestId())
	require.Nil(t, request.GetFlowConfigOverride())
	require.Nil(t, request.GetFlowAlreadyStartedOptions())
}

func TestCloneForkFlowRequestMapsEveryFlag(t *testing.T) {
	request, err := cloneForkFlowRequest(bytes.NewReader(nil), cloneForkFlowFlags{
		sourceFlowID: "source-1", sourceRunID: "run-1", flowID: "fork-1",
		pointTypeName: "step-execution-id", target: "ChargeOrder-2", stepMethodName: "wait-for",
		runAttempt: 2, attributesSource: `[{"key":"Status","value":"forked"}]`,
		configSource: `{"stepDurability":"async"}`, taskQueue: "session-2", flowTimeout: "90s",
		idReusePolicy: "terminate-running", shouldIgnoreAlreadyStarted: true,
		subFlowResetPolicy: "attach-else-fail", reason: "branch", requestID: "request-7",
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID, request.GetPointType())
	require.Equal(t, "ChargeOrder-2", request.GetStepExecutionId())
	require.Equal(t, dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR, request.GetStepMethod())
	require.Equal(t, "run-1", request.GetSourceRunId())
	require.Equal(t, int32(2), request.GetRunAttempt())
	require.Len(t, request.GetAttributes(), 1)
	require.Equal(t, "Status", request.GetAttributes()[0].GetKey())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, request.GetFlowConfigOverride().GetStepDurability())
	require.True(t, request.GetFlowConfigOverride().GetWorkerTarget().GetIsPullMode())
	require.Equal(t, "session-2", request.GetFlowConfigOverride().GetWorkerTarget().GetTaskQueue())
	require.Equal(t, int32(90), request.GetFlowTimeoutSeconds())
	require.Equal(t, dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING, request.GetIdReusePolicy())
	require.True(t, request.GetFlowAlreadyStartedOptions().GetIgnoreAlreadyStartedError())
	require.Equal(t, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL, request.GetSubFlowResetPolicy())
	require.Equal(t, "branch", request.GetReason())
	require.Equal(t, "request-7", request.GetRequestId())
}

func TestCloneForkFlowRequestMapsEventIDPoint(t *testing.T) {
	request, err := cloneForkFlowRequest(bytes.NewReader(nil), cloneForkFlowFlags{
		sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "event-id", target: "17",
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID, request.GetPointType())
	require.Equal(t, int64(17), request.GetEventId())
}

func TestCloneForkFlowRequestRejectsInvalidFlags(t *testing.T) {
	invalidFlags := map[string]cloneForkFlowFlags{
		"missing flow-id":       {sourceFlowID: "source-1", pointTypeName: "current"},
		"same flow-id":          {sourceFlowID: "source-1", flowID: "source-1", pointTypeName: "current"},
		"unknown type":          {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "latest"},
		"missing target":        {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "step-type"},
		"bad event ID":          {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "event-id", target: "0"},
		"step-method elsewhere": {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "beginning", stepMethodName: "execute"},
		"current run attempt":   {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", runAttempt: 1},
		"current target":        {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", target: "42"},
		"negative run attempt":  {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "beginning", runAttempt: -1},
		"negative timeout":      {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", flowTimeout: "-1s"},
		"worker target config":  {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", configSource: `{"workerTarget":{"taskQueue":"q"}}`},
		"unknown policy":        {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", subFlowResetPolicy: "restart"},
		"unknown reuse policy":  {sourceFlowID: "source-1", flowID: "fork-1", pointTypeName: "current", idReusePolicy: "allow"},
	}
	for name, values := range invalidFlags {
		_, err := cloneForkFlowRequest(bytes.NewReader(nil), values)
		require.Error(t, err, name)
	}
}

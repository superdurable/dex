// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestValidateStepOptionsHeartbeatTimeout(t *testing.T) {
	for _, timeoutSeconds := range []int32{0, 10, 60} {
		require.NoError(t, ValidateStepOptions(&dexpb.StepOptions{
			HeartbeatTimeoutSeconds: timeoutSeconds,
		}, 10*time.Second))
	}
	for _, timeoutSeconds := range []int32{-1, 1, 9} {
		require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
			HeartbeatTimeoutSeconds: timeoutSeconds,
		}, 10*time.Second), "heartbeat timeout")
	}
	require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
		ExecuteFailureProceedStepType: "recovery",
		ExecuteFailureProceedStepOptions: &dexpb.StepOptions{
			HeartbeatTimeoutSeconds: 9,
		},
	}, 10*time.Second), "heartbeat timeout")
	require.NoError(t, ValidateStepOptions(&dexpb.StepOptions{
		HeartbeatTimeoutSeconds: 2,
	}, 2*time.Second))
}

func TestValidateStepOptionsStateSelections(t *testing.T) {
	require.NoError(t, ValidateStepOptions(&dexpb.StepOptions{
		WaitForLoadAttributeMapInstances: []string{"items/", "items/tenant-a"},
		WaitForLoadChannelNames:          []string{"commands"},
		WaitForLoadChannelMapInstances:   []string{"commands-by-tenant/"},
		ExecuteLoadAttributeMapInstances: []string{"items/tenant-b"},
		ExecuteLoadChannelNames:          []string{"events"},
		ExecuteLoadChannelMapInstances:   []string{"events-by-tenant/tenant-b"},
	}, 10*time.Second))
	require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
		WaitForLoadChannelNames: []string{"commands", "commands"},
	}, 10*time.Second), "duplicated")
	require.ErrorContains(t, ValidateStepOptions(&dexpb.StepOptions{
		ExecuteLoadAttributeMapInstances: []string{"items"},
	}, 10*time.Second), "must contain one '/'")
}

func TestFlowTimeoutHandlerOptionsValidationAndProjection(t *testing.T) {
	options := &dexpb.FlowTimeoutHandlerOptions{
		MethodTimeoutSeconds:      20,
		HeartbeatTimeoutSeconds:   10,
		RetryPolicy:               &dexpb.RetryPolicy{MaximumAttempts: 3},
		FailurePolicy:             dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
		FailureProceedStepType:    "recovery",
		DurabilityOverride:        dexpb.StepDurability_STEP_DURABILITY_ASYNC,
		LockAttributeKeys:         []string{"status"},
		LoadAttributeMapInstances: []string{"items/"},
		LoadChannelNames:          []string{"commands"},
		LoadChannelMapInstances:   []string{"commands-by-tenant/tenant-a"},
	}
	require.NoError(t, ValidateFlowTimeoutHandlerOptions(
		60,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		options,
		10*time.Second,
	))
	stepOptions := FlowTimeoutHandlerStepOptions(options)
	require.Equal(t, int32(20), stepOptions.ExecuteTimeoutSeconds)
	require.Equal(t, int32(3), stepOptions.ExecuteRetryPolicy.MaximumAttempts)
	require.Equal(t, "recovery", stepOptions.ExecuteFailureProceedStepType)
	require.Equal(t, []string{"items/"}, stepOptions.ExecuteLoadAttributeMapInstances)
	require.Equal(t, []string{"commands"}, stepOptions.ExecuteLoadChannelNames)
	require.Equal(t, []string{"commands-by-tenant/tenant-a"}, stepOptions.ExecuteLoadChannelMapInstances)

	require.ErrorContains(t, ValidateFlowTimeoutHandlerOptions(
		0,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		options,
		10*time.Second,
	), "positive timeout")
	require.ErrorContains(t, ValidateFlowTimeoutHandlerOptions(
		60,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		options,
		10*time.Second,
	), "Handler policy")
}

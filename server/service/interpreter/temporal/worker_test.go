// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestMetricsProviders(t *testing.T) {
	require.Equal(t, "OrderFlow", interpreterWorkflowFlowType(&dexpb.InterpreterWorkflowInput{
		FlowType: "OrderFlow",
	}))

	waitInput := &dexpb.InvokeWaitForMethodActivityInput{Request: &dexpb.InvokeWaitForMethodRequest{
		FlowType: "OrderFlow",
		StepType: "WaitForPayment",
	}}
	require.Equal(t, "OrderFlow", waitForMethodFlowType(waitInput))
	require.Equal(t, "WaitForPayment", waitForMethodStepType(waitInput))

	executeInput := &dexpb.InvokeExecuteMethodActivityInput{Request: &dexpb.InvokeExecuteMethodRequest{
		FlowType: "OrderFlow",
		StepType: "ChargeCard",
	}}
	require.Equal(t, "OrderFlow", executeMethodFlowType(executeInput))
	require.Equal(t, "ChargeCard", executeMethodStepType(executeInput))

	rpcInput := &dexpb.InvokeWorkerRPCActivityInput{
		RpcPrep: &dexpb.PrepareRpcQueryResponse{FlowType: "OrderFlow"},
		Request: &dexpb.InvokeRPCRequest{RpcName: "ReserveInventory"},
	}
	require.Equal(t, "OrderFlow", invokeWorkerRPCFlowType(rpcInput))
	require.Equal(t, "ReserveInventory", invokeWorkerRPCName(rpcInput))

	require.Equal(t, "Fulfillment", startSubFlowType(&dexpb.StartSubFlowActivityInput{
		Condition: &dexpb.SubFlowCondition{SubFlowType: "Fulfillment"},
	}))
}

func TestMetricsProvidersReturnEmptyForMissingProtoFields(t *testing.T) {
	require.Empty(t, waitForMethodFlowType(&dexpb.InvokeWaitForMethodActivityInput{}))
	require.Empty(t, waitForMethodStepType(&dexpb.InvokeWaitForMethodActivityInput{}))
	require.Empty(t, executeMethodFlowType(&dexpb.InvokeExecuteMethodActivityInput{}))
	require.Empty(t, executeMethodStepType(&dexpb.InvokeExecuteMethodActivityInput{}))
	require.Empty(t, invokeWorkerRPCFlowType(&dexpb.InvokeWorkerRPCActivityInput{}))
	require.Empty(t, invokeWorkerRPCName(&dexpb.InvokeWorkerRPCActivityInput{}))
	require.Empty(t, startSubFlowType(&dexpb.StartSubFlowActivityInput{}))
}

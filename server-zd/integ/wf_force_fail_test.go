// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/wf_force_fail"
)

func TestFlowForceFailTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowForceFail(t, nil)
		smallWaitForFastTest()
		doTestFlowForceFail(t, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestFlowForceFail(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := wf_force_fail.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-force-fail-test-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_force_fail.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType: wf_force_fail.Step1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
		resp.GetErrorType(),
	)
	require.Equal(t, "test-data", resp.GetErrorMessage())
	require.Empty(t, resp.GetResults())
}

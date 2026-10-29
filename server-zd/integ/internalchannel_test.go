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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/interstate"
	"google.golang.org/protobuf/proto"
)

func TestInterStateWorkflowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestInterStateWorkflow(t, nil)
		smallWaitForFastTest()
		doTestInterStateWorkflow(
			t,

			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestInterStateWorkflow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := interstate.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := interstate.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           interstate.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: interstate.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions := assert.New(t)
	assertions.Equalf(map[string]int64{
		"S1_waitFor":  1,
		"S1_execute":  1,
		"S21_waitFor": 1,
		"S21_execute": 1,
		"S22_waitFor": 1,
		"S22_execute": 1,
		"S31_waitFor": 1,
		"S31_execute": 1,
	}, history, "interstate test fail, %v", history)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	assertions.Equal(0, len(response.GetResults()))
	assertions.True(proto.Equal(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: interstate.TestVal1}},
		data[interstate.State21+"received"].(*dexpb.Value),
	))
	assertions.True(proto.Equal(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: interstate.TestVal2}},
		data[interstate.State31+"received"].(*dexpb.Value),
	))
}

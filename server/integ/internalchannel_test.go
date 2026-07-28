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

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/interstate"
	"github.com/superdurable/iwf/service"
	"google.golang.org/protobuf/proto"
)

func TestInterStateWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestInterStateWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestInterStateWorkflow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_ASYNC),
		)
		smallWaitForFastTest()
	}
}

func TestInterStateWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestInterStateWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestInterStateWorkflow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func doTestInterStateWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := interstate.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := interstate.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           interstate.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      interstate.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
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

	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	assertions.Equal(0, len(response.GetResults()))
	assertions.True(proto.Equal(
		&iwfpb.Value{Kind: &iwfpb.Value_ObjValue{ObjValue: interstate.TestVal1}},
		data[interstate.State21+"received"].(*iwfpb.Value),
	))
	assertions.True(proto.Equal(
		&iwfpb.Value{Kind: &iwfpb.Value_ObjValue{ObjValue: interstate.TestVal2}},
		data[interstate.State31+"received"].(*iwfpb.Value),
	))
}

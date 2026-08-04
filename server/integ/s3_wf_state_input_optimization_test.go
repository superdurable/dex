// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	s3_state_input_optimization "github.com/superdurable/dex/integ/workflow/s3-state-input-optimization"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestS3WorkflowStateInputOptimizationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StateInputOptimization(t, service.BackendTypeTemporal, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func TestS3WorkflowStateInputOptimizationCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StateInputOptimization(t, service.BackendTypeCadence, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func doTestWorkflowWithS3StateInputOptimization(t *testing.T, backendType service.BackendType, lazyLoading bool) {
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
		LazyLoading:     ptr.Any(lazyLoading),
	})
	workerHandler := s3_state_input_optimization.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_state_input_optimization.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           s3_state_input_optimization.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType:    s3_state_input_optimization.State1,
		StepInput:        objJSONValue(`"this-is-a-large-input-that-exceeds-threshold"`),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, int64(1), history["S1_execute"])
	require.Equal(t, int64(1), history["S2_waitFor"])
	require.Equal(t, int64(1), history["S2_execute"])
	require.Equal(t, int64(1), history["S3_waitFor"])
	require.Equal(t, int64(1), history["S3_execute"])

	expectedData := `"this-is-a-large-input-that-exceeds-threshold"`
	require.Equal(t, expectedData, history["S1_input_data"])
	require.Equal(t, expectedData, history["S2_input_data"])
	require.Equal(t, expectedData, history["S3_input_data"])

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(1), objectCount)

	if backendType == service.BackendTypeTemporal {
		requireTemporalHistoryStoresBlobIdsNotPayloads(
			t,
			ctx,
			runtime.UnifiedClient,
			flowId,
			[]string{"s3-store-id|"},
			[]string{"this-is-a-large-input-that-exceeds-threshold"},
		)
	}
}

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
	s3_start_input "github.com/superdurable/dex/integ/workflow/s3-start-input"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestS3WorkflowStartInputTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StartInput(t, service.BackendTypeTemporal, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func TestS3WorkflowStartInputCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StartInput(t, service.BackendTypeCadence, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func doTestWorkflowWithS3StartInput(t *testing.T, backendType service.BackendType, lazyLoading bool) {
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
		LazyLoading:     ptr.Any(lazyLoading),
	})
	workerHandler := s3_start_input.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_start_input.WorkflowType + uuid.NewString()
	const largeInputPayload = `"12345678901"`
	stepInput := objJSONValue(largeInputPayload)

	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           s3_start_input.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType:    s3_start_input.State1,
		StepInput:        stepInput,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	s1WaitForInput := history["S1_waitFor_input"].(*dexpb.Value)
	s1ExecuteInput := history["S1_execute_input"].(*dexpb.Value)

	require.True(t, proto.Equal(stepInput, s1WaitForInput))
	require.True(t, proto.Equal(stepInput, s1ExecuteInput))
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, int64(1), history["S1_execute"])

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
			[]string{"12345678901"},
		)
	}
}

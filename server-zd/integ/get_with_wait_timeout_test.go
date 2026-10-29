// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFlowWaitForTimeoutTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowWithWaitTimeout(t, nil)
		smallWaitForFastTest()
	}
}

func doTestFlowWithWaitTimeout(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := "wf-wait-timeout-test-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 15,

		StartStepType: signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	startTimeUnix := time.Now().Unix()
	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	elapsedSeconds := time.Now().Unix() - startTimeUnix

	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
	require.Contains(
		t,
		grpcServiceErrorResponse(t, err).GetDetail(),
		"flow is still running",
	)
	require.True(t, elapsedSeconds >= 5 && elapsedSeconds <= 12,
		"expect to poll for ~12 seconds, actual value is %d", elapsedSeconds)
}

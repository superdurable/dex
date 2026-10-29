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
	"github.com/superdurable/dex/server-zd/integ/workflow/basic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStartDelayTemporal(t *testing.T) {
	doTestStartDelayRejected(t)
	return

}

// doTestStartDelayRejected checks that server-zd rejects a Flow start delay (C8).
func doTestStartDelayRejected(t *testing.T) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	flowID := basic.FlowType + "-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      basic.Step1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowStartDelaySeconds: 10,
		}, workerTarget),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "server-zd does not support flow_start_delay_seconds > 0")
	_, err = runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.Equal(t, codes.NotFound, status.Code(err))
}

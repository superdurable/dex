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
	"github.com/superdurable/dex/server-zd/integ/workflow/wf_ignore_already_started"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIgnoreAlreadyStartedFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doIgnoreAlreadyStartedFlow(t, nil, nil, "first", "second", true)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(t, nil, &dexpb.FlowAlreadyStartedOptions{
			IgnoreAlreadyStartedError: true,
		}, "retry", "retry", false)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(t, nil, &dexpb.FlowAlreadyStartedOptions{
			IgnoreAlreadyStartedError: true,
		}, "first", "second", true)
		smallWaitForFastTest()
	}
}

func doIgnoreAlreadyStartedFlow(
	t *testing.T,
	firstReqConfig *dexpb.FlowAlreadyStartedOptions,
	secondReqConfig *dexpb.FlowAlreadyStartedOptions,
	firstRequestID string,
	secondRequestID string,
	errorExpected bool,
) {
	workerTarget := startWorker(t, wf_ignore_already_started.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_ignore_already_started.FlowType + "-" + uuid.NewString()
	firstReq := createStartFlowRequest(flowId, workerTarget, firstRequestID, firstReqConfig)
	firstRes, err := flowClient.StartFlow(ctx, firstReq)
	require.NoError(t, err)

	secondReq := createStartFlowRequest(flowId, workerTarget, secondRequestID, secondReqConfig)
	secondRes, err := flowClient.StartFlow(ctx, secondReq)

	if errorExpected {
		require.Equal(t, codes.AlreadyExists, status.Code(err))
		require.Equal(
			t,
			dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
			grpcServiceErrorResponse(t, err).GetSubStatus(),
		)
	} else {
		require.NoError(t, err)
		require.Equal(t, firstRes.GetRunId(), secondRes.GetRunId())
	}

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func createStartFlowRequest(
	flowId string,
	workerTarget *dexpb.WorkerTarget,
	requestID string,
	options *dexpb.FlowAlreadyStartedOptions,
) *dexpb.StartFlowRequest {
	return &dexpb.StartFlowRequest{
		RequestId:          requestID,
		FlowId:             flowId,
		FlowType:           wf_ignore_already_started.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType: wf_ignore_already_started.Step1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowAlreadyStartedOptions: options,
		}, workerTarget),
	}
}

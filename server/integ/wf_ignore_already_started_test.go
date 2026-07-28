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
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/wf_ignore_already_started"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIgnoreAlreadyStartedFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doIgnoreAlreadyStartedFlow(t, service.BackendTypeTemporal, nil, nil, true)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(t, service.BackendTypeTemporal, nil, &iwfpb.FlowAlreadyStartedOptions{
			IgnoreAlreadyStartedError: true,
		}, false)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(
			t,
			service.BackendTypeTemporal,
			&iwfpb.FlowAlreadyStartedOptions{RequestId: "test"},
			&iwfpb.FlowAlreadyStartedOptions{
				IgnoreAlreadyStartedError: true,
				RequestId:                 "test",
			},
			false,
		)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(
			t,
			service.BackendTypeTemporal,
			&iwfpb.FlowAlreadyStartedOptions{RequestId: "test1"},
			&iwfpb.FlowAlreadyStartedOptions{
				IgnoreAlreadyStartedError: true,
				RequestId:                 "test2",
			},
			true,
		)
		smallWaitForFastTest()
	}
}

func TestIgnoreAlreadyStartedFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doIgnoreAlreadyStartedFlow(t, service.BackendTypeCadence, nil, nil, true)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(t, service.BackendTypeCadence, nil, &iwfpb.FlowAlreadyStartedOptions{
			IgnoreAlreadyStartedError: true,
		}, false)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(
			t,
			service.BackendTypeCadence,
			&iwfpb.FlowAlreadyStartedOptions{RequestId: "test"},
			&iwfpb.FlowAlreadyStartedOptions{
				IgnoreAlreadyStartedError: true,
				RequestId:                 "test",
			},
			false,
		)
		smallWaitForFastTest()

		doIgnoreAlreadyStartedFlow(
			t,
			service.BackendTypeCadence,
			&iwfpb.FlowAlreadyStartedOptions{RequestId: "test1"},
			&iwfpb.FlowAlreadyStartedOptions{
				IgnoreAlreadyStartedError: true,
				RequestId:                 "test2",
			},
			true,
		)
		smallWaitForFastTest()
	}
}

func doIgnoreAlreadyStartedFlow(
	t *testing.T,
	backendType service.BackendType,
	firstReqConfig *iwfpb.FlowAlreadyStartedOptions,
	secondReqConfig *iwfpb.FlowAlreadyStartedOptions,
	errorExpected bool,
) {
	workerTarget := startWorker(t, wf_ignore_already_started.NewHandler())
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_ignore_already_started.FlowType + "-" + uuid.NewString()
	firstReq := createStartFlowRequest(flowId, workerTarget, firstReqConfig)
	firstRes, err := flowClient.StartFlow(ctx, firstReq)
	require.NoError(t, err)

	secondReq := createStartFlowRequest(flowId, workerTarget, secondReqConfig)
	secondRes, err := flowClient.StartFlow(ctx, secondReq)

	if errorExpected {
		require.Equal(t, codes.AlreadyExists, status.Code(err))
		require.Equal(
			t,
			iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
			grpcErrorResponse(t, err).GetSubStatus(),
		)
	} else {
		require.NoError(t, err)
		require.Equal(t, firstRes.GetRunId(), secondRes.GetRunId())
	}

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func createStartFlowRequest(
	flowId string,
	workerTarget string,
	options *iwfpb.FlowAlreadyStartedOptions,
) *iwfpb.StartFlowRequest {
	return &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wf_ignore_already_started.FlowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      wf_ignore_already_started.Step1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowAlreadyStartedOptions: options,
		},
	}
}

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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	streamTestFlowType = "stream-test-flow"
	streamTestRedisURL = "redis://127.0.0.1:6379/15"
)

type streamingStepWorker struct {
	dexpb.UnimplementedWorkerServiceServer
	executeCalls       atomic.Int32
	restoredHeartbeats chan string
}

func (worker *streamingStepWorker) InvokeWaitForMethod(
	_ *dexpb.InvokeWaitForMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeWaitForMethodOutput],
) error {
	outputs := []*dexpb.InvokeWaitForMethodOutput{
		{Output: &dexpb.InvokeWaitForMethodOutput_Heartbeat{
			Heartbeat: &dexpb.StepMethodHeartbeat{Value: stringValue("wait-checkpoint")},
		}},
		waitForStreamOutput("progress", "wait-1"),
		waitForStreamOutput("progress", "wait-2"),
		waitForStreamOutput("other", "wait-other"),
		{Output: &dexpb.InvokeWaitForMethodOutput_Result{
			Result: &dexpb.InvokeWaitForMethodResponse{},
		}},
	}
	for _, output := range outputs {
		if err := stream.Send(output); err != nil {
			return err
		}
	}
	return nil
}

func (worker *streamingStepWorker) InvokeExecuteMethod(
	request *dexpb.InvokeExecuteMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeExecuteMethodOutput],
) error {
	attempt := worker.executeCalls.Add(1)
	if attempt > 1 {
		worker.restoredHeartbeats <- request.GetContext().GetLastHeartbeatValue().GetStringValue()
	}
	if attempt == 1 {
		if err := stream.Send(&dexpb.InvokeExecuteMethodOutput{
			Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{
				Heartbeat: &dexpb.StepMethodHeartbeat{Value: stringValue("execute-checkpoint")},
			},
		}); err != nil {
			return err
		}
	} else if attempt == 2 {
		if err := stream.Send(&dexpb.InvokeExecuteMethodOutput{
			Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{
				Heartbeat: &dexpb.StepMethodHeartbeat{},
			},
		}); err != nil {
			return err
		}
	}
	if err := stream.Send(executeStreamOutput("progress", fmt.Sprintf("execute-%d", attempt))); err != nil {
		return err
	}
	if attempt < 3 {
		return status.Error(codes.Unavailable, "retry after streamed progress")
	}
	if err := stream.Send(executeStreamOutput("other", "execute-other")); err != nil {
		return err
	}
	return stream.Send(&dexpb.InvokeExecuteMethodOutput{
		Output: &dexpb.InvokeExecuteMethodOutput_Result{
			Result: &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
					CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
				}},
			},
		},
	})
}

func waitForStreamOutput(streamName string, value string) *dexpb.InvokeWaitForMethodOutput {
	return &dexpb.InvokeWaitForMethodOutput{
		Output: &dexpb.InvokeWaitForMethodOutput_StreamWrite{
			StreamWrite: &dexpb.StepStreamWrite{
				StreamName:          streamName,
				StreamCapacityBytes: 1 << 20,
				Value:               stringValue(value),
			},
		},
	}
}

func executeStreamOutput(streamName string, value string) *dexpb.InvokeExecuteMethodOutput {
	return &dexpb.InvokeExecuteMethodOutput{
		Output: &dexpb.InvokeExecuteMethodOutput_StreamWrite{
			StreamWrite: &dexpb.StepStreamWrite{
				StreamName:          streamName,
				StreamCapacityBytes: 1 << 20,
				Value:               stringValue(value),
			},
		},
	}
}

func TestStreamStepWorkerStreamingTemporal(t *testing.T) {
	testStepWorkerStreaming(t)
}

func testStepWorkerStreaming(t *testing.T) {
	worker := &streamingStepWorker{restoredHeartbeats: make(chan string, 2)}
	workerTarget := startStreamingWorker(t, worker)
	runtime := startDexService(t, serverZDTestConfig{

		StreamStore: testconfig.StreamStoreConfig{Backend: testconfig.StreamStoreBackendMemory},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "step-streaming-" + newRequestID()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           "step-streaming-flow",
		FlowTimeoutSeconds: 25,
		StartStepType:      "stream-step",
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
		StepOptions: &dexpb.StepOptions{ExecuteRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: 1,
			MaximumIntervalSeconds: 1,
			BackoffCoefficient:     1,
			MaximumAttempts:        3,
			TotalDurationSeconds:   20,
		}},
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 25,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	require.Equal(t, int32(3), worker.executeCalls.Load())
	require.Equal(t, "execute-checkpoint", <-worker.restoredHeartbeats)
	require.Empty(t, <-worker.restoredHeartbeats)

	progressMessages := readFlowStreamMessages(t, ctx, runtime.FlowClient, flowID, "progress", 5)
	require.Equal(t, []string{
		"wait-1",
		"wait-2",
		"execute-1",
		"execute-2",
		"execute-3",
	}, protoStreamValues(progressMessages))
	require.Equal(t, []string{
		"#stream-step-1",
		"#stream-step-1",
		"#stream-step-1",
		"#stream-step-1",
		"#stream-step-1",
	}, protoStreamSources(progressMessages))
	otherMessages := readFlowStreamMessages(t, ctx, runtime.FlowClient, flowID, "other", 2)
	require.Equal(t, []string{"wait-other", "execute-other"}, protoStreamValues(otherMessages))
}

func readFlowStreamMessages(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	streamName string,
	count int,
) []*dexpb.StreamMessage {
	t.Helper()
	messages := make([]*dexpb.StreamMessage, 0, count)
	resumeToken := ""
	for len(messages) < count {
		response, err := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
			FlowId:          flowID,
			FlowType:        "step-streaming-flow",
			StreamName:      streamName,
			ResumeToken:     resumeToken,
			WaitTimeSeconds: 2,
		})
		require.NoError(t, err)
		messages = append(messages, response.GetMessage())
		resumeToken = response.GetMessage().GetResumeToken()
	}
	return messages
}

func protoStreamValues(messages []*dexpb.StreamMessage) []string {
	values := make([]string, len(messages))
	for index, message := range messages {
		values[index] = message.GetValue().GetStringValue()
	}
	return values
}

func protoStreamSources(messages []*dexpb.StreamMessage) []string {
	sources := make([]string, len(messages))
	for index, message := range messages {
		sources[index] = message.GetSource()
	}
	return sources
}

func TestStreamAPITemporal(t *testing.T) {
	streamName := "api-" + newRequestID()
	streamStoreConfig := testconfig.StreamStoreConfig{
		Backend:         testconfig.StreamStoreBackendMemory,
		MaxMessageBytes: 64,
		MaxReadMessages: 2,
	}
	// Storage assertions read Redis directly; server-zd has only the memory backend.

	runtime := startDexService(t, serverZDTestConfig{

		StreamStore: streamStoreConfig,
	})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	flowID := "nonexistent-" + newRequestID()
	_, err := flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("missing-key"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("first"),
		Source:              "client-key",
	})
	require.NoError(t, err)
	response, err := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		WaitTimeSeconds: 2,
	})
	require.NoError(t, err)
	require.Equal(t, "first", response.GetMessage().GetValue().GetStringValue())
	require.Equal(t, "client-key", response.GetMessage().GetSource())
	require.NotEmpty(t, response.GetMessage().GetResumeToken())
	require.WithinDuration(t, time.Now(), response.GetMessage().GetCreatedTime().AsTime(), 5*time.Second)
	listed, err := flowClient.ListStreamMessages(ctx, &dexpb.ListStreamMessagesRequest{
		FlowId:     flowID,
		FlowType:   streamTestFlowType,
		StreamName: streamName,
		PageSize:   2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, protoStreamValues(listed.GetMessages()))
	require.Empty(t, listed.GetNextPageToken())
	_, err = flowClient.ListStreamMessages(ctx, &dexpb.ListStreamMessagesRequest{
		FlowId:     flowID,
		FlowType:   streamTestFlowType,
		StreamName: streamName,
		PageSize:   3,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue(string(make([]byte, 64))),
		Source:              "message-too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	listed, err = flowClient.ListStreamMessages(ctx, &dexpb.ListStreamMessagesRequest{
		FlowId:     flowID,
		FlowType:   streamTestFlowType,
		StreamName: streamName,
		PageSize:   2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, protoStreamValues(listed.GetMessages()))

	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1,
		Value:               stringValue("too-large"),
		Source:              "too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	_, err = flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		ResumeToken:     "malformed",
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	var readFinished atomic.Bool
	readResult := make(chan *dexpb.ReadStreamResponse, 1)
	readErrors := make(chan error, 1)
	go func() {
		blockingResponse, readErr := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
			FlowId:          flowID,
			FlowType:        streamTestFlowType,
			StreamName:      streamName,
			ResumeToken:     response.GetMessage().GetResumeToken(),
			WaitTimeSeconds: 5,
		})
		if readErr != nil {
			readErrors <- readErr
		} else {
			readResult <- blockingResponse
		}
		readFinished.Store(true)
	}()
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              "different-flow",
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("other"),
		Source:              "other-key",
	})
	require.NoError(t, err)
	require.Never(t, readFinished.Load, 200*time.Millisecond, 10*time.Millisecond)
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("second"),
		Source:              "run-id#step-id",
	})
	require.NoError(t, err)
	latestToken := ""
	select {
	case readErr := <-readErrors:
		require.NoError(t, readErr)
	case blockingResponse := <-readResult:
		require.Equal(t, "second", blockingResponse.GetMessage().GetValue().GetStringValue())
		require.Equal(t, "run-id#step-id", blockingResponse.GetMessage().GetSource())
		latestToken = blockingResponse.GetMessage().GetResumeToken()
	case <-ctx.Done():
		require.FailNow(t, "blocking ReadStream did not finish", ctx.Err())
	}
	_, err = flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		ResumeToken:     latestToken,
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
}

func TestStreamDisabledTemporal(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              "flow",
		FlowType:            streamTestFlowType,
		StreamName:          "disabled",
		StreamCapacityBytes: 1024,
		Value:               stringValue("value"),
		Source:              "key",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

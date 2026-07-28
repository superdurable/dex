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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/command_thread_completion"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestCommandThreadCompletionTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCommandThreadCompletion(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()

		doTestCommandThreadCompletion(t, service.BackendTypeTemporal, &iwfpb.FlowConfig{
			ContinueAsNewThreshold: ptr.Any(int32(1)),
		})
		smallWaitForFastTest()
	}
}

func TestCommandThreadCompletionCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCommandThreadCompletion(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()

		doTestCommandThreadCompletion(t, service.BackendTypeCadence, &iwfpb.FlowConfig{
			ContinueAsNewThreshold: ptr.Any(int32(1)),
		})
		smallWaitForFastTest()
	}
}

func TestAnyCommandCompletedTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	doTestAnyCommandCompleted(t, service.BackendTypeTemporal)
}

func TestAnyCommandCompletedCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	doTestAnyCommandCompleted(t, service.BackendTypeCadence)
}

func doTestAnyCommandCompleted(t *testing.T, backendType service.BackendType) {
	workerHandler := command_thread_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := "any_cmd_can_test_" + uuid.NewString()
	startTime := time.Now()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           command_thread_completion.WorkflowType,
		FlowTimeoutSeconds: 60,
		WorkerTarget:       workerTarget,
		StartStepType:      command_thread_completion.StateAnyCmd,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: &iwfpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(1)),
			},
		},
	})
	require.NoError(t, err)

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, publishErr := flowClient.PublishToChannel(context.Background(), &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: "any-cmd-signal",
					Value:       stringValue("signal-data"),
				},
			},
		})
		if publishErr != nil {
			t.Logf("Warning: Failed to send signal: %v", publishErr)
		}
	}()

	assertions := assert.New(t)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	if err != nil {
		assertions.False(
			strings.Contains(err.Error(), "420"),
			"Workflow took too long to complete, which means that ANY_COMMAND_COMPLETED command was not completed as expected.",
		)
	}
	require.NoError(t, err)

	elapsedTime := time.Since(startTime)
	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData

	assertions.Equalf(map[string]int64{
		"S3_execute":          1,
		"S3_waitFor":          1,
		"StateAnyCmd_execute": 1,
		"StateAnyCmd_waitFor": 1,
	}, history, "State execution history mismatch: %v", history)

	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	signalReceived, ok := data["any_cmd_signal_received"].(bool)
	assertions.True(ok, "any_cmd_signal_received data should be present")
	assertions.True(signalReceived)
	assertions.Less(elapsedTime, 5*time.Second,
		"Workflow took %v, which suggests we waited for the long timer.", elapsedTime)
}

func doTestCommandThreadCompletion(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := command_thread_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := command_thread_completion.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           command_thread_completion.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      command_thread_completion.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, publishErr := flowClient.PublishToChannel(context.Background(), &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: "test-signal",
					Value:       stringValue("signal-data"),
				},
			},
		})
		if publishErr != nil {
			t.Logf("Warning: Failed to send signal: %v", publishErr)
		}
	}()

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions := assert.New(t)

	assertions.Equalf(map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
		"S3_waitFor": 1,
		"S3_execute": 1,
	}, history, "Command thread completion test failed - state execution history mismatch: %v", history)

	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())

	s1TimerFired, ok := data["s1_timer_fired"].(bool)
	assertions.True(ok)
	assertions.True(s1TimerFired)

	s1SignalReceived, ok := data["s1_signal_received"].(bool)
	assertions.True(ok)
	assertions.True(s1SignalReceived)

	s1ChannelReceived, ok := data["s1_channel_received"].(bool)
	assertions.True(ok)
	assertions.True(s1ChannelReceived)

	s2ChannelReceived, ok := data["s2_channel_received"].(bool)
	assertions.True(ok)
	assertions.True(s2ChannelReceived)

	if s2ChannelReceived {
		channelValue, ok := data["s2_channel_value"].(*iwfpb.Value)
		assertions.True(ok)
		if ok {
			expected := &iwfpb.Value{
				Kind: &iwfpb.Value_ObjValue{
					ObjValue: &iwfpb.EncodedObject{
						Encoding: "json",
						Payload:  []byte("channel-data"),
					},
				},
			}
			assertions.True(proto.Equal(expected, channelValue))
		}
	}

	s3TimerFired, ok := data["s3_timer_fired"].(bool)
	assertions.True(ok)
	assertions.True(s3TimerFired)
}

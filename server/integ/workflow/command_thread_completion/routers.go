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

package command_thread_completion

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/common"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow validates that all command threads complete before continue-as-new snapshots state.
 * It tests the fix for the bug where internal channel signals were lost during continue-as-new.
 *
 * Flow structure:
 * Step1:
 *   - WaitFor: Set up timer, signal, and internal channel conditions with ALL_COMPLETED
 *   - Execute: Publish to internal channel, move to Step2
 * Step2:
 *   - WaitFor: Wait for the internal channel from Step1
 *   - Execute: Complete flow
 *
 * The test triggers continue-as-new after Step1 execute but before Step2 starts.
 * This ensures the internal channel signal published by Step1 is captured before continue-as-new.
 */
const (
	WorkflowType = "command_thread_completion"
	State1       = "S1"
	State2       = "S2"
	State3       = "S3"
	StateAnyCmd  = "StateAnyCmd" // Tests ANY_COMPLETED with CAN

	testChannel    = "test-channel"
	testSignal     = "test-signal"
	testTimerCmd   = "test-timer"
	testChannelCmd = "test-channel-cmd"
	testSignalCmd  = "test-signal-cmd"
)

var testChannelValue = &iwfpb.EncodedObject{
	Encoding: "json",
	Payload:  []byte("channel-data"),
}

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
		invokeData:    sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.recordInvoke(request.GetStepType() + "_waitFor")

	switch request.GetStepType() {
	case State1:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				TimerConditions: []*iwfpb.TimerCondition{
					{
						ConditionId:     testTimerCmd,
						DurationSeconds: 2,
					},
				},
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ConditionId: testSignalCmd, ChannelName: testSignal},
					{ConditionId: testChannelCmd, ChannelName: testChannel},
				},
			},
			PublishToChannel: []*iwfpb.ChannelMessage{
				{
					ChannelName: testChannel,
					Value: &iwfpb.Value{
						Kind: &iwfpb.Value_ObjValue{ObjValue: testChannelValue},
					},
				},
			},
		}, nil
	case State2:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ConditionId: "s2-channel-cmd", ChannelName: testChannel + "2"},
				},
			},
		}, nil
	case State3:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				TimerConditions: []*iwfpb.TimerCondition{
					{
						ConditionId:     "s3-timer-cmd",
						DurationSeconds: 2,
					},
				},
			},
		}, nil
	case StateAnyCmd:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
				TimerConditions: []*iwfpb.TimerCondition{
					{
						ConditionId:     "any-cmd-timer",
						DurationSeconds: 20,
					},
				},
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ConditionId: "any-cmd-signal-cmd", ChannelName: "any-cmd-signal"},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.recordInvoke(request.GetStepType() + "_execute")

	switch request.GetStepType() {
	case State1:
		cmdResults := request.GetConditionResults()

		timerFired := false
		for _, timerResult := range cmdResults.GetTimerResults() {
			if timerResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				timerFired = true
				h.recordData("s1_timer_fired", true)
			}
		}
		if !timerFired {
			log.Println("ERROR: Timer should have fired in State1")
		}

		signalReceived := false
		for _, channelResult := range cmdResults.GetChannelResults() {
			if channelResult.GetChannelName() == testSignal &&
				channelResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				signalReceived = true
				h.recordData("s1_signal_received", true)
			}
		}
		if !signalReceived {
			log.Println("ERROR: Signal should have been received in State1")
		}

		channelReceived := false
		for _, channelResult := range cmdResults.GetChannelResults() {
			if channelResult.GetChannelName() == testChannel &&
				channelResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				channelReceived = true
				h.recordData("s1_channel_received", true)
			}
		}
		if !channelReceived {
			log.Println("ERROR: Internal channel should have been received in State1")
		}

		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: State2},
					{StepType: State3},
				},
			},
			PublishToChannel: []*iwfpb.ChannelMessage{
				{
					ChannelName: testChannel + "2",
					Value: &iwfpb.Value{
						Kind: &iwfpb.Value_ObjValue{ObjValue: testChannelValue},
					},
				},
			},
		}, nil
	case State2:
		cmdResults := request.GetConditionResults()

		channelReceived := false
		for _, channelResult := range cmdResults.GetChannelResults() {
			if channelResult.GetChannelName() == testChannel+"2" &&
				channelResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				channelReceived = true
				h.recordData("s2_channel_received", true)
				if values := channelResult.GetValues(); len(values) > 0 {
					h.recordData("s2_channel_value", values[0])
				}
			}
		}

		if !channelReceived {
			log.Println("ERROR: State2 channel was NOT received! This indicates the bug exists.")
			h.recordData("s2_channel_received", false)
		}

		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.DeadEndFlowStepType},
				},
			},
		}, nil
	case State3:
		cmdResults := request.GetConditionResults()

		timerFired := false
		for _, timerResult := range cmdResults.GetTimerResults() {
			if timerResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				timerFired = true
				h.recordData("s3_timer_fired", true)
			}
		}

		if !timerFired {
			log.Println("ERROR: Timer should have fired in State3")
			h.recordData("s3_timer_fired", false)
		}

		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
				},
			},
		}, nil
	case StateAnyCmd:
		cmdResults := request.GetConditionResults()

		signalReceived := false
		timerFired := false

		for _, channelResult := range cmdResults.GetChannelResults() {
			if channelResult.GetChannelName() == "any-cmd-signal" &&
				channelResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				signalReceived = true
				h.recordData("any_cmd_signal_received", true)
			}
		}

		for _, timerResult := range cmdResults.GetTimerResults() {
			if timerResult.GetConditionId() == "any-cmd-timer" &&
				timerResult.GetConditionStatus() == iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
				timerFired = true
			}
		}

		if !signalReceived {
			log.Println("ERROR: Signal should have been received in StateAnyCmd (ANY_COMPLETED)")
			h.recordData("any_cmd_signal_received", false)
		}

		if timerFired {
			log.Println("WARNING: Timer fired in StateAnyCmd - this suggests we waited for it instead of proceeding with signal")
		}

		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: State3},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) GetTestResult() common.TestResult {
	history := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		history[key.(string)] = value.(int64)
		return true
	})

	data := make(map[string]interface{})
	h.invokeData.Range(func(key, value interface{}) bool {
		data[key.(string)] = value
		return true
	})

	return common.TestResult{InvokeHistory: history, InvokeData: data}
}

func (h *handler) recordInvoke(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
	} else {
		h.invokeHistory.Store(key, int64(1))
	}
}

func (h *handler) recordData(key string, value interface{}) {
	h.invokeData.Store(key, value)
}

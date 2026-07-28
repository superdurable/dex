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

package interpreter

import (
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type SignalReceiver struct {
	failFlowByClient       bool
	reasonFailFlowByClient *string
	provider               interfaces.WorkflowProvider
	timerProcessor         interfaces.TimerProcessor
	flowConfiger           *config.FlowConfiger
	channelStore           *ChannelStore
	stepRequestQueue       *StepRequestQueue
	persistenceManager     *PersistenceManager
}

func NewSignalReceiver(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	channelStore *ChannelStore,
	stepRequestQueue *StepRequestQueue,
	persistenceManager *PersistenceManager,
	timerProcessor interfaces.TimerProcessor,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	flowConfiger *config.FlowConfiger,
) *SignalReceiver {
	sr := &SignalReceiver{
		provider:           provider,
		failFlowByClient:   false,
		timerProcessor:     timerProcessor,
		flowConfiger:       flowConfiger,
		channelStore:       channelStore,
		stepRequestQueue:   stepRequestQueue,
		persistenceManager: persistenceManager,
	}

	//The thread waits until a FailWorkflowSignalChannelName signal has been
	//received or a continueAsNew run is triggered. When a signal has been received it sets
	//SignalReceiver.failFlowByClient to true and sets SignalReceiver.reasonFailFlowByClient to the reason
	//given in the signal's value. If continueIsNew is triggered, the thread completes after all signals have been processed.
	provider.GoNamed(ctx, "fail-flow-system-signal-handler", func(ctx interfaces.UnifiedContext) {
		for {
			ch := provider.GetSignalChannel(ctx, service.FailWorkflowSignalChannelName)

			val := dexpb.FailFlowSignalRequest{}
			received := false
			err := provider.Await(ctx, func() bool {
				received = ch.ReceiveAsync(&val)
				// NOTE: continueAsNew will wait for all threads to complete, so we must stop this thread for continueAsNew when no more signals to process
				return received || continueAsNewCounter.IsThresholdMet()
			})
			if err != nil {
				break
			}
			if received {
				continueAsNewCounter.IncSignalsReceived()
				sr.failFlowByClient = true
				sr.reasonFailFlowByClient = ptr.Any(val.GetReason())
			} else {
				// NOTE: continueAsNew will wait for all threads to complete, so we must stop this thread for continueAsNew when no more signals to process
				break
			}
		}
	})

	//The thread waits until a SkipTimerSignalChannelName signal has been
	//received or a continueAsNew run is triggered. When a signal has been received it skips the specific timer
	//described in the signal's value. If continueIsNew is triggered, the thread completes after all signals have been processed.
	provider.GoNamed(ctx, "skip-timer-system-signal-handler", func(ctx interfaces.UnifiedContext) {
		for {
			ch := provider.GetSignalChannel(ctx, service.SkipTimerSignalChannelName)
			val := dexpb.SkipTimerSignalRequest{}

			received := false
			err := provider.Await(ctx, func() bool {
				received = ch.ReceiveAsync(&val)
				return received || continueAsNewCounter.IsThresholdMet()
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received {
				continueAsNewCounter.IncSignalsReceived()
				timerProcessor.SkipTimer(
					val.GetStepExecutionId(),
					val.GetTimerConditionId(),
					int(val.GetTimerConditionIndex()),
				)
			} else {
				// NOTE: continueAsNew will wait for all threads to complete, so we must stop this thread for continueAsNew when no more signals to process
				return
			}
		}
	})

	//The thread waits until a UpdateConfigSignalChannelName signal has been
	//received or a continueAsNew run is triggered. When a signal has been received it updates the flow config
	//defined in the signal's value. If continueIsNew is triggered, the thread completes after all signals have been processed.
	provider.GoNamed(ctx, "update-config-system-signal-handler", func(ctx interfaces.UnifiedContext) {
		for {
			ch := provider.GetSignalChannel(ctx, service.UpdateConfigSignalChannelName)
			val := dexpb.UpdateFlowConfigRequest{}

			received := false
			err := provider.Await(ctx, func() bool {
				received = ch.ReceiveAsync(&val)
				return received || continueAsNewCounter.IsThresholdMet()
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received {
				continueAsNewCounter.IncSignalsReceived()
				if err := flowConfiger.UpdateByAPI(val.GetFlowConfig()); err != nil {
					sr.failFlowByClient = true
					sr.reasonFailFlowByClient = ptr.Any(err.Error())
				}
			} else {
				// NOTE: continueAsNew will wait for all threads to complete, so we must stop this thread for continueAsNew when no more signals to process
				return
			}
		}
	})

	//The thread waits until a TriggerContinueAsNewSignalChannelName signal has
	//been received or a continueAsNew run is triggered. When a signal has been received it triggers a continueAsNew run.
	//Since this thread is triggering a continueAsNew run it doesn't need to wait for signals to drain from the channel.
	provider.GoNamed(ctx, "trigger-continue-as-new-handler", func(ctx interfaces.UnifiedContext) {
		// NOTE: unlike other signal channels, this one doesn't need to drain during continueAsNew
		// because if there is a continueAsNew, this signal is not needed anymore
		ch := provider.GetSignalChannel(ctx, service.TriggerContinueAsNewSignalChannelName)

		received := false
		err := provider.Await(ctx, func() bool {
			received = ch.ReceiveAsync(nil)
			return received || continueAsNewCounter.IsThresholdMet()
		})
		if err != nil {
			return
		}
		if received {
			continueAsNewCounter.TriggerByAPI()
			return
		}
		return
	})

	//The thread waits until a ExecuteRpcSignalChannelName signal has been
	//received or a continueAsNew run is triggered. When a signal has been received it upserts attributes
	//(if they exist in the signal value), publishes messages to channels,
	//and/or schedules new steps (if StepDecision is set in the signal value).
	//If continueIsNew is triggered, the thread completes after all signals have been processed.
	provider.GoNamed(ctx, "execute-rpc-signal-handler", func(ctx interfaces.UnifiedContext) {
		for {
			ch := provider.GetSignalChannel(ctx, service.ExecuteRpcSignalChannelName)
			var val dexpb.ExecuteRpcSignalRequest

			received := false
			err := provider.Await(ctx, func() bool {
				received = ch.ReceiveAsync(&val)
				return received || continueAsNewCounter.IsThresholdMet()
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received {
				continueAsNewCounter.IncSignalsReceived()
				if err := sr.persistenceManager.ApplyAttributeWrites(
					ctx,
					val.GetUpsertAttributes(),
				); err != nil {
					sr.provider.GetLogger(ctx).Error("apply RPC result failed", "error", err)
					continue
				}
				sr.channelStore.ProcessPublishing(val.GetPublishToChannel())
				if val.GetStepDecision() != nil {
					sr.stepRequestQueue.AddStepStartRequests(
						val.GetStepDecision().GetNextSteps(),
					)
				}
			} else {
				// NOTE: continueAsNew will wait for all threads to complete, so we must stop this thread for continueAsNew when no more signals to process
				return
			}
		}
	})
	return sr
}

// DrainAllReceivedButUnprocessedSignals will process all the signals that are received but not processed in the current
// flow task.
// There are two cases this is needed:
// 1. ContinueAsNew:
// retrieve signals that after signal handler threads are stopped,
// so that the signals can be carried over to next run by continueAsNew.
// 2. Conditional close/complete flow on channel:
// retrieve all channel messages before checking the channels
func (sr *SignalReceiver) DrainAllReceivedButUnprocessedSignals(
	ctx interfaces.UnifiedContext,
) {
	ch := sr.provider.GetSignalChannel(ctx, service.FailWorkflowSignalChannelName)
	for {
		val := dexpb.FailFlowSignalRequest{}
		if ch.ReceiveAsync(&val) {
			sr.failFlowByClient = true
			sr.reasonFailFlowByClient = ptr.Any(val.GetReason())
		} else {
			break
		}
	}

	ch = sr.provider.GetSignalChannel(ctx, service.SkipTimerSignalChannelName)
	for {
		val := dexpb.SkipTimerSignalRequest{}
		if ch.ReceiveAsync(&val) {
			sr.timerProcessor.SkipTimer(
				val.GetStepExecutionId(),
				val.GetTimerConditionId(),
				int(val.GetTimerConditionIndex()),
			)
		} else {
			break
		}
	}

	ch = sr.provider.GetSignalChannel(ctx, service.UpdateConfigSignalChannelName)
	for {
		val := dexpb.UpdateFlowConfigRequest{}
		if ch.ReceiveAsync(&val) {
			if err := sr.flowConfiger.UpdateByAPI(val.GetFlowConfig()); err != nil {
				sr.failFlowByClient = true
				sr.reasonFailFlowByClient = ptr.Any(err.Error())
			}
		} else {
			break
		}
	}

	ch = sr.provider.GetSignalChannel(ctx, service.ExecuteRpcSignalChannelName)
	for {
		val := dexpb.ExecuteRpcSignalRequest{}
		if ch.ReceiveAsync(&val) {
			if err := sr.persistenceManager.ApplyAttributeWrites(
				ctx,
				val.GetUpsertAttributes(),
			); err != nil {
				sr.provider.GetLogger(ctx).Error("apply RPC result failed", "error", err)
				continue
			}
			sr.channelStore.ProcessPublishing(val.GetPublishToChannel())
			if val.GetStepDecision() != nil {
				sr.stepRequestQueue.AddStepStartRequests(
					val.GetStepDecision().GetNextSteps(),
				)
			}
		} else {
			break
		}
	}
}

func (sr *SignalReceiver) IsFailWorkFlowRequested() (bool, error) {
	reason := "fail by client"
	if sr.reasonFailFlowByClient != nil {
		reason = *sr.reasonFailFlowByClient
	}
	if sr.failFlowByClient {
		return true, sr.provider.NewWorkflowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
			reason,
		)
	}

	return false, nil
}

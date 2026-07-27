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
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/cont"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

type SignalReceiver struct {
	failFlowByClient       bool
	reasonFailFlowByClient *string
	completeFlowByClient   bool
	provider               interfaces.WorkflowProvider
	timerProcessor         interfaces.TimerProcessor
	flowConfiger           *config.FlowConfiger
	channelStore           *ChannelStore
	stepRequestQueue       *StepRequestQueue
	persistenceManager     *PersistenceManager
	continueAsNewCounter   *cont.ContinueAsNewCounter
}

type signalAwaiter struct {
	receiver *SignalReceiver
	channel  interfaces.ReceiveChannel
	valuePtr interface{}
	received bool
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
	if provider == nil || channelStore == nil || stepRequestQueue == nil ||
		persistenceManager == nil || timerProcessor == nil ||
		continueAsNewCounter == nil || flowConfiger == nil {
		panic("SignalReceiver requires non-nil dependencies")
	}
	signalReceiver := &SignalReceiver{
		provider:             provider,
		timerProcessor:       timerProcessor,
		flowConfiger:         flowConfiger,
		channelStore:         channelStore,
		stepRequestQueue:     stepRequestQueue,
		persistenceManager:   persistenceManager,
		continueAsNewCounter: continueAsNewCounter,
	}

	// Handlers drain their system signal until continue-as-new or flow termination.
	provider.GoNamed(ctx, "fail-flow-system-signal-handler", signalReceiver.runFailFlow)
	provider.GoNamed(ctx, "complete-flow-system-signal-handler", signalReceiver.runCompleteFlow)
	provider.GoNamed(ctx, "skip-timer-system-signal-handler", signalReceiver.runSkipTimer)
	provider.GoNamed(ctx, "update-config-system-signal-handler", signalReceiver.runUpdateConfig)
	provider.GoNamed(ctx, "execute-rpc-system-signal-handler", signalReceiver.runExecuteRPC)
	provider.GoNamed(ctx, "trigger-continue-as-new-signal-handler", signalReceiver.runTriggerContinueAsNew)
	return signalReceiver
}

func (sr *SignalReceiver) runFailFlow(ctx interfaces.UnifiedContext) {
	for {
		var request iwfpb.FailFlowSignalRequest
		if !sr.receiveOrStop(ctx, service.FailWorkflowSignalChannelName, &request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.failFlowByClient = true
		sr.reasonFailFlowByClient = ptr.Any(request.GetReason())
	}
}

func (sr *SignalReceiver) runCompleteFlow(ctx interfaces.UnifiedContext) {
	for {
		var request iwfpb.CompleteFlowSignalRequest
		if !sr.receiveOrStop(ctx, service.CompleteFlowSignalChannelName, &request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.completeFlowByClient = true
		sr.provider.GetLogger(ctx).Info(
			"complete flow requested",
			"reason",
			request.GetReason(),
		)
	}
}

func (sr *SignalReceiver) runSkipTimer(ctx interfaces.UnifiedContext) {
	for {
		var request iwfpb.SkipTimerSignalRequest
		if !sr.receiveOrStop(ctx, service.SkipTimerSignalChannelName, &request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.timerProcessor.SkipTimer(
			request.GetStepExecutionId(),
			request.GetTimerConditionId(),
			int(request.GetTimerConditionIndex()),
		)
	}
}

func (sr *SignalReceiver) runUpdateConfig(ctx interfaces.UnifiedContext) {
	for {
		var request iwfpb.UpdateFlowConfigRequest
		if !sr.receiveOrStop(ctx, service.UpdateConfigSignalChannelName, &request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.processConfigUpdate(request.GetFlowConfig())
	}
}

func (sr *SignalReceiver) runExecuteRPC(ctx interfaces.UnifiedContext) {
	for {
		var request iwfpb.ExecuteRpcSignalRequest
		if !sr.receiveOrStop(ctx, service.ExecuteRpcSignalChannelName, &request) {
			return
		}
		sr.processExecuteRPC(ctx, &request)
	}
}

func (sr *SignalReceiver) runTriggerContinueAsNew(ctx interfaces.UnifiedContext) {
	// An ongoing continue-as-new supersedes queued trigger requests.
	if sr.receiveOrStop(ctx, service.TriggerContinueAsNewSignalChannelName, nil) {
		sr.continueAsNewCounter.TriggerByAPI()
	}
}

func (sr *SignalReceiver) receiveOrStop(
	ctx interfaces.UnifiedContext,
	signalName string,
	valuePtr interface{},
) bool {
	waiter := &signalAwaiter{
		receiver: sr,
		channel:  sr.provider.GetSignalChannel(ctx, signalName),
		valuePtr: valuePtr,
	}
	if err := sr.provider.Await(ctx, waiter.ready); err != nil {
		return false
	}
	return waiter.received
}

func (w *signalAwaiter) ready() bool {
	w.received = w.channel.ReceiveAsync(w.valuePtr)
	return w.received || w.receiver.shouldStop()
}

func (sr *SignalReceiver) processConfigUpdate(flowConfig *iwfpb.FlowConfig) {
	if err := sr.flowConfiger.UpdateByAPI(flowConfig); err != nil {
		sr.failFlowByClient = true
		sr.reasonFailFlowByClient = ptr.Any(err.Error())
	}
}

func (sr *SignalReceiver) processExecuteRPC(
	ctx interfaces.UnifiedContext,
	request *iwfpb.ExecuteRpcSignalRequest,
) {
	sr.continueAsNewCounter.IncSignalsReceived()
	decision := request.GetStepDecision()
	if err := sr.persistenceManager.ApplyAttributeWrites(
		ctx,
		request.GetUpsertAttributes(),
	); err != nil {
		sr.provider.GetLogger(ctx).Error("apply RPC result failed", "error", err)
		return
	}
	sr.channelStore.ProcessPublishing(request.GetPublishToChannel())
	sr.stepRequestQueue.AddStepStartRequests(decision.GetNextSteps())
}

func (sr *SignalReceiver) DrainAllReceivedButUnprocessedSignals(
	ctx interfaces.UnifiedContext,
) {
	// Draining closes the receive-to-snapshot gap before continue-as-new and conditional close.
	sr.drainFailFlow(ctx)
	sr.drainCompleteFlow(ctx)
	sr.drainSkipTimer(ctx)
	sr.drainUpdateConfig(ctx)
	sr.drainExecuteRPC(ctx)
}

func (sr *SignalReceiver) drainFailFlow(ctx interfaces.UnifiedContext) {
	channel := sr.provider.GetSignalChannel(ctx, service.FailWorkflowSignalChannelName)
	for {
		var request iwfpb.FailFlowSignalRequest
		if !channel.ReceiveAsync(&request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.failFlowByClient = true
		sr.reasonFailFlowByClient = ptr.Any(request.GetReason())
	}
}

func (sr *SignalReceiver) drainCompleteFlow(ctx interfaces.UnifiedContext) {
	channel := sr.provider.GetSignalChannel(ctx, service.CompleteFlowSignalChannelName)
	for {
		var request iwfpb.CompleteFlowSignalRequest
		if !channel.ReceiveAsync(&request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.completeFlowByClient = true
		sr.provider.GetLogger(ctx).Info(
			"complete flow requested",
			"reason",
			request.GetReason(),
		)
	}
}

func (sr *SignalReceiver) drainSkipTimer(ctx interfaces.UnifiedContext) {
	channel := sr.provider.GetSignalChannel(ctx, service.SkipTimerSignalChannelName)
	for {
		var request iwfpb.SkipTimerSignalRequest
		if !channel.ReceiveAsync(&request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.timerProcessor.SkipTimer(
			request.GetStepExecutionId(),
			request.GetTimerConditionId(),
			int(request.GetTimerConditionIndex()),
		)
	}
}

func (sr *SignalReceiver) drainUpdateConfig(ctx interfaces.UnifiedContext) {
	channel := sr.provider.GetSignalChannel(ctx, service.UpdateConfigSignalChannelName)
	for {
		var request iwfpb.UpdateFlowConfigRequest
		if !channel.ReceiveAsync(&request) {
			return
		}
		sr.continueAsNewCounter.IncSignalsReceived()
		sr.processConfigUpdate(request.GetFlowConfig())
	}
}

func (sr *SignalReceiver) drainExecuteRPC(ctx interfaces.UnifiedContext) {
	channel := sr.provider.GetSignalChannel(ctx, service.ExecuteRpcSignalChannelName)
	for {
		var request iwfpb.ExecuteRpcSignalRequest
		if !channel.ReceiveAsync(&request) {
			return
		}
		sr.processExecuteRPC(ctx, &request)
	}
}

func (sr *SignalReceiver) IsFailFlowRequested() (bool, error) {
	if !sr.failFlowByClient {
		return false, nil
	}
	reason := "fail by client"
	if sr.reasonFailFlowByClient != nil {
		reason = *sr.reasonFailFlowByClient
	}
	return true, sr.provider.NewWorkflowError(
		iwfpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
		reason,
	)
}

func (sr *SignalReceiver) IsCompleteFlowRequested() bool {
	return sr.completeFlowByClient
}

func (sr *SignalReceiver) shouldStop() bool {
	return sr.continueAsNewCounter.IsThresholdMet() ||
		sr.isTerminalRequested()
}

func (sr *SignalReceiver) isTerminalRequested() bool {
	return sr.failFlowByClient || sr.completeFlowByClient
}

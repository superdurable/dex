// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type SignalReceiver struct {
	provider           interfaces.WorkflowProvider
	timerProcessor     interfaces.TimerProcessor
	flowConfiger       *config.FlowConfiger
	channelStore       *ChannelStore
	stepRequestQueue   *StepRequestQueue
	persistenceManager *PersistenceManager
	stopFlowRequested  bool
	stopFlowErr        error
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
		timerProcessor:     timerProcessor,
		flowConfiger:       flowConfiger,
		channelStore:       channelStore,
		stepRequestQueue:   stepRequestQueue,
		persistenceManager: persistenceManager,
	}

	//The thread waits until a StopWorkflowSignalChannelName signal has been
	//received or a continueAsNew run is triggered. When a signal has been received it requests
	//cooperative terminal cleanup. If continueIsNew is triggered, the thread completes after all signals have been processed.
	provider.GoNamed(ctx, "stop-flow-system-signal-handler", func(ctx interfaces.UnifiedContext) {
		for {
			ch := provider.GetSignalChannel(ctx, service.StopWorkflowSignalChannelName)

			val := dexpb.StopFlowSignalRequest{}
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
				sr.requestStopFlow(&val)
				return
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
				return received || continueAsNewCounter.IsThresholdMet() || sr.stopFlowRequested
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received && !sr.stopFlowRequested {
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
				return received || continueAsNewCounter.IsThresholdMet() || sr.stopFlowRequested
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received && !sr.stopFlowRequested {
				continueAsNewCounter.IncSignalsReceived()
				if err := flowConfiger.UpdateByAPI(val.GetFlowConfig()); err != nil {
					sr.failInvalidFlowConfig(err)
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
			return received || continueAsNewCounter.IsThresholdMet() || sr.stopFlowRequested
		})
		if err != nil {
			return
		}
		if received && !sr.stopFlowRequested {
			continueAsNewCounter.TriggerByAPI()
			return
		}
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
				return received || continueAsNewCounter.IsThresholdMet() || sr.stopFlowRequested
			})
			if err != nil {
				// break the loop to prevent goroutine leakage
				break
			}
			if received && !sr.stopFlowRequested {
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
	ch := sr.provider.GetSignalChannel(ctx, service.StopWorkflowSignalChannelName)
	for {
		val := dexpb.StopFlowSignalRequest{}
		if ch.ReceiveAsync(&val) {
			sr.requestStopFlow(&val)
		} else {
			break
		}
	}

	ch = sr.provider.GetSignalChannel(ctx, service.SkipTimerSignalChannelName)
	for {
		val := dexpb.SkipTimerSignalRequest{}
		if ch.ReceiveAsync(&val) {
			if sr.stopFlowRequested {
				continue
			}
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
			if sr.stopFlowRequested {
				continue
			}
			if err := sr.flowConfiger.UpdateByAPI(val.GetFlowConfig()); err != nil {
				sr.failInvalidFlowConfig(err)
			}
		} else {
			break
		}
	}

	ch = sr.provider.GetSignalChannel(ctx, service.ExecuteRpcSignalChannelName)
	for {
		val := dexpb.ExecuteRpcSignalRequest{}
		if ch.ReceiveAsync(&val) {
			if sr.stopFlowRequested {
				continue
			}
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

func (sr *SignalReceiver) failInvalidFlowConfig(err error) {
	sr.requestStopFlowWithError(sr.provider.NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
		&dexpb.ErrorResponse{Detail: err.Error()},
	))
}

func (sr *SignalReceiver) requestStopFlow(request *dexpb.StopFlowSignalRequest) {
	if request == nil || sr.stopFlowRequested {
		return
	}
	switch request.GetStopType() {
	case dexpb.StopType_STOP_TYPE_CANCEL:
		sr.requestStopFlowWithError(sr.provider.NewCanceledError(request.GetReason()))
	case dexpb.StopType_STOP_TYPE_FAIL:
		reason := request.GetReason()
		if reason == "" {
			reason = "fail by client"
		}
		sr.requestStopFlowWithError(sr.provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
			&dexpb.ErrorResponse{Detail: reason},
		))
	}
}

func (sr *SignalReceiver) requestStopFlowWithError(stopErr error) {
	if sr.stopFlowRequested {
		return
	}
	if stopErr == nil {
		panic("stop Flow error is nil")
	}
	sr.stopFlowRequested = true
	sr.stopFlowErr = stopErr
}

func (sr *SignalReceiver) GetIfStopFlowRequested() (bool, error) {
	return sr.stopFlowRequested, sr.stopFlowErr
}

func (sr *SignalReceiver) IsStopFlowRequested() bool {
	return sr.stopFlowRequested
}

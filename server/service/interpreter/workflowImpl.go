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
	"fmt"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/retry"
	"github.com/superdurable/dex/service/interpreter/channel"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"github.com/superdurable/dex/service/interpreter/timers"
)

type Interpreter struct {
	activities   *Activities
	sharedConfig *config.Config
}

func NewInterpreter(sharedConfig *config.Config, activities *Activities) *Interpreter {
	if sharedConfig == nil || activities == nil {
		panic("Interpreter requires non-nil dependencies")
	}
	return &Interpreter{
		activities:   activities,
		sharedConfig: sharedConfig,
	}
}

func (i *Interpreter) StartEngineFlow(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	input *dexpb.InterpreterWorkflowInput,
) (out *dexpb.InterpreterWorkflowOutput, retErr error) {
	if provider == nil || input == nil {
		panic("Interpreter requires non-nil dependencies")
	}
	invokeRpcBootstrap := newInvokeRpcBootstrap(provider)
	if err := invokeRpcBootstrap.register(ctx); err != nil {
		return nil, err
	}

	var persistenceManager *PersistenceManager

	globalVersioner := NewGlobalVersioner(provider, ctx)
	flowConfiger := interpreterconfig.NewFlowConfiger(input.GetConfig())
	runStartedTimestamp := provider.Now(ctx).Unix()
	basicInfo := service.BasicInfo{
		FlowType:            input.GetFlowType(),
		RunStartedTimestamp: runStartedTimestamp,
	}

	var channelStore *ChannelStore
	var stepRequestQueue *StepRequestQueue
	var timerProcessor interfaces.TimerProcessor
	var continueAsNewCounter *cont.ContinueAsNewCounter
	var signalReceiver *SignalReceiver
	var stepExecutionCounter *StepExecutionCounter
	var outputCollector *OutputCollector
	var continueAsNewer *ContinueAsNewer
	var attributeSynchronizer *AttributeSynchronizer
	if input.GetIsResumeFromContinueAsNew() {
		previous, err := i.LoadInternalsFromPreviousRun(
			ctx,
			provider,
			input.GetContinueAsNewInput().GetPreviousInternalRunId(),
			flowConfiger.EffectiveContinueAsNewPageSizeInBytes(),
		)
		if err != nil {
			return nil, err
		}

		// The below initialization order should be the same as for non-continueAsNew

		channelStore = RebuildChannelStore(previous.GetChannelReceived())
		stepRequestQueue = NewStepRequestQueueWithResumeRequests(
			previous.GetStepsToStartFromBeginning(),
			previous.GetStepExecutionsToResume(),
		)
		continueAsNewCounter = cont.NewContinueAsCounter(flowConfiger, ctx, provider)
		attributeSynchronizer = NewAttributeSynchronizer(
			&i.sharedConfig.AttributeStore,
			i.activities,
			provider,
			ctx,
			continueAsNewCounter,
			previous.GetPendingAttributeSyncItems(),
		)
		persistenceManager = NewPersistenceManager(
			provider,
			previous.GetAttributes(),
			attributeSynchronizer,
			flowConfiger,
		)
		timerProcessor = timers.NewGreedyTimerProcessor(
			ctx,
			provider,
			continueAsNewCounter,
			previous.GetStaleSkipTimers(),
		)
		stepExecutionCounter = RebuildStepExecutionCounter(
			ctx,
			provider,
			flowConfiger,
			continueAsNewCounter,
			previous.GetCounterInfo(),
		)
		outputCollector = NewOutputCollector(previous.GetStepOutputs())
		continueAsNewer = NewContinueAsNewer(
			&i.sharedConfig.Api,
			provider,
			invokeRpcBootstrap,
			channelStore,
			stepExecutionCounter,
			persistenceManager,
			stepRequestQueue,
			outputCollector,
			timerProcessor,
			attributeSynchronizer,
		)
	} else {
		channelStore = NewChannelStore()
		stepRequestQueue = NewStepRequestQueue()
		continueAsNewCounter = cont.NewContinueAsCounter(flowConfiger, ctx, provider)
		attributeSynchronizer = NewAttributeSynchronizer(
			&i.sharedConfig.AttributeStore,
			i.activities,
			provider,
			ctx,
			continueAsNewCounter,
			nil,
		)
		persistenceManager = NewPersistenceManager(
			provider,
			attributeWritesToKVs(input.GetInitAttributes()),
			attributeSynchronizer,
			flowConfiger,
		)
		attributeSynchronizer.AppendingToPendings(
			input.GetInitAttributes(),
			flowConfiger.Get().GetAttributeSyncConfigName(),
		)
		timerProcessor = timers.NewGreedyTimerProcessor(
			ctx,
			provider,
			continueAsNewCounter,
			nil,
		)
		stepExecutionCounter = NewStepExecutionCounter(
			ctx,
			provider,
			flowConfiger,
			continueAsNewCounter,
		)
		outputCollector = NewOutputCollector(nil)
		continueAsNewer = NewContinueAsNewer(
			&i.sharedConfig.Api,
			provider,
			invokeRpcBootstrap,
			channelStore,
			stepExecutionCounter,
			persistenceManager,
			stepRequestQueue,
			outputCollector,
			timerProcessor,
			attributeSynchronizer,
		)
	}
	signalReceiver = NewSignalReceiver(
		ctx,
		provider,
		channelStore,
		stepRequestQueue,
		persistenceManager,
		timerProcessor,
		continueAsNewCounter,
		flowConfiger,
	)
	attributeSynchronizer.Start()

	// we need these global varirables because sub threads(goroutine) need to report error back
	// to main goroutine to return.
	// Note that different errors could overwrite each other.
	// We only support last one wins. we may use multiError to improve.
	var errToFailWf error
	var forceCompleteWf bool
	var shouldGracefulComplete bool

	terminalCoordinator := NewTerminalCoordinator(
		provider,
		ctx,
		continueAsNewer,
		attributeSynchronizer,
		signalReceiver,
		&forceCompleteWf,
	)
	workflowUpdater, updateErr := NewWorkflowUpdater(
		&i.sharedConfig.Api,
		i.activities,
		ctx,
		provider,
		persistenceManager,
		stepRequestQueue,
		continueAsNewer,
		continueAsNewCounter,
		channelStore,
		signalReceiver,
		terminalCoordinator,
		stepExecutionCounter,
		flowConfiger,
		basicInfo,
	)
	if updateErr != nil {
		return nil, updateErr
	}
	invokeRpcBootstrap.updater = workflowUpdater

	defer func() {
		retErr = terminalCoordinator.CoordinateAndFinalizeError(retErr)
		if provider.IsReplaying(ctx) {
			return
		}
		eventType := event.EventTypeUnspecified
		if retErr == nil {
			eventType = event.EventTypeFlowComplete
		} else if provider.IsApplicationError(retErr) {
			eventType = event.EventTypeFlowFail
		}
		if eventType == event.EventTypeUnspecified {
			return
		}
		info := provider.GetWorkflowInfo(ctx)
		var attributes []*dexpb.KV
		if persistenceManager != nil {
			attributes = persistenceManager.GetAllAttributes()
		}
		// send metrics for the workflow result
		event.Handle(event.Event{
			FlowId:             info.WorkflowExecution.ID,
			RunId:              info.WorkflowExecution.RunID,
			FlowType:           input.GetFlowType(),
			EventType:          eventType,
			StartTimestampInMs: info.WorkflowStartTime.UnixMilli(),
			Attributes:         attributes,
		})
	}()
	// We intentionally set the query handler after the continueAsNew/dumpInternal activity.
	// This is to ensure the correctness. If we set the query handler before that,
	// the query handler could return empty data (since the loading hasn't completed), which will be incorrect response.
	// We would rather return server errors and let the client retry later.
	if err := SetQueryHandlers(
		ctx,
		provider,
		timerProcessor,
		persistenceManager,
		channelStore,
		continueAsNewer,
		stepExecutionCounter,
		flowConfiger,
		basicInfo,
	); err != nil {
		return nil, err
	}

	if !input.GetIsResumeFromContinueAsNew() {
		// it's possible that a flow is started without any starting step
		// it will wait for a new step coming in (by RPC results)
		if input.GetStartStepType() != "" {
			stepRequestQueue.AddSingleStepStartRequest(
				input.GetStartStepType(),
				input.GetStepInput(),
				input.GetStepOptions(),
				service.StartingStepFromStepExecutionId,
				nil,
			)
		}

		if !provider.IsReplaying(ctx) {
			info := provider.GetWorkflowInfo(ctx)
			event.Handle(event.Event{
				FlowId:             info.WorkflowExecution.ID,
				RunId:              info.WorkflowExecution.RunID,
				FlowType:           basicInfo.FlowType,
				EventType:          event.EventTypeFlowStart,
				StartTimestampInMs: info.WorkflowStartTime.UnixMilli(),
				Attributes:         persistenceManager.GetAllAttributes(),
			})
		}
	}

	for {
		if err := provider.Await(ctx, func() bool {
			return !stepRequestQueue.IsEmpty() || signalReceiver.IsStopFlowRequested() || shouldGracefulComplete || continueAsNewCounter.IsThresholdMet()
		}); err != nil {
			return nil, err
		}

		if stopBySignal, stopErr := signalReceiver.GetIfStopFlowRequested(); stopBySignal {
			return &dexpb.InterpreterWorkflowOutput{
				StepCompletionOutputs: outputCollector.GetAll(),
			}, stopErr
		}

		// gracefully complete flow when all steps are executed to dead ends
		if shouldGracefulComplete && stepRequestQueue.IsEmpty() {
			break
		}

		for !stepRequestQueue.IsEmpty() {
			var stepsToExecute []StepRequest
			if !continueAsNewCounter.IsThresholdMet() {
				stepsToExecute = stepRequestQueue.TakeAll()
				if err := stepExecutionCounter.MarkStepTypeActiveIfNotYet(
					stepsToExecute,
				); err != nil {
					return nil, err
				}
			}

			for _, stepReqForLoopingOnly := range stepsToExecute {
				// execute in another thread for parallelism
				// step must be passed via parameter https://stackoverflow.com/questions/67263092
				stepCtx := provider.ExtendContextWithValue(ctx, "stepRequest", stepReqForLoopingOnly)
				attributeSynchronizer.ProducerStarted()
				provider.GoNamed(stepCtx, "step-execution-thread:"+stepReqForLoopingOnly.GetStepType(), func(ctx interfaces.UnifiedContext) {
					defer attributeSynchronizer.ProducerFinished()
					stepRequest, ok := provider.GetContextValue(
						ctx,
						"stepRequest",
					).(StepRequest)
					if !ok {
						errToFailWf = provider.NewFlowError(
							dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
							&dexpb.ErrorResponse{Detail: "cannot read step request from workflow context"},
						)
						return
					}

					step := stepRequest.GetStepMovement()
					var stepExeId string
					if stepRequest.IsResumeRequest() {
						stepExeId = stepRequest.GetStepResumeRequest().
							GetStepExecutionId()
					} else {
						stepExeId = stepExecutionCounter.CreateNextExecutionId(step.GetStepType())
					}
					continueAsNewer.TrackActiveStep(stepExeId, step)

					decision, stepExecutionStatus, stepExeErr := i.processStepExecution(ctx, provider,
						basicInfo,
						stepRequest,
						stepExeId,
						persistenceManager,
						channelStore,
						timerProcessor,
						continueAsNewer,
						continueAsNewCounter,
						stepExecutionCounter,
						flowConfiger,
						globalVersioner,
						signalReceiver,
					)
					if stepExecutionStatus == service.StepExecutionStatusInternalError {
						errToFailWf = stepExeErr
						return
					}
					if stepExecutionStatus == service.StepExecutionStatusFailedNoProceed && stepExeErr != nil {
						// this is the case where stepExecutionStatus == FailureStepExecutionStatus
						errToFailWf = normalizeStepFailureError(provider, stepExeErr)
						// step execution fail should fail the flow, no more processing
						return
					}
					if stepExecutionStatus == service.StepExecutionStatusCompleted {
						// NOTE: decision is only available on this CompletedStepExecutionStatus
						canGoNext, gracefulComplete, forceComplete, forceFail, output, checkErr := checkClosingWorkflow(
							ctx,
							provider,
							decision,
							step.GetStepType(),
							stepExeId,
							channelStore,
							signalReceiver,
						)
						if checkErr != nil {
							errToFailWf = checkErr
							// no return so that it can fall through to call MarkStepExecutionCompleted
						}
						if gracefulComplete {
							shouldGracefulComplete = true
						}
						if (gracefulComplete || forceComplete) && output != nil {
							outputCollector.Add(output)
						}
						if forceComplete {
							forceCompleteWf = true
						}
						if forceFail {
							detail := ""
							if output != nil {
								detail = getFlowFailedDetailFromValue(output.GetCompletedStepOutput())
							}
							errToFailWf = provider.NewFlowError(
								dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
								&dexpb.ErrorResponse{Detail: detail},
							)
						}
						if canGoNext {
							stepRequestQueue.AddStepStartRequests(decision.GetNextSteps())
						}
						// finally, mark step completed and may also update system search attribute
						continueAsNewer.RemoveActiveStep(stepExeId)
						if err := stepExecutionCounter.MarkStepExecutionCompleted(
							step,
							stepExeId,
							decision.GetNextSteps(),
						); err != nil {
							errToFailWf = err
						}
					} else if stepExecutionStatus == service.StepExecutionStatusFailedAndProceed {
						recoveryError, mappingErr := provider.MapToWorkerError(stepExeErr)
						if mappingErr != nil {
							errToFailWf = provider.NewFlowError(
								dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
								&dexpb.ErrorResponse{Detail: mappingErr.Error()},
							)
							return
						}
						options := step.GetStepOptions()
						stepRequestQueue.AddSingleStepStartRequest(
							options.GetExecuteFailureProceedStepType(),
							step.StepInput,
							options.ExecuteFailureProceedStepOptions,
							stepExeId,
							recoveryError,
						)
						// finally, mark state completed and may also update activeStepType search attribute
						continueAsNewer.RemoveActiveStep(stepExeId)
						err := stepExecutionCounter.MarkStepExecutionCompleted(
							step,
							stepExeId,
							decision.GetNextSteps(),
						)
						if err != nil {
							errToFailWf = err
						}
					} else if stepExecutionStatus == service.StepExecutionStatusWaitingAborted {
						// NOTE: noop for WaitingCommandsStepExecutionStatus, because it means continueAsNew
					}

				}) // end of executing one step movement goroutine
			} // end loop of executing all steps from the queue for one pass

			// The conditions here are quite tricky:
			// For !stepRequestQueue.IsEmpty(): We need some condition to wait here because all the step executions are running in different threads.
			//    Right after the queue is popped it becomes empty. When it's not empty, it means there are new steps to execute pushed into the queue,
			//    and it's time to wake up the outer loop to go to next iteration. Alternatively, waiting for all current started in this iteration to complete will also work,
			//    but not as efficient as this one because it will take much longer time.
			// For errToFailFlow != nil || forceCompleteFlow: this means we need to close flow immediately
			// For stepExecutionCounter.GetTotalCurrentlyExecutingCount() == 0: this means all the step executions have reached "Dead Ends" so the flow can complete gracefully without output
			// For continueAsNewCounter.IsThresholdMet(): this means flow needs to continueAsNew
			awaitError := provider.Await(ctx, func() bool {
				stopBySignal, stopErr := signalReceiver.GetIfStopFlowRequested()
				if stopBySignal {
					errToFailWf = stopErr
					return true
				}
				return !stepRequestQueue.IsEmpty() ||
					errToFailWf != nil ||
					forceCompleteWf ||
					stepExecutionCounter.GetTotalCurrentlyExecutingCount() == 0 ||
					continueAsNewCounter.IsThresholdMet()
			})
			if continueAsNewCounter.IsThresholdMet() {
				// NOTE: drain thread before checking errToFailFlow/forceCompleteFlow so that we can close the flow if possible
				if err := continueAsNewer.DrainThreads(ctx); err != nil {
					awaitError = err
				}
			}

			if errToFailWf != nil || forceCompleteWf {
				return &dexpb.InterpreterWorkflowOutput{
					StepCompletionOutputs: outputCollector.GetAll(),
				}, errToFailWf
			}
			if awaitError != nil {
				// this could happen for cancellation
				return nil, awaitError
			}
			if continueAsNewCounter.IsThresholdMet() {
				// the outer logic will do the actual continue as new
				break
			}
		} // end loop until no more step can be executed (dead end)

		if continueAsNewCounter.IsThresholdMet() {
			// we have to drain this again because this can be from non-step cases
			if err := continueAsNewer.DrainThreads(ctx); err != nil {
				errToFailWf = err
				break
			}
			// NOTE: This must be the last thing before continueAsNew!!!
			// Otherwise, there could be signals unhandled
			signalReceiver.DrainAllReceivedButUnprocessedSignals(ctx)

			// after draining signals, there could be some changes like
			// last fail flow signal, return the flow so that we don't carry over the fail request
			if stopBySignal, stopErr := signalReceiver.GetIfStopFlowRequested(); stopBySignal {
				return &dexpb.InterpreterWorkflowOutput{
					StepCompletionOutputs: outputCollector.GetAll(),
				}, stopErr
			}
			if stepRequestQueue.IsEmpty() && !continueAsNewer.HasAnyStepExecutionToResume() && shouldGracefulComplete {
				// if it is empty and no stepExecutionsToResume and request a graceful complete just complete the loop
				// so that we don't carry over shouldGracefulComplete
				return &dexpb.InterpreterWorkflowOutput{
					StepCompletionOutputs: outputCollector.GetAll(),
				}, nil
			}
			// last update config, do it here because we use input to carry over config, not continueAsNewer query
			input.Config = flowConfiger.Get()
			input.IsResumeFromContinueAsNew = true
			input.ContinueAsNewInput = &dexpb.ContinueAsNewInput{
				PreviousInternalRunId: provider.GetWorkflowInfo(ctx).WorkflowExecution.RunID,
			}
			// nix the unused data
			input.StartStepType = ""
			input.StepInput = nil
			input.StepOptions = nil
			input.InitAttributes = nil
			return nil, provider.NewInterpreterContinueAsNewError(ctx, input)
		}

	} // end main loop

	// gracefully complete workflow when all states are executed to dead ends
	return &dexpb.InterpreterWorkflowOutput{
		StepCompletionOutputs: outputCollector.GetAll(),
	}, errToFailWf
}

func normalizeStepFailureError(
	provider interfaces.WorkflowProvider,
	err error,
) error {
	if err == nil || provider.IsApplicationError(err) {
		return err
	}
	// Non-application activity failures (timeout, cancel, etc.) as worker API fail.
	return provider.NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		&dexpb.ErrorResponse{
			Detail:    err.Error(),
			SubStatus: dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		},
	)
}

func checkClosingWorkflow(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	decision *dexpb.StepDecision,
	currentStepType, currentStepExeId string,
	channelStore *ChannelStore,
	signalReceiver *SignalReceiver,
) (
	canGoNext bool,
	gracefulComplete bool,
	forceComplete bool,
	forceFail bool,
	completeOutput *dexpb.StepCompletionOutput,
	err error,
) {
	closeDecision := decision.GetCloseDecision()
	if closeDecision == nil {
		canGoNext = true
		return
	}

	switch closeDecision.GetCloseDecisionType() {
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY:
		// trigger a signal draining so that all the channel messages are processed
		signalReceiver.DrainAllReceivedButUnprocessedSignals(ctx)
		// Messages of channels could be published via step executions, within the same workflow task.
		// If we don't do any draining and process them, the conditional completion could lose the messages
		// Just yield, by waiting on an empty lambda, nothing else.
		// It will let other workflow threads/coroutines run.
		// This will drain the messages published from step APIs.
		// NOTE that this is extremely tricky in Cadence/Temporal programming model.
		// Read more: https://stackoverflow.com/questions/71356668/how-does-multi-threading-works-in-cadence-temporal-workflow
		// https://docs.temporal.io/encyclopedia/go-sdk-multithreading
		if err = provider.Await(ctx, func() bool { return true }); err != nil {
			return
		}

		conditionMet := true
		for _, channelName := range closeDecision.GetConditionalChannelNames() {
			if channelStore.HasData(channelName) {
				conditionMet = false
			}
		}

		if conditionMet {
			// condition is met, force complete the workflow
			forceComplete = true
			completeOutput = &dexpb.StepCompletionOutput{
				CompletedStepType:        currentStepType,
				CompletedStepExecutionId: currentStepExeId,
				CompletedStepOutput:      closeDecision.GetCloseInput(),
			}
			return
		}
		canGoNext = true
		return
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE:
		gracefulComplete = true
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE:
		forceComplete = true
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL:
		forceFail = true
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END:
		return
	default:
		err = provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			&dexpb.ErrorResponse{Detail: "invalid close decision type"},
		)
		return
	}

	completeOutput = &dexpb.StepCompletionOutput{
		CompletedStepType:        currentStepType,
		CompletedStepExecutionId: currentStepExeId,
		CompletedStepOutput:      closeDecision.GetCloseInput(),
	}
	return
}

func (i *Interpreter) processStepExecution(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	stepRequest StepRequest,
	stepExeId string,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	timerProcessor interfaces.TimerProcessor,
	continueAsNewer *ContinueAsNewer,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	stepExecutionCounter *StepExecutionCounter,
	flowConfiger *interpreterconfig.FlowConfiger,
	globalVersioner *GlobalVersioner,
	signalReceiver *SignalReceiver,
) (*dexpb.StepDecision, service.StepExecutionStatus, error) {
	info := provider.GetWorkflowInfo(ctx)
	step := stepRequest.GetStepMovement()
	executionContext := &dexpb.Context{
		FlowId:               info.WorkflowExecution.ID,
		RunId:                info.FirstRunID,
		FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
		StepExecutionId:      stepExeId,
		FromStepExecutionId:  step.GetFromStepExecutionIdInternalOnly(),
		RecoveryError:        step.GetRecoveryErrorInternalOnly(),
	}
	activityOptions := interfaces.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	}
	if globalVersioner.UsesDeterministicStepActivityIDs() {
		activityOptions.ActivityID = service.WaitForStepActivityID(stepExeId)
	}

	var waitForMethErr error
	var stepExeLocals []*dexpb.KV
	var waitingCondition *dexpb.WaitingCondition
	var transientStep *dexpb.StepMovement
	//This variable tells all (timer) condition threads to stop waiting and exit, even if their specific condition has not been completed.
	waitingConditionDoneOrCanceled := false
	completedTimerConditions := map[int32]dexpb.InternalTimerStatus{}

	isResumeFromContinueAsNew := stepRequest.IsResumeRequest()

	options := step.GetStepOptions()
	if options.GetSkipWaitFor() {
		return i.invokeExecuteMethod(
			ctx,
			provider,
			basicInfo,
			step,
			stepExeId,
			persistenceManager,
			channelStore,
			executionContext,
			nil,
			continueAsNewer,
			flowConfiger,
			stepExeLocals,
			false,
			globalVersioner,
		)
	}

	if isResumeFromContinueAsNew {
		resumeRequest := stepRequest.GetStepResumeRequest()
		stepExeLocals = resumeRequest.GetStepExeLocals()
		waitingCondition = resumeRequest.GetWaitingCondition()
		if resumeRequest.GetCompletedConditions() != nil &&
			resumeRequest.GetCompletedConditions().GetCompletedTimerConditions() != nil {
			completedTimerConditions = resumeRequest.GetCompletedConditions().GetCompletedTimerConditions()
		}
	} else {
		if step.StepOptions != nil {
			waitForMethodTimeout := options.GetWaitForTimeoutSeconds()
			if waitForMethodTimeout > 0 {
				activityOptions.StartToCloseTimeout = time.Duration(waitForMethodTimeout) * time.Second
			}
			activityOptions.RetryPolicy = options.GetWaitForRetryPolicy()
		}

		ctx = provider.WithActivityOptions(ctx, activityOptions)

		lockAttributeKeys := options.GetWaitForLockAttributeKeys()
		attributes := persistenceManager.GetAllAttributes()
		if len(lockAttributeKeys) > 0 {
			loadedAttributes, err := persistenceManager.LoadAttributes(ctx, lockAttributeKeys)
			if err != nil {
				return nil, service.StepExecutionStatusInternalError, err
			}
			attributes = loadedAttributes
		}

		activityInput := &dexpb.InvokeWaitForMethodActivityInput{
			WorkerTarget: flowConfiger.GetWorkerTarget(),
			Request: &dexpb.InvokeWaitForMethodRequest{
				Context:    executionContext,
				FlowType:   basicInfo.FlowType,
				StepType:   step.GetStepType(),
				StepInput:  step.GetStepInput(),
				Attributes: attributes,
			},
		}
		var activityOutput dexpb.InvokeWaitForMethodActivityOutput
		waitForMethErr = provider.ExecuteActivity(
			&activityOutput,
			flowConfiger.ResolveWaitForDurability(options),
			ctx,
			i.activities.InvokeWaitForMethod,
			activityInput,
			&dexpb.InternalLocalActivityInput{
				CurrentRunStartedTimestamp: basicInfo.RunStartedTimestamp,
				MethodOptions:              stepMethodOptions(activityOptions),
			},
		)
		persistenceManager.UnlockKeys(lockAttributeKeys)

		if waitForMethErr != nil && !shouldProceedOnWaitForMethodError(step) {
			return nil, service.StepExecutionStatusFailedNoProceed, waitForMethErr
		}

		if waitForMethErr == nil {
			waitingCondition = activityOutput.Response.GetWaitingCondition()
			if err := persistenceManager.ApplyAttributeWrites(
				ctx,
				activityOutput.Response.GetUpsertAttributes(),
			); err != nil {
				return nil, service.StepExecutionStatusInternalError, err
			}
			channelStore.ProcessPublishing(activityOutput.Response.GetPublishToChannel())
			waitingCondition = activityOutput.Response.GetWaitingCondition()
			stepExeLocals = activityOutput.Response.GetUpsertStepExeLocals()
			transientStep = activityOutput.Response.GetTransientStepMovement()
		}
	}
	if transientStep != nil {
		transientStatus, transientErr := i.processTransientStepExecution(
			ctx,
			provider,
			basicInfo,
			transientStep,
			persistenceManager,
			channelStore,
			continueAsNewer,
			stepExecutionCounter,
			flowConfiger,
			globalVersioner,
		)
		if transientStatus != service.StepExecutionStatusCompleted {
			return nil, transientStatus, transientErr
		}
	}
	if !isResumeFromContinueAsNew && waitingCondition != nil {
		waitingCondition = timers.FixTimerConditionFromActivityOutput(
			provider.Now(ctx),
			waitingCondition,
		)
	}
	if waitingCondition == nil {
		waitingCondition = &dexpb.WaitingCondition{}
	}

	waitForThreads := map[string]bool{}

	if len(waitingCondition.GetTimerConditions()) > 0 {
		timerProcessor.AddTimers(
			stepExeId,
			waitingCondition.GetTimerConditions(),
			completedTimerConditions,
		)
		for idx, timerCondition := range waitingCondition.GetTimerConditions() {
			if _, ok := completedTimerConditions[int32(idx)]; ok {
				// skip the completed timers(from continueAsNew)
				continue
			}
			timerCtx := provider.ExtendContextWithValue(ctx, "timerIndex", idx)
			//Start timer in a new thread
			threadName := fmt.Sprintf(
				"%s-timer-%d-%s",
				stepExeId,
				idx,
				timerCondition.GetConditionId(),
			)
			waitForThreads[threadName] = false
			provider.GoNamed(timerCtx, threadName, func(ctx interfaces.UnifiedContext) {
				index, ok := provider.GetContextValue(ctx, "timerIndex").(int)
				if !ok {
					panic("cannot read timer index from workflow context")
				}

				// Note that waitingConditionDoneOrCanceled is needed for two cases:
				// 1. will be true when trigger type of the waitingCondition is completed(e.g. AnyCompleted) so we don't need to wait for all conditions. Returning the thread to avoid thread leakage.
				// 2. will be true to cancel the wait for unblocking continueAsNew(continueAsNew will wait for all threads to complete)
				status := timerProcessor.WaitForTimerFiredOrSkipped(
					ctx,
					stepExeId,
					index,
					&waitingConditionDoneOrCanceled,
				)
				if status == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
					status == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
					completedTimerConditions[int32(index)] = status
				}
				waitForThreads[threadName] = true
			})
		}
	}

	//Passing a map of references of completed or soon to be completed conditions (once the above threads are complete) and the step execution variables to the continueAsNewer.
	//After this method completes and if continueAsNewCounter.IsThresholdMet() is true, this snapshot will be used to start a new continueAsNew flow while preserving the state of the flow at the end of this method.
	//This snapshot is also used to query the flow state, which can be done at anytime.
	continueAsNewer.AddPotentialStepExecutionToResume(
		&dexpb.StepExecutionResumeInfo{
			StepExecutionId: stepExeId,
			Step:            step,
			CompletedConditions: &dexpb.StepExecutionCompletedConditions{
				CompletedTimerConditions: completedTimerConditions,
			},
			WaitingCondition: waitingCondition,
			StepExeLocals:    stepExeLocals,
		},
	)

	var matchPlan *channel.MatchPlan
	var conditionMet bool

	// Wait for condition met, stop signal, or continue-as-new threshold
	_ = provider.Await(ctx, func() bool {
		matchPlan, conditionMet = channel.Plan(
			waitingCondition,
			channelStore.Availability(),
			completedTimerConditions,
		)
		return conditionMet || signalReceiver.IsStopFlowRequested() || continueAsNewCounter.IsThresholdMet()
	})

	waitingConditionDoneOrCanceled = true

	_ = provider.Await(ctx, func() bool {
		for _, isCompleted := range waitForThreads {
			if !isCompleted {
				return false
			}
		}
		return true
	})

	if signalReceiver.IsStopFlowRequested() || !conditionMet {
		// this means stop was requested or continueAsNewCounter.IsThresholdMet == true
		// not using only continueAsNewCounter.IsThresholdMet because matchPlan has higher priority without a terminal request
		// it won't continueAsNew in those cases
		// 1. waitFor method fail with proceed policy
		// 2. empty condition
		// 3. both condition and continueAsNewThreshold are met
		return nil, service.StepExecutionStatusWaitingAborted, nil
	}

	if len(waitingCondition.GetTimerConditions()) > 0 {
		timerProcessor.RemovePendingTimersOfStep(stepExeId)
	}

	consumed := channelStore.CommitMatch(matchPlan)
	conditionResults := channel.BuildConditionResults(
		waitingCondition,
		completedTimerConditions,
		consumed,
	)
	if waitForMethErr != nil {
		conditionResults.WaitForFailed = true
		recoveryError, mappingErr := provider.MapToWorkerError(waitForMethErr)
		if mappingErr != nil {
			return nil, service.StepExecutionStatusInternalError, mappingErr
		}
		executionContext.RecoveryError = recoveryError
	}

	return i.invokeExecuteMethod(
		ctx,
		provider,
		basicInfo,
		step,
		stepExeId,
		persistenceManager,
		channelStore,
		executionContext,
		conditionResults,
		continueAsNewer,
		flowConfiger,
		stepExeLocals,
		false,
		globalVersioner,
	)
}

func (i *Interpreter) processTransientStepExecution(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	step *dexpb.StepMovement,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	continueAsNewer *ContinueAsNewer,
	stepExecutionCounter *StepExecutionCounter,
	flowConfiger *interpreterconfig.FlowConfiger,
	globalVersioner *GlobalVersioner,
) (service.StepExecutionStatus, error) {
	stepRequest := NewStepStartRequest(step)
	if err := stepExecutionCounter.MarkStepTypeActiveIfNotYet(
		[]StepRequest{stepRequest},
	); err != nil {
		return service.StepExecutionStatusInternalError, err
	}

	stepExecutionId := stepExecutionCounter.CreateNextExecutionId(step.GetStepType())
	continueAsNewer.TrackActiveStep(stepExecutionId, step)
	info := provider.GetWorkflowInfo(ctx)
	executionContext := &dexpb.Context{
		FlowId:               info.WorkflowExecution.ID,
		RunId:                info.FirstRunID,
		FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
		StepExecutionId:      stepExecutionId,
		FromStepExecutionId:  step.GetFromStepExecutionIdInternalOnly(),
		RecoveryError:        step.GetRecoveryErrorInternalOnly(),
	}
	decision, status, err := i.invokeExecuteMethod(
		ctx,
		provider,
		basicInfo,
		step,
		stepExecutionId,
		persistenceManager,
		channelStore,
		executionContext,
		nil,
		continueAsNewer,
		flowConfiger,
		nil,
		true,
		globalVersioner,
	)
	if status != service.StepExecutionStatusCompleted {
		return status, err
	}
	continueAsNewer.RemoveActiveStep(stepExecutionId)
	if err := stepExecutionCounter.MarkStepExecutionCompleted(
		step,
		stepExecutionId,
		decision.GetNextSteps(),
	); err != nil {
		return service.StepExecutionStatusInternalError, err
	}
	return service.StepExecutionStatusCompleted, nil
}

func (i *Interpreter) invokeExecuteMethod(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	step *dexpb.StepMovement,
	stepExeId string,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	executionContext *dexpb.Context,
	conditionResults *dexpb.ConditionResults,
	continueAsNewer *ContinueAsNewer,
	flowConfiger *interpreterconfig.FlowConfiger,
	stepExeLocals []*dexpb.KV,
	isTransientStep bool,
	globalVersioner *GlobalVersioner,
) (*dexpb.StepDecision, service.StepExecutionStatus, error) {
	var err error
	activityOptions := interfaces.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	}
	if globalVersioner.UsesDeterministicStepActivityIDs() {
		activityOptions.ActivityID = service.ExecuteStepActivityID(stepExeId)
	}
	if step.StepOptions != nil {
		executeMethodTimeout := step.GetStepOptions().GetExecuteTimeoutSeconds()
		if executeMethodTimeout > 0 {
			activityOptions.StartToCloseTimeout = time.Duration(executeMethodTimeout) * time.Second
		}
		activityOptions.RetryPolicy = step.GetStepOptions().GetExecuteRetryPolicy()
	}

	ctx = provider.WithActivityOptions(ctx, activityOptions)

	lockAttributeKeys := step.GetStepOptions().GetExecuteLockAttributeKeys()
	attributes := persistenceManager.GetAllAttributes()
	if len(lockAttributeKeys) > 0 {
		attributes, err = persistenceManager.LoadAttributes(ctx, lockAttributeKeys)
		if err != nil {
			return nil, service.StepExecutionStatusInternalError, err
		}
	}

	continueAsNewer.RemoveStepExecutionToResume(stepExeId)
	activityInput := &dexpb.InvokeExecuteMethodActivityInput{
		WorkerTarget:    flowConfiger.GetWorkerTarget(),
		IsTransientStep: isTransientStep,
		Request: &dexpb.InvokeExecuteMethodRequest{
			Context:          executionContext,
			FlowType:         basicInfo.FlowType,
			StepType:         step.GetStepType(),
			StepInput:        step.GetStepInput(),
			Attributes:       attributes,
			StepExeLocals:    stepExeLocals,
			ConditionResults: conditionResults,
		},
	}
	var activityOutput dexpb.InvokeExecuteMethodActivityOutput
	exeMethErr := provider.ExecuteActivity(
		&activityOutput,
		flowConfiger.ResolveExecuteDurability(step.GetStepOptions()),
		ctx,
		i.activities.InvokeExecuteMethod,
		activityInput,
		&dexpb.InternalLocalActivityInput{
			CurrentRunStartedTimestamp: basicInfo.RunStartedTimestamp,
			MethodOptions:              stepMethodOptions(activityOptions),
		},
	)
	// always unlock regardless of step success/failure
	persistenceManager.UnlockKeys(lockAttributeKeys)

	if exeMethErr != nil {
		if shouldProceedOnExecuteMethodError(step) {
			return nil, service.StepExecutionStatusFailedAndProceed, exeMethErr
		}
		return nil, service.StepExecutionStatusFailedNoProceed, exeMethErr
	}
	executeResponse := activityOutput.GetResponse()
	if err := persistenceManager.ApplyAttributeWrites(
		ctx,
		executeResponse.GetUpsertAttributes(),
	); err != nil {
		return nil, service.StepExecutionStatusInternalError, err
	}
	channelStore.ProcessPublishing(executeResponse.GetPublishToChannel())

	return executeResponse.GetStepDecision(), service.StepExecutionStatusCompleted, nil
}

func stepMethodOptions(options interfaces.ActivityOptions) *dexpb.StepMethodOptions {
	return &dexpb.StepMethodOptions{
		TimeoutSeconds: int32(options.StartToCloseTimeout / time.Second),
		RetryPolicy:    retry.ActivityRetryPolicyWithDefaults(options.RetryPolicy),
	}
}

func shouldProceedOnWaitForMethodError(step *dexpb.StepMovement) bool {
	return step.GetStepOptions().GetWaitForFailurePolicy() ==
		dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE
}

func shouldProceedOnExecuteMethodError(step *dexpb.StepMovement) bool {
	options := step.GetStepOptions()
	return options.GetExecuteFailureProceedStepType() != "" &&
		options.GetExecuteFailurePolicy() ==
			dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP
}

func (i *Interpreter) BlobStoreCleanup(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	storeId string,
) (int, error) {
	activityCtx := provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
		StartToCloseTimeout: 24 * time.Hour,
		RetryPolicy:         &dexpb.RetryPolicy{MaximumAttempts: 10},
	})
	var output dexpb.CleanupBlobStoreActivityOutput
	if err := provider.ExecuteActivity(
		&output,
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		activityCtx,
		i.activities.CleanupBlobsAfterAllRunsDeleted,
		&dexpb.CleanupBlobStoreActivityInput{
			StoreId: storeId,
		},
		nil,
	); err != nil {
		return 0, err
	}
	return int(output.GetTotalDeleted()), nil
}

func getFlowFailedDetailFromValue(value *dexpb.Value) string {
	if value == nil {
		return ""
	}
	return value.GetStringValue()
}

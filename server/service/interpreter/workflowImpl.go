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
	"fmt"
	"time"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/event"
	"github.com/superdurable/iwf/service/interpreter/channel"
	interpreterconfig "github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/cont"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"github.com/superdurable/iwf/service/interpreter/timers"
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
	input *iwfpb.InterpreterWorkflowInput,
) (out *iwfpb.InterpreterWorkflowOutput, retErr error) {
	if provider == nil || input == nil {
		panic("Interpreter requires non-nil dependencies")
	}

	var persistenceManager *PersistenceManager
	defer func() {
		if provider.IsReplaying(ctx) {
			return
		}
		eventType := ""
		if retErr == nil {
			eventType = "FLOW_COMPLETE"
		} else if provider.IsApplicationError(retErr) {
			eventType = "FLOW_FAIL"
		}
		if eventType == "" {
			return
		}
		info := provider.GetWorkflowInfo(ctx)
		var attributes []*iwfpb.KV
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

	NewGlobalVersioner(provider, ctx)
	flowConfiger := interpreterconfig.NewFlowConfiger(input.GetConfig())
	basicInfo := service.BasicInfo{
		FlowType:     input.GetFlowType(),
		WorkerTarget: input.GetWorkerTarget(),
	}

	var channelStore *ChannelStore
	var stepRequestQueue *StepRequestQueue
	var timerProcessor interfaces.TimerProcessor
	var continueAsNewCounter *cont.ContinueAsNewCounter
	var signalReceiver *SignalReceiver
	var stepExecutionCounter *StepExecutionCounter
	var outputCollector *OutputCollector
	var continueAsNewer *ContinueAsNewer
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
		persistenceManager = NewPersistenceManager(provider, previous.GetAttributes())
		continueAsNewCounter = cont.NewContinueAsCounter(flowConfiger, ctx, provider)
		timerProcessor = timers.NewGreedyTimerProcessor(
			ctx,
			provider,
			continueAsNewCounter,
			previous.GetStaleSkipTimers(),
		)
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
			channelStore,
			stepExecutionCounter,
			persistenceManager,
			stepRequestQueue,
			outputCollector,
			timerProcessor,
		)
	} else {
		channelStore = NewChannelStore()
		stepRequestQueue = NewStepRequestQueue()
		persistenceManager = NewPersistenceManager(provider, input.GetInitAttributes())
		continueAsNewCounter = cont.NewContinueAsCounter(flowConfiger, ctx, provider)
		timerProcessor = timers.NewGreedyTimerProcessor(
			ctx,
			provider,
			continueAsNewCounter,
			nil,
		)
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
			channelStore,
			stepExecutionCounter,
			persistenceManager,
			stepRequestQueue,
			outputCollector,
			timerProcessor,
		)
	}

	updateErr := NewWorkflowUpdater(
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
		stepExecutionCounter,
		basicInfo,
	)
	if updateErr != nil {
		return nil, updateErr
	}
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
		flowConfiger,
		basicInfo,
	); err != nil {
		return nil, err
	}

	var errToFailWf error // Note that today different errors could overwrite each other, we only support last one wins. we may use multiError to improve.
	var forceCompleteWf bool
	var shouldGracefulComplete bool

	if !input.GetIsResumeFromContinueAsNew() {
		// it's possible that a flow is started without any starting step
		// it will wait for a new step coming in (by RPC results)
		if input.GetStartStepType() != "" {
			stepRequestQueue.AddSingleStepStartRequest(
				input.GetStartStepType(),
				input.GetStepInput(),
				input.GetStepOptions(),
			)
		}

		if !provider.IsReplaying(ctx) {
			info := provider.GetWorkflowInfo(ctx)
			event.Handle(event.Event{
				FlowId:             info.WorkflowExecution.ID,
				RunId:              info.WorkflowExecution.RunID,
				FlowType:           basicInfo.FlowType,
				EventType:          "FLOW_START",
				StartTimestampInMs: info.WorkflowStartTime.UnixMilli(),
				Attributes:         persistenceManager.GetAllAttributes(),
			})
		}
	}

	for {
		if err := provider.Await(ctx, func() bool {
			failFlowByClient, _ := signalReceiver.IsFailWorkFlowRequested()
			return !stepRequestQueue.IsEmpty() || failFlowByClient || shouldGracefulComplete || continueAsNewCounter.IsThresholdMet()
		}); err != nil {
			return nil, err
		}

		failWorkflowByClient, failErr := signalReceiver.IsFailWorkFlowRequested()
		if failWorkflowByClient {
			return nil, failErr
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
				provider.GoNamed(stepCtx, "step-execution-thread:"+stepReqForLoopingOnly.GetStepType(), func(ctx interfaces.UnifiedContext) {
					stepRequest, ok := provider.GetContextValue(
						ctx,
						"stepRequest",
					).(StepRequest)
					if !ok {
						errToFailWf = provider.NewWorkflowError(
							iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
							"cannot read step request from workflow context",
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

					decision, stepExecutionStatus, stepExeErr := i.processStepExecution(ctx, provider,
						basicInfo,
						stepRequest,
						stepExeId,
						persistenceManager,
						channelStore,
						timerProcessor,
						continueAsNewer,
						continueAsNewCounter,
						flowConfiger,
					)
					if stepExeErr != nil {
						// this is the case where stepExecutionStatus == FailureStepExecutionStatus
						errToFailWf = stepExeErr
						// step execution fail should fail the flow, no more processing
						return
					}
					if stepExecutionStatus == service.CompletedStepExecutionStatus {
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
						if (gracefulComplete || forceComplete || forceFail) && output != nil {
							outputCollector.Add(output)
						}
						if forceComplete {
							forceCompleteWf = true
						}
						if forceFail {
							errToFailWf = provider.NewWorkflowError(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW, outputCollector.GetAll())
						}
						if canGoNext {
							stepRequestQueue.AddStepStartRequests(decision.GetNextSteps())
						}
						// finally, mark step completed and may also update system search attribute
						if err := stepExecutionCounter.MarkStepExecutionCompleted(
							step,
							stepExeId,
							decision.GetNextSteps(),
						); err != nil {
							errToFailWf = provider.NewWorkflowError(
								iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
								err.Error(),
							)
						}
					} else if stepExecutionStatus == service.ExecuteApiFailedAndProceed {
						options := step.GetStepOptions()
						stepRequestQueue.AddSingleStepStartRequest(options.GetExecuteFailureProceedStepType(), step.StepInput, options.ExecuteFailureProceedStepOptions)
						// finally, mark state completed and may also update activeStepType search attribute
						err := stepExecutionCounter.MarkStepExecutionCompleted(
							step,
							stepExeId,
							decision.GetNextSteps(),
						)
						if err != nil {
							errToFailWf = err
						}
					} else if stepExecutionStatus == service.WaitingConditionsStepExecutionStatus {
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
				failFlowByClient, failErr := signalReceiver.IsFailWorkFlowRequested()
				if failFlowByClient {
					errToFailWf = failErr
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
				return &iwfpb.InterpreterWorkflowOutput{
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
			failByApi, failErr := signalReceiver.IsFailWorkFlowRequested()
			if failByApi {
				return &iwfpb.InterpreterWorkflowOutput{
					StepCompletionOutputs: outputCollector.GetAll(),
				}, failErr
			}
			if stepRequestQueue.IsEmpty() && !continueAsNewer.HasAnyStepExecutionToResume() && shouldGracefulComplete {
				// if it is empty and no stepExecutionsToResume and request a graceful complete just complete the loop
				// so that we don't carry over shouldGracefulComplete
				return &iwfpb.InterpreterWorkflowOutput{
					StepCompletionOutputs: outputCollector.GetAll(),
				}, nil
			}
			// last update config, do it here because we use input to carry over config, not continueAsNewer query
			input.Config = flowConfiger.Get()
			input.IsResumeFromContinueAsNew = true
			input.ContinueAsNewInput = &iwfpb.ContinueAsNewInput{
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
	return &iwfpb.InterpreterWorkflowOutput{
		StepCompletionOutputs: outputCollector.GetAll(),
	}, errToFailWf
}

func checkClosingWorkflow(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	decision *iwfpb.StepDecision,
	currentStepType, currentStepExeId string,
	channelStore *ChannelStore,
	signalReceiver *SignalReceiver,
) (
	canGoNext bool,
	gracefulComplete bool,
	forceComplete bool,
	forceFail bool,
	completeOutput *iwfpb.StepCompletionOutput,
	err error,
) {
	if conditionalClose := decision.GetConditionalClose(); conditionalClose != nil {
		if conditionalClose.ConditionalCloseType !=
			iwfpb.FlowConditionalCloseType_FLOW_CONDITIONAL_CLOSE_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY {
			err = provider.NewWorkflowError(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
				"invalid step decisions. Unsupported ConditionalCloseType ")
			return
		}
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
		for _, chName := range conditionalClose.ChannelNames {
			if channelStore.HasData(chName) {
				conditionMet = false
			}
		}

		if conditionMet {
			// condition is met, force complete the workflow
			forceComplete = true
			completeOutput = &iwfpb.StepCompletionOutput{
				CompletedStepType:        currentStepType,
				CompletedStepExecutionId: currentStepExeId,
				CompletedStepOutput:      conditionalClose.CloseInput,
			}
			return
		}
		for _, st := range decision.GetNextSteps() {
			if service.ValidClosingFlowStepType[st.GetStepType()] {
				err = provider.NewWorkflowError(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
					"invalid ConditionUnmetDecision with stepType: "+st.GetStepType())
				return
			}
		}

		canGoNext = true
		return

	}

	canGoNext = true
	for _, movement := range decision.GetNextSteps() {
		switch movement.GetStepType() {
		case service.GracefulCompletingFlowStepType:
			canGoNext = false
			gracefulComplete = true
			completeOutput = &iwfpb.StepCompletionOutput{
				CompletedStepType:        currentStepType,
				CompletedStepExecutionId: currentStepExeId,
				CompletedStepOutput:      movement.GetStepInput(),
			}
		case service.ForceCompletingFlowStepType:
			canGoNext = false
			forceComplete = true
			completeOutput = &iwfpb.StepCompletionOutput{
				CompletedStepType:        currentStepType,
				CompletedStepExecutionId: currentStepExeId,
				CompletedStepOutput:      movement.GetStepInput(),
			}
		case service.ForceFailingFlowStepType:
			canGoNext = false
			forceFail = true
			completeOutput = &iwfpb.StepCompletionOutput{
				CompletedStepType:        currentStepType,
				CompletedStepExecutionId: currentStepExeId,
				CompletedStepOutput:      movement.GetStepInput(),
			}
		case service.DeadEndFlowStepType:
			canGoNext = false
		}
	}

	if !canGoNext && len(decision.NextSteps) > 1 {
		// Illegal decision
		err = provider.NewWorkflowError(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
			"invalid state decisions. Closing workflow decision cannot be combined with other state decisions")
	}
	return
}

func (i *Interpreter) processStepExecution(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	stepRequest StepRequest,
	stepExecutionId string,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	timerProcessor interfaces.TimerProcessor,
	continueAsNewer *ContinueAsNewer,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	flowConfiger *interpreterconfig.FlowConfiger,
) (*iwfpb.StepDecision, service.StepExecutionStatus, error) {
	step := stepRequest.GetStepMovement()
	var stepExeLocals []*iwfpb.KV
	var waitingCondition *iwfpb.WaitingCondition
	completedTimerConditions := map[int32]iwfpb.InternalTimerStatus{}
	waitForFailed := false

	if stepRequest.IsResumeRequest() {
		resumeRequest := stepRequest.GetStepResumeRequest()
		stepExeLocals = resumeRequest.GetStepExeLocals()
		waitingCondition = resumeRequest.GetWaitingCondition()
		if completed := resumeRequest.GetCompletedConditions(); completed != nil {
			completedTimerConditions = completed.GetCompletedTimerConditions()
		}
	} else if !step.GetStepOptions().GetSkipWaitFor() {
		waitForResponse, proceed, failed, err := i.invokeWaitForMethod(
			ctx,
			provider,
			basicInfo,
			step,
			stepExecutionId,
			persistenceManager,
			flowConfiger,
		)
		if err != nil {
			return nil, service.FailureStepExecutionStatus, err
		}
		if !proceed {
			return nil, service.FailureStepExecutionStatus, nil
		}
		waitForFailed = failed
		if waitForResponse.GetWaitingCondition() != nil {
			waitForResponse.WaitingCondition = timers.FixTimerConditionFromActivityOutput(
				provider.Now(ctx),
				waitForResponse.GetWaitingCondition(),
			)
		}
		if err := persistenceManager.ApplyAttributeWrites(
			ctx,
			waitForResponse.GetUpsertAttributes(),
		); err != nil {
			return nil, service.FailureStepExecutionStatus, err
		}
		channelStore.ProcessPublishing(waitForResponse.GetPublishToChannel())
		stepExeLocals = waitForResponse.GetUpsertStepExeLocals()
		waitingCondition = waitForResponse.GetWaitingCondition()
	}

	conditionResults := &iwfpb.ConditionResults{WaitForFailed: waitForFailed}
	if waitingCondition != nil &&
		len(waitingCondition.GetTimerConditions())+
			len(waitingCondition.GetChannelConditions()) > 0 {
		results, matched, err := waitForConditions(
			ctx,
			provider,
			stepExecutionId,
			step,
			stepExeLocals,
			waitingCondition,
			completedTimerConditions,
			channelStore,
			timerProcessor,
			continueAsNewer,
			continueAsNewCounter,
		)
		if err != nil {
			return nil, service.FailureStepExecutionStatus, err
		}
		if !matched {
			return nil, service.WaitingConditionsStepExecutionStatus, nil
		}
		conditionResults = results
		conditionResults.WaitForFailed = waitForFailed
	}

	executeResponse, executeFailureDecision, err := i.invokeExecuteMethod(
		ctx,
		provider,
		basicInfo,
		step,
		stepExecutionId,
		stepExeLocals,
		conditionResults,
		persistenceManager,
		flowConfiger,
	)
	if err != nil {
		return nil, service.FailureStepExecutionStatus, err
	}
	if executeFailureDecision != nil {
		continueAsNewer.RemoveStepExecutionToResume(stepExecutionId)
		return executeFailureDecision, service.CompletedStepExecutionStatus, nil
	}
	if executeResponse == nil {
		return nil, service.FailureStepExecutionStatus, nil
	}
	if err := persistenceManager.ApplyAttributeWrites(
		ctx,
		executeResponse.GetUpsertAttributes(),
	); err != nil {
		return nil, service.FailureStepExecutionStatus, err
	}
	channelStore.ProcessPublishing(executeResponse.GetPublishToChannel())
	continueAsNewer.RemoveStepExecutionToResume(stepExecutionId)
	return executeResponse.GetStepDecision(), service.CompletedStepExecutionStatus, nil
}

func (i *Interpreter) invokeWaitForMethod(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	step *iwfpb.StepMovement,
	stepExecutionId string,
	persistenceManager *PersistenceManager,
	flowConfiger *interpreterconfig.FlowConfiger,
) (*iwfpb.InvokeWaitForMethodResponse, bool, bool, error) {
	lockAttributeKeys := step.GetStepOptions().GetWaitForLockAttributeKeys()
	attributes := persistenceManager.GetAllAttributes()
	if len(lockAttributeKeys) > 0 {
		loadedAttributes, err := persistenceManager.LoadAttributes(ctx, lockAttributeKeys)
		if err != nil {
			return nil, false, false, err
		}
		attributes = loadedAttributes
	}
	var output iwfpb.InvokeWaitForMethodActivityOutput
	err := provider.ExecuteActivity(
		&output,
		flowConfiger.ResolveWaitForDurability(step.GetStepOptions()),
		stepActivityContext(ctx, provider, step.GetStepOptions(), true),
		i.activities.InvokeWaitForMethod,
		&iwfpb.InvokeWaitForMethodActivityInput{
			WorkerTarget: basicInfo.WorkerTarget,
			Request: &iwfpb.InvokeWaitForMethodRequest{
				Context: newStepContext(
					provider.GetWorkflowInfo(ctx),
					stepExecutionId,
				),
				FlowType:   basicInfo.FlowType,
				StepType:   step.GetStepType(),
				StepInput:  step.GetStepInput(),
				Attributes: attributes,
			},
		},
	)
	persistenceManager.UnlockKeys(lockAttributeKeys)
	if err != nil {
		return waitForFailureResult(provider, step.GetStepOptions(), err.Error())
	}
	response, responseErr := waitForActivityResponse(&output)
	if responseErr != nil {
		return waitForFailureResult(
			provider,
			step.GetStepOptions(),
			responseErr.Error(),
		)
	}
	return response, true, false, nil
}

func waitForFailureResult(
	provider interfaces.WorkflowProvider,
	options *iwfpb.StepOptions,
	reason string,
) (*iwfpb.InvokeWaitForMethodResponse, bool, bool, error) {
	if options.GetWaitForFailurePolicy() ==
		iwfpb.WaitForApiFailurePolicy_WAIT_FOR_API_FAILURE_POLICY_PROCEED_ON_FAILURE {
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{},
		}, true, true, nil
	}
	return nil, false, false, provider.NewWorkflowError(
		iwfpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_API_FAIL,
		reason,
	)
}

func waitForConditions(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	stepExecutionId string,
	step *iwfpb.StepMovement,
	stepExeLocals []*iwfpb.KV,
	waitingCondition *iwfpb.WaitingCondition,
	completedTimerConditions map[int32]iwfpb.InternalTimerStatus,
	channelStore *ChannelStore,
	timerProcessor interfaces.TimerProcessor,
	continueAsNewer *ContinueAsNewer,
	continueAsNewCounter *cont.ContinueAsNewCounter,
) (*iwfpb.ConditionResults, bool, error) {
	timerProcessor.AddTimers(
		stepExecutionId,
		waitingCondition.GetTimerConditions(),
		completedTimerConditions,
	)
	// Passing a map of references of completed or soon to be completed conditions (once the condition threads complete) and the step execution variables to the continueAsNewer.
	// After this method completes and if continueAsNewCounter.IsThresholdMet() is true, this snapshot will be used to start a new continueAsNew flow while preserving the state of the flow at the end of this method.
	// This snapshot is also used to query the flow state, which can be done at anytime.
	continueAsNewer.AddPotentialStepExecutionToResume(
		&iwfpb.StepExecutionResumeInfo{
			StepExecutionId: stepExecutionId,
			Step:            step,
			CompletedConditions: &iwfpb.StepExecutionCompletedConditions{
				CompletedTimerConditions:   completedTimerConditions,
				CompletedChannelConditions: map[int32]*iwfpb.ChannelValues{},
			},
			WaitingCondition: waitingCondition,
			StepExeLocals:    stepExeLocals,
		},
	)

	waitingConditionDoneOrCanceled := false
	waitForThreads := startTimerWaiters(
		ctx,
		provider,
		timerProcessor,
		stepExecutionId,
		waitingCondition,
		completedTimerConditions,
		&waitingConditionDoneOrCanceled,
	)

	var matchPlan *channel.MatchPlan
	// Wait for condition trigger (ANY/ALL condition completed) OR continue-as-new threshold
	err := provider.Await(ctx, func() bool {
		plan, matched := channel.Plan(
			waitingCondition,
			channelStore.Availability(),
			completedTimerIndexes(completedTimerConditions),
		)
		if matched {
			matchPlan = plan
		}
		return matched || continueAsNewCounter.IsThresholdMet()
	})
	// This variable tells all condition threads to stop waiting and exit, even if their specific condition has not been completed.
	// In both cases, the trigger condition has been met or the continue-as-new threshold has been reached, so we want the above condition threads to stop waiting.
	waitingConditionDoneOrCanceled = true
	if err != nil {
		return nil, false, err
	}
	// Wait for condition threads to drain. After the waiting condition await completes, condition threads
	// may still be in the process of storing retrieved data into completed condition maps.
	// We must wait for these threads to finish before assembling condition results, otherwise
	// retrieved data will be lost (the thread retrieved it but never stored it before we return the maps).
	// We only wait for threads of conditions that currently have data or have been canceled or fired.
	// A thread that doesn't have data can be canceled when waitingConditionDoneOrCanceled is set to true. This preserves ANY_COMPLETED semantics.
	if err := provider.Await(ctx, func() bool {
		for _, completed := range waitForThreads {
			if !completed {
				return false
			}
		}
		return true
	}); err != nil {
		return nil, false, err
	}

	// This recheck means continueAsNewCounter.IsThresholdMet == true when no condition matches.
	// We don't use only continueAsNewCounter.IsThresholdMet because a condition trigger has higher priority.
	// It won't continueAsNew when a condition and continueAsNew are both met.
	if plan, matched := channel.Plan(
		waitingCondition,
		channelStore.Availability(),
		completedTimerIndexes(completedTimerConditions),
	); matched {
		matchPlan = plan
	}
	if matchPlan == nil {
		return nil, false, nil
	}

	consumed := channelStore.CommitMatch(matchPlan)
	timerProcessor.RemovePendingTimersOfStep(stepExecutionId)
	continueAsNewer.RemoveStepExecutionToResume(stepExecutionId)
	return channel.BuildConditionResults(
		waitingCondition,
		completedTimerIndexes(completedTimerConditions),
		consumed,
	), true, nil
}

func startTimerWaiters(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	timerProcessor interfaces.TimerProcessor,
	stepExecutionId string,
	waitingCondition *iwfpb.WaitingCondition,
	completedTimerConditions map[int32]iwfpb.InternalTimerStatus,
	waitingConditionDoneOrCanceled *bool,
) map[string]bool {
	waitForThreads := map[string]bool{}
	for timerIndex, timerCondition := range waitingCondition.GetTimerConditions() {
		if isCompletedTimerStatus(completedTimerConditions[int32(timerIndex)]) {
			continue
		}
		timerCtx := provider.ExtendContextWithValue(ctx, "timerIndex", timerIndex)
		// Start timer in a new thread
		threadName := fmt.Sprintf(
			"%s-timer-%d-%s",
			stepExecutionId,
			timerIndex,
			timerCondition.GetConditionId(),
		)
		waitForThreads[threadName] = false
		provider.GoNamed(timerCtx, threadName, func(ctx interfaces.UnifiedContext) {
			index, ok := provider.GetContextValue(ctx, "timerIndex").(int)
			if !ok {
				panic("cannot read timer index from workflow context")
			}
			// Note that waitingConditionDoneOrCanceled is needed for two cases:
			// 1. It becomes true when the waiting condition is completed so we don't need to wait for all conditions. Returning the thread avoids thread leakage.
			// 2. It becomes true to cancel the wait for unblocking continueAsNew, which waits for all threads to complete.
			status := timerProcessor.WaitForTimerFiredOrSkipped(
				ctx,
				stepExecutionId,
				index,
				waitingConditionDoneOrCanceled,
			)
			if isCompletedTimerStatus(status) {
				completedTimerConditions[int32(index)] = status
			}
			waitForThreads[threadName] = true
		})
	}
	return waitForThreads
}

func completedTimerIndexes(
	completedTimerConditions map[int32]iwfpb.InternalTimerStatus,
) map[int]bool {
	completed := make(map[int]bool, len(completedTimerConditions))
	for timerIndex, status := range completedTimerConditions {
		if isCompletedTimerStatus(status) {
			completed[int(timerIndex)] = true
		}
	}
	return completed
}

func isCompletedTimerStatus(status iwfpb.InternalTimerStatus) bool {
	return status == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
		status == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
}

func (i *Interpreter) invokeExecuteMethod(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	basicInfo service.BasicInfo,
	step *iwfpb.StepMovement,
	stepExecutionId string,
	stepExeLocals []*iwfpb.KV,
	conditionResults *iwfpb.ConditionResults,
	persistenceManager *PersistenceManager,
	flowConfiger *interpreterconfig.FlowConfiger,
) (
	*iwfpb.InvokeExecuteMethodResponse,
	*iwfpb.StepDecision,
	error,
) {
	lockAttributeKeys := step.GetStepOptions().GetExecuteLockAttributeKeys()
	attributes := persistenceManager.GetAllAttributes()
	if len(lockAttributeKeys) > 0 {
		loadedAttributes, err := persistenceManager.LoadAttributes(ctx, lockAttributeKeys)
		if err != nil {
			return nil, nil, err
		}
		attributes = loadedAttributes
	}
	var output iwfpb.InvokeExecuteMethodActivityOutput
	err := provider.ExecuteActivity(
		&output,
		flowConfiger.ResolveExecuteDurability(step.GetStepOptions()),
		stepActivityContext(ctx, provider, step.GetStepOptions(), false),
		i.activities.InvokeExecuteMethod,
		&iwfpb.InvokeExecuteMethodActivityInput{
			WorkerTarget: basicInfo.WorkerTarget,
			Request: &iwfpb.InvokeExecuteMethodRequest{
				Context: newStepContext(
					provider.GetWorkflowInfo(ctx),
					stepExecutionId,
				),
				FlowType:         basicInfo.FlowType,
				StepType:         step.GetStepType(),
				StepInput:        step.GetStepInput(),
				Attributes:       attributes,
				StepExeLocals:    stepExeLocals,
				ConditionResults: conditionResults,
			},
		},
	)
	persistenceManager.UnlockKeys(lockAttributeKeys)
	if err == nil {
		response, responseErr := executeActivityResponse(&output)
		if responseErr == nil {
			return response, nil, nil
		}
		err = responseErr
	}

	options := step.GetStepOptions()
	if options.GetExecuteFailurePolicy() ==
		iwfpb.ExecuteApiFailurePolicy_EXECUTE_API_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP &&
		options.GetExecuteFailureProceedStepType() != "" {
		return nil, &iwfpb.StepDecision{
			NextSteps: []*iwfpb.StepMovement{{
				StepType:    options.GetExecuteFailureProceedStepType(),
				StepInput:   step.GetStepInput(),
				StepOptions: options.GetExecuteFailureProceedStepOptions(),
			}},
		}, nil
	}
	return nil, nil, provider.NewWorkflowError(
		iwfpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_API_FAIL,
		err.Error(),
	)
}

func stepActivityContext(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	options *iwfpb.StepOptions,
	waitFor bool,
) interfaces.UnifiedContext {
	timeoutSeconds := options.GetExecuteTimeoutSeconds()
	retryPolicy := options.GetExecuteRetryPolicy()
	if waitFor {
		timeoutSeconds = options.GetWaitForTimeoutSeconds()
		retryPolicy = options.GetWaitForRetryPolicy()
	}
	timeout := 30 * time.Second
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	return provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy:         retryPolicy,
	})
}

func waitForActivityResponse(
	output *iwfpb.InvokeWaitForMethodActivityOutput,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	if (output.GetResponse() == nil) == (output.GetError() == nil) {
		return nil, fmt.Errorf("WaitFor activity returned an invalid result envelope")
	}
	if interpreterErr := output.GetError(); interpreterErr != nil {
		return nil, interpreterError(interpreterErr)
	}
	return output.GetResponse(), nil
}

func executeActivityResponse(
	output *iwfpb.InvokeExecuteMethodActivityOutput,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	if (output.GetResponse() == nil) == (output.GetError() == nil) {
		return nil, fmt.Errorf("Execute activity returned an invalid result envelope")
	}
	if interpreterErr := output.GetError(); interpreterErr != nil {
		return nil, interpreterError(interpreterErr)
	}
	return output.GetResponse(), nil
}

func interpreterError(interpreterErr *iwfpb.InterpreterError) error {
	if interpreterErr.GetError() == nil {
		return fmt.Errorf("worker error with gRPC code %d", interpreterErr.GetGrpcCode())
	}
	return fmt.Errorf(
		"worker error with gRPC code %d: %s",
		interpreterErr.GetGrpcCode(),
		interpreterErr.GetError().GetDetail(),
	)
}

func newStepContext(
	info interfaces.WorkflowInfo,
	stepExecutionId string,
) *iwfpb.Context {
	return &iwfpb.Context{
		FlowId:               info.WorkflowExecution.ID,
		RunId:                info.FirstRunID,
		FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
		StepExecutionId:      stepExecutionId,
	}
}

func (i *Interpreter) BlobStoreCleanup(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	storeId string,
) (int, error) {
	activityCtx := provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
		StartToCloseTimeout: 24 * time.Hour,
		RetryPolicy:         &iwfpb.RetryPolicy{MaximumAttempts: 10},
	})
	var output iwfpb.CleanupBlobStoreActivityOutput
	if err := provider.ExecuteActivity(
		&output,
		iwfpb.StepDurability_STEP_DURABILITY_SYNC,
		activityCtx,
		i.activities.CleanupBlobStore,
		&iwfpb.CleanupBlobStoreActivityInput{
			StoreId: storeId,
		},
	); err != nil {
		return 0, err
	}
	if output.GetError() != nil {
		return 0, interpreterError(output.GetError())
	}
	return int(output.GetTotalDeleted()), nil
}

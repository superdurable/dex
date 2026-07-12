package dex

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/evaluate"
)

type stepTaskMethodKind int

const (
	stepTaskMethodKindExecute stepTaskMethodKind = iota
	stepTaskMethodKindWaitFor
)

// stepInvocationTask represents a step task to be invoked.
type stepInvocationTask struct {
	stepExeId string
	kind      stepTaskMethodKind

	// consumedChannelMessages only for stepTaskMethodKindExecute
	// which was previously WAITING_FOR_CONDITION, and then conditions are met.
	consumedChannelMessages map[string][]*pb.Value

	// activeStepExe is snapshotted from allActiveStepExecutions by kickOffStepTask
	// (in the main-loop goroutine) so the step goroutine never reads that shared
	// map — the main loop mutates it as siblings complete/promote.
	activeStepExe *pb.ActiveStepExecution

	// for canceling the task(sibling canceling feature)
	cancel context.CancelFunc
}

// runExecutor owns the lifecycle of a run
type runExecutor struct {
	// ref back to global worker
	worker *Worker
	// per run channel to receive external event
	extChMsgInbox chan *pb.ExternalEvent
	// per-run server-aligned clock authority for timer
	timerMgr *timerManager
	// per-run channel to receive stepTaskCompletion from Execute / WaitFor goroutine
	stepTaskCompletionCh chan stepTaskCompletion
	// per-run outbound StepRetryState updates to the server(via worker RPC)
	retryStateSyncCh chan retrySyncEvent

	// constant values
	runID     string
	flowType  string
	namespace string

	//// atomic values
	// requestCounter is the per-run from server's worker_request_counter.
	requestCounter atomic.Int64
	// lastSeenExtMsgID tracks the highest external_channel_message_id
	// we've observed for this run
	lastSeenExtMsgID atomic.Int64

	//// counters
	// stepExeIDCounters for minting new step_exe_ids ("stepID-N") locally
	stepExeIDCounters map[string]int32
	// stepMethodExeCounter for minting stepMethodExeId
	stepMethodExeCounter int64

	//// persistence
	// state is copy-on-write; Load for read, Store after merge.
	state atomic.Pointer[map[string]*pb.Value]
	// unconsumedChannels is the worker's in-memory FIFO buffer of
	// channel messages not yet consumed by any waiting step, keyed
	// by channel name → ordered values.
	//
	//   Init from pollResponse
	//   Appended at the tail:
	//   - inter-step publishes from steps when a
	//     StepDecision / WaitFor returns with PublishToChannel
	//   - external publishes pushed via PollForExternalEvents
	//   Spliced out only when a reserved INVOKING_EXECUTE completes or cancels.
	unconsumedChannels map[string][]*pb.Value

	//// step tasks
	// keyed by stepExeId
	allActiveStepExecutions map[string]*pb.ActiveStepExecution
	// tasks that need to be invoked by kickOffStepTask
	// keys are stepExeId
	pendingStepTasks map[string]*stepInvocationTask
	// tasks that are currently running by step_method_runner
	// keys are stepExeId
	runningStepTasks map[string]*stepInvocationTask
	// tasks that are waiting for conditions to be met
	// keys are stepExeId
	keysOfWaitingStepTasks map[string]bool
}

func newRunExecutor(
	worker *Worker,
	pollResponse *pb.PollForRunResponse,
	inbox chan *pb.ExternalEvent,
) *runExecutor {

	re := &runExecutor{
		worker:        worker,
		extChMsgInbox: inbox,
		// Capacity 1: under fan-out there are many concurrent writers (one per
		// running step goroutine); they serialize on this channel while the run
		// loop drains exactly one completion per select iteration.
		stepTaskCompletionCh: make(chan stepTaskCompletion, 1),
		timerMgr:             newTimerManager(pollResponse.ServerTimestampMs),
		retryStateSyncCh:     make(chan retrySyncEvent, 1024),

		runID:     pollResponse.RunId,
		flowType:  pollResponse.FlowType,
		namespace: pollResponse.Namespace,

		stepExeIDCounters:    make(map[string]int32, len(pollResponse.StepExecutionIdCounters)),
		stepMethodExeCounter: pollResponse.StepMethodExeCounter,

		unconsumedChannels: make(map[string][]*pb.Value, len(pollResponse.UnconsumedChannelMessages)),

		allActiveStepExecutions: pollResponse.ActiveStepExecutions,
		pendingStepTasks:        make(map[string]*stepInvocationTask),
		runningStepTasks:        make(map[string]*stepInvocationTask),
		keysOfWaitingStepTasks:  make(map[string]bool, len(pollResponse.ActiveStepExecutions)),
	}

	re.requestCounter.Store(pollResponse.WorkerRequestCounter)
	re.lastSeenExtMsgID.Store(pollResponse.ExternalChannelMessageCounter)

	for channelName, channelMsgs := range pollResponse.UnconsumedChannelMessages {
		if channelMsgs == nil {
			continue
		}
		extractedValues := make([]*pb.Value, 0, len(channelMsgs.Messages))
		for _, msg := range channelMsgs.Messages {
			extractedValues = append(extractedValues, msg.Value)
		}
		re.unconsumedChannels[channelName] = extractedValues
	}

	for stepID, counter := range pollResponse.StepExecutionIdCounters {
		re.stepExeIDCounters[stepID] = counter
	}
	re.initRunStateFromPoll(pollResponse.State)
	re.initStepTasksFromPollResponse()
	return re
}

func (re *runExecutor) initRunStateFromPoll(pollState map[string]*pb.Value) {
	initial := maps.Clone(pollState)
	if initial == nil {
		initial = make(map[string]*pb.Value)
	}
	re.state.Store(&initial)
}

// initStepTasksFromPollResponse initialize the step tasks.
func (re *runExecutor) initStepTasksFromPollResponse() {

	sortedKeys := slices.Sorted(maps.Keys(re.allActiveStepExecutions))

	for _, stepExeID := range sortedKeys {
		stepExe := re.allActiveStepExecutions[stepExeID]
		switch stepExe.Status {
		case pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_EXECUTE:
			task := &stepInvocationTask{
				stepExeId: stepExeID,
				kind:      stepTaskMethodKindExecute,
			}

			task.consumedChannelMessages = evaluate.GetStepExeReservedMessages(stepExeID, re.allActiveStepExecutions, re.unconsumedChannels)
			re.pendingStepTasks[stepExeID] = task
		case pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_WAIT_FOR:
			task := &stepInvocationTask{
				stepExeId: stepExeID,
				kind:      stepTaskMethodKindWaitFor,
			}

			re.pendingStepTasks[stepExeID] = task
		case pb.StepExecutionStatus_STEP_EXECUTION_STATUS_WAITING_FOR_CONDITION:
			// By contracts, server should promote steps when transition the run from non-running to running.
			// otherwise we need to call StepUnblock here if promoting any stepExes.
			// TODO in heartbeat timeout & redispatch case, make sure server promotes the stepTasks correctly

			re.keysOfWaitingStepTasks[stepExeID] = true
			re.addTimers(stepExe)
		}
	}
}

// runMainLoop is the main event loop.
// !!! Most of the mutable components are mutated within this goroutine to avoid
// racing condition !!!
func (re *runExecutor) runMainLoop() error {
	heartbeatTicker := time.NewTicker(re.worker.opts.heartbeatInterval())
	defer heartbeatTicker.Stop()

	for {
		// 1. kick off all pending tasks in separate goroutines
		for _, task := range re.pendingStepTasks {
			re.kickOffStepTask(task)
		}
		// reset the pending tasks map
		re.pendingStepTasks = make(map[string]*stepInvocationTask)

		done, err := re.checkToExitRunLoopIfAllStepsAreWaitingForConditions()
		if err != nil || done {
			return err
		}

		select {
		case <-re.worker.rootCtx.Done():
			return re.worker.rootCtx.Err()
		case <-heartbeatTicker.C:
			stop, err := re.handleHeartbeat()
			if err != nil || stop {
				return err
			}
		case <-re.timerMgr.GetTimerFiredEvents():
			stop, err := re.handleTimerFired()
			if err != nil || stop {
				return err
			}
		case event := <-re.extChMsgInbox:
			stop, err := re.handleExternalEvent(event)
			if err != nil || stop {
				return err
			}
		case completion := <-re.stepTaskCompletionCh:
			stop, err := re.handleStepTaskCompletion(completion)
			if err != nil || stop {
				return err
			}
		}
	}
}

// handleHeartbeat is the run loop's response to a heartbeat-ticker fire.
func (re *runExecutor) handleHeartbeat() (stop bool, err error) {
	callContext := re.buildWorkerCallContext(nil)
	ownershipLost, hbErr := re.worker.sendHeartbeat(re.runID, re.extChMsgInbox, callContext)
	// Ownership loss carries a non-nil err. Check it before hbErr,
	// else we loop forever on a stopped run.
	if ownershipLost {
		return true, nil
	}
	return false, hbErr
}

// checkToExitRunLoopIfAllStepsAreWaitingForConditions is called when
// no work is in flight or queued.
// Drains extChMsgInbox, re-evaluates waiting steps, parks via ProcessReleaseRun
// when idle with only waiting steps left.
func (re *runExecutor) checkToExitRunLoopIfAllStepsAreWaitingForConditions() (done bool, err error) {
	if len(re.runningStepTasks) > 0 || len(re.pendingStepTasks) > 0 {
		return false, nil
	}
	if len(re.keysOfWaitingStepTasks) == 0 {
		return true, nil
	}
drainLoop:
	for {
		select {
		case event := <-re.extChMsgInbox:
			_, stop := re.applyExternalEvent(event)
			if stop {
				return true, nil
			}
		default:
			break drainLoop
		}
	}

	promotedAny, stop, err := re.reEvaluateWaitingStepsAndHandleStepUnblocked()
	if err != nil || stop {
		return stop, err
	}
	if promotedAny {
		return false, nil
	}

	// now request to stop, need confirm from server
	done, parkErr := re.worker.releaseRunAllStepsWaiting(re.runID, re.makeCallContext(), re.extChMsgInbox)
	return done, parkErr
}

// handleTimerFired is run's response to a timer fire kick.
// Returns stop=true when handleStepsUnblocked signals ownership lost
func (re *runExecutor) handleTimerFired() (stop bool, err error) {
	_, stop, err = re.reEvaluateWaitingStepsAndHandleStepUnblocked()
	return stop, err
}

// handleExternalEvent merges one event from extChMsgInbox and re-evaluates
// if the buffer changed. Returns stop=true when StopRequested was
// seen OR handleStepsUnblocked signals ownership lost.
func (re *runExecutor) handleExternalEvent(event *pb.ExternalEvent) (stop bool, err error) {
	changed, gotStop := re.applyExternalEvent(event)
	if gotStop {
		return true, nil
	}
	if !changed {
		return false, nil
	}
	_, stop, err = re.reEvaluateWaitingStepsAndHandleStepUnblocked()
	return stop, err
}

// handleStepTaskCompletion processes a result from a step's Execute /
// WaitFor goroutine. All routing lives here; sub-handlers do not dispatch.
func (re *runExecutor) handleStepTaskCompletion(completion stepTaskCompletion) (stop bool, err error) {
	defer func() {
		delete(re.runningStepTasks, completion.task.stepExeId)
	}()

	if completion.stopErr != nil {
		return true, completion.stopErr
	}

	if completion.empty {
		return false, nil
	}

	switch completion.task.kind {
	case stepTaskMethodKindWaitFor:
		if completion.waitForMethodFailed {
			// special route for waitFor method failed(including failed and proceed to next steps)
			// no special route for execute method failed because execute reuse Decision
			return re.handleWaitForMethodFailed(completion)
		}
		return re.handleWaitForSucceeded(completion)
	case stepTaskMethodKindExecute:
		// Again, no handleExecuteMethodFailed exists: Execute failure is just use fail decision
		return re.handleExecuteCompletion(completion)
	}
	return false, fmt.Errorf("unknown step kind: %v", completion.task.kind)
}

func (re *runExecutor) handleWaitForMethodFailed(completion stepTaskCompletion) (stop bool, err error) {
	var nextSteps []*pb.NextStep
	if completion.decision != nil {
		var encodeErr error
		nextSteps, encodeErr = re.movementsToNextSteps(stepDecisionMovements(completion.decision))
		if encodeErr != nil {
			return false, fmt.Errorf("encode proceed next steps for %s: %w", completion.task.stepExeId, encodeErr)
		}
	}

	if len(nextSteps) > 0 {
		// waitFor failed but go to next step as error handler(SAGA)
		re.processNextSteps(completion.task.stepExeId, nextSteps)
	}
	delete(re.allActiveStepExecutions, completion.task.stepExeId)
	req := &pb.StepWaitForCompletedRequest{
		Namespace:     re.namespace,
		RunId:         re.runID,
		StepExeId:     completion.task.stepExeId,
		WaitForMethod: completion.methodReport,
		NextSteps:     nextSteps,
		Context:       re.buildWorkerCallContext(nil),
	}
	var resp *pb.StepWaitForCompletedResponse
	ownershipLost, rpcErr := re.worker.callRunRPC(re.worker.rootCtx, "ProcessStepWaitForCompleted", re.runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(re.worker.rootCtx, re.worker.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = re.worker.runsClient.ProcessStepWaitForCompleted(ctx, req)
		return inner
	})
	if ownershipLost || rpcErr != nil {
		return true, rpcErr
	}
	if re.worker.applyCallResponse(re.runID, resp.GetWorkerCallResponse(), re.extChMsgInbox) {
		return true, nil
	}
	if len(nextSteps) > 0 {
		return false, nil
	}
	return true, nil
}

// handleWaitForSucceeded processes a WaitFor method succeeded -- this
// includes attempt failed but recovered from retry. It will
// encode and merges any ChannelPublish, re-evaluates waiting steps, ships the
// StepWaitForCompleted RPC, and applies the WorkerCallResponse.
func (re *runExecutor) handleWaitForSucceeded(completion stepTaskCompletion) (stop bool, err error) {
	stepExeId := completion.task.stepExeId
	waitProto, err := waitForToProto(completion.waitForCondition, re.timerMgr.Now())
	if err != nil {
		return false, fmt.Errorf("encode wait for %s: %w", stepExeId, err)
	}

	channelPubProto, err := channelMessagesToProto(re.worker.registry.ObjectCodec(), completion.persistence.flushPublishes())
	if err != nil {
		return false, fmt.Errorf("encode wait channel publish for %s: %w", stepExeId, err)
	}
	for _, pub := range channelPubProto {
		re.unconsumedChannels[pub.ChannelName] = append(re.unconsumedChannels[pub.ChannelName], pub.Values...)
	}

	stateMapUpsert := completion.persistence.flushState()
	re.mergeStateUpsert(stateMapUpsert)

	stepExe := re.allActiveStepExecutions[stepExeId]
	stepExe.Status = pb.StepExecutionStatus_STEP_EXECUTION_STATUS_WAITING_FOR_CONDITION
	stepExe.WaitForCondition = waitProto
	re.keysOfWaitingStepTasks[stepExeId] = true
	re.addTimers(stepExe)

	promoted, evalErr := re.reEvaluateWaitingSteps()
	if evalErr != nil {
		return false, evalErr
	}
	// handling promoted steps locally
	unblocks := make([]*pb.StepUnblocked, 0, len(promoted))
	for _, prom := range promoted {
		unBlockStepExe := re.allActiveStepExecutions[prom.stepExeID]
		unblocks = append(unblocks, &pb.StepUnblocked{
			StepExeId:          prom.stepExeID,
			ConditionResults:   unBlockStepExe.ConditionResults,
			ExecuteMethodExeId: unBlockStepExe.ExecuteMethodExeId,
		})
		re.pendingStepTasks[prom.stepExeID] = &stepInvocationTask{
			stepExeId:               prom.stepExeID,
			kind:                    stepTaskMethodKindExecute,
			consumedChannelMessages: prom.consumedChannelMessages,
		}
	}

	req := &pb.StepWaitForCompletedRequest{
		Namespace:        re.namespace,
		RunId:            re.runID,
		StepExeId:        stepExeId,
		WaitForCondition: waitProto,
		StateToUpsert:    stateMapUpsert,
		ChannelPublish:   channelPubProto,
		StepsUnblocked:   unblocks,
		Context:          re.buildWorkerCallContext(&stepExeId),
		WaitForMethod:    completion.methodReport,
	}
	var resp *pb.StepWaitForCompletedResponse
	ownershipLost, rpcErr := re.worker.callRunRPC(re.worker.rootCtx, "ProcessStepWaitForCompleted", re.runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(re.worker.rootCtx, re.worker.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = re.worker.runsClient.ProcessStepWaitForCompleted(ctx, req)
		return inner
	})
	if ownershipLost || rpcErr != nil {
		return true, rpcErr
	}
	if re.worker.applyCallResponse(re.runID, resp.GetWorkerCallResponse(), re.extChMsgInbox) {
		return true, nil
	}
	return false, nil
}

func (re *runExecutor) handleExecuteCompletion(completion stepTaskCompletion) (stop bool, err error) {
	stepExeId := completion.task.stepExeId
	cancelStepIDs := stepDecisionCancelIDs(completion.decision)
	stopDec, nextSteps, err := re.decisionToProto(completion.decision)
	if err != nil {
		return false, fmt.Errorf("convert decision for %s: %w", stepExeId, err)
	}

	// handling cancellation
	cancelStepExeIDs := resolveCancelTargets(stepExeId, re.allActiveStepExecutions, cancelStepIDs)

	if len(cancelStepExeIDs) > 0 {
		evaluate.SpliceUnconsumed(cancelStepExeIDs, re.allActiveStepExecutions, re.unconsumedChannels)
	}

	for _, seid := range cancelStepExeIDs {
		task, ok := re.runningStepTasks[seid]
		if ok && task.cancel != nil {
			task.cancel()
		}
		delete(re.allActiveStepExecutions, seid)
		delete(re.pendingStepTasks, seid)
		delete(re.runningStepTasks, seid)
		delete(re.keysOfWaitingStepTasks, seid)
	}

	channelPubProto, err := channelMessagesToProto(re.worker.registry.ObjectCodec(), completion.persistence.flushPublishes())
	if err != nil {
		return false, fmt.Errorf("encode execute channel publish for %s: %w", stepExeId, err)
	}
	for _, pub := range channelPubProto {
		re.unconsumedChannels[pub.ChannelName] = append(re.unconsumedChannels[pub.ChannelName], pub.Values...)
	}

	stateMapUpsert := completion.persistence.flushState()
	re.mergeStateUpsert(stateMapUpsert)

	promoted, evalErr := re.reEvaluateWaitingSteps()
	if evalErr != nil {
		return false, evalErr
	}
	// handling promoted steps locally
	unblocks := make([]*pb.StepUnblocked, 0, len(promoted))
	for _, prom := range promoted {
		unBlockStepExe := re.allActiveStepExecutions[prom.stepExeID]
		unblocks = append(unblocks, &pb.StepUnblocked{
			StepExeId:          prom.stepExeID,
			ConditionResults:   unBlockStepExe.ConditionResults,
			ExecuteMethodExeId: unBlockStepExe.ExecuteMethodExeId,
		})
		re.pendingStepTasks[prom.stepExeID] = &stepInvocationTask{
			stepExeId:               prom.stepExeID,
			kind:                    stepTaskMethodKindExecute,
			consumedChannelMessages: prom.consumedChannelMessages,
		}
	}

	re.processNextSteps(stepExeId, nextSteps)

	req := &pb.StepExecuteCompletedRequest{
		Namespace:              re.namespace,
		RunId:                  re.runID,
		StepExeId:              stepExeId,
		StopDecision:           stopDec,
		StateToUpsert:          stateMapUpsert,
		ChannelPublish:         channelPubProto,
		NextSteps:              nextSteps,
		CanceledStepExecutions: cancelStepExeIDs,
		StepsUnblocked:         unblocks,
		Context:                re.buildWorkerCallContext(nil),
		ExecuteMethod:          completion.methodReport,
	}

	var resp *pb.StepExecuteCompletedResponse
	ownershipLost, rpcErr := re.worker.callRunRPC(re.worker.rootCtx, "ProcessStepExecuteCompleted", re.runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(re.worker.rootCtx, re.worker.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = re.worker.runsClient.ProcessStepExecuteCompleted(ctx, req)
		return inner
	})
	if ownershipLost || rpcErr != nil {
		return true, fmt.Errorf("ProcessStepExecuteCompleted for %s: %w", stepExeId, rpcErr)
	}
	if re.worker.applyCallResponse(re.runID, resp.GetWorkerCallResponse(), re.extChMsgInbox) {
		return true, nil
	}

	evaluate.SpliceUnconsumed([]string{stepExeId}, re.allActiveStepExecutions, re.unconsumedChannels)
	delete(re.allActiveStepExecutions, stepExeId)
	delete(re.runningStepTasks, stepExeId)

	if req.StopDecision == pb.StopDecision_STOP_DECISION_COMPLETE ||
		req.StopDecision == pb.StopDecision_STOP_DECISION_FAIL {
		return true, nil
	}

	return false, nil
}

// kickOffStepTask launches a goroutine to invoke task's Execute / WaitFor
// method on the user's step.
func (re *runExecutor) kickOffStepTask(task *stepInvocationTask) {
	taskCtx, cancelTask := context.WithCancel(re.worker.rootCtx)
	task.cancel = cancelTask
	// Snapshot the step's record here (main-loop goroutine) so the launched
	// goroutine never reads the shared allActiveStepExecutions map.
	task.activeStepExe = re.allActiveStepExecutions[task.stepExeId]
	re.runningStepTasks[task.stepExeId] = task

	go func() {
		defer cancelTask()
		reg, ok := re.worker.registry.getFlowRegistration(re.flowType)
		if !ok {
			err := newFlowNotRegisteredError(re.flowType)
			re.worker.log.Error("step goroutine: flow not registered in worker registry, ignore "+
				"and let next worker to pick up",
				"runID", re.runID,
				"flowType", re.flowType,
				"stepExeID", task.stepExeId,
				"error", err,
			)
			re.stepTaskCompletionCh <- stepTaskCompletion{
				task:    task,
				stopErr: fmt.Errorf("step goroutine: flow not registered in worker registry"),
			}
			return
		}
		stepID := StepIDFromStepExecutionID(task.stepExeId)
		step, ok := re.worker.registry.getStep(re.flowType, stepID)
		if !ok {
			err := newStepNotFoundError(stepID, re.flowType)
			re.worker.log.Error("step goroutine: step not found in worker registry, ignore "+
				"and let next worker to pick up",
				"runID", re.runID,
				"flowType", re.flowType,
				"stepID", stepID,
				"stepExeID", task.stepExeId,
				"error", err,
			)
			re.stepTaskCompletionCh <- stepTaskCompletion{
				task:    task,
				stopErr: fmt.Errorf("step goroutine: step not found in worker registry"),
			}
			return
		}
		runner := newStepMethodRunner(re, task, step, reg, taskCtx)
		re.stepTaskCompletionCh <- runner.run()
	}()
}

// cancelInFlightStepTasks cancels every running step's context and drains
// its completion. Called on run-loop exit (stop / ownership-lost / shutdown)
// so blocked Execute/WaitFor goroutines observe ctx.Done() and don't leak.
func (re *runExecutor) cancelInFlightStepTasks() {
	pending := len(re.runningStepTasks)
	for _, task := range re.runningStepTasks {
		if task.cancel != nil {
			task.cancel()
		}
	}
	// Each kickOffStepTask goroutine sends exactly one completion.
	for i := 0; i < pending; i++ {
		<-re.stepTaskCompletionCh
	}
	re.runningStepTasks = make(map[string]*stepInvocationTask)
}

func (re *runExecutor) reEvaluateWaitingStepsAndHandleStepUnblocked() (promotedAny bool, shouldStop bool, err error) {
	promoted, evalErr := re.reEvaluateWaitingSteps()
	if evalErr != nil {
		return false, false, evalErr
	}
	if len(promoted) > 0 {
		ownershipLost, err := re.handleStepsUnblocked(promoted)
		if ownershipLost {
			return false, true, nil
		}
		if err != nil {
			return false, false, err
		}
		for _, prom := range promoted {
			re.pendingStepTasks[prom.stepExeID] = &stepInvocationTask{
				stepExeId:               prom.stepExeID,
				kind:                    stepTaskMethodKindExecute,
				consumedChannelMessages: prom.consumedChannelMessages,
			}
		}
		return true, false, nil
	}
	return false, false, nil
}

type promotedStep struct {
	stepExeID               string
	consumedChannelMessages map[string][]*pb.Value
}

func (re *runExecutor) reEvaluateWaitingSteps() ([]*promotedStep, error) {
	if len(re.keysOfWaitingStepTasks) == 0 {
		return nil, nil
	}
	sortedKeys := slices.Sorted(maps.Keys(re.keysOfWaitingStepTasks))

	var promotedSteps []*promotedStep
	eval := evaluate.NewConditionEvaluator(re.allActiveStepExecutions, re.timerMgr.Now(), re.unconsumedChannels)

	for _, stepExeId := range sortedKeys {
		result, err := eval.EvaluateWaitForCondition(stepExeId)
		if err != nil {
			return nil, wrapRunNonProcessableError(err, "evaluate wait condition for "+stepExeId)
		}
		if result.Satisfied {

			stepExe := re.allActiveStepExecutions[stepExeId]
			stepExe.ExecuteMethodExeId = re.bumpStepMethodExeCounter()
			stepExe.ConditionResults = result.ConditionResults
			stepExe.Status = pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_EXECUTE
			delete(re.keysOfWaitingStepTasks, stepExeId)

			consumed := evaluate.GetStepExeReservedMessages(stepExeId, re.allActiveStepExecutions, re.unconsumedChannels)

			promoted := &promotedStep{
				stepExeID:               stepExeId,
				consumedChannelMessages: consumed,
			}
			promotedSteps = append(promotedSteps, promoted)
		}
	}
	return promotedSteps, nil
}

// handleStepsUnblocked calls RunsService.ProcessStepsUnblocked
// synchronously to durably commit the unblock decision before the
// worker invokes Execute
func (re *runExecutor) handleStepsUnblocked(
	promoted []*promotedStep,
) (ownershipLost bool, err error) {

	unblocks := make([]*pb.StepUnblocked, 0, len(promoted))
	for _, step := range promoted {
		stepExe := re.allActiveStepExecutions[step.stepExeID]
		unblocks = append(unblocks, &pb.StepUnblocked{
			StepExeId:          step.stepExeID,
			ConditionResults:   stepExe.ConditionResults,
			ExecuteMethodExeId: stepExe.ExecuteMethodExeId,
		})
	}

	req := &pb.StepsUnblockedRequest{
		Namespace:      re.namespace,
		RunId:          re.runID,
		StepsUnblocked: unblocks,
		Context:        re.buildWorkerCallContext(nil),
	}
	var resp *pb.StepsUnblockedResponse
	ownershipLost, err = re.worker.callRunRPC(re.worker.rootCtx, "ProcessStepsUnblocked", re.runID, 0, func() error {
		var inner error
		ctx, cancel := context.WithTimeout(re.worker.rootCtx, re.worker.opts.regularRPCTimeout())
		defer cancel()
		resp, inner = re.worker.runsClient.ProcessStepsUnblocked(ctx, req)
		return inner
	})
	if ownershipLost || err != nil {
		return ownershipLost, err
	}
	re.worker.applyCallResponse(re.runID, resp.GetWorkerCallResponse(), re.extChMsgInbox)
	return false, nil
}

// applyExternalEvent merges one ExternalEvent (push or catch-up)
// into the in-memory unconsumed buffer, deduping against
// atomics.lastSeenExtMsgID. Returns (changed, stop): hasNewMsg=true
// means at least one new message landed (caller should re-evaluate
// waiting steps); stop=true means a StopRequested signal was observed.
func (re *runExecutor) applyExternalEvent(event *pb.ExternalEvent) (hasNewMsg bool, stop bool) {
	if event == nil {
		return false, false
	}
	if _, isStop := event.Event.(*pb.ExternalEvent_StopRequested); isStop {
		return false, true
	}
	channelMessages := event.GetChannelMessagesReceived()
	if channelMessages == nil {
		return false, false
	}
	// Dedup against the snapshot taken before the loop, NOT a running max:
	// messages within an event may not be strictly id-ascending, and a higher
	// id seen early must not skip later still-new ids.
	lastSeen := re.lastSeenExtMsgID.Load()
	highestID := lastSeen
	for _, msg := range channelMessages.Messages {
		if msg.Id <= lastSeen {
			continue
		}
		re.unconsumedChannels[channelMessages.ChannelName] = append(re.unconsumedChannels[channelMessages.ChannelName], msg.Value)
		if msg.Id > highestID {
			highestID = msg.Id
		}
		hasNewMsg = true
	}
	if highestID > lastSeen {
		re.lastSeenExtMsgID.Store(highestID)
	}
	return hasNewMsg, false
}

// addTimers schedules a local timer for every TimerCondition inside
// stepExe.WaitForCondition.
func (re *runExecutor) addTimers(stepExe *pb.ActiveStepExecution) {
	if stepExe.WaitForCondition == nil {
		return
	}
	for _, condition := range stepExe.WaitForCondition.Conditions {
		timer := condition.GetTimer()
		if timer == nil {
			continue
		}
		re.timerMgr.Add(time.UnixMilli(timer.FireAtUnixMs))
	}
}

func (re *runExecutor) processNextSteps(fromStepExeID string, nextSteps []*pb.NextStep) {
	for _, nextStep := range nextSteps {
		counter := re.stepExeIDCounters[nextStep.StepId] + 1
		re.stepExeIDCounters[nextStep.StepId] = counter
		newExeID := nextStep.StepId + "-" + strconv.Itoa(int(counter))
		status := pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_WAIT_FOR
		kind := stepTaskMethodKindWaitFor
		if nextStep.SkipWaitFor {
			status = pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_EXECUTE
			kind = stepTaskMethodKindExecute
		}

		stepExe := &pb.ActiveStepExecution{
			Input:              nextStep.Input,
			Status:             status,
			FromStepExeId:      fromStepExeID,
			WaitForMethodExeId: nextStep.WaitForMethodExeId,
			ExecuteMethodExeId: nextStep.ExecuteMethodExeId,
		}
		re.allActiveStepExecutions[newExeID] = stepExe

		re.pendingStepTasks[newExeID] = &stepInvocationTask{
			stepExeId: newExeID,
			kind:      kind,
		}
	}
}

// decisionToProto converts the internal StepDecision into the
// StepExecuteCompletedRequest proto
func (re *runExecutor) decisionToProto(
	decision StepDecision,
) (pb.StopDecision, []*pb.NextStep, error) {
	stopDecision := pb.StopDecision_STOP_DECISION_NONE

	if terminal := stepDecisionTerminal(decision); terminal != nil {
		switch terminal.Type {
		case TerminalComplete:
			stopDecision = pb.StopDecision_STOP_DECISION_COMPLETE
		case TerminalFail:
			stopDecision = pb.StopDecision_STOP_DECISION_FAIL
		case TerminalDeadEnd:
			stopDecision = pb.StopDecision_STOP_DECISION_DEAD_END
		}
	}

	movements := stepDecisionMovements(decision)
	if len(movements) > 0 {
		var err error
		nextSteps, err := re.movementsToNextSteps(movements)
		if err != nil {
			return pb.StopDecision_STOP_DECISION_NONE, nil, err
		}
		return stopDecision, nextSteps, nil
	}

	return stopDecision, nil, nil
}

func (re *runExecutor) movementsToNextSteps(movements []StepMovement) ([]*pb.NextStep, error) {
	if len(movements) == 0 {
		return nil, nil
	}
	codec := re.worker.registry.ObjectCodec()
	nextSteps := make([]*pb.NextStep, len(movements))
	for index, movement := range movements {
		inputVal, err := codec.EncodeValue(movement.Input)
		if err != nil {
			return nil, fmt.Errorf("encode movement input: %w", err)
		}
		skipWaitFor := false
		var optionsSnapshot *pb.StepOptionsSnapshot
		if step, ok := re.worker.registry.getStep(re.flowType, movement.StepID); ok {
			skipWaitFor = ShouldSkipWaitFor(step)
			optionsSnapshot = stepOptionsToSnapshot(step)
		}
		nextSteps[index] = &pb.NextStep{
			StepId:              movement.StepID,
			Input:               inputVal,
			SkipWaitFor:         skipWaitFor,
			StepOptionsSnapshot: optionsSnapshot,
		}
		if skipWaitFor {
			nextSteps[index].ExecuteMethodExeId = re.bumpStepMethodExeCounter()
		} else {
			nextSteps[index].WaitForMethodExeId = re.bumpStepMethodExeCounter()
		}
	}
	return nextSteps, nil
}

func (re *runExecutor) bumpStepMethodExeCounter() int64 {
	re.stepMethodExeCounter++
	return re.stepMethodExeCounter
}

type retrySyncEvent struct {
	stepExeID string
	kind      stepTaskMethodKind
	state     *pb.StepRetryState
}

// enqueueRetryStateSyncChannel hands a retry-state update to the run loop.
// The send is cancelable: when the buffer is full and the task ctx is canceled
// (run-loop exit → cancelInFlightStepTasks), we drop the update instead of
// blocking forever — nothing drains retryStateSyncCh after the loop exits, and
// the state is best-effort (server also has the last synced value).
func (re *runExecutor) enqueueRetryStateSyncChannel(ctx context.Context, stepExeID string, kind stepTaskMethodKind, state *pb.StepRetryState) {
	state = truncateStepRetryState(state, re.worker.opts.stepRetryLastErrorMaxBytes())
	select {
	case re.retryStateSyncCh <- retrySyncEvent{stepExeID: stepExeID, kind: kind, state: state}:
	case <-ctx.Done():
	}
}

func (re *runExecutor) buildWorkerCallContext(completedWaitForStepExeId *string) *pb.WorkerCallContext {
	callContext := re.makeCallContext()
	callContext.ActiveStepRetryUpdates = re.collectRetryUpdates(completedWaitForStepExeId)
	return callContext
}

func (re *runExecutor) collectRetryUpdates(completedWaitForStepExeId *string) map[string]*pb.StepRetryStateUpdate {
	events := re.drainRetrySyncChannel()
	if len(events) == 0 && completedWaitForStepExeId == nil {
		return nil
	}
	updates := mergeRetrySyncEvents(events, completedWaitForStepExeId)
	if len(updates) == 0 {
		return nil
	}
	return updates
}

func mergeRetrySyncEvents(
	events []retrySyncEvent,
	completedWaitForStepExeId *string,
) map[string]*pb.StepRetryStateUpdate {
	updates := make(map[string]*pb.StepRetryStateUpdate)
	for _, event := range events {
		update := updates[event.stepExeID]
		if update == nil {
			update = &pb.StepRetryStateUpdate{}
			updates[event.stepExeID] = update
		}
		switch event.kind {
		case stepTaskMethodKindWaitFor:
			update.WaitForRetryState = event.state
		case stepTaskMethodKindExecute:
			update.ExecuteRetryState = event.state
		}
	}
	if completedWaitForStepExeId != nil {
		update, ok := updates[*completedWaitForStepExeId]
		if !ok {
			update = &pb.StepRetryStateUpdate{}
			updates[*completedWaitForStepExeId] = update
		}
		update.WaitForRetryState = nil
		update.ExecuteRetryState = nil
		update.ClearWaitForRetryState = true
	}

	return updates
}

func (re *runExecutor) drainRetrySyncChannel() []retrySyncEvent {
	var events []retrySyncEvent
	for {
		select {
		case event := <-re.retryStateSyncCh:
			events = append(events, event)
		default:
			return events
		}
	}
}

func (re *runExecutor) makeCallContext() *pb.WorkerCallContext {
	counter := re.requestCounter.Add(1)
	return &pb.WorkerCallContext{
		WorkerId:                             re.worker.workerID,
		WorkerRequestCounter:                 counter,
		LastReceivedExternalChannelMessageId: re.lastSeenExtMsgID.Load(),
	}
}

func truncateStepRetryState(state *pb.StepRetryState, maxBytes int) *pb.StepRetryState {
	if state == nil {
		return nil
	}
	return &pb.StepRetryState{
		FirstAttemptTimeMs:  state.FirstAttemptTimeMs,
		CurrentAttempts:     state.CurrentAttempts,
		LastError:           truncateUTF8Bytes(state.LastError, maxBytes),
		LastErrorStackTrace: truncateUTF8Bytes(state.LastErrorStackTrace, maxBytes),
	}
}

// use state snapshot to avoid concurrent access racing from multi goroutines
func (re *runExecutor) getStateSnapshot() map[string]*pb.Value {
	current := re.state.Load()
	if current == nil {
		return make(map[string]*pb.Value)
	}
	return maps.Clone(*current)
}

func (re *runExecutor) mergeStateUpsert(upsert map[string]*pb.Value) {
	if len(upsert) == 0 {
		return
	}
	var base map[string]*pb.Value
	if current := re.state.Load(); current != nil {
		base = *current
	}
	next := maps.Clone(base)
	for key, value := range upsert {
		next[key] = value
	}
	re.state.Store(&next)
}

func resolveCancelTargets(
	currStepExeId string,
	allActiveStepExecutions map[string]*pb.ActiveStepExecution,
	cancelStepIDs map[string]bool,
) []string {
	if len(cancelStepIDs) == 0 {
		return nil
	}

	parentStepExeId := allActiveStepExecutions[currStepExeId].FromStepExeId
	var out []string
	for exeID, stepExe := range allActiveStepExecutions {
		if exeID == currStepExeId || stepExe.FromStepExeId != parentStepExeId {
			continue
		}
		if !cancelStepIDs[StepIDFromStepExecutionID(exeID)] {
			continue
		}
		out = append(out, exeID)
	}
	return out
}

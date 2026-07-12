package dex

import (
	"context"
	"errors"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// stepTaskCompletion is step-method final result sent from a stepMethodRunner to runExecutor.
//
// Typical combinations:
//
//		WaitFor OK:        waitForCondition + persistence + methodReport if recovered
//		Execute OK:        decision + persistence + methodReport if recovered
//		Execute failure:   decision=Fail or GoTo(proceed) + methodReport=failed
//		WaitFor failure:   waitForMethodFailed + methodReport=failed + decision if proceed
//		Empty:             to ignore only -- task ctx cancel or worker shutdown;
//	    StopErr: 		   run cannot make more progress due to an error, need to stop the runExecutor
type stepTaskCompletion struct {
	// Which inflight step produced this completion (id, kind, input, pre-eval).
	task *stepInvocationTask

	// Execute terminal/movement outcome; WaitFor proceed stores GoTo here.
	decision StepDecision

	// WaitFor condition tree on success path only.
	waitForCondition WaitForCondition

	// State upserts and channel publishes staged during the method body.
	persistence *persistenceImpl

	// Retry diagnostics for StepMethodReport on the RPC (failed, recovered, or proceed).
	// Absent on first-attempt success with no prior failures.
	methodReport *pb.StepMethodReport

	// Routes to handleWaitForMethodFailed instead of handleWaitForSucceeded.
	waitForMethodFailed bool

	// Set when task ctx was canceled or worker shut down; runExecutor skips server RPC.
	empty bool

	// Set when run cannot make more progress
	stopErr error
}

type stepMethodRunner struct {
	executor *runExecutor
	task     *stepInvocationTask
	step     stepCommon
	reg      *flowRegistration
	taskCtx  context.Context

	// note: only keep at most 2 last failures
	last2AttemptFailures []methodAttemptFailure
}

type methodAttemptFailure struct {
	err   error
	stack string
}

// attemptOutcome is one invokeOnce result inside the retry loop.
type attemptOutcome struct {
	decision          StepDecision
	waitForCondition  WaitForCondition
	persistence       *persistenceImpl
	err               error
	failureStackTrace string
}

func newStepMethodRunner(
	executor *runExecutor, task *stepInvocationTask, step stepCommon, reg *flowRegistration, taskCtx context.Context,
) *stepMethodRunner {
	return &stepMethodRunner{
		executor: executor,
		task:     task,
		step:     step,
		reg:      reg,
		taskCtx:  taskCtx,
	}
}

func (smr *stepMethodRunner) run() stepTaskCompletion {
	opts := smr.step.GetStepOptions()
	var retryPolicy *RetryPolicy
	var timeout time.Duration
	var retryKind stepTaskMethodKind

	switch smr.task.kind {
	case stepTaskMethodKindWaitFor:
		var configuredRetry *RetryPolicy
		var configuredTimeout time.Duration
		if opts != nil {
			configuredRetry = opts.WaitForMethodRetryPolicy
			configuredTimeout = opts.WaitForMethodTimeout
		}
		retryPolicy = resolveRetryPolicy(configuredRetry)
		timeout = resolveMethodTimeout(configuredTimeout, defaultWaitForMethodTimeout)
		retryKind = stepTaskMethodKindWaitFor
	case stepTaskMethodKindExecute:
		var configuredRetry *RetryPolicy
		var configuredTimeout time.Duration
		if opts != nil {
			configuredRetry = opts.ExecuteMethodRetryPolicy
			configuredTimeout = opts.ExecuteMethodTimeout
		}
		retryPolicy = resolveRetryPolicy(configuredRetry)
		timeout = resolveMethodTimeout(configuredTimeout, defaultExecuteMethodTimeout)
		retryKind = stepTaskMethodKindExecute
	default:
		return stepTaskCompletion{stopErr: fmt.Errorf("unknown step kind: %v", smr.task.kind)}
	}

	maxErrorBytes := smr.executor.worker.opts.stepRetryLastErrorMaxBytes()

	currRetryState := smr.initRetryStateFromActive()

	for {
		if currRetryState != nil {
			delay := getRetryDelayBeforeNextAttempt(retryPolicy, currRetryState)
			if delay > 0 {
				smr.executor.worker.log.Debug("Step method retry scheduled",
					"runID", smr.executor.runID,
					"stepExeID", smr.task.stepExeId,
					"attempt", currRetryState.CurrentAttempts,
					"delayMs", delay.Milliseconds())
				if !smr.waitForRetryOrCanceled(smr.taskCtx, delay) {
					return smr.buildEmptyCompletion()
				}
			}
		}

		methodCtx, cancelMethod := context.WithTimeout(smr.taskCtx, timeout)
		outcome := smr.invokeOnce(methodCtx)
		cancelMethod()

		if smr.taskCtx.Err() != nil {
			return smr.buildEmptyCompletion()
		}

		if outcome.err != nil && isRunNonProcessableError(outcome.err) {
			smr.executor.worker.log.Error("Run non-processable error, yield run",
				"runID", smr.executor.runID,
				"stepExeID", smr.task.stepExeId,
				"stepTaskMethodKind", smr.task.kind,
				"error", outcome.err,
			)
			return stepTaskCompletion{task: smr.task, stopErr: outcome.err}
		}

		if outcome.err == nil {
			attemptCount := int32(1)
			if currRetryState != nil {
				attemptCount = currRetryState.CurrentAttempts + 1
			}
			return smr.buildSuccessCompletion(outcome, attemptCount, maxErrorBytes)
		}
		if errors.Is(outcome.err, context.DeadlineExceeded) {
			smr.executor.worker.log.Debug("Step method timeout",
				"runID", smr.executor.runID,
				"stepExeID", smr.task.stepExeId,
				"timeoutMs", timeout.Milliseconds())
		}

		effectiveNowMs := smr.executor.timerMgr.Now()
		currRetryState = newRetryStateAfterFailure(
			currRetryState, outcome.err, outcome.failureStackTrace, effectiveNowMs, maxErrorBytes,
		)
		smr.recordToLast2AttemptFailures(outcome.err, outcome.failureStackTrace)
		smr.executor.enqueueRetryStateSyncChannel(smr.taskCtx, smr.task.stepExeId, retryKind, currRetryState)

		if isRetryExhausted(retryPolicy, currRetryState, time.UnixMilli(effectiveNowMs)) {
			lastErr := outcome.err
			methodReport := buildMethodReportFailed(smr.last2AttemptFailures, currRetryState.CurrentAttempts, maxErrorBytes)
			if proceedAsCompletion := smr.buildRetryExhaustedProceedToIfRegisteredHandler(retryKind, methodReport); proceedAsCompletion != nil {
				return *proceedAsCompletion
			}
			smr.executor.worker.log.Warn("Step method retries exhausted, failing the run",
				"runID", smr.executor.runID,
				"stepExeID", smr.task.stepExeId,
				"attempts", currRetryState.CurrentAttempts,
				"error", lastErr)
			return buildFailureCompletion(
				smr.task, smr.last2AttemptFailures, currRetryState.CurrentAttempts, maxErrorBytes,
			)
		}
	}
}

func (smr *stepMethodRunner) buildSuccessCompletion(
	outcome attemptOutcome, attemptCount int32, maxErrorBytes int,
) stepTaskCompletion {
	completion := stepTaskCompletion{
		task:        smr.task,
		persistence: outcome.persistence,
		methodReport: buildMethodReportIfRecoveredFromFailure(
			smr.last2AttemptFailures, attemptCount, maxErrorBytes,
		),
	}
	if outcome.decision != nil {
		completion.decision = outcome.decision
	}
	if outcome.waitForCondition != nil {
		completion.waitForCondition = outcome.waitForCondition
	}
	return completion
}

func (smr *stepMethodRunner) buildEmptyCompletion() stepTaskCompletion {
	smr.executor.worker.log.Debug("step completion empty",
		"runID", smr.executor.runID,
		"stepExeID", smr.task.stepExeId,
		"stepTaskMethodKind", smr.task.kind,
	)
	return stepTaskCompletion{task: smr.task, empty: true}
}

func (smr *stepMethodRunner) recordToLast2AttemptFailures(err error, stack string) {
	smr.last2AttemptFailures = append(
		smr.last2AttemptFailures, methodAttemptFailure{err: err, stack: stack},
	)
	total := len(smr.last2AttemptFailures)
	if total > 2 {
		smr.last2AttemptFailures = smr.last2AttemptFailures[total-2:]
	}
}

func (smr *stepMethodRunner) initRetryStateFromActive() *pb.StepRetryState {
	active := smr.task.activeStepExe
	if active == nil {
		return nil
	}
	switch smr.task.kind {
	case stepTaskMethodKindWaitFor:
		return active.WaitForRetryState
	case stepTaskMethodKindExecute:
		return active.ExecuteRetryState
	default:
		return nil
	}
}

func (smr *stepMethodRunner) invokeOnce(ctx context.Context) attemptOutcome {
	stepExe := smr.task.activeStepExe
	fromStepExeID := ""
	var inputVal *pb.Value
	if stepExe != nil {
		fromStepExeID = stepExe.FromStepExeId
		inputVal = stepExe.Input
	}
	lfCtx := newBaseContext(
		ctx, smr.executor.runID, smr.task.stepExeId, fromStepExeID,
		smr.executor.worker.rootCtx.Done(),
	)
	stateSnapshot := smr.executor.getStateSnapshot()
	switch smr.task.kind {
	case stepTaskMethodKindExecute:
		results := conditionResultsForExecute(stepExe, smr.task.consumedChannelMessages)
		decision, persistence, err := executeStepReflect(
			smr.executor.worker.registry.ObjectCodec(),
			smr.step, lfCtx, inputVal,
			stateSnapshot, smr.reg.schema, results,
		)
		return smr.withAttemptFailureStack(
			attemptOutcome{decision: decision, persistence: persistence, err: err},
			stepTaskMethodKindExecute,
		)
	case stepTaskMethodKindWaitFor:
		waitCondition, persistence, err := executeWaitForReflect(
			smr.executor.worker.registry.ObjectCodec(),
			smr.step, lfCtx, inputVal,
			stateSnapshot, smr.reg.schema,
		)
		return smr.withAttemptFailureStack(
			attemptOutcome{waitForCondition: waitCondition, persistence: persistence, err: err},
			stepTaskMethodKindWaitFor,
		)
	default:
		return attemptOutcome{err: fmt.Errorf("unknown step kind: %v", smr.task.kind)}
	}
}

func (smr *stepMethodRunner) withAttemptFailureStack(outcome attemptOutcome, kind stepTaskMethodKind) attemptOutcome {
	if outcome.err != nil {
		outcome.failureStackTrace = formatMethodFailureStack(
			outcome.err, smr.step, kind, captureCallerFrame(2))
	}
	return outcome
}

// return false if wait is canceled
func (smr *stepMethodRunner) waitForRetryOrCanceled(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-smr.executor.worker.rootCtx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (smr *stepMethodRunner) buildRetryExhaustedProceedToIfRegisteredHandler(
	retryKind stepTaskMethodKind, methodReport *pb.StepMethodReport,
) *stepTaskCompletion {
	opts := smr.step.GetStepOptions()
	handler := proceedHandlerForMethod(opts, retryKind)
	if handler == nil {
		smr.executor.worker.log.Debug("ProceedToAfterRetryExhausted not defined",
			"runID", smr.executor.runID,
			"stepExeID", smr.task.stepExeId)
		return nil
	}
	handlerStepID := stepIDFromCommon(handler)
	if _, ok := smr.reg.steps[handlerStepID]; !ok {
		smr.executor.worker.log.Error("ProceedToAfterRetryExhausted handler not registered",
			"runID", smr.executor.runID,
			"stepExeID", smr.task.stepExeId,
			"handlerStepID", handlerStepID)
		return &stepTaskCompletion{
			stopErr: fmt.Errorf("handler not registered: %v", handlerStepID),
		}
	}

	smr.executor.worker.log.Info("Step method retries exhausted, proceeding to error handler",
		"runID", smr.executor.runID,
		"stepExeID", smr.task.stepExeId,
		"stepTaskMethodKind", retryKind,
		"handlerStepID", handlerStepID,
		"attempts", methodReport.GetAttemptCount())
	stepExe := smr.task.activeStepExe
	var failingInput *pb.Value
	if stepExe != nil {
		failingInput = stepExe.Input
	}
	completion, err := buildProceedCompletion(
		retryKind,
		smr.task,
		handler,
		failingInput,
		methodReport,
		smr.executor.worker.registry.ObjectCodec(),
	)
	if err != nil {
		smr.executor.worker.log.Error("ProceedToAfterRetryExhausted proceed build failed",
			"runID", smr.executor.runID,
			"stepExeID", smr.task.stepExeId,
			"error", err)
		return &stepTaskCompletion{
			stopErr: err,
		}
	}
	return completion
}

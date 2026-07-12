package dex

import (
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func bestDiagnosticFailure(attempts []methodAttemptFailure) (errMsg, stack string) {
	if len(attempts) == 0 {
		return "", ""
	}
	last := attempts[len(attempts)-1]
	if len(attempts) == 1 {
		return last.err.Error(), last.stack
	}
	if !isTimeoutError(last.err) {
		return last.err.Error(), last.stack
	}
	for index := len(attempts) - 2; index >= 0; index-- {
		if !isTimeoutError(attempts[index].err) {
			return attempts[index].err.Error(), attempts[index].stack
		}
	}
	return last.err.Error(), last.stack
}

func buildMethodReportFailed(attempts []methodAttemptFailure, attemptCount int32, maxBytes int) *pb.StepMethodReport {
	errMsg, stack := bestDiagnosticFailure(attempts)
	return &pb.StepMethodReport{
		Outcome:         pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED,
		Error:           truncateUTF8Bytes(errMsg, maxBytes),
		ErrorStackTrace: truncateUTF8Bytes(stack, maxBytes),
		AttemptCount:    attemptCount,
	}
}

func buildMethodReportIfRecoveredFromFailure(attempts []methodAttemptFailure, attemptCount int32, maxBytes int) *pb.StepMethodReport {
	if len(attempts) == 0 || attemptCount <= 1 {
		return nil
	}
	errMsg, stack := bestDiagnosticFailure(attempts)
	return &pb.StepMethodReport{
		Outcome:         pb.StepMethodOutcome_STEP_METHOD_OUTCOME_SUCCEEDED,
		Error:           truncateUTF8Bytes(errMsg, maxBytes),
		ErrorStackTrace: truncateUTF8Bytes(stack, maxBytes),
		AttemptCount:    attemptCount,
	}
}

// buildFailureCompletion forms a channel-ready failure completion.
func buildFailureCompletion(
	task *stepInvocationTask,
	failures []methodAttemptFailure,
	attemptCount int32,
	maxErrorBytes int,
) stepTaskCompletion {
	methodReport := buildMethodReportFailed(failures, attemptCount, maxErrorBytes)
	completion := stepTaskCompletion{
		task:         task,
		methodReport: methodReport,
	}
	if task.kind == stepTaskMethodKindWaitFor {
		completion.waitForMethodFailed = true
	} else {
		completion.decision = Fail(methodReport.GetError())
	}
	return completion
}

func computeRetryDelay(policy *RetryPolicy, failedAttempts int32) time.Duration {
	if failedAttempts <= 0 {
		return 0
	}
	initial := policy.InitialInterval
	if initial <= 0 {
		initial = time.Second
	}
	coefficient := policy.BackoffCoefficient
	if coefficient < 1.0 {
		coefficient = 1.0
	}
	delay := float64(initial)
	for index := int32(1); index < failedAttempts; index++ {
		delay *= coefficient
	}
	maximum := policy.MaximumInterval
	if maximum > 0 && time.Duration(delay) > maximum {
		return maximum
	}
	return time.Duration(delay)
}

func isRetryExhausted(policy *RetryPolicy, state *pb.StepRetryState, now time.Time) bool {
	if policy.MaxAttempts > 0 && state.CurrentAttempts >= int32(policy.MaxAttempts) {
		return true
	}
	if policy.TotalTimeout > 0 {
		firstAttempt := time.UnixMilli(state.FirstAttemptTimeMs)
		if now.Sub(firstAttempt) >= policy.TotalTimeout {
			return true
		}
	}
	return false
}

func newRetryStateAfterFailure(
	existing *pb.StepRetryState,
	methodErr error,
	stackTrace string,
	serverNowMs int64,
	maxBytes int,
) *pb.StepRetryState {
	state := &pb.StepRetryState{
		CurrentAttempts:    1,
		FirstAttemptTimeMs: serverNowMs,
	}
	if existing != nil {
		state.FirstAttemptTimeMs = existing.FirstAttemptTimeMs
		state.CurrentAttempts = existing.CurrentAttempts + 1
	}
	state.LastError = truncateUTF8Bytes(methodErr.Error(), maxBytes)
	state.LastErrorStackTrace = truncateUTF8Bytes(stackTrace, maxBytes)
	return state
}

func getRetryDelayBeforeNextAttempt(policy *RetryPolicy, state *pb.StepRetryState) time.Duration {
	if state == nil || state.CurrentAttempts <= 0 {
		return 0
	}
	return computeRetryDelay(policy, state.CurrentAttempts)
}

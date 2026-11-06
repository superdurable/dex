// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"strings"
	"time"
)

// RecoveryErrorInfo describes the final failure that selected a recovery path.
// Worker failures preserve their detail and error type. Backend failures use the backend failure type.
//
// A handler reads this value with Context.RecoveryError. It is present in an Execute that follows
// exhausted WaitFor retries under ProceedOnWaitForFailure. It is also present in a recovery Step
// selected by StepOptions.ExecuteFailure or FlowTimeoutHandlerOptions.Failure.
//
// Use IsMethodTimeout, IsHeartbeatTimeout, and IsRetryTotalDurationExceeded to recognize the Dex
// time limits. Do not compare ErrorType with a fixed string for these conditions. Each Dex backend
// spells these error types differently, and the methods accept every spelling. The methods return
// false for a nil receiver, so the result of Context.RecoveryError needs no nil check before the
// call. Other failures keep the error type that the Worker reported, such as an application error
// type.
//
//	package billing
//
//	import "github.com/superdurable/dex/sdk-go/dex"
//
//	// ChargeRecoveryStep runs after the charge Step exhausts its Execute retries.
//	type ChargeRecoveryStep struct {
//		dex.StepDefaultsNoWaitFor[string]
//	}
//
//	func (ChargeRecoveryStep) Execute(ctx dex.Context, orderID string) (*dex.StepDecision, error) {
//		failure := ctx.RecoveryError()
//		switch {
//		case failure.IsMethodTimeout():
//			return dex.ForceFail("charging order " + orderID + " exceeded its method timeout"), nil
//		case failure.IsHeartbeatTimeout():
//			return dex.ForceFail("charging order " + orderID + " stopped sending heartbeats"), nil
//		case failure != nil && failure.AttemptProgress == dex.StepAttemptProgressNotStarted:
//			return dex.ForceFail("charging order " + orderID + " never reached a Worker"), nil
//		case failure.IsRetryTotalDurationExceeded():
//			return dex.ForceFail("charging order " + orderID + " used its whole retry duration"), nil
//		case failure != nil:
//			return dex.ForceFail("charging order " + orderID + " failed: " + failure.ErrorType), nil
//		default:
//			return dex.ForceFail("charging order " + orderID + " has no recorded failure"), nil
//		}
//	}
type RecoveryErrorInfo struct {
	// Detail is the failure message supplied by the Worker or backend.
	Detail string
	// ErrorType is the original Worker error type or backend failure type.
	ErrorType string
	// AttemptProgress reports the furthest progress across all attempts of this Step method call.
	// NotStarted guarantees the handler did not run. NotCompleted means side effects may have happened.
	// Unspecified is the zero value from older Servers and must be treated like NotCompleted.
	// It also covers method calls begun before the upgrade. Only synchronous Steps can report NotStarted.
	// Asynchronous failures always report NotCompleted because an uncommitted start may have rolled back.
	// NotCompleted does not prove execution: a task can be lost after hand-out or fail decoding before the handler.
	// Reconcile external effects using a stable business or idempotency key before repeating them.
	// Available since SDK v1.35.0.
	// For example, retry a side effect directly only when failure.AttemptProgress == StepAttemptProgressNotStarted.
	AttemptProgress StepAttemptProgress
}

// StepAttemptProgress describes whether any attempt of a failed Step method call may have run.
// Read RecoveryErrorInfo.AttemptProgress in a recovery handler before repeating external side effects.
// The zero value is StepAttemptProgressUnspecified; treat it like StepAttemptProgressNotCompleted.
// See the complete RecoveryErrorInfo example for a recovery handler that checks progress.
// Available since SDK v1.35.0.
type StepAttemptProgress uint8

const (
	// StepAttemptProgressUnspecified means an older Server did not report progress.
	// Available since SDK v1.35.0.
	// Treat it like StepAttemptProgressNotCompleted because side effects may have happened.
	StepAttemptProgressUnspecified StepAttemptProgress = iota
	// StepAttemptProgressNotStarted guarantees the handler did not run in any attempt.
	// Available since SDK v1.35.0.
	StepAttemptProgressNotStarted
	// StepAttemptProgressNotCompleted means some attempt may have run, but none completed.
	// Available since SDK v1.35.0.
	// Recovery must account for possible external side effects before repeating them.
	StepAttemptProgressNotCompleted
)

// IsMethodTimeout reports whether the final attempt ran longer than its Step method timeout.
//
// Use it in a recovery path to handle an attempt that exceeded StepOptions.WaitForMethodTimeout,
// StepOptions.ExecuteMethodTimeout, or FlowTimeoutHandlerOptions.MethodTimeout. See the
// RecoveryErrorInfo example.
//
// If the call to the Worker fails before Dex records the method timeout, Dex reports a Worker API
// failure instead, and this method returns false. A Worker API failure has many other causes, such
// as an unreachable Worker or an invalid response, so do not treat it as a timeout.
//
// A Worker error type that reuses the backend's method timeout name also matches. Give application
// error types distinct names.
//
// IsMethodTimeout returns true when ErrorType names a method timeout, ignoring letter case and
// underscores. It returns false for every other ErrorType and for a nil receiver.
func (info *RecoveryErrorInfo) IsMethodTimeout() bool {
	return info.hasCanonicalErrorType("STARTTOCLOSE")
}

// IsHeartbeatTimeout reports whether the final attempt stopped recording heartbeats in time.
//
// Use it in a recovery path to handle an attempt that did not call Context.RecordHeartbeat within
// StepOptions.HeartbeatTimeout or FlowTimeoutHandlerOptions.HeartbeatTimeout. See the
// RecoveryErrorInfo example. A Worker error type that reuses the backend's heartbeat timeout name
// also matches. Give application error types distinct names.
//
// IsHeartbeatTimeout returns true when ErrorType names a heartbeat timeout, ignoring letter case
// and underscores. It returns false for every other ErrorType and for a nil receiver.
func (info *RecoveryErrorInfo) IsHeartbeatTimeout() bool {
	return info.hasCanonicalErrorType("HEARTBEAT")
}

// IsRetryTotalDurationExceeded recognizes historical failures that ended a running attempt at the retry total duration.
// Current server-zd never ends a running attempt at RetryPolicy.TotalDuration, so this returns false for current records.
// It may return true for records written before the upgrade. TotalDuration now only decides whether to retry after failure.
// Changed in SDK v1.35.0: the current Server no longer produces retry-duration failures for running attempts.
// See the RecoveryErrorInfo example for use in a recovery handler.
//
// When the total duration ends between attempts, Dex starts no further attempt. It reports the
// error type of the final attempt instead, so this method returns false. Check the other methods
// and ErrorType to handle that case. A Worker error type that reuses the backend's deadline name
// also matches. Give application error types distinct names.
//
// IsRetryTotalDurationExceeded returns true when ErrorType names the historical overall deadline, ignoring
// letter case and underscores. It returns false for every other ErrorType and for a nil receiver.
func (info *RecoveryErrorInfo) IsRetryTotalDurationExceeded() bool {
	return info.hasCanonicalErrorType("SCHEDULETOCLOSE")
}

// hasCanonicalErrorType compares ErrorType after uppercasing it and dropping underscores.
func (info *RecoveryErrorInfo) hasCanonicalErrorType(canonicalErrorType string) bool {
	if info == nil {
		return false
	}
	return strings.ReplaceAll(strings.ToUpper(info.ErrorType), "_", "") == canonicalErrorType
}

// Context provides invocation metadata and stages durable mutations for Step and RPC handlers.
//
// A Context belongs to one handler attempt and must not outlive it. Attribute writes, Channel
// publications, locals, and events commit atomically only when the handler returns successfully.
type Context interface {
	// Context supports cancellation and deadlines for the active handler attempt.
	// Deadline returns the Server-supplied absolute deadline and whether one exists.
	// Use a child context for an external call that needs a shorter timeout.
	// Changed in SDK v1.35.0: a Step attempt deadline is hand-out time plus the method timeout,
	// without shortening it to the remaining retry budget. Queue and lock waits are excluded.
	context.Context

	// FlowID returns the application-assigned Flow ID.
	FlowID() string
	// RunID returns the server-assigned run ID.
	// The Flow ID stays stable when Dex creates a replacement run, for example after TimeTravel;
	// RunID identifies that particular run and can change during the Flow's lifetime.
	RunID() string
	// FlowStartedAt returns when the current Flow run started.
	FlowStartedAt() time.Time
	// StepExecutionID returns the current Step execution ID, or empty for an RPC.
	// It stays stable across retries of this Step execution. Together with FlowID it can key idempotent external work.
	StepExecutionID() string
	// FromStepExecutionID returns the predecessor Step execution ID, or empty when absent.
	FromStepExecutionID() string
	// RecoveryError returns the final failure that selected this recovery path, or nil when absent.
	RecoveryError() *RecoveryErrorInfo
	// FirstAttemptAt returns when the first attempt of this handler invocation started.
	FirstAttemptAt() time.Time
	// Attempt returns the one-based handler attempt number.
	// WaitFor and Execute have independent retry sequences. Do not use Attempt as an idempotency key.
	// Changed in SDK v1.35.0: an interrupted handed-out attempt consumes a retry.
	// An asynchronous start lost before commit can repeat the same attempt number; see StepDurabilityAsync.
	Attempt() int32

	// HasTimerFired reports whether any Timer Condition completed for this Execute invocation.
	HasTimerFired() bool
	// HasTimerFiredByIndex reports whether the zero-based Timer Condition completed.
	HasTimerFiredByIndex(index int) bool
	// WaitForMethodFailed reports whether Execute followed an exhausted WaitFor with Proceed policy.
	WaitForMethodFailed() bool

	// RecordHeartbeat reports progress for the active WaitFor, Execute, or Flow timeout invocation.
	//
	// A nil value, including a typed nil, clears persisted heartbeat details. Any
	// other value is encoded and supplied to the next retry attempt.
	// The call waits only for the Worker response frame to enter the gRPC stream;
	// it does not wait for backend heartbeat persistence. Checkpoints are batched and lossy:
	// an attempt ending or a Server node failure may lose the latest value. A retry receives the last stored value,
	// which can be older than the last value sent. Never use it as the sole proof of an external effect.
	// RecordHeartbeat and
	// Stream.Write may be called concurrently and serialize their output.
	// Encoding failures return ValueMappingError; transport failures return the
	// underlying gRPC error.
	//
	// RPC invocations and calls after the handler returns produce an error.
	//
	//	var checkpoint ImportCheckpoint
	//	found, err := ctx.GetLastHeartbeatValue(&checkpoint)
	//	if err != nil {
	//		return nil, err
	//	}
	//	if !found {
	//		checkpoint = ImportCheckpoint{}
	//	}
	//	if err := ctx.RecordHeartbeat(checkpoint); err != nil {
	//		return nil, err
	//	}
	RecordHeartbeat(value any) error
	// GetLastHeartbeatValue decodes the latest retryable handler heartbeat details.
	//
	// valuePtr must be a non-nil pointer. The method returns false without changing
	// valuePtr when no details exist. Checkpoints are available across retries in both
	// durability modes. Decode failures return ValueMappingError. See
	// RecordHeartbeat for an example.
	GetLastHeartbeatValue(valuePtr any) (found bool, err error)

	// SetStepExecutionLocal stages value under key for later attempts of this Step execution.
	SetStepExecutionLocal(key string, value any) error
	// GetStepExecutionLocal decodes a local value into non-nil valuePtr and reports whether it exists.
	GetStepExecutionLocal(key string, valuePtr any) (found bool, err error)
	// RecordEvent records one uniquely named diagnostic event with a serializable payload.
	RecordEvent(name string, value any) error
}

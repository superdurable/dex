// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"time"
)

// Context provides invocation metadata and stages durable mutations for Step and RPC handlers.
//
// A Context belongs to one handler attempt and must not outlive it. Attribute writes, Channel
// publications, locals, and events commit atomically only when the handler returns successfully.
type Context interface {
	// Context supports cancellation and deadlines for the active handler attempt.
	context.Context

	// FlowID returns the application-assigned Flow ID.
	FlowID() string
	// RunID returns the server-assigned run ID.
	RunID() string
	// FlowStartedAt returns when the current Flow run started.
	FlowStartedAt() time.Time
	// StepExecutionID returns the current Step execution ID, or empty for an RPC.
	StepExecutionID() string
	// FromStepExecutionID returns the predecessor Step execution ID, or empty when absent.
	FromStepExecutionID() string
	// FirstAttemptAt returns when the first attempt of this handler invocation started.
	FirstAttemptAt() time.Time
	// Attempt returns the one-based handler attempt number.
	Attempt() int32

	// HasTimerFired reports whether any Timer Condition completed for this Execute invocation.
	HasTimerFired() bool
	// HasTimerFiredByIndex reports whether the zero-based Timer Condition completed.
	HasTimerFiredByIndex(index int) bool
	// WaitForMethodFailed reports whether Execute followed an exhausted WaitFor with Proceed policy.
	WaitForMethodFailed() bool

	// RecordHeartbeat reports progress for the active WaitFor or Execute invocation.
	//
	// A nil value, including a typed nil, clears persisted heartbeat details. Any
	// other value is encoded and supplied to the next regular activity attempt.
	// The call waits only for the Worker response frame to enter the gRPC stream;
	// it does not wait for backend heartbeat persistence. RecordHeartbeat and
	// Stream.Write may be called concurrently and serialize their output.
	// Encoding failures return ValueMappingError; transport failures return the
	// underlying gRPC error.
	//
	// RPC and Flow timeout invocations, and calls after the handler returns, produce an error.
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
	// GetLastHeartbeatValue decodes the latest regular-activity heartbeat details.
	//
	// valuePtr must be a non-nil pointer. The method returns false without changing
	// valuePtr when no details exist. Local-activity heartbeats are not available to
	// fallback regular attempts. Decode failures return ValueMappingError. See
	// RecordHeartbeat for an example.
	GetLastHeartbeatValue(valuePtr any) (found bool, err error)

	// SetStepExecutionLocal stages value under key for later attempts of this Step execution.
	SetStepExecutionLocal(key string, value any) error
	// GetStepExecutionLocal decodes a local value into non-nil valuePtr and reports whether it exists.
	GetStepExecutionLocal(key string, valuePtr any) (found bool, err error)
	// RecordEvent records one uniquely named diagnostic event with a serializable payload.
	RecordEvent(name string, value any) error
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import "time"

// RetryPolicy overrides retry timing for a Step's WaitFor or Execute method.
// Zero fields preserve server defaults; MaximumAttempts counts the initial attempt.
type RetryPolicy struct {
	// InitialInterval is the delay before the first retry.
	InitialInterval time.Duration
	// BackoffCoefficient multiplies each successive retry interval.
	BackoffCoefficient float64
	// MaximumInterval caps the delay between retries.
	MaximumInterval time.Duration
	// MaximumAttempts limits total attempts, including the initial attempt.
	MaximumAttempts int32
	// TotalDuration limits elapsed time across all attempts.
	TotalDuration time.Duration
}

// StepDurability controls when staged persistence must be durably acknowledged.
type StepDurability uint8

const (
	// StepDurabilityDefault uses the Flow or server default.
	StepDurabilityDefault StepDurability = iota
	// StepDurabilitySync waits for persistence before completing the handler request.
	StepDurabilitySync
	// StepDurabilityAsync permits persistence after accepting the handler result.
	StepDurabilityAsync
)

// WaitForFailurePolicy selects behavior after WaitFor exhausts its retry policy.
type WaitForFailurePolicy uint8

const (
	// FailFlowOnWaitForFailure ends the Flow after WaitFor exhausts retries.
	FailFlowOnWaitForFailure WaitForFailurePolicy = iota
	// ProceedOnWaitForFailure invokes Execute and exposes the failure through Context.
	ProceedOnWaitForFailure
)

// ExecuteFailure identifies a recovery movement after Execute exhausts retries.
// Create it with ProceedToOnExecuteFailure.
type ExecuteFailure struct {
	step    StepDef
	options *StepOptions
}

// ProceedToOnExecuteFailure routes an Execute failure to step with the same typed input.
// options override the recovery Step configuration for this movement; nil uses registered options.
func ProceedToOnExecuteFailure[IN any](
	step Step[IN],
	options *StepOptions,
) *ExecuteFailure {
	return &ExecuteFailure{
		step:    typedStepDef[IN]{step: step},
		options: options,
	}
}

// StepOptions configures one Step's handler execution and persistence behavior.
//
// Zero values preserve server timeouts, retry behavior, and durability defaults. WaitFor failures
// fail the Flow by default. Locks are held only for their respective handler invocation.
type StepOptions struct {
	// WaitForMethodTimeout limits one WaitFor attempt; zero uses the server default.
	WaitForMethodTimeout time.Duration
	// ExecuteMethodTimeout limits one Execute attempt; zero uses the server default.
	ExecuteMethodTimeout time.Duration
	// WaitForRetry overrides the WaitFor retry policy; nil uses server defaults.
	WaitForRetry *RetryPolicy
	// ExecuteRetry overrides the Execute retry policy; nil uses server defaults.
	ExecuteRetry *RetryPolicy
	// WaitForFailure selects exhausted WaitFor behavior; zero fails the Flow.
	WaitForFailure WaitForFailurePolicy
	// ExecuteFailure selects a recovery Step after Execute exhausts retries.
	ExecuteFailure *ExecuteFailure
	// WaitForDurability overrides persistence durability for WaitFor writes.
	WaitForDurability StepDurability
	// ExecuteDurability overrides persistence durability for Execute writes.
	ExecuteDurability StepDurability
	// WaitForLockAttributes are acquired together for the WaitFor invocation.
	WaitForLockAttributes []AttributeLock
	// ExecuteLockAttributes are acquired together for the Execute invocation.
	ExecuteLockAttributes []AttributeLock
}

// WorkerTarget identifies the application WorkerService endpoint Dex should call.
type WorkerTarget struct {
	// Address is the advertised plaintext gRPC target, normally host:port.
	Address string
	// Headless bypasses service discovery and requires a direct host:port target.
	Headless bool
}

// ActiveStepSearchMode controls which active Step types Dex indexes for Flow search.
type ActiveStepSearchMode uint8

const (
	// SearchActiveStepsDefault uses the server default indexing policy.
	SearchActiveStepsDefault ActiveStepSearchMode = iota
	// SearchAllActiveSteps indexes every active Step, including execute-only Steps.
	SearchAllActiveSteps
	// SearchActiveStepsWithWaitFor indexes a Step only after WaitFor runs.
	SearchActiveStepsWithWaitFor
	// DisableActiveStepSearch disables active-Step indexing for the Flow.
	DisableActiveStepSearch
)

// FlowConfig overrides mutable runtime behavior for one Flow.
// Nil fields preserve server defaults.
type FlowConfig struct {
	// ActiveStepSearchMode controls active Step indexing.
	ActiveStepSearchMode *ActiveStepSearchMode
	// ContinueAsNewThreshold is the event-count threshold for Continue-As-New.
	ContinueAsNewThreshold *int32
	// ContinueAsNewPageSizeBytes caps history bytes carried into Continue-As-New.
	ContinueAsNewPageSizeBytes *int32
	// StepDurability sets the default persistence durability for Step writes.
	StepDurability *StepDurability
	// WorkerTarget routes future Step and RPC invocations.
	WorkerTarget *WorkerTarget
}

// StartFlowOptions configures a new Flow execution.
// Nil and zero fields preserve server defaults; RequestID is generated when omitted.
type StartFlowOptions struct {
	// Timeout limits total Flow execution duration.
	Timeout *time.Duration
	// IDReusePolicy controls reuse of an existing Flow ID.
	IDReusePolicy IDReusePolicy
	// CronSchedule is the server cron expression for recurring runs.
	CronSchedule string
	// StartDelay postpones the first Step after start acceptance.
	StartDelay *time.Duration
	// RetryPolicy configures whole-Flow retries after terminal failures.
	RetryPolicy *FlowRetryPolicy
	// Attributes supplies encoded initial Attribute and Attribute-map values.
	Attributes []InitialAttributeDef
	// ConfigOverride replaces registered Flow configuration for this execution.
	ConfigOverride *FlowConfig
	// AlreadyStarted controls whether an existing Flow is returned as success.
	AlreadyStarted *AlreadyStartedOptions
	// RequestID defaults to a UUID; set a stable business identifier for cross-call retries.
	RequestID *string
}

// IDReusePolicy controls whether StartFlow may reuse a Flow ID.
type IDReusePolicy uint8

const (
	// IDReuseDefault uses the server-configured reuse policy.
	IDReuseDefault IDReusePolicy = iota
	// IDReuseAllowIfPreviousFailed permits reuse only after a failed run.
	IDReuseAllowIfPreviousFailed
	// IDReuseAllowIfNotRunning permits reuse when no run is active.
	IDReuseAllowIfNotRunning
	// IDReuseDisallow rejects reuse regardless of previous status.
	IDReuseDisallow
	// IDReuseTerminateIfRunning terminates an active run before replacement.
	IDReuseTerminateIfRunning
)

// FlowRetryPolicy configures whole-Flow retry timing after terminal failure.
type FlowRetryPolicy struct {
	// InitialInterval is the delay before the first Flow retry.
	InitialInterval time.Duration
	// BackoffCoefficient multiplies successive Flow retry intervals.
	BackoffCoefficient float64
	// MaximumInterval caps time between Flow retries.
	MaximumInterval time.Duration
	// MaximumAttempts limits total Flow attempts, including the initial attempt.
	MaximumAttempts int32
}

// AlreadyStartedOptions configures StartFlow behavior when the Flow ID already exists.
type AlreadyStartedOptions struct {
	// IgnoreError returns the existing run ID instead of FlowAlreadyStartedError.
	IgnoreError bool
}

// InvokeOptions configures one RPC invocation.
type InvokeOptions struct {
	// Timeout limits the RPC handler; zero uses its registered or server default.
	Timeout time.Duration
	// LockAttributes are acquired atomically for the RPC invocation.
	LockAttributes []AttributeLock
}

// WaitOptions configures one long-poll wait operation.
type WaitOptions struct {
	// Timeout limits the long poll; zero uses the server default.
	Timeout time.Duration
}

// WaitForFlowOptions controls Flow-result hydration and long-poll timeout.
type WaitForFlowOptions struct {
	// NeedsResults asks Dex to include completed Step outputs.
	NeedsResults bool
	// Timeout limits the long poll; zero waits without an SDK override.
	Timeout time.Duration
}

// StopType selects how StopFlow ends an active Flow.
type StopType uint8

const (
	// CancelFlow requests cooperative cancellation.
	CancelFlow StopType = iota + 1
	// TerminateFlow ends the Flow immediately without cleanup.
	TerminateFlow
	// FailFlow marks the Flow failed with the supplied reason.
	FailFlow
)

// StopOptions configures Client.StopFlow.
type StopOptions struct {
	// Type selects cancel, terminate, or fail behavior.
	Type StopType
	// Reason is recorded with the stop operation.
	Reason string
}

// StepExecutionID identifies one execution of a Step type within a Flow run.
type StepExecutionID struct {
	// StepType is the stable registered Step type.
	StepType string
	// ExecutionNumber defaults to one when omitted.
	ExecutionNumber *int32
}

// TimerID selects one Timer Condition within a Step execution.
type TimerID struct {
	// ConditionID targets a Condition configured with WithConditionID.
	ConditionID string
	// Index targets a zero-based Timer Condition position when non-nil.
	Index *int32
}

// ResetType identifies the historical point selector used by ResetFlow.
type ResetType uint8

const (
	// ResetByHistoryEventID resets at a workflow-history event ID.
	ResetByHistoryEventID ResetType = iota + 1
	// ResetToBeginning resets before the first history event.
	ResetToBeginning
	// ResetByHistoryEventTime resets at the last eligible event by time.
	ResetByHistoryEventTime
	// ResetByStepType resets before the first execution of a Step type.
	ResetByStepType
	// ResetByStepExecutionID resets before an exact Step execution.
	ResetByStepExecutionID
)

// ResetOptions configures Client.ResetFlow and one reset-point selector.
type ResetOptions struct {
	// Type selects which reset-point field Dex reads.
	Type ResetType
	// HistoryEventID supplies the event ID for ResetByHistoryEventID.
	HistoryEventID int32
	// Reason is recorded with the reset operation.
	Reason string
	// HistoryEventTime supplies the time for ResetByHistoryEventTime.
	HistoryEventTime time.Time
	// StepType supplies the stable Step type for ResetByStepType.
	StepType string
	// StepExecutionID supplies the exact execution for ResetByStepExecutionID.
	StepExecutionID string
	// SkipChannelMessagesReapply prevents replay of post-reset Channel messages.
	SkipChannelMessagesReapply bool
	// SkipLockingRPCReapply prevents replay of post-reset locking RPCs.
	SkipLockingRPCReapply bool
}

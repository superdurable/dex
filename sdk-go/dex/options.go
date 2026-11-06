// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import "time"

// RetryPolicy overrides retry timing for a Step's WaitFor or Execute method.
// Zero fields preserve server defaults, including a four-hour total duration.
// MaximumAttempts counts the initial attempt.
// With asynchronous durability, all execution attempts share the retry limits and elapsed duration.
// Retry backoff continues across the attempts of the method call.
// Durations must be nonnegative and fit in int32 seconds; positive fractions round up to seconds.
// Invalid durations or a non-finite backoff coefficient fail registration or request validation.
//
// Example: allow three attempts with delays of one and two seconds, and a one-hour retry budget:
//
//	policy := &dex.RetryPolicy{
//		InitialInterval: time.Second, BackoffCoefficient: 2,
//		MaximumInterval: 100 * time.Second, MaximumAttempts: 3, TotalDuration: time.Hour,
//	}
//	options := &dex.StepOptions{ExecuteRetry: policy, ExecuteMethodTimeout: time.Hour}
type RetryPolicy struct {
	// InitialInterval is the delay before the first retry. Zero uses one second.
	InitialInterval time.Duration
	// BackoffCoefficient multiplies each successive retry interval. Zero uses 2.
	BackoffCoefficient float64
	// MaximumInterval caps the delay between retries. Zero uses 100 seconds.
	// RetryAfter on a returned error overrides the next delay, including this cap.
	MaximumInterval time.Duration
	// MaximumAttempts limits total attempts, including the initial attempt.
	// Zero leaves the attempt count unlimited within TotalDuration; one disables retries.
	MaximumAttempts int32
	// TotalDuration bounds retry scheduling from the first hand-out of this Step method to a Worker.
	// After an attempt fails, Dex uses it to decide whether to schedule another retry.
	// It never ends a running attempt. Zero uses the Server default (4 hours).
	// The next backoff must fit within this budget when Dex schedules the retry.
	// Initial lock and Worker waits do not consume it. Returning an unrun first attempt resets the clock.
	// Later queue waits count toward it, but Dex does not recheck it while the retry is queued.
	// A queued retry may therefore start or finish after the budget expires.
	// Use WaitForMethodTimeout or ExecuteMethodTimeout to bound one attempt; heartbeat detection still applies.
	// Changed in SDK v1.35.0: the budget starts at first hand-out and only gates scheduling another retry.
	// It no longer shortens a running attempt's method timeout.
	TotalDuration time.Duration
}

// StepDurability controls when staged persistence must be durably acknowledged.
type StepDurability uint8

const (
	// StepDurabilityDefault uses the Flow or server default.
	StepDurabilityDefault StepDurability = iota
	// StepDurabilitySync waits for persistence before completing the handler request.
	// Changed in SDK v1.35.0: Dex also persists the attempt start before handing the task to a Worker.
	StepDurabilitySync
	// StepDurabilityAsync permits persistence after accepting the handler result.
	// It lowers latency and speeds up individual Flows; sustained throughput is about the same as synchronous durability.
	// Dex batches commits within about 20 milliseconds by default. On low-latency object storage this reduces requests.
	// A node failure before commit can lose the result and rerun the method and later work derived from it.
	// Changed in SDK v1.35.0: the attempt start is also asynchronous. A node failure before its commit
	// can rerun the same attempt number without consuming a retry, possibly with different input.
	// Those executions can overlap and repeat Stream writes with the same source. Make external effects idempotent.
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

// FlowTimeoutHandlerFailure identifies a recovery Step after a timeout handler exhausts retries.
// Create it with ProceedToOnFlowTimeoutHandlerFailure. The target must be a Step[None]
// registered by the timed-out Flow. The target reads the final handler error from Context.
type FlowTimeoutHandlerFailure struct {
	step    StepDef
	options *StepOptions
}

// ProceedToOnFlowTimeoutHandlerFailure routes a timeout handler failure to a no-input Step.
// options override the recovery Step configuration for this movement; nil uses registered options.
// The recovery Step receives None and reads the final handler error from Context.
func ProceedToOnFlowTimeoutHandlerFailure(
	step Step[None],
	options *StepOptions,
) *FlowTimeoutHandlerFailure {
	return &FlowTimeoutHandlerFailure{
		step:    typedStepDef[None]{step: step},
		options: options,
	}
}

// StepOptions configures one Step's handler execution and persistence behavior.
//
// Zero values preserve server defaults. Every WaitFor or Execute attempt defaults to a two-hour method
// timeout and a one-minute heartbeat timeout. Durability resolves from the method override, FlowConfig, then synchronous.
// Both durability modes use the same retry, timeout, and heartbeat policies for every attempt.
// WaitFor failures fail the Flow by default. Each lock is held for the matching logical method call,
// including retries, retry backoff, and a returned attempt's queue wait. The first attempt's state snapshot
// is reused across retries, so retrying does not reload changes made by another Step or RPC.
// Keep lock scope narrow and retry budgets bounded; a conflicting RPC receives RPCLockConflictError.
// Durability is pinned when the method becomes pending; FlowConfig changes affect only later method calls.
// Cancellation is cooperative, so a later attempt or recovery Step may overlap an interrupted handler.
//
// Changed in SDK v1.35.0: a Step with no available Worker waits durably without spending a retry.
// After hand-out, an interrupted attempt consumes a retry even if no output reached Dex.
// Method and heartbeat timeouts start at hand-out, excluding lock and Worker queue waits.
//
//	options := &dex.StepOptions{
//		ExecuteMethodTimeout: time.Hour, HeartbeatTimeout: time.Minute,
//		ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 3, TotalDuration: time.Hour},
//		ExecuteDurability: dex.StepDurabilitySync,
//		ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(OrderStatus)},
//	}
type StepOptions struct {
	// WaitForMethodTimeout limits one WaitFor attempt; zero uses two hours.
	// Positive durations round up to whole seconds and must fit in int32 seconds; negative values are invalid.
	WaitForMethodTimeout time.Duration
	// ExecuteMethodTimeout limits one Execute attempt; zero uses two hours.
	// It has the same duration validation as WaitForMethodTimeout.
	ExecuteMethodTimeout time.Duration
	// HeartbeatTimeout detects stalled WaitFor and Execute attempts. It applies to every attempt.
	// Zero uses the one-minute default. Positive values must be whole seconds within int32 range
	// and meet the server-configured minimum, which defaults to ten seconds.
	// RecordHeartbeat or a Stream write refreshes liveness; a silent handler can time out even while doing useful work.
	// Heartbeats do not extend the method timeout. Handle Context cancellation promptly.
	HeartbeatTimeout time.Duration
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
	// Locks survive retries and backoff until the logical WaitFor call ends; see StepOptions.
	WaitForLockAttributes []AttributeLock
	// ExecuteLockAttributes are acquired together for the Execute invocation.
	// Locks survive retries and backoff until the logical Execute call ends; see StepOptions.
	ExecuteLockAttributes []AttributeLock
	// WaitForLoadAttributeMaps includes every current instance of each AttributeMap in WaitFor.
	WaitForLoadAttributeMaps []AttributeDef
	// WaitForLoadAttributeMapInstances includes exact AttributeMap instances in WaitFor.
	WaitForLoadAttributeMapInstances []AttributeMapLoad
	// WaitForLoadChannels includes pending messages from selected Channels in WaitFor.
	WaitForLoadChannels []ChannelDef
	// WaitForLoadChannelMaps includes every current ChannelMap instance in WaitFor.
	WaitForLoadChannelMaps []ChannelDef
	// WaitForLoadChannelMapInstances includes exact ChannelMap instance messages in WaitFor.
	WaitForLoadChannelMapInstances []ChannelMapLoad
	// ExecuteLoadAttributeMaps includes every current instance of each AttributeMap in Execute.
	ExecuteLoadAttributeMaps []AttributeDef
	// ExecuteLoadAttributeMapInstances includes exact AttributeMap instances in Execute.
	ExecuteLoadAttributeMapInstances []AttributeMapLoad
	// ExecuteLoadChannels includes pending messages from selected Channels in Execute.
	ExecuteLoadChannels []ChannelDef
	// ExecuteLoadChannelMaps includes every current ChannelMap instance in Execute.
	ExecuteLoadChannelMaps []ChannelDef
	// ExecuteLoadChannelMapInstances includes exact ChannelMap instance messages in Execute.
	ExecuteLoadChannelMapInstances []ChannelMapLoad
}

// FlowTimeoutHandlerOptions configures timeout-handler execution and state loading.
// Zero values preserve the server's Execute defaults. Ordinary Attributes and Channel size
// metadata are loaded automatically. AttributeMap values and pending Channel messages require
// an explicit load selection. One logical handler execution may contain multiple retry attempts.
type FlowTimeoutHandlerOptions struct {
	// MethodTimeout limits one timeout-handler attempt; zero uses the server default.
	MethodTimeout time.Duration
	// HeartbeatTimeout detects a stalled regular timeout-handler attempt.
	// Zero uses the server default. Positive values must be whole seconds within int32 range.
	HeartbeatTimeout time.Duration
	// Retry overrides the timeout-handler retry policy; nil uses server defaults.
	Retry *RetryPolicy
	// Failure selects a no-input recovery Step after the handler exhausts retries.
	Failure *FlowTimeoutHandlerFailure
	// Durability overrides persistence durability for timeout-handler writes.
	Durability StepDurability
	// LockAttributes are acquired together for the timeout-handler invocation.
	LockAttributes []AttributeLock
	// LoadAttributeMaps includes every current instance of each AttributeMap.
	LoadAttributeMaps []AttributeDef
	// LoadAttributeMapInstances includes exact AttributeMap instances.
	LoadAttributeMapInstances []AttributeMapLoad
	// LoadChannels includes pending messages from selected Channels.
	LoadChannels []ChannelDef
	// LoadChannelMaps includes every current ChannelMap instance.
	LoadChannelMaps []ChannelDef
	// LoadChannelMapInstances includes exact ChannelMap instance messages.
	LoadChannelMapInstances []ChannelMapLoad
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
	// AttributeStoreNames selects Server-configured Attribute Stores for opted-in writes.
	// A nil slice preserves the current targets; an empty non-nil slice disables future projections.
	AttributeStoreNames []string
}

// StartFlowOptions configures a new Flow execution.
// Nil and zero fields preserve server defaults; RequestID is generated when omitted.
//
// To retry one logical start safely, for example after a lost response, set a stable
// RequestID and AlreadyStarted.IgnoreError together. Reuse the same values on every retry:
//
//	flowID := "order-" + orderID
//	requestID := "start-order-" + orderID
//	runID, err := client.StartFlow(ctx, orderFlow, flowID, input, dex.StartFlowOptions{
//		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
//		RequestID:      &requestID,
//	})
type StartFlowOptions struct {
	// Timeout sets Dex's durable soft timeout; nil or zero disables it.
	Timeout *time.Duration
	// TimeoutPolicy controls what Dex does when Timeout expires.
	// The default uses Handler when the Flow implements FlowTimeoutHandler, otherwise Fail.
	TimeoutPolicy FlowTimeoutPolicy
	// TimeoutHandlerOptions configures execution when TimeoutPolicy resolves to TimeoutHandler.
	// It requires a positive Timeout and is invalid with Fail or Cancel.
	TimeoutHandlerOptions *FlowTimeoutHandlerOptions
	// IDReusePolicy controls reuse of an existing Flow ID.
	IDReusePolicy IDReusePolicy
	// StartDelay postpones the first Step after start acceptance.
	StartDelay *time.Duration
	// RetryPolicy configures whole-Flow retries after terminal failures.
	RetryPolicy *FlowRetryPolicy
	// Attributes supplies encoded initial Attribute and Attribute-map values.
	Attributes []InitialAttributeDef
	// ConfigOverride replaces registered Flow configuration for this execution.
	ConfigOverride *FlowConfig
	// AlreadyStarted controls whether an existing Flow is returned as success.
	// IgnoreError returns the existing run only when RequestID matches the request that started it.
	AlreadyStarted *AlreadyStartedOptions
	// RequestID defaults to a UUID; set a stable business identifier for cross-call retries.
	// A retried StartFlow returns the existing run only when AlreadyStarted.IgnoreError is true
	// and RequestID matches the request that started that run.
	// Without IgnoreError, the retry returns FlowAlreadyStartedError even when the first attempt
	// created the Flow.
	// The match applies only when IDReusePolicy rejects the start.
	// A policy that permits another run starts a new run instead.
	// Dex compares only request IDs, not input or other options.
	// Never reuse a request ID for a different logical start.
	// See StartFlowOptions for an example that sets both fields.
	RequestID *string
}

// FlowTimeoutPolicy controls how a positive Flow timeout ends or redirects execution.
type FlowTimeoutPolicy uint8

const (
	// TimeoutDefault selects Handler for a FlowTimeoutHandler and Fail otherwise.
	TimeoutDefault FlowTimeoutPolicy = iota
	// TimeoutFail fails the Flow with FlowErrorTimeout and permits Flow retries.
	TimeoutFail
	// TimeoutCancel cancels the Flow without retrying it.
	TimeoutCancel
	// TimeoutHandler invokes FlowTimeoutHandler.HandleTimeout as one retryable logical execution.
	TimeoutHandler
)

// SubFlowReusePolicy controls how a generated SubFlow Flow ID resolves an existing execution.
type SubFlowReusePolicy uint8

const (
	// RestartSubFlowIfPreviousExitedAbnormally attaches to running executions, returns completed
	// results, and replaces failed, canceled, timed-out, or terminated executions.
	RestartSubFlowIfPreviousExitedAbnormally SubFlowReusePolicy = iota
	// AttachSubFlow attaches to a running execution or returns its existing terminal result.
	AttachSubFlow
	// AlwaysRestartSubFlow replaces a different existing execution, including a running one.
	AlwaysRestartSubFlow
)

// SubFlowOptions configures one durable SubFlow Condition.
//
// Zero values inherit normal Flow start defaults and use
// RestartSubFlowIfPreviousExitedAbnormally. The server owns the generated Flow ID and request ID;
// callers cannot override either value. Attributes must belong to the target SubFlow.
// The child Flow ID is derived from the parent Flow ID, Step execution ID, and SubFlow condition index.
// A matching generated request ID attaches to an existing child or returns its terminal result,
// regardless of ReusePolicy. This preserves one logical start after a lost response.
// For a different request ID, ReusePolicy determines whether to attach or replace the child.
// Timeout nil or zero disables the child timeout; positive durations round up to whole seconds.
// Timeout and StartDelay must be nonnegative and fit in int32 seconds.
type SubFlowOptions struct {
	// Timeout limits total SubFlow execution duration.
	Timeout *time.Duration
	// TimeoutPolicy controls what Dex does when Timeout expires.
	// The default uses Handler when the SubFlow implements FlowTimeoutHandler, otherwise Fail.
	TimeoutPolicy FlowTimeoutPolicy
	// TimeoutHandlerOptions configures execution when TimeoutPolicy resolves to TimeoutHandler.
	// It requires a positive Timeout and is invalid with Fail or Cancel.
	TimeoutHandlerOptions *FlowTimeoutHandlerOptions
	// StartDelay postpones the SubFlow starting Step after start acceptance.
	StartDelay *time.Duration
	// RetryPolicy configures whole-Flow retries after terminal failures.
	RetryPolicy *FlowRetryPolicy
	// Attributes supplies encoded initial Attribute and Attribute-map values.
	Attributes []InitialAttributeDef
	// ConfigOverride overrides fields inherited from the parent Flow configuration.
	ConfigOverride *FlowConfig
	// ReusePolicy controls how an execution already using the generated Flow ID is resolved.
	ReusePolicy SubFlowReusePolicy
	// ConditionID assigns the stable ID required by AnyComboOf.
	ConditionID string
}

func defaultSubFlowOptions() SubFlowOptions {
	return SubFlowOptions{ReusePolicy: RestartSubFlowIfPreviousExitedAbnormally}
}

// IDReusePolicy controls whether StartFlow may reuse a Flow ID.
type IDReusePolicy uint8

const (
	// IDReuseDefault uses the server default, which rejects every previously used Flow ID.
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
	// It applies only when StartFlowOptions.RequestID matches the request ID that started the
	// existing run. A different or SDK-generated request ID still returns FlowAlreadyStartedError.
	IgnoreError bool
}

// RPCOptions configures every invocation of one registered RPC.
//
// Return these options with DefineRPC from Flow.GetRPCs. The Client applies the same immutable
// settings to every invocation. Its context may still impose a shorter caller-side deadline.
type RPCOptions struct {
	// Name is the protocol RPC name that Clients invoke and Workers serve.
	// Available since SDK v1.35.0. Empty uses the Go method name, such as "GetStatus".
	//
	// Set it when a Flow implemented in another language names the RPC, so a Go Worker serves the
	// same RPC and a Go Client invokes it. For example, a Python Flow's send_user_message method:
	//
	//	dex.DefineRPC(flow.SendUserMessage, &dex.RPCOptions{Name: "send_user_message"})
	//
	// Go code still refers to the RPC by its bound method, such as Client.InvokeRPC with
	// flow.SendUserMessage. The name may contain only ASCII letters, digits, '_', and '-'. It must
	// differ from every other RPC name of the Flow, and from the Go method name of every other RPC
	// of the Flow, so a name never points at another method. Registry construction rejects any
	// other name. Keep it stable: running Flows and their callers use it.
	Name string
	// Action declares this RPC as a state-dependent Action with one required permission.
	// Nil leaves the RPC outside Work Queue permission projection. The projection identifies
	// candidate Runs only; the RPC gateway must independently authorize every invocation.
	Action ActionDef
	// Timeout limits the RPC handler; zero uses the server default.
	Timeout time.Duration
	// LockAttributes are acquired atomically for the RPC invocation.
	LockAttributes []AttributeLock
	// IsTransactional requests transactional reads and writes without requiring Attribute locks.
	// Channel deletions require this option to make a missing message abort all RPC writes.
	IsTransactional bool
	// LoadAttributeMaps includes every current instance of each AttributeMap in the RPC snapshot.
	LoadAttributeMaps []AttributeDef
	// LoadAttributeMapInstances includes exact AttributeMap instances in the RPC snapshot.
	LoadAttributeMapInstances []AttributeMapLoad
	// LoadChannels includes pending messages from the selected Channels in the RPC snapshot.
	LoadChannels []ChannelDef
	// LoadChannelMaps includes every current instance of each ChannelMap in the RPC snapshot.
	LoadChannelMaps []ChannelDef
	// LoadChannelMapInstances includes exact ChannelMap instance messages in the RPC snapshot.
	LoadChannelMapInstances []ChannelMapLoad
}

// RPCInvokeOptions adds runtime-selected map instances to one RPC invocation.
//
// The Client merges these selections with the RPC's registered RPCOptions. Invocation options
// cannot remove registered locks or loads. AttributeMap locks and loads remain independent; a
// read-modify-write RPC must include the same instance in both fields.
type RPCInvokeOptions struct {
	// LockAttributeMapInstances are acquired with the RPC's registered Attribute locks.
	// Every lock must be created with LockAttributeMap.
	LockAttributeMapInstances []AttributeLock
	// LoadAttributeMapInstances adds exact AttributeMap instances to the RPC snapshot.
	LoadAttributeMapInstances []AttributeMapLoad
	// LoadChannelMapInstances adds exact ChannelMap instance messages to the RPC snapshot.
	LoadChannelMapInstances []ChannelMapLoad
}

// WaitForFlowOptions controls Flow-result hydration.
type WaitForFlowOptions struct {
	// NeedsResults asks Dex to include completed Step outputs.
	NeedsResults bool
}

// WaitForStepCompletionOptions configures one durable Step completion wait.
//
// When RequestID is empty, the server derives a stable ID from the Step
// execution. Reuse an override only when retrying the same Step completion
// wait. RequestTimeout bounds the caller-visible request across transparent
// transport reattachments. Leave it zero to wait indefinitely.
// The Server caps each transport wait; the SDK reattaches with the same RequestID without
// resetting RequestTimeout. A caller disconnect releases its wait slot.
// InternalHandlerTimeout is retained for compatibility and has no effect on the current Server.
// Both durations must use whole seconds within int32 range.
//
//	options := dex.WaitForStepCompletionOptions{
//		RequestTimeout: time.Second * 10,
//	}
type WaitForStepCompletionOptions struct {
	// RequestID overrides the stable ID derived from the Step execution.
	RequestID string
	// RequestTimeout bounds the caller-visible request. Zero waits indefinitely.
	RequestTimeout time.Duration
	// InternalHandlerTimeout is accepted and validated for compatibility but ignored by the current Server.
	// Zero is the default. Negative or fractional-second values are invalid, as are values above int32 seconds.
	//
	// Deprecated: leave this field zero; use RequestTimeout or ctx to bound the wait.
	InternalHandlerTimeout time.Duration
}

// WaitForAttributeOptions configures one durable Attribute match wait.
//
// When RequestID is empty, the server derives a stable ID from the Attribute
// condition. Reuse an override only when retrying the same Attribute match.
// RequestTimeout bounds the caller-visible request across transparent transport
// reattachments. Leave it zero to wait indefinitely. The Server caps each transport wait;
// the SDK reattaches with the same RequestID without resetting RequestTimeout.
// A caller disconnect releases its wait slot.
// InternalHandlerTimeout is retained for compatibility and has no effect on the current Server.
// Both durations must use whole seconds within int32 range.
//
//	options := dex.WaitForAttributeOptions{
//		RequestTimeout: time.Second * 10,
//	}
type WaitForAttributeOptions struct {
	// RequestID overrides the stable ID derived from the Attribute condition.
	RequestID string
	// RequestTimeout bounds the caller-visible request. Zero waits indefinitely.
	RequestTimeout time.Duration
	// InternalHandlerTimeout is accepted and validated for compatibility but ignored by the current Server.
	// Zero is the default. Negative or fractional-second values are invalid, as are values above int32 seconds.
	//
	// Deprecated: leave this field zero; use RequestTimeout or ctx to bound the wait.
	InternalHandlerTimeout time.Duration
}

// StopType selects how StopFlow ends an active Flow.
type StopType uint8

const (
	// CancelFlow requests cooperative cancellation.
	CancelFlow StopType = iota + 1
	// TerminateFlow ends the Flow immediately without cleanup.
	TerminateFlow
	// FailFlow marks the Flow failed with Reason.
	FailFlow
)

// StopOptions configures Client.StopFlow.
type StopOptions struct {
	// Type selects cancel, terminate, or fail behavior. Zero defaults to cooperative cancellation.
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

// TimeTravelType identifies the historical point selector used by Client.TimeTravel.
type TimeTravelType uint8

const (
	// TimeTravelToBeginning resumes before the first history event.
	TimeTravelToBeginning TimeTravelType = iota + 1
	// TimeTravelByHistoryEventTime resumes at the last eligible event by time.
	TimeTravelByHistoryEventTime
	// TimeTravelByStepType resumes before the first execution of a Step type.
	TimeTravelByStepType
	// TimeTravelByStepExecutionID resumes before an exact Step execution.
	TimeTravelByStepExecutionID
)

// TimeTravelStepMethod selects the Step method used as a time travel boundary.
type TimeTravelStepMethod uint8

const (
	// TimeTravelStepWaitFor resumes before the selected Step execution's WaitFor method.
	TimeTravelStepWaitFor TimeTravelStepMethod = iota + 1
	// TimeTravelStepExecute resumes before the selected Step execution's Execute method.
	TimeTravelStepExecute
)

// TimeTravelOptions configures Client.TimeTravel with exactly one historical point.
type TimeTravelOptions struct {
	// Type selects which time travel point field Dex reads.
	Type TimeTravelType
	// Reason is recorded with the time travel operation; empty leaves it unspecified.
	Reason string
	// HistoryEventTime supplies the time for TimeTravelByHistoryEventTime.
	HistoryEventTime time.Time
	// StepType supplies the stable Step type for TimeTravelByStepType.
	StepType string
	// StepExecutionID supplies the exact execution for TimeTravelByStepExecutionID.
	StepExecutionID string
	// StepMethod selects WaitFor or Execute when Type is TimeTravelByStepExecutionID.
	StepMethod TimeTravelStepMethod
	// SkipWritesReapply prevents replay of later RPCs, Channel publications, and Attribute writes; false reapplies them.
	SkipWritesReapply bool
}

// SearchFlowsOptions selects whether visibility searches include earlier runs replaced during history maintenance.
type SearchFlowsOptions struct {
	// IncludeContinuedAsNew disables the server's default exclusion without changing the caller's query.
	IncludeContinuedAsNew bool
}

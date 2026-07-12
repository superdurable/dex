package dex

import "time"

// StepOptions configures per-step behavior such as state field locking and
// durability mode. Returned by Step.GetStepOptions(); nil means all defaults.
type StepOptions struct {
	// WaitForMethodTimeout is the maximum duration of the WaitFor method execution.
	// Zero means no timeout. Default is 10 minutes.
	WaitForMethodTimeout time.Duration

	// ExecuteMethodTimeout is the maximum duration of the Execute method execution.
	// Zero means no timeout. Default is 10 minutes.
	ExecuteMethodTimeout time.Duration

	// WaitForMethodRetryPolicy is the retry policy for the WaitFor method.
	// If nil, defaultRetryPolicy is used.
	WaitForMethodRetryPolicy *RetryPolicy

	// ExecuteMethodRetryPolicy is the retry policy for the Execute method.
	// If nil, defaultRetryPolicy is used.
	ExecuteMethodRetryPolicy *RetryPolicy

	// WaitForMethodProceedToAfterRetryExhausted is the error-handler step when
	// WaitFor retries exhaust. Nil fails the run. Handler gets same input as failing step.
	// Registry.Register validates handler is in-flow and input types match.
	WaitForMethodProceedToAfterRetryExhausted stepCommon

	// ExecuteMethodProceedToAfterRetryExhausted is the error-handler step when
	// Execute retries exhaust. Nil fails the run. Handler gets same input as failing step.
	// Registry.Register validates handler is in-flow and input types match.
	ExecuteMethodProceedToAfterRetryExhausted stepCommon

	// WaitForMethodStateLockingKeys specifies which state fields to lock during
	// the WaitFor phase. If nil, no fields are locked during WaitFor.
	// TODO
	WaitForMethodStateLockingKeys *LockingKeys

	// ExecuteMethodStateLockingKeys specifies which state fields to lock during
	// the Execute phase. If nil, no fields are locked during Execute.
	// TODO
	ExecuteMethodStateLockingKeys *LockingKeys

	// WaitForMethodDurability controls whether the WaitFor result is persisted
	// before the framework acknowledges completion.
	// Default (zero value) is DurabilitySync.
	// TODO
	// WaitForMethodDurability Durability

	// ExecuteMethodDurability controls whether the Execute result is persisted
	// before the framework acknowledges completion.
	// Default (zero value) is DurabilitySync.
	// TODO
	// ExecuteMethodDurability Durability
}

// defaultRetryPolicy is the default retry policy for a step.
// unlimited retries, 1 second initial interval, 2x backoff, 1 hour maximum interval.
var defaultRetryPolicy = &RetryPolicy{
	MaxAttempts:        0,
	InitialInterval:    1 * time.Second,
	BackoffCoefficient: 2.0,
	MaximumInterval:    1 * time.Hour,
}

const (
	defaultWaitForMethodTimeout = 10 * time.Minute
	defaultExecuteMethodTimeout = 10 * time.Minute
)

// RetryPolicy configures retry behavior for a step method (WaitFor or Execute).
type RetryPolicy struct {
	// MaxAttempts is the maximum number of attempts, including the first
	// execution. For example, MaxAttempts=3 means 1 initial attempt + 2
	// retries. Zero or negative means unlimited retries.
	MaxAttempts int

	// InitialInterval is the delay before the first retry after a failure.
	InitialInterval time.Duration

	// BackoffCoefficient is the multiplier applied to the interval after
	// each consecutive failure. E.g. 2.0 doubles the wait each retry.
	// Must be >= 1.0; values < 1.0 are treated as 1.0 (no backoff).
	BackoffCoefficient float64

	// MaximumInterval caps the retry delay so it never exceeds this value,
	// regardless of the backoff coefficient.
	MaximumInterval time.Duration

	// TotalTimeout is the end-to-end deadline for the entire retry sequence,
	// measured from the start of the first attempt. If this duration elapses
	// the method fails permanently, even if MaxAttempts has not been reached.
	// Zero means no total timeout (retries are bounded only by MaxAttempts).
	TotalTimeout time.Duration
}

// Durability controls how a step phase result is persisted.
type Durability int

const (
	// DurabilitySync (default): Guarantees that once the
	// step returns, the result will survive server crashes.
	DurabilitySync Durability = iota

	// DurabilityAsync: persists the result asynchronously in the background.
	// Faster but the result may be lost if the server crashes before persistence
	// completes
	DurabilityAsync
)

// LockingKeys defines which state fields to load and whether to acquire
// exclusive locks on them. This prevents concurrent step executions from
// reading/writing the same fields simultaneously.
type LockingKeys struct {
	StaticKeyNames     []string
	DynamicKeyPrefixes []string

	// LockType controls the locking behavior.
	LockType LockType
}

// LockType controls how fields are locked during a step phase.
type LockType int

const (
	// LockNone loads fields without acquiring any lock (default).
	LockNone LockType = iota

	// LockExclusive acquires an exclusive lock on the specified keys.
	// Other step executions that request a lock on the same keys will
	// block until this step method completes.
	LockExclusive
)

// --- Helper constructors ---

// LockStateKey exclusively locks typed static state keys.
func LockStateKey[T any](keys ...StateKey[T]) *LockingKeys {
	names := make([]string, len(keys))
	for index, key := range keys {
		names[index] = key.Name
	}
	return &LockingKeys{
		StaticKeyNames: names,
		LockType:       LockExclusive,
	}
}

// LockDynamicStateKey exclusively locks typed dynamic state key families.
func LockDynamicStateKey[T any](keys ...DynamicStateKey[T]) *LockingKeys {
	prefixes := make([]string, len(keys))
	for index, key := range keys {
		prefixes[index] = key.Prefix
	}
	return &LockingKeys{
		DynamicKeyPrefixes: prefixes,
		LockType:           LockExclusive,
	}
}

func MergeLockingKeys(lockingKeys ...*LockingKeys) *LockingKeys {
	lkeys := LockingKeys{
		LockType: LockExclusive,
	}
	for _, lockingKey := range lockingKeys {
		lkeys.StaticKeyNames = append(lkeys.StaticKeyNames, lockingKey.StaticKeyNames...)
		lkeys.DynamicKeyPrefixes = append(lkeys.DynamicKeyPrefixes, lockingKey.DynamicKeyPrefixes...)
	}
	return &lkeys
}

func resolveRetryPolicy(configuredPolicy *RetryPolicy) *RetryPolicy {
	if configuredPolicy == nil {
		return defaultRetryPolicy
	}
	return configuredPolicy
}

func resolveMethodTimeout(configured time.Duration, defaultTimeout time.Duration) time.Duration {
	if configured == 0 {
		return defaultTimeout
	}
	return configured
}

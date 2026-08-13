// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package retry

import (
	"math"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/cadence/workflow"
)

func ConvertCadenceWorkflowRetryPolicy(policy *dexpb.FlowRetryPolicy) *workflow.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff := flowRetryDefaults(policy)

	return &workflow.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func ConvertCadenceActivityRetryPolicy(policy *dexpb.RetryPolicy) *workflow.RetryPolicy {
	// Cadence has no server-side default; Temporal does when RetryPolicy is omitted.
	// Match Temporal's defaults so waitFor/execute keep infinite backoff without options.
	if policy == nil {
		policy = &dexpb.RetryPolicy{}
	}
	initial, maxInterval, maxAttempts, backoff, totalDuration := activityRetryDefaults(policy)

	expirationInterval := time.Duration(0)
	if totalDuration > 0 {
		expirationInterval = time.Second * time.Duration(totalDuration)
	} else {
		expirationInterval = time.Hour * 24 * 365 * 1
	}

	return &workflow.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
		ExpirationInterval: expirationInterval,
	}
}

// ConvertCadenceLocalActivityRetryPolicy accounts for Cadence counting retries instead of total attempts.
func ConvertCadenceLocalActivityRetryPolicy(policy *dexpb.RetryPolicy) *workflow.RetryPolicy {
	cadencePolicy := ConvertCadenceActivityRetryPolicy(policy)
	maximumAttempts := policy.GetMaximumAttempts()
	if maximumAttempts == 1 {
		return nil
	}
	if maximumAttempts > 1 {
		cadencePolicy.MaximumAttempts = maximumAttempts - 1
	}
	return cadencePolicy
}

func ConvertTemporalWorkflowRetryPolicy(policy *dexpb.FlowRetryPolicy) *temporal.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff := flowRetryDefaults(policy)

	return &temporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func ConvertTemporalActivityRetryPolicy(policy *dexpb.RetryPolicy) *temporal.RetryPolicy {
	if policy == nil {
		return nil
	}
	initial, maxInterval, maxAttempts, backoff, _ := activityRetryDefaults(policy)

	return &temporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initial),
		MaximumInterval:    time.Second * time.Duration(maxInterval),
		MaximumAttempts:    maxAttempts,
		BackoffCoefficient: float64(backoff),
	}
}

func ActivityRetryPolicyWithDefaults(policy *dexpb.RetryPolicy) *dexpb.RetryPolicy {
	if policy == nil {
		policy = &dexpb.RetryPolicy{}
	}
	initial, maxInterval, maxAttempts, backoff, totalDuration := activityRetryDefaults(policy)
	return &dexpb.RetryPolicy{
		InitialIntervalSeconds: initial,
		BackoffCoefficient:     backoff,
		MaximumIntervalSeconds: maxInterval,
		MaximumAttempts:        maxAttempts,
		TotalDurationSeconds:   totalDuration,
	}
}

// LocalActivityRetryPolicy bounds retries to attempts whose backoff fits before the local timeout.
func LocalActivityRetryPolicy(policy *dexpb.RetryPolicy, timeout time.Duration) *dexpb.RetryPolicy {
	localPolicy := ActivityRetryPolicyWithDefaults(policy)
	localMaximumAttempts := maximumLocalActivityAttempts(localPolicy, timeout)
	if localPolicy.GetMaximumAttempts() == 0 || localMaximumAttempts < localPolicy.GetMaximumAttempts() {
		localPolicy.MaximumAttempts = localMaximumAttempts
	}
	localPolicy.TotalDurationSeconds = int32(math.Ceil(timeout.Seconds()))
	return localPolicy
}

func maximumLocalActivityAttempts(policy *dexpb.RetryPolicy, timeout time.Duration) int32 {
	maximumAttempts := int32(1)
	elapsedSeconds := float64(0)
	nextIntervalSeconds := float64(policy.GetInitialIntervalSeconds())
	timeoutSeconds := timeout.Seconds()
	for policy.GetMaximumAttempts() == 0 || maximumAttempts < policy.GetMaximumAttempts() {
		if nextIntervalSeconds <= 0 || elapsedSeconds+nextIntervalSeconds >= timeoutSeconds {
			break
		}
		elapsedSeconds += nextIntervalSeconds
		maximumAttempts++
		nextIntervalSeconds *= float64(policy.GetBackoffCoefficient())
		if math.IsInf(nextIntervalSeconds, 1) || nextIntervalSeconds >= float64(policy.GetMaximumIntervalSeconds()) {
			nextIntervalSeconds = float64(policy.GetMaximumIntervalSeconds())
		}
	}
	return maximumAttempts
}

// RemainingActivityRetryPolicy subtracts attempts and elapsed time before regular fallback.
func RemainingActivityRetryPolicy(
	policy *dexpb.RetryPolicy,
	previousAttempts int32,
	elapsed time.Duration,
) (*dexpb.RetryPolicy, bool) {
	remaining := ActivityRetryPolicyWithDefaults(policy)
	if remaining.GetMaximumAttempts() > 0 {
		if previousAttempts >= remaining.GetMaximumAttempts() {
			return nil, false
		}
		remaining.MaximumAttempts -= previousAttempts
	}
	if remaining.GetTotalDurationSeconds() > 0 {
		remainingDuration := time.Duration(remaining.GetTotalDurationSeconds())*time.Second - elapsed
		if remainingDuration <= 0 {
			return nil, false
		}
		remaining.TotalDurationSeconds = int32(math.Ceil(remainingDuration.Seconds()))
	}
	remaining.InitialIntervalSeconds = adjustedInitialIntervalSeconds(
		remaining.GetInitialIntervalSeconds(),
		remaining.GetBackoffCoefficient(),
		remaining.GetMaximumIntervalSeconds(),
		previousAttempts,
	)
	return remaining, true
}

func adjustedInitialIntervalSeconds(
	initialIntervalSeconds int32,
	backoffCoefficient float32,
	maximumIntervalSeconds int32,
	previousAttempts int32,
) int32 {
	if previousAttempts <= 0 {
		return initialIntervalSeconds
	}
	adjusted := float64(initialIntervalSeconds) * math.Pow(float64(backoffCoefficient), float64(previousAttempts))
	if math.IsInf(adjusted, 1) || adjusted >= float64(maximumIntervalSeconds) {
		return maximumIntervalSeconds
	}
	return int32(math.Ceil(adjusted))
}

func flowRetryDefaults(policy *dexpb.FlowRetryPolicy) (initial, maxInterval, maxAttempts int32, backoff float32) {
	initial = policy.GetInitialIntervalSeconds()
	if initial == 0 {
		initial = 1
	}
	backoff = policy.GetBackoffCoefficient()
	if backoff == 0 {
		backoff = 2
	}
	maxInterval = policy.GetMaximumIntervalSeconds()
	if maxInterval == 0 {
		maxInterval = 100
	}
	maxAttempts = policy.GetMaximumAttempts()
	return
}

func activityRetryDefaults(policy *dexpb.RetryPolicy) (initial, maxInterval, maxAttempts int32, backoff float32, totalDuration int32) {
	initial = policy.GetInitialIntervalSeconds()
	if initial == 0 {
		initial = 1
	}
	backoff = policy.GetBackoffCoefficient()
	if backoff == 0 {
		backoff = 2
	}
	maxInterval = policy.GetMaximumIntervalSeconds()
	if maxInterval == 0 {
		maxInterval = 100
	}
	maxAttempts = policy.GetMaximumAttempts()
	totalDuration = policy.GetTotalDurationSeconds()
	return
}

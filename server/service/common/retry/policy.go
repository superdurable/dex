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

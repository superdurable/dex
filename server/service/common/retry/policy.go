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
	"context"
	"math"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/cadence/workflow"
)

type Backoff struct {
	nextInterval       time.Duration
	maximumInterval    time.Duration
	backoffCoefficient float64
	maximumAttempts    int32
	totalDuration      time.Duration
	attempts           int32
	startedAt          time.Time
}

var defaultStepActivityRetryPolicy = config.RetryPolicy{
	InitialInterval:    time.Second,
	BackoffCoefficient: 2,
	MaximumInterval:    100 * time.Second,
	TotalDuration:      4 * time.Hour,
}

const maximumLocalStepActivityAttempts int32 = 3

func NewQueryWorkflowBackoff(policy *config.RetryPolicy) *Backoff {
	return newBackoff(config.RetryPolicyWithDefaults(
		policy,
		config.DefaultQueryWorkflowFailedRetryPolicy,
	))
}

func NewInvokeRPCBackoff(policy *config.RetryPolicy) *Backoff {
	return newBackoff(config.RetryPolicyWithDefaults(
		policy,
		config.DefaultInvokeRPCContinuedAsNewErrorRetryPolicy,
	))
}

func newBackoff(policy config.RetryPolicy) *Backoff {
	return &Backoff{
		nextInterval:       policy.InitialInterval,
		maximumInterval:    policy.MaximumInterval,
		backoffCoefficient: policy.BackoffCoefficient,
		maximumAttempts:    policy.MaximumAttempts,
		totalDuration:      policy.TotalDuration,
		attempts:           1,
		startedAt:          time.Now(),
	}
}

func (b *Backoff) WaitForNextAttempt(ctx context.Context) (bool, error) {
	if b.maximumAttempts > 0 && b.attempts >= b.maximumAttempts {
		return false, nil
	}
	if b.totalDuration > 0 && time.Since(b.startedAt)+b.nextInterval >= b.totalDuration {
		return false, nil
	}
	timer := time.NewTimer(b.nextInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
	}
	b.attempts++
	b.nextInterval = nextBackoffInterval(
		b.nextInterval,
		b.maximumInterval,
		b.backoffCoefficient,
	)
	return true, nil
}

func nextBackoffInterval(
	currentInterval time.Duration,
	maximumInterval time.Duration,
	backoffCoefficient float64,
) time.Duration {
	nextInterval := time.Duration(float64(currentInterval) * backoffCoefficient)
	if nextInterval <= 0 || nextInterval > maximumInterval {
		return maximumInterval
	}
	return nextInterval
}

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

func ConvertCadenceActivityRetryPolicy(policy *config.RetryPolicy) *workflow.RetryPolicy {
	// Cadence has no server-side default; Temporal does when RetryPolicy is omitted.
	// Match Temporal's defaults so waitFor/execute keep infinite backoff without options.
	if policy == nil {
		policy = stepActivityRetryPolicyWithDefaults(nil)
	} else {
		policy = ActivityRetryPolicyWithDefaults(policy)
	}

	expirationInterval := time.Duration(0)
	if policy.TotalDuration > 0 {
		expirationInterval = policy.TotalDuration
	} else {
		expirationInterval = time.Hour * 24 * 365 * 1
	}

	return &workflow.RetryPolicy{
		InitialInterval:    policy.InitialInterval,
		MaximumInterval:    policy.MaximumInterval,
		MaximumAttempts:    policy.MaximumAttempts,
		BackoffCoefficient: policy.BackoffCoefficient,
		ExpirationInterval: expirationInterval,
	}
}

// ConvertCadenceLocalActivityRetryPolicy accounts for Cadence counting retries instead of total attempts.
func ConvertCadenceLocalActivityRetryPolicy(policy *config.RetryPolicy) *workflow.RetryPolicy {
	effectivePolicy := ActivityRetryPolicyWithDefaults(policy)
	cadencePolicy := ConvertCadenceActivityRetryPolicy(effectivePolicy)
	maximumAttempts := effectivePolicy.MaximumAttempts
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

func ConvertTemporalActivityRetryPolicy(policy *config.RetryPolicy) *temporal.RetryPolicy {
	if policy == nil {
		return nil
	}
	policy = ActivityRetryPolicyWithDefaults(policy)

	return &temporal.RetryPolicy{
		InitialInterval:    policy.InitialInterval,
		MaximumInterval:    policy.MaximumInterval,
		MaximumAttempts:    policy.MaximumAttempts,
		BackoffCoefficient: policy.BackoffCoefficient,
	}
}

func ActivityRetryPolicyFromProto(policy *dexpb.RetryPolicy) *config.RetryPolicy {
	converted := &config.RetryPolicy{}
	if policy != nil {
		converted.InitialInterval = time.Duration(policy.GetInitialIntervalSeconds()) * time.Second
		converted.BackoffCoefficient = float64(policy.GetBackoffCoefficient())
		converted.MaximumInterval = time.Duration(policy.GetMaximumIntervalSeconds()) * time.Second
		converted.MaximumAttempts = policy.GetMaximumAttempts()
		converted.TotalDuration = time.Duration(policy.GetTotalDurationSeconds()) * time.Second
	}
	return stepActivityRetryPolicyWithDefaults(converted)
}

func ActivityRetryPolicyToProto(policy *config.RetryPolicy) *dexpb.RetryPolicy {
	policy = stepActivityRetryPolicyWithDefaults(policy)
	return &dexpb.RetryPolicy{
		InitialIntervalSeconds: durationSeconds(policy.InitialInterval),
		BackoffCoefficient:     float32(policy.BackoffCoefficient),
		MaximumIntervalSeconds: durationSeconds(policy.MaximumInterval),
		MaximumAttempts:        policy.MaximumAttempts,
		TotalDurationSeconds:   durationSeconds(policy.TotalDuration),
	}
}

func ActivityRetryPolicyWithDefaults(policy *config.RetryPolicy) *config.RetryPolicy {
	effective := config.RetryPolicyWithDefaults(policy, config.DefaultInternalActivityRetryPolicy)
	return &effective
}

func stepActivityRetryPolicyWithDefaults(policy *config.RetryPolicy) *config.RetryPolicy {
	effective := config.RetryPolicyWithDefaults(policy, defaultStepActivityRetryPolicy)
	return &effective
}

// LocalActivityRetryPolicy bounds retries to attempts whose backoff fits before the local timeout.
func LocalActivityRetryPolicy(policy *config.RetryPolicy, timeout time.Duration) *config.RetryPolicy {
	localPolicy := ActivityRetryPolicyWithDefaults(policy)
	localMaximumAttempts := maximumLocalActivityAttempts(localPolicy, timeout)
	if localPolicy.MaximumAttempts == 0 || localMaximumAttempts < localPolicy.MaximumAttempts {
		localPolicy.MaximumAttempts = localMaximumAttempts
	}
	localPolicy.TotalDuration = timeout
	return localPolicy
}

func maximumLocalActivityAttempts(policy *config.RetryPolicy, timeout time.Duration) int32 {
	maximumAttempts := int32(1)
	elapsed := time.Duration(0)
	nextInterval := policy.InitialInterval
	for maximumAttempts < maximumLocalStepActivityAttempts &&
		(policy.MaximumAttempts == 0 || maximumAttempts < policy.MaximumAttempts) {
		if nextInterval <= 0 || elapsed+nextInterval >= timeout {
			break
		}
		elapsed += nextInterval
		maximumAttempts++
		nextInterval = nextBackoffInterval(nextInterval, policy.MaximumInterval, policy.BackoffCoefficient)
	}
	return maximumAttempts
}

// RemainingActivityRetryPolicy subtracts attempts and elapsed time before regular fallback.
func RemainingActivityRetryPolicy(
	policy *config.RetryPolicy,
	previousAttempts int32,
	elapsed time.Duration,
) (*config.RetryPolicy, bool) {
	remaining := ActivityRetryPolicyWithDefaults(policy)
	if remaining.MaximumAttempts > 0 {
		if previousAttempts >= remaining.MaximumAttempts {
			return nil, false
		}
		remaining.MaximumAttempts -= previousAttempts
	}
	if remaining.TotalDuration > 0 {
		remainingDuration := remaining.TotalDuration - elapsed
		if remainingDuration <= 0 {
			return nil, false
		}
		remaining.TotalDuration = ceilDurationToSecond(remainingDuration)
	}
	remaining.InitialInterval = adjustedInitialInterval(
		remaining.InitialInterval,
		remaining.BackoffCoefficient,
		remaining.MaximumInterval,
		previousAttempts,
	)
	return remaining, true
}

func adjustedInitialInterval(
	initialInterval time.Duration,
	backoffCoefficient float64,
	maximumInterval time.Duration,
	previousAttempts int32,
) time.Duration {
	if previousAttempts <= 0 {
		return initialInterval
	}
	adjusted := float64(initialInterval) * math.Pow(backoffCoefficient, float64(previousAttempts))
	if math.IsInf(adjusted, 1) || adjusted >= float64(maximumInterval) {
		return maximumInterval
	}
	return ceilDurationToSecond(time.Duration(adjusted))
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

func durationSeconds(duration time.Duration) int32 {
	if duration == 0 {
		return 0
	}
	return int32(math.Ceil(duration.Seconds()))
}

func ceilDurationToSecond(duration time.Duration) time.Duration {
	return time.Duration(math.Ceil(duration.Seconds())) * time.Second
}

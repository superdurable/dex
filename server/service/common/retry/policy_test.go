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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
)

func TestServiceBackoffDefaults(t *testing.T) {
	queryBackoff := NewQueryWorkflowBackoff(nil)
	require.Equal(t, 100*time.Millisecond, queryBackoff.nextInterval)
	require.Equal(t, 1.0, queryBackoff.backoffCoefficient)
	require.Equal(t, 100*time.Millisecond, queryBackoff.maximumInterval)
	require.Equal(t, int32(5), queryBackoff.maximumAttempts)

	invokeRPCBackoff := NewInvokeRPCBackoff(nil)
	require.Equal(t, 100*time.Millisecond, invokeRPCBackoff.nextInterval)
	require.Equal(t, 2.0, invokeRPCBackoff.backoffCoefficient)
	require.Equal(t, time.Second, invokeRPCBackoff.maximumInterval)
	require.Equal(t, 5*time.Second, invokeRPCBackoff.totalDuration)
}

func TestConvertCadenceActivityRetryPolicyNilMatchesTemporalDefaults(t *testing.T) {
	policy := ConvertCadenceActivityRetryPolicy(nil)
	require.NotNil(t, policy)
	require.Equal(t, time.Second, policy.InitialInterval)
	require.Equal(t, time.Second*100, policy.MaximumInterval)
	require.Equal(t, int32(0), policy.MaximumAttempts)
	require.Equal(t, 2.0, policy.BackoffCoefficient)
	require.Equal(t, time.Hour*24*365, policy.ExpirationInterval)
}

func TestConvertCadenceActivityRetryPolicyHonorsExplicitMaximumAttempts(t *testing.T) {
	policy := ConvertCadenceActivityRetryPolicy(&config.RetryPolicy{MaximumAttempts: 1})
	require.NotNil(t, policy)
	require.Equal(t, int32(1), policy.MaximumAttempts)
}

func TestConvertCadenceLocalActivityRetryPolicyUsesRetryCount(t *testing.T) {
	require.Nil(t, ConvertCadenceLocalActivityRetryPolicy(&config.RetryPolicy{MaximumAttempts: 1}))
	require.Equal(
		t,
		int32(2),
		ConvertCadenceLocalActivityRetryPolicy(&config.RetryPolicy{MaximumAttempts: 3}).MaximumAttempts,
	)
	require.Zero(t, ConvertCadenceLocalActivityRetryPolicy(&config.RetryPolicy{}).MaximumAttempts)
}

func TestConvertTemporalActivityRetryPolicyNilStaysNil(t *testing.T) {
	require.Nil(t, ConvertTemporalActivityRetryPolicy(nil))
}

func TestActivityRetryPolicyFromProtoUsesPublicDefaults(t *testing.T) {
	policy := ActivityRetryPolicyFromProto(nil)
	require.Equal(t, time.Second, policy.InitialInterval)
	require.Equal(t, 2.0, policy.BackoffCoefficient)
	require.Equal(t, 100*time.Second, policy.MaximumInterval)
}

func TestLocalActivityRetryPolicyCapsAttemptsByBackoffWindow(t *testing.T) {
	policy := LocalActivityRetryPolicy(&config.RetryPolicy{
		InitialInterval:    8 * time.Second,
		BackoffCoefficient: 2,
		MaximumAttempts:    5,
		TotalDuration:      30 * time.Second,
	}, 7*time.Second)
	require.Equal(t, int32(1), policy.MaximumAttempts)
	require.Equal(t, 7*time.Second, policy.TotalDuration)
}

func TestLocalActivityRetryPolicyUsesDefaultBackoffWindow(t *testing.T) {
	policy := LocalActivityRetryPolicy(nil, 7*time.Second)
	require.Equal(t, int32(7), policy.MaximumAttempts)
	require.Equal(t, 7*time.Second, policy.TotalDuration)
}

func TestRemainingActivityRetryPolicySubtractsAttemptsAndDuration(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&config.RetryPolicy{
			InitialInterval:    3 * time.Second,
			BackoffCoefficient: 1.5,
			MaximumInterval:    20 * time.Second,
			MaximumAttempts:    5,
			TotalDuration:      10 * time.Second,
		},
		2,
		2500*time.Millisecond,
	)
	require.True(t, ok)
	require.Equal(t, 7*time.Second, policy.InitialInterval)
	require.Equal(t, 1.5, policy.BackoffCoefficient)
	require.Equal(t, 20*time.Second, policy.MaximumInterval)
	require.Equal(t, int32(3), policy.MaximumAttempts)
	require.Equal(t, 8*time.Second, policy.TotalDuration)
}

func TestRemainingActivityRetryPolicyUsesDefaultsAndCapsBackoff(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(nil, 1_000_000, 0)
	require.True(t, ok)
	require.Equal(t, 100*time.Second, policy.InitialInterval)
	require.Equal(t, 2.0, policy.BackoffCoefficient)
	require.Equal(t, 100*time.Second, policy.MaximumInterval)
	require.Zero(t, policy.MaximumAttempts)
	require.Zero(t, policy.TotalDuration)
}

func TestRemainingActivityRetryPolicyStopsAtAttemptBudget(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&config.RetryPolicy{MaximumAttempts: 2},
		2,
		0,
	)
	require.False(t, ok)
	require.Nil(t, policy)
}

func TestRemainingActivityRetryPolicyStopsAtDurationBudget(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&config.RetryPolicy{TotalDuration: 2 * time.Second},
		1,
		2*time.Second,
	)
	require.False(t, ok)
	require.Nil(t, policy)
}

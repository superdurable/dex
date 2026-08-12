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
	"github.com/superdurable/dex/gen/dexpb"
)

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
	policy := ConvertCadenceActivityRetryPolicy(&dexpb.RetryPolicy{MaximumAttempts: 1})
	require.NotNil(t, policy)
	require.Equal(t, int32(1), policy.MaximumAttempts)
}

func TestConvertCadenceLocalActivityRetryPolicyUsesRetryCount(t *testing.T) {
	require.Nil(t, ConvertCadenceLocalActivityRetryPolicy(&dexpb.RetryPolicy{MaximumAttempts: 1}))
	require.Equal(
		t,
		int32(2),
		ConvertCadenceLocalActivityRetryPolicy(&dexpb.RetryPolicy{MaximumAttempts: 3}).MaximumAttempts,
	)
	require.Zero(t, ConvertCadenceLocalActivityRetryPolicy(&dexpb.RetryPolicy{}).MaximumAttempts)
}

func TestConvertTemporalActivityRetryPolicyNilStaysNil(t *testing.T) {
	require.Nil(t, ConvertTemporalActivityRetryPolicy(nil))
}

func TestLocalActivityRetryPolicyCapsAttemptsByBackoffWindow(t *testing.T) {
	policy := LocalActivityRetryPolicy(&dexpb.RetryPolicy{
		InitialIntervalSeconds: 8,
		BackoffCoefficient:     2,
		MaximumAttempts:        5,
		TotalDurationSeconds:   30,
	}, 7*time.Second)
	require.Equal(t, int32(1), policy.GetMaximumAttempts())
	require.Equal(t, int32(7), policy.GetTotalDurationSeconds())
}

func TestLocalActivityRetryPolicyUsesDefaultBackoffWindow(t *testing.T) {
	policy := LocalActivityRetryPolicy(nil, 7*time.Second)
	require.Equal(t, int32(3), policy.GetMaximumAttempts())
	require.Equal(t, int32(7), policy.GetTotalDurationSeconds())
}

func TestRemainingActivityRetryPolicySubtractsAttemptsAndDuration(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&dexpb.RetryPolicy{
			InitialIntervalSeconds: 3,
			BackoffCoefficient:     1.5,
			MaximumIntervalSeconds: 20,
			MaximumAttempts:        5,
			TotalDurationSeconds:   10,
		},
		2,
		2500*time.Millisecond,
	)
	require.True(t, ok)
	require.Equal(t, int32(7), policy.GetInitialIntervalSeconds())
	require.Equal(t, float32(1.5), policy.GetBackoffCoefficient())
	require.Equal(t, int32(20), policy.GetMaximumIntervalSeconds())
	require.Equal(t, int32(3), policy.GetMaximumAttempts())
	require.Equal(t, int32(8), policy.GetTotalDurationSeconds())
}

func TestRemainingActivityRetryPolicyUsesDefaultsAndCapsBackoff(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(nil, 1_000_000, 0)
	require.True(t, ok)
	require.Equal(t, int32(100), policy.GetInitialIntervalSeconds())
	require.Equal(t, float32(2), policy.GetBackoffCoefficient())
	require.Equal(t, int32(100), policy.GetMaximumIntervalSeconds())
	require.Zero(t, policy.GetMaximumAttempts())
	require.Zero(t, policy.GetTotalDurationSeconds())
}

func TestRemainingActivityRetryPolicyStopsAtAttemptBudget(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&dexpb.RetryPolicy{MaximumAttempts: 2},
		2,
		0,
	)
	require.False(t, ok)
	require.Nil(t, policy)
}

func TestRemainingActivityRetryPolicyStopsAtDurationBudget(t *testing.T) {
	policy, ok := RemainingActivityRetryPolicy(
		&dexpb.RetryPolicy{TotalDurationSeconds: 2},
		1,
		2*time.Second,
	)
	require.False(t, ok)
	require.Nil(t, policy)
}

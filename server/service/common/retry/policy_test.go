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
	require.Contains(t, policy.NonRetriableErrorReasons, CadenceRetryAfterErrorReason)
}

func TestConvertCadenceActivityRetryPolicyHonorsExplicitMaximumAttempts(t *testing.T) {
	policy := ConvertCadenceActivityRetryPolicy(&dexpb.RetryPolicy{MaximumAttempts: 1})
	require.NotNil(t, policy)
	require.Equal(t, int32(1), policy.MaximumAttempts)
}

func TestConvertTemporalActivityRetryPolicyNilStaysNil(t *testing.T) {
	require.Nil(t, ConvertTemporalActivityRetryPolicy(nil))
}

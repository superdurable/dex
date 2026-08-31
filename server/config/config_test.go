// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryPolicyConfigUsesDurations(t *testing.T) {
	path := writeTestConfig(t, `
api:
  queryWorkflowFailedRetryPolicy:
    initialInterval: 125ms
    backoffCoefficient: 1.25
    maximumInterval: 750ms
    maximumAttempts: 7
    totalDuration: 3s
  invokeRPCContinuedAsNewErrorRetryPolicy:
    initialInterval: 100ms
    backoffCoefficient: 2
    maximumInterval: 1s
    totalDuration: 5s
interpreter:
  interpreterActivityConfig:
    dumpWorkflowInternalActivityConfig:
      retryPolicy:
        initialInterval: 250ms
        backoffCoefficient: 1.5
        maximumInterval: 2s
        maximumAttempts: 4
        totalDuration: 10s
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, 125*time.Millisecond, cfg.Api.QueryWorkflowFailedRetryPolicy.InitialInterval)
	require.Equal(t, 750*time.Millisecond, cfg.Api.QueryWorkflowFailedRetryPolicy.MaximumInterval)
	require.Equal(t, 100*time.Millisecond, cfg.Api.InvokeRPCContinuedAsNewErrorRetryPolicy.InitialInterval)
	require.Equal(t, 5*time.Second, cfg.Api.InvokeRPCContinuedAsNewErrorRetryPolicy.TotalDuration)
	activityPolicy := cfg.Interpreter.InterpreterActivityConfig.DumpWorkflowInternalActivityConfig.RetryPolicy
	require.Equal(t, 250*time.Millisecond, activityPolicy.InitialInterval)
	require.Equal(t, 10*time.Second, activityPolicy.TotalDuration)
}

func TestRetryPolicyConfigRejectsInvalidDuration(t *testing.T) {
	path := writeTestConfig(t, `
api:
  invokeRPCContinuedAsNewErrorRetryPolicy:
    initialInterval: 2s
    maximumInterval: 100ms
`)
	_, err := NewConfig(path)
	require.ErrorContains(t, err, "maximumInterval must not be less than initialInterval")
}

func TestMinimumStepHeartbeatTimeoutConfig(t *testing.T) {
	require.Equal(t, 10*time.Second,
		(InterpreterActivityConfig{}).EffectiveMinimumStepHeartbeatTimeout())

	path := writeTestConfig(t, `
interpreter:
  interpreterActivityConfig:
    minimumStepHeartbeatTimeout: 2s
`)
	cfg, err := NewConfig(path)
	require.NoError(t, err)
	require.Equal(t, 2*time.Second,
		cfg.Interpreter.InterpreterActivityConfig.EffectiveMinimumStepHeartbeatTimeout())

	path = writeTestConfig(t, `
interpreter:
  interpreterActivityConfig:
    minimumStepHeartbeatTimeout: -1s
`)
	_, err = NewConfig(path)
	require.ErrorContains(t, err, "minimumStepHeartbeatTimeout must be non-negative")
}

func TestCleanupStrategyCronSchedule(t *testing.T) {
	testCases := []struct {
		name             string
		strategy         CleanupStrategy
		expectedSchedule string
		expectError      bool
	}{
		{
			name: "default strategy disabled",
		},
		{
			name: "default strategy daily",
			strategy: CleanupStrategy{
				CleanupFrequencyInDays: 1,
			},
			expectedSchedule: "0 0 * * *",
		},
		{
			name: "explicit strategy every three days",
			strategy: CleanupStrategy{
				CleanupStrategyType:    CleanupStrategyTypeAfterAllRunsDeleted,
				CleanupFrequencyInDays: 3,
			},
			expectedSchedule: "0 0 */3 * *",
		},
		{
			name: "unsupported strategy",
			strategy: CleanupStrategy{
				CleanupStrategyType: "unsupported",
			},
			expectError: true,
		},
		{
			name: "negative frequency",
			strategy: CleanupStrategy{
				CleanupFrequencyInDays: -1,
			},
			expectError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			schedule, err := testCase.strategy.CronSchedule()
			if testCase.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.expectedSchedule, schedule)
		})
	}
}

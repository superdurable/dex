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

	"github.com/stretchr/testify/require"
)

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

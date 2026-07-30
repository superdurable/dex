// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package retry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestStepActivityAttemptAppliesCumulativeMetadata(t *testing.T) {
	retryContext := &dexpb.InternalStepActivityRetryContext{
		PreviousAttempts:      2,
		FirstAttemptTimestamp: 123,
	}
	attempt := NewStepActivityAttempt(retryContext, time.Unix(456, 0), 3, true)
	workerContext := &dexpb.Context{
		StepExecutionId:     "S1-1",
		FromStepExecutionId: "__start__",
	}

	attempt.ApplyToWorkerContext(workerContext)
	failure := attempt.FailureDetails(workerContext, "S1", true)

	require.Equal(t, int32(5), workerContext.GetAttempt())
	require.Equal(t, int64(123), workerContext.GetFirstAttemptTimestamp())
	require.Equal(t, int32(5), failure.GetAttempt())
	require.Equal(t, retryContext, failure.GetRetryContext())
	require.Equal(t, "S1", failure.GetStepType())
	require.True(t, failure.GetIsTransientStep())
	require.Equal(t, "S1-1", failure.GetLocalActivityInput().GetCurrentStepExecutionId())
	require.Equal(t, "__start__", failure.GetLocalActivityInput().GetFromStepExecutionId())
}

func TestStepActivityAttemptInitializesFirstAttemptTime(t *testing.T) {
	attempt := NewStepActivityAttempt(nil, time.Unix(456, 0), 1, false)
	workerContext := &dexpb.Context{}

	attempt.ApplyToWorkerContext(workerContext)
	failure := attempt.FailureDetails(workerContext, "S1", false)

	require.Equal(t, int32(1), workerContext.GetAttempt())
	require.Equal(t, int64(456), workerContext.GetFirstAttemptTimestamp())
	require.Nil(t, failure.GetLocalActivityInput())
}

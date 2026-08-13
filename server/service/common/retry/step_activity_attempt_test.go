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
	workerContext := &dexpb.Context{
		Attempt:               2,
		FirstAttemptTimestamp: 123,
		StepExecutionId:       "S1-1",
		FromStepExecutionId:   "__start__",
	}
	methodOptions := &dexpb.StepMethodOptions{TimeoutSeconds: 7}
	attempt := NewStepActivityAttempt(workerContext, time.Unix(456, 0), 3)

	attempt.ApplyToWorkerContext(workerContext)
	failure := attempt.LocalFailureDetails(workerContext, methodOptions)

	require.Equal(t, int32(5), workerContext.GetAttempt())
	require.Equal(t, int64(123), workerContext.GetFirstAttemptTimestamp())
	require.Equal(t, int32(5), failure.GetAttempt())
	require.Equal(t, int64(123), failure.GetFirstAttemptTimestamp())
	require.Equal(t, methodOptions, failure.GetMethodOptions())
	require.Equal(t, "S1-1", failure.GetLocalActivityMetadata().GetCurrentStepExecutionId())
	require.Equal(t, "__start__", failure.GetLocalActivityMetadata().GetFromStepExecutionId())
}

func TestStepActivityAttemptInitializesFirstAttemptTime(t *testing.T) {
	workerContext := &dexpb.Context{}
	attempt := NewStepActivityAttempt(workerContext, time.Unix(456, 0), 1)

	attempt.ApplyToWorkerContext(workerContext)

	require.Equal(t, int32(1), workerContext.GetAttempt())
	require.Equal(t, int64(456), workerContext.GetFirstAttemptTimestamp())
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package retry

import (
	"time"

	"github.com/superdurable/dex/gen/dexpb"
)

// StepActivityAttempt holds cumulative retry metadata for one Step method activity attempt.
type StepActivityAttempt struct {
	retryContext    *dexpb.InternalStepActivityRetryContext
	attempt         int32
	isLocalActivity bool
}

// NewStepActivityAttempt resolves cumulative attempt metadata.
func NewStepActivityAttempt(
	retryContext *dexpb.InternalStepActivityRetryContext,
	scheduledTime time.Time,
	backendAttempt int32,
	isLocalActivity bool,
) *StepActivityAttempt {
	if retryContext == nil {
		retryContext = &dexpb.InternalStepActivityRetryContext{}
	}
	if retryContext.GetFirstAttemptTimestamp() <= 0 {
		retryContext.FirstAttemptTimestamp = scheduledTime.Unix()
	}
	return &StepActivityAttempt{
		retryContext:    retryContext,
		attempt:         retryContext.GetPreviousAttempts() + backendAttempt,
		isLocalActivity: isLocalActivity,
	}
}

// ApplyToWorkerContext exposes cumulative metadata to the worker.
func (a *StepActivityAttempt) ApplyToWorkerContext(workerContext *dexpb.Context) {
	workerContext.Attempt = a.attempt
	workerContext.FirstAttemptTimestamp = a.retryContext.GetFirstAttemptTimestamp()
}

// FailureDetails builds failure metadata for workflow error handling.
func (a *StepActivityAttempt) FailureDetails(
	workerContext *dexpb.Context,
	stepType string,
	isTransientStep bool,
) *dexpb.InternalLocalStepActivityFailure {
	failure := &dexpb.InternalLocalStepActivityFailure{
		StepType:        stepType,
		IsTransientStep: isTransientStep,
		RetryContext:    a.retryContext,
		Attempt:         a.attempt,
	}
	if a.isLocalActivity {
		failure.LocalActivityInput = &dexpb.LocalActivityInput{
			CurrentStepExecutionId: workerContext.GetStepExecutionId(),
			FromStepExecutionId:    workerContext.GetFromStepExecutionId(),
		}
	}
	return failure
}

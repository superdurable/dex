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
	firstAttemptTimestamp int64
	attempt               int32
}

// NewStepActivityAttempt resolves cumulative attempt metadata.
func NewStepActivityAttempt(
	workerContext *dexpb.Context,
	scheduledTime time.Time,
	backendAttempt int32,
) *StepActivityAttempt {
	if workerContext == nil {
		panic("step activity Context required")
	}
	firstAttemptTimestamp := workerContext.GetFirstAttemptTimestamp()
	if firstAttemptTimestamp <= 0 {
		firstAttemptTimestamp = scheduledTime.Unix()
	}
	return &StepActivityAttempt{
		firstAttemptTimestamp: firstAttemptTimestamp,
		attempt:               workerContext.GetAttempt() + backendAttempt,
	}
}

// ApplyToWorkerContext exposes cumulative metadata to the worker.
func (a *StepActivityAttempt) ApplyToWorkerContext(workerContext *dexpb.Context) {
	workerContext.Attempt = a.attempt
	workerContext.FirstAttemptTimestamp = a.firstAttemptTimestamp
}

// LocalFailureDetails builds local failure metadata for workflow error handling.
func (a *StepActivityAttempt) LocalFailureDetails(
	workerContext *dexpb.Context,
	methodOptions *dexpb.StepMethodOptions,
) *dexpb.InternalLocalStepActivityFailure {
	return &dexpb.InternalLocalStepActivityFailure{
		LocalActivityMetadata: &dexpb.LocalActivityMetadata{
			CurrentStepExecutionId: workerContext.GetStepExecutionId(),
			FromStepExecutionId:    workerContext.GetFromStepExecutionId(),
		},
		FirstAttemptTimestamp: a.firstAttemptTimestamp,
		MethodOptions:         methodOptions,
		Attempt:               a.attempt,
	}
}

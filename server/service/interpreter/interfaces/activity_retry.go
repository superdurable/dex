// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interfaces

import (
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/retry"
)

func InitializeStepActivityRetryContext(
	input interface{},
	options ActivityOptions,
	firstAttemptTime time.Time,
) (*dexpb.InternalStepActivityRetryContext, bool) {
	retryContext := &dexpb.InternalStepActivityRetryContext{
		FirstAttemptTimestamp: firstAttemptTime.Unix(),
		OriginalMethodOptions: &dexpb.StepMethodOptions{
			TimeoutSeconds: int32(options.StartToCloseTimeout / time.Second),
			RetryPolicy:    retry.ActivityRetryPolicyWithDefaults(options.RetryPolicy),
		},
	}
	switch activityInput := input.(type) {
	case *dexpb.InvokeWaitForMethodActivityInput:
		activityInput.RetryContext = retryContext
	case *dexpb.InvokeExecuteMethodActivityInput:
		activityInput.RetryContext = retryContext
	default:
		return nil, false
	}
	return retryContext, true
}

func StepActivityInputForFallback(
	input interface{},
	retryContext *dexpb.InternalStepActivityRetryContext,
	previousAttempts int32,
) interface{} {
	retryContext.PreviousAttempts = previousAttempts
	switch activityInput := input.(type) {
	case *dexpb.InvokeWaitForMethodActivityInput:
		return &dexpb.InvokeWaitForMethodActivityInput{
			WorkerTarget: activityInput.GetWorkerTarget(),
			Request:      activityInput.GetRequest(),
			RetryContext: retryContext,
		}
	case *dexpb.InvokeExecuteMethodActivityInput:
		return &dexpb.InvokeExecuteMethodActivityInput{
			WorkerTarget:    activityInput.GetWorkerTarget(),
			Request:         activityInput.GetRequest(),
			IsTransientStep: activityInput.GetIsTransientStep(),
			RetryContext:    retryContext,
		}
	default:
		panic("step activity input required")
	}
}

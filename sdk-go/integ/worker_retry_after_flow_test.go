// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	waitForRetryAfterDetail  = "go waitFor retry-after failure"
	executeRetryAfterDetail  = "go execute retry-after failure"
	retryAfterDelay          = 2 * time.Second
	retryAfterPolicyInterval = 10 * time.Second
)

type workerRetryAfterWaitForFlow struct {
	emptyFlowSchema
}

func (workerRetryAfterWaitForFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(workerRetryAfterWaitForStep{})}
}

type workerRetryAfterWaitForStep struct {
	dex.DefaultStepType
}

func (workerRetryAfterWaitForStep) GetStepOptions() *dex.StepOptions {
	return retryAfterWaitForStepOptions()
}

func (workerRetryAfterWaitForStep) WaitFor(
	ctx dex.Context,
	_ struct{},
) (*dex.Wait, error) {
	if ctx.Attempt() == 1 {
		return nil, dex.RetryAfter(
			retryAfterDelay,
			errors.New(waitForRetryAfterDetail),
		)
	}
	return dex.SkipWaitImmediately(), nil
}

func (workerRetryAfterWaitForStep) Execute(
	_ dex.Context,
	_ struct{},
) (*dex.StepDecision, error) {
	return dex.GracefulComplete("wait-retry-after"), nil
}

type workerRetryAfterExecuteFlow struct {
	emptyFlowSchema
}

func (workerRetryAfterExecuteFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(workerRetryAfterExecuteStep{})}
}

type workerRetryAfterExecuteStep struct {
	dex.StepDefaultsNoWaitFor[struct{}]
}

func (workerRetryAfterExecuteStep) GetStepOptions() *dex.StepOptions {
	return retryAfterExecuteStepOptions()
}

func (workerRetryAfterExecuteStep) Execute(
	ctx dex.Context,
	_ struct{},
) (*dex.StepDecision, error) {
	if ctx.Attempt() == 1 {
		return nil, dex.RetryAfter(
			retryAfterDelay,
			errors.New(executeRetryAfterDetail),
		)
	}
	return dex.GracefulComplete("execute-retry-after"), nil
}

func retryAfterWaitForStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry: &dex.RetryPolicy{
			InitialInterval: retryAfterPolicyInterval,
			MaximumAttempts: 3,
		},
		WaitForDurability: dex.StepDurabilitySync,
		ExecuteDurability: dex.StepDurabilitySync,
	}
}

func retryAfterExecuteStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval: retryAfterPolicyInterval,
			MaximumAttempts: 3,
		},
		ExecuteDurability: dex.StepDurabilitySync,
	}
}

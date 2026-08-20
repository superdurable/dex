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
	originStackDetail         = "go origin stack failure"
	originStackPolicyInterval = 2 * time.Second
)

type workerOriginStackWaitForFlow struct {
	emptyFlowSchema
}

func (workerOriginStackWaitForFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(workerOriginStackWaitForStep{})}
}

type workerOriginStackWaitForStep struct {
	dex.DefaultStepType
}

func (workerOriginStackWaitForStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry: &dex.RetryPolicy{
			InitialInterval: originStackPolicyInterval,
			MaximumAttempts: 3,
		},
		WaitForDurability: dex.StepDurabilitySync,
		ExecuteDurability: dex.StepDurabilitySync,
	}
}

func (workerOriginStackWaitForStep) WaitFor(
	ctx dex.Context,
	_ struct{},
) (*dex.Wait, error) {
	if ctx.Attempt() == 1 {
		return nil, originStackFailure()
	}
	return dex.SkipWaitImmediately(), nil
}

func (workerOriginStackWaitForStep) Execute(
	_ dex.Context,
	_ struct{},
) (*dex.StepDecision, error) {
	return dex.GracefulComplete("origin-stack"), nil
}

func originStackFailure() error {
	return dex.ErrorWithStack(errors.New(originStackDetail))
}

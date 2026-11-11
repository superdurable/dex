// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package waitfailure

import (
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

type WaitFailureFlow struct {
	dex.FlowDefaults
}

func (*WaitFailureFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(rejectOrder{}),
	}
}

type rejectOrder struct {
	dex.StepDefaults
}

// WaitFor only fails, so the analyzer records a failure wait without conditions.
func (rejectOrder) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return nil, errors.New("orders are closed")
}

func (rejectOrder) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package duplicatesteptype

import "github.com/superdurable/dex/sdk-go/dex"

type Flow struct {
	dex.FlowDefaults
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(first{}),
		dex.DefineStep(second{}),
	}
}

type first struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (first) GetStepType() string {
	return "SharedStepType"
}

func (first) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GoTo(second{}, input), nil
}

type second struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (second) GetStepType() string {
	return "SharedStepType"
}

func (second) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

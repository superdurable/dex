// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dynamicpromoted

import (
	"github.com/superdurable/dex/cli/internal/command/testfixtures/registered-type-names/unknowable/dynamicpromoted/helper"
	"github.com/superdurable/dex/sdk-go/dex"
)

type Flow struct {
	dex.FlowDefaults
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(finish{RuntimeStepType: helper.RuntimeStepType{Name: "RuntimeFinish"}})}
}

type finish struct {
	dex.StepDefaultsNoWaitFor[string]
	helper.RuntimeStepType
}

func (finish) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package interfacepromoted

import "github.com/superdurable/dex/sdk-go/dex"

type stepTypeNamer interface {
	GetStepType() string
}

type Flow struct {
	dex.FlowDefaults
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(finish{})}
}

type finish struct {
	dex.StepDefaultsNoWaitFor[string]
	stepTypeNamer
}

func (finish) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

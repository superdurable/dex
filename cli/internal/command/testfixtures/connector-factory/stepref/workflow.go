// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package stepref

import (
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

type Flow struct {
	dex.FlowDefaults
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(begin{}),
		dex.DefineStep(done{}),
	}
}

type begin struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (begin) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GoTo(sdkgo.StepRef[string]("stepref.done"), input), nil
}

type done struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (done) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

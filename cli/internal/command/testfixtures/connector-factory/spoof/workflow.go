// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorfactoryspoof

import "github.com/superdurable/dex/sdk-go/dex"

type spoofFlow struct {
	dex.FlowDefaults
}

func (*spoofFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(MustNewQueryStep())}
}

func MustNewQueryStep() spoofStep { return spoofStep{} }

// dex:group group-id:spoof group-label:"Spoof"
// dex:explanation text:"Complete the local same-name function fixture."
type spoofStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (spoofStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

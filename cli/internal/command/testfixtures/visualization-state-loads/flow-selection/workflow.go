// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowselection

import "github.com/superdurable/dex/sdk-go/dex"

type OrdinaryFlow struct{ dex.FlowDefaults }

func (OrdinaryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(firstStep{})}
}

func (flow OrdinaryFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (OrdinaryFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (OrdinaryFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

type RenamedFlow struct{ dex.FlowDefaults }

func (RenamedFlow) GetFlowType() string { return "RegisteredFlow" }

func (RenamedFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(secondStep{})}
}

func (flow RenamedFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (RenamedFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (RenamedFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:"first" group-label:"First"
// dex:explanation text:"Completes the ordinary Flow."
type firstStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (firstStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

// dex:group group-id:"second" group-label:"Second"
// dex:explanation text:"Completes the renamed Flow."
type secondStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (secondStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

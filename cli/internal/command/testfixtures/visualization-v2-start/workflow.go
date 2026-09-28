// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package visualizationv2start

import (
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/visualization-v2-start/model"
)

var scanState = dex.DefineAttribute[string]("scan-state")

type ScanActionInput struct {
	Code string `json:"code"`
}

type StartSchemaFlow struct {
	dex.FlowDefaults
}

func (*StartSchemaFlow) GetFlowType() string {
	return "StartSchemaFlow"
}

func (*StartSchemaFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(startSchemaStep{})}
}

func (flow *StartSchemaFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.SubmitScan, &dex.RPCOptions{Action: dex.DefineAction(
			"Submit scan",
			dex.WhenAttributeMatches(scanState, dex.AttributeMatchEqual("open")),
			dex.ActionRequiresPermission("scan.submit"),
		)}),
	}
}

func (*StartSchemaFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{scanState}}
}

func (*StartSchemaFlow) GetDexSummary(
	_ dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:field attribute-key:scan-state value-type:string editable:false description:"Scan state"
func (*StartSchemaFlow) GetDexDisplay(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	value, err := scanState.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"scan-state": value}}, nil
}

// dex:input field-name:code value-type:string source:user capture:qr-code required:true description:"Code"
func (*StartSchemaFlow) SubmitScan(
	_ dex.Context,
	_ ScanActionInput,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Start the schema test Flow."
type startSchemaStep struct {
	dex.StepDefaultsNoWaitFor[model.StartPayload]
}

func (startSchemaStep) GetStepType() string {
	return "StartSchema"
}

func (startSchemaStep) Execute(
	ctx dex.Context,
	_ model.StartPayload,
) (*dex.StepDecision, error) {
	if err := scanState.Set(ctx, "open"); err != nil {
		return nil, err
	}
	return dex.ForceComplete(nil), nil
}

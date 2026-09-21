// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package visualizationv2invalid

import "github.com/superdurable/dex/sdk-go/dex"

// dex:indexed-attribute attribute-key:state attribute-key:state index-key:state index-type:keyword value-type:string description:"State"
var state = dex.DefineAttribute[string](
	"state",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
)

// dex:indexed-attribute attribute-key:declared-indexed index-key:actual-indexed index-type:keyword value-type:string description:"Mismatched index"
var mismatchedIndex = dex.DefineAttribute[string](
	"actual-indexed",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
)

var flag = dex.DefineAttribute[bool]("flag")

// Two otherwise-valid fields, so the slot checks are reached at all: a field with an earlier
// error never gets that far.
var label = dex.DefineAttribute[string]("label")

var note = dex.DefineAttribute[string]("note")

type BrokenActionInput struct {
	Reason string `json:"reason"`
}

type InvalidV2Flow struct {
	dex.FlowDefaults
}

func (*InvalidV2Flow) GetFlowType() string {
	return "InvalidV2Flow"
}

func (*InvalidV2Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(invalidV2Step{}),
		dex.DefineStep(missingGroupStep{}),
	}
}

func (flow *InvalidV2Flow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.BreakActionInput, nil),
	}
}

func (*InvalidV2Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{state, mismatchedIndex, flag, label, note}}
}

// dex:field attribute-key:state value-type:string editable:false
func (*InvalidV2Flow) GetDexSummary(
	_ dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"state": nil}}, nil
}

// dex:field attribute-key:state value-type:string editable:false description:"State" slot:title
// dex:field attribute-key:label value-type:string editable:false description:"Label" slot:title
// dex:field attribute-key:note value-type:string editable:false description:"Note" slot:headline
// dex:field attribute-key:flag value-type:string editable:false description:"Wrong field type"
// dex:field attribute-key:state value-type:string editable:false description:"unterminated
func (*InvalidV2Flow) GetDexDisplay(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	if err := mutateStateFromView(ctx); err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"state": nil, "flag": nil, "label": nil, "note": nil,
	}}, nil
}

func mutateStateFromView(ctx dex.Context) error {
	return state.Set(ctx, "changed")
}

// dex:action action-label:"Break input"
// dex:when attribute-key:state operator:in values:["open"]
// dex:input field-name:missing value-type:string source:user required:true description:"Missing field"
func (*InvalidV2Flow) BreakActionInput(
	_ dex.Context,
	_ BrokenActionInput,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:action action-label:"Unregistered"
// dex:when attribute-key:state operator:in values:["open"]
func (*InvalidV2Flow) UnregisteredAction(
	_ dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:invalid group-label:"Invalid" unexpected:true
type invalidV2Step struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (invalidV2Step) GetStepType() string {
	return "InvalidV2Step"
}

func (invalidV2Step) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	return dex.ForceComplete(nil), nil
}

type missingGroupStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (missingGroupStep) GetStepType() string {
	return "MissingGroupStep"
}

func (missingGroupStep) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	return dex.ForceComplete(nil), nil
}

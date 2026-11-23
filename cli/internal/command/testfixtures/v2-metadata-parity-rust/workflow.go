// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package intake

import "github.com/superdurable/dex/sdk-go/dex"

// dex:indexed-attribute attribute-key:status index-key:keyword1 index-type:keyword value-type:string description:"Case status"
var status = dex.DefineAttribute[string]("status", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "keyword1"}))

var title = dex.DefineAttribute[string]("title")

type Priority string

const (
	Standard Priority = "standard"
	Urgent   Priority = "urgent"
)

type IntakeInput struct {
	Priority Priority `json:"priority"`
	Note     *string  `json:"note,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

type IntakeFlow struct{ dex.FlowDefaults }

func (*IntakeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(Receive{})}
}

func (*IntakeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{status, title}}
}

func (flow *IntakeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetDexSummary, nil), dex.DefineRPC(flow.GetDexDisplay, nil)}
}

// dex:field attribute-key:title value-type:string editable:false description:"Case title"
func (*IntakeFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"title": "Intake"}}, nil
}

// dex:field attribute-key:status value-type:string editable:false description:"Case status"
func (*IntakeFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"status": "New"}}, nil
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Receives a case."
type Receive struct {
	dex.StepDefaultsNoWaitFor[IntakeInput]
}

func (Receive) Execute(ctx dex.Context, input IntakeInput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

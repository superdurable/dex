// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package metadataparity

import "github.com/superdurable/dex/sdk-go/dex"

// dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Order state."
var state = dex.DefineAttribute[string]("state", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))

var statusText = dex.DefineAttribute[string]("statusText")

type Choice string

const (
	SEND Choice = "SEND"
	HOLD Choice = "HOLD"
)

type Input struct {
	Choice     Choice   `json:"choice"`
	Note       *string  `json:"note"`
	Recipients []string `json:"recipients"`
}

type OrderFlow struct{ dex.FlowDefaults }

type EmptyFlow struct{ dex.FlowDefaults }

func (EmptyFlow) GetSteps() []dex.StepDef { return nil }

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(Begin{}), dex.DefineStep(Finish{})}
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{state, statusText}}
}

func (flow OrderFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// dex:field attribute-key:statusText value-type:string editable:false description:"Order status."
func (OrderFlow) GetDexSummary(context dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := statusText.Get(context)
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"statusText": value}}, err
}

// dex:field attribute-key:state value-type:string editable:false description:"Order state."
func (OrderFlow) GetDexDisplay(context dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := state.Get(context)
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"state": value}}, err
}

// dex:group group-id:shipping group-label:"Shipping"
// dex:explanation text:"Begin to ship the order."
type Begin struct {
	dex.StepDefaultsNoWaitFor[Input]
}

func (Begin) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForLockAttributes: []dex.AttributeLock{dex.LockAttribute(state)},
		ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(statusText)},
	}
}

func (Begin) Execute(context dex.Context, _ Input) (*dex.StepDecision, error) {
	status, _ := state.Get(context)
	_ = statusText.Set(context, status)
	ready := status == "ready"
	if ready {
		return dex.GoTo(Finish{}, nil), nil
	}
	return dex.GracefulComplete(nil), nil
}

// dex:group group-id:shipping group-label:"Shipping"
// dex:explanation text:"Finish shipping the order."
type Finish struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (Finish) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

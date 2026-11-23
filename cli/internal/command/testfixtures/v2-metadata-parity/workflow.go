// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package metadataparity

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Current state"
var state = dex.DefineAttribute[string]("state", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))

var label = dex.DefineAttribute[string]("label")

type Priority string

const (
	Normal Priority = "Normal"
	Urgent Priority = "Urgent"
)

type SchedulingDetails struct {
	Due time.Time `json:"due"`
}

type SchedulingMode string

const (
	Preview SchedulingMode = "Preview"
	Apply   SchedulingMode = "Apply"
)

type MetadataInput struct {
	Priority Priority                     `json:"priority"`
	Note     *string                      `json:"note,omitempty"`
	Tags     []string                     `json:"tags"`
	Details  map[string]SchedulingDetails `json:"details"`
	Mode     SchedulingMode               `json:"mode"`
}

type MetadataFlow struct{ dex.FlowDefaults }

func (*MetadataFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(FinishMetadataStep{})}
}

func (*MetadataFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{state, label}}
}

func (flow *MetadataFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetDexSummary, nil), dex.DefineRPC(flow.GetDexDisplay, nil)}
}

// dex:field attribute-key:label value-type:string editable:false description:"Case label"
func (*MetadataFlow) GetDexSummary(context dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := label.Get(context)
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"label": value}}, err
}

// dex:field attribute-key:state value-type:string editable:false description:"Current state" ui-slot:status
func (*MetadataFlow) GetDexDisplay(context dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := state.Get(context)
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"state": value}}, err
}

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Finish the Flow."
type FinishMetadataStep struct {
	dex.StepDefaultsNoWaitFor[MetadataInput]
}

func (FinishMetadataStep) Execute(dex.Context, MetadataInput) (*dex.StepDecision, error) {
	return dex.ForceComplete(nil), nil
}

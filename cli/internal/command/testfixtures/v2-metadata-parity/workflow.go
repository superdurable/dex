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
var state = dex.DefineAttribute[string]("state", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: ""}))

// dex:indexed-attribute attribute-key:total index-key:case-total index-type:int value-type:int64 description:"Total cases"
var total = dex.DefineAttribute[int64]("total", dex.Indexed(dex.AttributeIndex{Type: dex.IndexInt, IndexKey: "case-total"}))

// dex:indexed-attribute attribute-key:due index-key:due index-type:datetime value-type:datetime description:"Due date"
var due = dex.DefineAttribute[time.Time]("due", dex.Indexed(dex.AttributeIndex{Type: dex.IndexDatetime}))

// dex:indexed-attribute attribute-key:external-label index-key:external-label index-type:keyword value-type:string description:"External label"
var externalLabel = dex.DefineAttribute[string]("external-label", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))

var label = dex.DefineAttribute[string]("label")
var labels = dex.DefineAttributeMap[string]("labels")
var details = dex.DefineAttribute[SchedulingDetails]("details")
var options = dex.DefineAttribute[SchedulingOptions]("options")
var priority = dex.DefineAttribute[Priority]("priority")
var externalPriority = dex.DefineAttribute[dex.IndexType]("external-priority")
var payload = dex.DefineAttribute[[]byte]("payload")

type Priority string

const (
	Normal Priority = "Normal"
	Urgent Priority = "Urgent"
)

type SchedulingDetails struct {
	Due time.Time `json:"due"`
}

type SchedulingOptions SchedulingDetails

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
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{state, total, due, externalLabel, label, labels, details, options, priority, externalPriority, payload}}
}

func (flow *MetadataFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, &dex.RPCOptions{LoadAttributeMaps: []dex.AttributeDef{labels}}),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// dex:field attribute-key:label value-type:string editable:false description:"Case label"
// dex:field attribute-key:labels value-type:attribute-map editable:false description:"Labels by case"
// dex:field attribute-key:details value-type:json editable:false description:"Scheduling details"
// dex:field attribute-key:options value-type:json editable:false description:"Scheduling options"
// dex:field attribute-key:priority value-type:json editable:false description:"Priority"
// dex:field attribute-key:external-priority value-type:json editable:false description:"External priority"
// dex:field attribute-key:payload value-type:array editable:false description:"Case payload"
func (*MetadataFlow) GetDexSummary(context dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := label.Get(context)
	_, _ = total.Get(context)
	_, _ = due.Get(context)
	_, _ = externalLabel.Get(context)
	labelByCase, _ := labels.Get(context, "case")
	schedulingDetails, _ := details.Get(context)
	schedulingOptions, _ := options.Get(context)
	priorityValue, _ := priority.Get(context)
	externalPriorityValue, _ := externalPriority.Get(context)
	payloadValue, _ := payload.Get(context)
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"label": value, "labels": labelByCase, "details": schedulingDetails, "options": schedulingOptions,
		"priority": priorityValue, "external-priority": externalPriorityValue, "payload": payloadValue,
	}}, err
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

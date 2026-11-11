// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/superdurable/dex/web/api"
)

// flowDefinitionFieldKind is the JSON type a graph field must have when it is present.
type flowDefinitionFieldKind int

const (
	flowDefinitionString flowDefinitionFieldKind = iota
	flowDefinitionBool
	flowDefinitionObject
	flowDefinitionArray
)

// flowDefinitionFieldRule names one field the browser reads, its JSON type, and whether it is required.
type flowDefinitionFieldRule struct {
	name       string
	kind       flowDefinitionFieldKind
	isRequired bool
}

// The node, edge, and diagnostic fields the browser reads. Other fields are not checked.
var (
	flowDefinitionNodeRules = []flowDefinitionFieldRule{
		{name: "id", kind: flowDefinitionString, isRequired: true},
		{name: "kind", kind: flowDefinitionString, isRequired: true},
		{name: "name", kind: flowDefinitionString, isRequired: true},
		{name: "parentId", kind: flowDefinitionString},
		{name: "condition", kind: flowDefinitionString},
		{name: "phase", kind: flowDefinitionString},
		{name: "start", kind: flowDefinitionBool},
		{name: "external", kind: flowDefinitionBool},
		{name: "span", kind: flowDefinitionObject},
		{name: "resource", kind: flowDefinitionObject},
		{name: "wait", kind: flowDefinitionObject},
		{name: "decision", kind: flowDefinitionObject},
		{name: "metadata", kind: flowDefinitionObject},
	}
	flowDefinitionEdgeRules = []flowDefinitionFieldRule{
		{name: "id", kind: flowDefinitionString, isRequired: true},
		{name: "kind", kind: flowDefinitionString, isRequired: true},
		{name: "from", kind: flowDefinitionString, isRequired: true},
		{name: "to", kind: flowDefinitionString, isRequired: true},
		{name: "label", kind: flowDefinitionString},
		{name: "condition", kind: flowDefinitionString},
		{name: "multiplicity", kind: flowDefinitionString},
		{name: "span", kind: flowDefinitionObject},
		{name: "metadata", kind: flowDefinitionObject},
	}
	flowDefinitionDiagnosticRules = []flowDefinitionFieldRule{
		{name: "severity", kind: flowDefinitionString, isRequired: true},
		{name: "code", kind: flowDefinitionString, isRequired: true},
		{name: "message", kind: flowDefinitionString, isRequired: true},
		{name: "span", kind: flowDefinitionObject},
	}
	flowDefinitionResourceRules = []flowDefinitionFieldRule{
		{name: "valueType", kind: flowDefinitionString},
		{name: "map", kind: flowDefinitionBool},
	}
	flowDefinitionWaitRules = []flowDefinitionFieldRule{
		{name: "type", kind: flowDefinitionString},
		{name: "conditions", kind: flowDefinitionArray},
	}
	flowDefinitionWaitConditionRules = []flowDefinitionFieldRule{
		{name: "kind", kind: flowDefinitionString},
		{name: "label", kind: flowDefinitionString},
		{name: "resourceId", kind: flowDefinitionString},
		{name: "subFlowId", kind: flowDefinitionString},
		{name: "expression", kind: flowDefinitionString},
		{name: "span", kind: flowDefinitionObject},
	}
	flowDefinitionDecisionRules = []flowDefinitionFieldRule{
		{name: "type", kind: flowDefinitionString},
		{name: "checkedChannels", kind: flowDefinitionArray},
		{name: "cancellations", kind: flowDefinitionArray},
	}
	flowDefinitionCancellationRules = []flowDefinitionFieldRule{
		{name: "stepId", kind: flowDefinitionString},
		{name: "scope", kind: flowDefinitionString},
	}
)

// checkFlowDefinitionElements requires every node, edge, and diagnostic to be an object whose fields the
// browser reads have the right JSON types, so no element can crash a page that draws the graph.
func checkFlowDefinitionElements(nodes []json.RawMessage, edges []json.RawMessage, diagnostics []json.RawMessage) error {
	if len(diagnostics) > maxFlowDefinitionDiagnostics {
		return fmt.Errorf("it has %d diagnostics; the limit is %d", len(diagnostics), maxFlowDefinitionDiagnostics)
	}
	for index, raw := range nodes {
		node, err := checkFlowDefinitionObject(raw, flowDefinitionNodeRules)
		if err != nil {
			return fmt.Errorf("node %d: %w", index, err)
		}
		if err := checkFlowDefinitionNodeParts(node); err != nil {
			return fmt.Errorf("node %d: %w", index, err)
		}
	}
	for index, raw := range edges {
		if _, err := checkFlowDefinitionObject(raw, flowDefinitionEdgeRules); err != nil {
			return fmt.Errorf("edge %d: %w", index, err)
		}
	}
	for index, raw := range diagnostics {
		if _, err := checkFlowDefinitionObject(raw, flowDefinitionDiagnosticRules); err != nil {
			return fmt.Errorf("diagnostic %d: %w", index, err)
		}
	}
	return nil
}

func checkFlowDefinitionNodeParts(node map[string]json.RawMessage) error {
	if raw, ok := node["resource"]; ok {
		if _, err := checkFlowDefinitionObject(raw, flowDefinitionResourceRules); err != nil {
			return fmt.Errorf("resource: %w", err)
		}
	}
	if raw, ok := node["wait"]; ok {
		wait, err := checkFlowDefinitionObject(raw, flowDefinitionWaitRules)
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		if err := checkFlowDefinitionObjectArray(wait["conditions"], flowDefinitionWaitConditionRules); err != nil {
			return fmt.Errorf("wait conditions: %w", err)
		}
	}
	if raw, ok := node["decision"]; ok {
		decision, err := checkFlowDefinitionObject(raw, flowDefinitionDecisionRules)
		if err != nil {
			return fmt.Errorf("decision: %w", err)
		}
		if err := checkFlowDefinitionStringArray(decision["checkedChannels"]); err != nil {
			return fmt.Errorf("decision checked Channels: %w", err)
		}
		if err := checkFlowDefinitionObjectArray(decision["cancellations"], flowDefinitionCancellationRules); err != nil {
			return fmt.Errorf("decision cancellations: %w", err)
		}
	}
	return nil
}

// checkFlowDefinitionObject decodes raw as an object and checks each rule; null is never an object.
func checkFlowDefinitionObject(raw json.RawMessage, rules []flowDefinitionFieldRule) (map[string]json.RawMessage, error) {
	if flowDefinitionKindOf(raw) != flowDefinitionObject {
		return nil, fmt.Errorf("is not a JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	for _, rule := range rules {
		value, isPresent := object[rule.name]
		if !isPresent {
			if rule.isRequired {
				return nil, fmt.Errorf("is missing field %q", rule.name)
			}
			continue
		}
		if flowDefinitionKindOf(value) != rule.kind {
			return nil, fmt.Errorf("field %q has the wrong JSON type", rule.name)
		}
	}
	return object, nil
}

// checkFlowDefinitionObjectArray checks each element of an optional array; an absent array passes.
func checkFlowDefinitionObjectArray(raw json.RawMessage, rules []flowDefinitionFieldRule) error {
	if raw == nil {
		return nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return err
	}
	for index, element := range elements {
		if _, err := checkFlowDefinitionObject(element, rules); err != nil {
			return fmt.Errorf("element %d: %w", index, err)
		}
	}
	return nil
}

func checkFlowDefinitionStringArray(raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return err
	}
	for index, element := range elements {
		if flowDefinitionKindOf(element) != flowDefinitionString {
			return fmt.Errorf("element %d is not a string", index)
		}
	}
	return nil
}

// flowDefinitionKindOf reads a JSON value's type from its first byte; numbers and null match no rule.
func flowDefinitionKindOf(raw json.RawMessage) flowDefinitionFieldKind {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return -1
	}
	switch trimmed[0] {
	case '"':
		return flowDefinitionString
	case 't', 'f':
		return flowDefinitionBool
	case '{':
		return flowDefinitionObject
	case '[':
		return flowDefinitionArray
	default:
		return -1
	}
}

// checkV2DefinitionItemCounts bounds every list in the v2 block, which Work Queue reads for every Flow type.
func checkV2DefinitionItemCounts(definition api.V2Definition) error {
	type itemCount struct {
		list  string
		count int
	}
	counts := []itemCount{
		{list: "indexed Attributes", count: len(definition.IndexedAttributes)},
		{list: "Summary fields", count: len(definition.Summary.Fields)},
		{list: "Display fields", count: len(definition.Display.Fields)},
		{list: "Actions", count: len(definition.Actions)},
		{list: "Connector Trigger bindings", count: len(definition.ConnectorTriggerBindings)},
	}
	for _, action := range definition.Actions {
		counts = append(counts, itemCount{list: "input fields in one Action", count: len(action.Input.Fields)})
		if action.Condition != nil {
			counts = append(counts, itemCount{list: "condition values in one Action", count: len(action.Condition.Values)})
		}
	}
	for _, binding := range definition.ConnectorTriggerBindings {
		counts = append(counts, itemCount{list: "Connector UI units in one binding", count: len(binding.ConfigurationUI.Units)})
		for _, unit := range binding.ConfigurationUI.Units {
			counts = append(counts, itemCount{list: "bindings in one Connector UI unit", count: len(unit.Bindings)})
		}
	}
	for _, item := range counts {
		if item.count > maxFlowDefinitionV2Items {
			return fmt.Errorf("it has %d %s; the limit is %d", item.count, item.list, maxFlowDefinitionV2Items)
		}
	}
	return nil
}

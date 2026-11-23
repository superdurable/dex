// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"fmt"
	"strconv"
	"strings"
)

// v2DirectiveLine carries a dex: directive without the comment marker and its optional source span.
type v2DirectiveLine struct {
	Text string `json:"text"`
	Span *Span  `json:"span,omitempty"`
}

// v2AttributeFact is one Attribute or AttributeMap the Flow file declares, with its optional index settings and directive lines.
type v2AttributeFact struct {
	ResourceID string `json:"resourceId,omitempty"`
	Key        string `json:"key"`
	ValueType  string `json:"valueType"`
	IsMap      bool   `json:"isMap"`
	IsIndexed  bool   `json:"isIndexed,omitempty"`
	// IndexKey and IndexType hold the final values after the analyzer applies SDK defaults.
	IndexKey       string            `json:"indexKey,omitempty"`
	IndexType      string            `json:"indexType,omitempty"`
	DirectiveLines []v2DirectiveLine `json:"directives,omitempty"`
	Span           *Span             `json:"span,omitempty"`
}

// v2ViewRPCFact describes a view RPC. Analyzers check GetRPCs registration, signature, and read-only behavior.
type v2ViewRPCFact struct {
	ProtocolName string            `json:"protocolName"`
	Directives   []v2DirectiveLine `json:"directives"`
	Span         *Span             `json:"span,omitempty"`
	// OutputKeys is nil when the analyzer cannot determine the keys.
	OutputKeys []string `json:"outputKeys,omitzero"`
	Error      string   `json:"error,omitempty"`
}

func (analysis *scriptAnalysis) applyV2Metadata() {
	graph := &analysis.Graph
	attributes := make(map[string]v2AttributeDeclaration, len(analysis.Attributes))
	indexedAttributeFacts := make([]v2AttributeDeclaration, 0)
	for _, attribute := range analysis.Attributes {
		if _, exists := attributes[attribute.Key]; exists {
			graph.AddDiagnostic("error", "v2_indexed_attribute", fmt.Sprintf("Attribute %q is declared more than once", attribute.Key), attribute.Span)
			continue
		}
		switch {
		case attribute.IsMap:
			attribute.ValueType = "attribute-map"
		case attribute.ValueType == "list[str]", attribute.ValueType == "string[]",
			attribute.ValueType == "Array<string>", attribute.ValueType == "readonlystring[]":
			attribute.ValueType = "string-array"
		}
		declaration := v2AttributeDeclaration{v2AttributeFact: attribute}
		if attribute.IsIndexed || len(attribute.DirectiveLines) > 0 {
			declaration.parsedDirectives = graph.parseV2DirectiveLines(attribute.DirectiveLines, attribute.Span)
			indexedAttributeFacts = append(indexedAttributeFacts, declaration)
		}
		attributes[attribute.Key] = declaration
	}
	if len(indexedAttributeFacts) > 0 {
		graph.V2.IndexedAttributes = graph.buildV2IndexedAttributes(indexedAttributeFacts, "declaration")
	}
	graph.applyV2Views(analysis.ViewRPCs, attributes)
	if analysis.StartInput != nil {
		graph.V2.Start = graph.buildStartDefinition(*analysis.StartInput, "start_input_unresolved")
	}
}

func (graph *Graph) applyV2Views(facts []v2ViewRPCFact, attributes map[string]v2AttributeDeclaration) {
	seenRPCNames := make(map[string]bool, len(facts))
	for _, fact := range facts {
		if fact.Error != "" {
			graph.AddDiagnostic("error", "v2_view_rpc", fact.Error, fact.Span)
		}
		if seenRPCNames[fact.ProtocolName] {
			graph.AddDiagnostic("error", "v2_view_rpc", fmt.Sprintf("view RPC %q is declared more than once", fact.ProtocolName), fact.Span)
			continue
		}
		seenRPCNames[fact.ProtocolName] = true
		switch fact.ProtocolName {
		case "GetDexSummary":
			graph.V2.Summary = graph.buildV2View(fact, attributes, graph.V2.IndexedAttributes)
		case "GetDexDisplay":
			graph.V2.Display = graph.buildV2View(fact, attributes, graph.V2.IndexedAttributes)
		default:
			graph.AddDiagnostic("error", "v2_view_rpc", fmt.Sprintf("unexpected view RPC protocol name %q", fact.ProtocolName), fact.Span)
		}
	}
}

func (graph *Graph) parseV2DirectiveLines(lines []v2DirectiveLine, fallbackSpan *Span) []v2Directive {
	directives := make([]v2Directive, 0, len(lines))
	for _, line := range lines {
		span := line.Span
		if span == nil {
			span = fallbackSpan
		}
		text := strings.TrimSpace(line.Text)
		if !strings.HasPrefix(text, "dex:") {
			continue
		}
		directive, err := parseV2Directive(text, span)
		if err != nil {
			graph.AddDiagnostic("error", "v2_directive", err.Error(), span)
			continue
		}
		directives = append(directives, directive)
	}
	return directives
}

func (graph *Graph) buildV2IndexedAttributes(
	attributes []v2AttributeDeclaration,
	declarationLabel string,
) []IndexedAttribute {
	indexedAttributes := make([]IndexedAttribute, 0)
	for _, declaration := range attributes {
		directives := directivesNamed(declaration.parsedDirectives, "indexed-attribute")
		if declaration.IsIndexed && len(directives) != 1 {
			graph.AddDiagnostic(
				"error",
				"v2_indexed_attribute",
				fmt.Sprintf("indexed Attribute %q must declare exactly one dex:indexed-attribute directive", declaration.Key),
				declaration.Span,
			)
			continue
		}
		if !declaration.IsIndexed && len(directives) > 0 {
			graph.addV2DirectiveError(directives[0], fmt.Sprintf("Attribute %q is not indexed", declaration.Key))
			continue
		}
		if !declaration.IsIndexed {
			continue
		}
		directive := directives[0]
		allowed := []string{"attribute-key", "index-key", "index-type", "value-type", "description"}
		if !graph.validateV2Directive(directive, allowed, allowed) {
			continue
		}
		comparisons := []struct {
			argument string
			actual   string
		}{
			{argument: "attribute-key", actual: declaration.Key},
			{argument: "index-key", actual: declaration.IndexKey},
			{argument: "index-type", actual: declaration.IndexType},
			{argument: "value-type", actual: declaration.ValueType},
		}
		isValid := true
		for _, comparison := range comparisons {
			declared := directive.arguments[comparison.argument].text
			if declared == comparison.actual {
				continue
			}
			graph.addV2DirectiveError(
				directive,
				fmt.Sprintf("%s %q does not match the %s %q", comparison.argument, declared, declarationLabel, comparison.actual),
			)
			isValid = false
		}
		if !isV2IndexCompatible(declaration.IndexType, declaration.ValueType) {
			graph.addV2DirectiveError(
				directive,
				fmt.Sprintf("index-type %q does not support value-type %q", declaration.IndexType, declaration.ValueType),
			)
			isValid = false
		}
		if !isValid {
			continue
		}
		indexedAttributes = append(indexedAttributes, IndexedAttribute{
			AttributeKey: declaration.Key,
			IndexKey:     declaration.IndexKey,
			IndexType:    declaration.IndexType,
			ValueType:    declaration.ValueType,
			Description:  directive.arguments["description"].text,
		})
	}
	return indexedAttributes
}

func (graph *Graph) buildV2View(
	fact v2ViewRPCFact,
	attributes map[string]v2AttributeDeclaration,
	indexedAttributes []IndexedAttribute,
) RPCView {
	view := RPCView{RPCName: fact.ProtocolName, Fields: make([]ViewField, 0)}
	canEdit := fact.ProtocolName == "GetDexDisplay"
	indexedKeys := make(map[string]bool, len(indexedAttributes))
	for _, attribute := range indexedAttributes {
		indexedKeys[attribute.AttributeKey] = true
	}
	seen := make(map[string]bool)
	claimedUISlots := make(map[string]string)
	for _, directive := range directivesNamed(graph.parseV2DirectiveLines(fact.Directives, fact.Span), "field") {
		required := []string{"attribute-key", "value-type", "editable", "description"}
		allowed := append(append([]string{}, required...), "ui-slot")
		if !graph.validateV2Directive(directive, allowed, required) {
			continue
		}
		attributeKey := directive.arguments["attribute-key"].text
		attribute, found := attributes[attributeKey]
		if !found {
			graph.addV2DirectiveError(directive, fmt.Sprintf("field Attribute %q must be declared in this file", attributeKey))
			continue
		}
		if seen[attributeKey] {
			graph.addV2DirectiveError(directive, fmt.Sprintf("field Attribute %q is duplicated", attributeKey))
			continue
		}
		seen[attributeKey] = true
		valueType := directive.arguments["value-type"].text
		if valueType != attribute.ValueType {
			graph.addV2DirectiveError(directive, fmt.Sprintf("value-type %q does not match Attribute %q type %q", valueType, attributeKey, attribute.ValueType))
			continue
		}
		isEditable, err := strconv.ParseBool(directive.arguments["editable"].text)
		if err != nil {
			graph.addV2DirectiveError(directive, "editable must be true or false")
			continue
		}
		if isEditable && !canEdit {
			graph.addV2DirectiveError(directive, "GetDexSummary fields cannot be editable")
			continue
		}
		if isEditable && !isV2EditableType(valueType) {
			graph.addV2DirectiveError(directive, fmt.Sprintf("value-type %q is not editable", valueType))
			continue
		}
		if !canEdit && indexedKeys[attributeKey] {
			graph.addV2DirectiveError(directive, fmt.Sprintf("Summary field %q duplicates an indexed Attribute", attributeKey))
			continue
		}
		uiSlot, uiSlotOK := graph.v2FieldUISlot(directive, claimedUISlots, attributeKey)
		if !uiSlotOK {
			continue
		}
		view.Fields = append(view.Fields, ViewField{
			AttributeKey: attributeKey,
			ValueType:    valueType,
			Editable:     isEditable,
			Description:  directive.arguments["description"].text,
			UISlot:       uiSlot,
		})
	}
	if fact.OutputKeys != nil {
		graph.validateV2ViewOutputKeys(fact, view.Fields)
	}
	return view
}

func (graph *Graph) validateV2ViewOutputKeys(fact v2ViewRPCFact, fields []ViewField) {
	declaredKeys := make(map[string]bool, len(fields))
	for _, field := range fields {
		declaredKeys[field.AttributeKey] = true
	}
	outputKeys := make(map[string]bool, len(fact.OutputKeys))
	for _, key := range fact.OutputKeys {
		outputKeys[key] = true
	}
	for key := range declaredKeys {
		if !outputKeys[key] {
			graph.AddDiagnostic("error", "v2_view_rpc_output", fmt.Sprintf("%s omits declared field %q", fact.ProtocolName, key), fact.Span)
		}
	}
	for key := range outputKeys {
		if !declaredKeys[key] {
			graph.AddDiagnostic("error", "v2_view_rpc_output", fmt.Sprintf("%s returns undeclared field %q", fact.ProtocolName, key), fact.Span)
		}
	}
}

// UI slots a field may claim, mapped to whether only one field may claim each.
//
// Closed because the renderer is written against it. An unknown value would be ignored silently and
// the author would never learn the field did not land where they meant it to.
var v2FieldUISlots = map[string]bool{
	"title":          true,
	"subtitle":       true,
	"status":         true,
	"recommendation": true,
	"reason":         false,
}

// Reads and checks `ui-slot`, which is optional. Returns false when already reported.
func (graph *Graph) v2FieldUISlot(
	directive v2Directive,
	claimedUISlots map[string]string,
	attributeKey string,
) (string, bool) {
	argument, declared := directive.arguments["ui-slot"]
	if !declared {
		return "", true
	}
	uiSlot := argument.text
	unique, known := v2FieldUISlots[uiSlot]
	if !known {
		graph.addV2DirectiveError(directive, fmt.Sprintf("ui-slot %q is not a UI slot this view has", uiSlot))
		return "", false
	}
	if unique {
		if holder, taken := claimedUISlots[uiSlot]; taken {
			graph.addV2DirectiveError(directive, fmt.Sprintf("ui-slot %q is already taken by Attribute %q", uiSlot, holder))
			return "", false
		}
		claimedUISlots[uiSlot] = attributeKey
	}
	return uiSlot, true
}

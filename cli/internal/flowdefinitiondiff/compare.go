// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package flowdefinitiondiff compares two Flow Definition Graph 2.0 documents of one Flow type.
package flowdefinitiondiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"

	"github.com/superdurable/dex/cli/internal/flowviz"
)

// Severity tells how a change affects Flows that started under the old definition.
type Severity string

const (
	// SeverityBreaking marks a change that can break open Flows or existing callers.
	SeverityBreaking Severity = "breaking"
	// SeverityRisky marks a change that may break open Flows or callers and needs a review.
	SeverityRisky Severity = "risky"
	// SeverityCompatible marks an addition that open Flows and existing callers do not notice.
	SeverityCompatible Severity = "compatible"
)

// Element names the part of a Flow definition that a change touches.
const (
	ElementFlowType         = "Flow type"
	ElementDefinition       = "Definition"
	ElementStepType         = "Step type"
	ElementStartStep        = "Start Step"
	ElementStartInput       = "Start input"
	ElementRPC              = "RPC"
	ElementAttribute        = "Attribute"
	ElementAttributeMap     = "AttributeMap"
	ElementChannel          = "Channel"
	ElementChannelMap       = "ChannelMap"
	ElementStream           = "Stream"
	ElementAction           = "Action"
	ElementIndexedAttribute = "Indexed Attribute"
	ElementSummary          = "Summary"
	ElementDisplay          = "Display"
)

// Change is one difference between the old and the new definition of a Flow type.
type Change struct {
	FlowType string   `json:"flowType"`
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Element  string   `json:"element"`
	Name     string   `json:"name"`
	Message  string   `json:"message"`
}

var schemaVersionPattern = regexp.MustCompile(`^2\.[0-9]+$`)

// requiredGraphFields lists the top-level fields every published FDG 2.x document has and the diff reads.
var requiredGraphFields = map[string]byte{
	"schemaVersion": '"', "valid": 0, "source": '{', "flow": '{', "nodes": '[', "edges": '[', "diagnostics": '[',
}

// ParseGraph decodes FDG 2.x JSON content and rejects a graph that cannot support a comparison.
// It returns an error for malformed JSON; a missing, null, or wrongly typed schemaVersion, valid,
// source, flow, nodes, edges, or diagnostics; a schemaVersion that is not 2.<number>; an empty
// flow.name; or a graph whose valid field is false because the analyzer reported blocking diagnostics.
func ParseGraph(content []byte) (*flowviz.Graph, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return nil, fmt.Errorf("decode Flow Definition Graph: %w", err)
	}
	for _, name := range sortedKeys(requiredGraphFields) {
		value := bytes.TrimSpace(fields[name])
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return nil, fmt.Errorf("the required field %s is missing or null", name)
		}
		if opening := requiredGraphFields[name]; opening != 0 && value[0] != opening {
			return nil, fmt.Errorf("the required field %s has the wrong JSON type", name)
		}
	}
	var graph flowviz.Graph
	if err := json.Unmarshal(content, &graph); err != nil {
		return nil, fmt.Errorf("decode Flow Definition Graph: %w", err)
	}
	if !schemaVersionPattern.MatchString(graph.SchemaVersion) {
		return nil, fmt.Errorf("schemaVersion %q is not 2.<number>", graph.SchemaVersion)
	}
	if graph.Flow.Name == "" {
		return nil, fmt.Errorf("flow.name is empty")
	}
	if !graph.Valid {
		return nil, fmt.Errorf("the graph has blocking diagnostics, so it may be missing parts of the Flow; fix the source and generate it again")
	}
	return &graph, nil
}

// BaselineMissing reports a new Flow type that the server has no stored definition for.
func BaselineMissing(flowType string) Change {
	return Change{
		FlowType: flowType, Severity: SeverityRisky, Code: "baseline_missing",
		Element: ElementFlowType, Name: flowType,
		Message: "the server has no stored definition to compare with; a new Flow type, or one never uploaded",
	}
}

// FlowTypeAdded reports a Flow type that only the new definitions have.
func FlowTypeAdded(flowType string) Change {
	return Change{
		FlowType: flowType, Severity: SeverityCompatible, Code: "flow_type_added",
		Element: ElementFlowType, Name: flowType, Message: "new Flow type",
	}
}

// FlowTypeRemoved reports a Flow type that only the old definitions have.
func FlowTypeRemoved(flowType string) Change {
	return Change{
		FlowType: flowType, Severity: SeverityBreaking, Code: "flow_type_removed",
		Element: ElementFlowType, Name: flowType, Message: "Flow type removed; open Flows of this type can no longer run",
	}
}

// SortChanges orders changes by severity, then Flow type, element, name, and code.
func SortChanges(changes []Change) {
	rank := map[Severity]int{SeverityBreaking: 0, SeverityRisky: 1, SeverityCompatible: 2}
	sort.SliceStable(changes, func(left int, right int) bool {
		first, second := changes[left], changes[right]
		if first.Severity != second.Severity {
			return rank[first.Severity] < rank[second.Severity]
		}
		if first.FlowType != second.FlowType {
			return first.FlowType < second.FlowType
		}
		if first.Element != second.Element {
			return first.Element < second.Element
		}
		if first.Name != second.Name {
			return first.Name < second.Name
		}
		return first.Code < second.Code
	})
}

// CompareGraphs matches one Flow type's elements by registered name.
func CompareGraphs(flowType string, oldGraph *flowviz.Graph, newGraph *flowviz.Graph) []Change {
	comparison := &graphComparison{flowType: flowType}
	isSameLanguage := oldGraph.Source.Language == newGraph.Source.Language
	if !isSameLanguage {
		comparison.add(SeverityRisky, "source_language_changed", ElementDefinition, flowType,
			fmt.Sprintf("source language changed from %s to %s; Attribute, Channel, and Stream value types are not compared",
				oldGraph.Source.Language, newGraph.Source.Language))
	}
	comparison.compareStepTypes(stepPhasesByRegisteredName(oldGraph), stepPhasesByRegisteredName(newGraph))
	comparison.compareStartStep(startStepType(oldGraph), startStepType(newGraph))
	comparison.compareRPCs(oldGraph, newGraph)
	comparison.compareResources(resourcesByKey(oldGraph), resourcesByKey(newGraph), isSameLanguage)
	oldV2, newV2 := oldGraph.V2, newGraph.V2
	if oldV2 == nil || newV2 == nil {
		return comparison.changes
	}
	if oldV2.Start != nil && newV2.Start != nil {
		comparison.compareInputSchema("", oldV2.Start.Input, newV2.Start.Input)
	}
	comparison.compareActions(oldV2.Actions, newV2.Actions)
	comparison.compareIndexedAttributes(oldV2.IndexedAttributes, newV2.IndexedAttributes)
	comparison.compareViewRPC(ElementSummary, "summary_rpc_changed", oldV2.Summary.RPCName, newV2.Summary.RPCName)
	comparison.compareViewRPC(ElementDisplay, "display_rpc_changed", oldV2.Display.RPCName, newV2.Display.RPCName)
	return comparison.changes
}

type graphComparison struct {
	flowType string
	changes  []Change
}

func (comparison *graphComparison) add(severity Severity, code string, element string, name string, message string) {
	comparison.changes = append(comparison.changes, Change{
		FlowType: comparison.flowType, Severity: severity, Code: code,
		Element: element, Name: name, Message: message,
	})
}

// stepPhasesByRegisteredName maps each Step type to its phases, such as "execute" or "wait_for+execute".
// Wait conditions are not compared: their recorded arguments are source text in five languages.
func stepPhasesByRegisteredName(graph *flowviz.Graph) map[string]string {
	steps := make(map[string]string)
	for _, node := range graph.Nodes {
		if node.Kind == "step" {
			if _, exists := steps[node.Name]; !exists {
				steps[node.Name] = node.Phase
			}
		}
	}
	return steps
}

func (comparison *graphComparison) compareStepTypes(oldSteps map[string]string, newSteps map[string]string) {
	for _, name := range sortedKeys(oldSteps) {
		newPhase, exists := newSteps[name]
		if !exists {
			comparison.add(SeverityBreaking, "step_type_removed", ElementStepType, name,
				"Step type removed or renamed; open Flows at this Step can no longer run it")
			continue
		}
		if oldPhase := oldSteps[name]; oldPhase != newPhase {
			comparison.add(SeverityRisky, "step_phase_changed", ElementStepType, name,
				fmt.Sprintf("phases changed from %s to %s", oldPhase, newPhase))
		}
	}
	for _, name := range sortedKeys(newSteps) {
		if _, exists := oldSteps[name]; !exists {
			comparison.add(SeverityCompatible, "step_type_added", ElementStepType, name, "new Step type")
		}
	}
}

func startStepType(graph *flowviz.Graph) string {
	if graph.V2 != nil && graph.V2.Start != nil && graph.V2.Start.StepType != "" {
		return graph.V2.Start.StepType
	}
	for _, node := range graph.Nodes {
		if node.Kind == "step" && (node.Start || (graph.Flow.StartStepID != "" && node.ID == graph.Flow.StartStepID)) {
			return node.Name
		}
	}
	return ""
}

func (comparison *graphComparison) compareStartStep(oldStart string, newStart string) {
	if oldStart == "" || newStart == "" || oldStart == newStart {
		return
	}
	comparison.add(SeverityRisky, "start_step_changed", ElementStartStep, newStart,
		fmt.Sprintf("start Step changed from %s to %s; new Flows start there, open Flows keep their Steps", oldStart, newStart))
}

func (comparison *graphComparison) compareRPCs(oldGraph *flowviz.Graph, newGraph *flowviz.Graph) {
	oldRPCs, newRPCs := namesOfKind(oldGraph, "rpc"), namesOfKind(newGraph, "rpc")
	for _, name := range sortedKeys(oldRPCs) {
		if !newRPCs[name] {
			comparison.add(SeverityBreaking, "rpc_removed", ElementRPC, name,
				"RPC removed or renamed; callers and open Flows can no longer call it")
		}
	}
	for _, name := range sortedKeys(newRPCs) {
		if !oldRPCs[name] {
			comparison.add(SeverityCompatible, "rpc_added", ElementRPC, name, "new RPC")
		}
	}
}

func namesOfKind(graph *flowviz.Graph, kind string) map[string]bool {
	names := make(map[string]bool)
	for _, node := range graph.Nodes {
		if node.Kind == kind {
			names[node.Name] = true
		}
	}
	return names
}

type resourceKey struct {
	kind string
	name string
}

func resourcesByKey(graph *flowviz.Graph) map[resourceKey]flowviz.ResourceDetails {
	resources := make(map[resourceKey]flowviz.ResourceDetails)
	for _, node := range graph.Nodes {
		if node.Kind != "attribute" && node.Kind != "channel" && node.Kind != "stream" {
			continue
		}
		details := flowviz.ResourceDetails{}
		if node.Resource != nil {
			details = *node.Resource
		}
		resources[resourceKey{kind: node.Kind, name: node.Name}] = details
	}
	return resources
}

func resourceElement(kind string, details flowviz.ResourceDetails) string {
	switch {
	case kind == "attribute" && details.Map:
		return ElementAttributeMap
	case kind == "attribute":
		return ElementAttribute
	case kind == "channel" && details.Map:
		return ElementChannelMap
	case kind == "channel":
		return ElementChannel
	default:
		return ElementStream
	}
}

func (comparison *graphComparison) compareResources(
	oldResources map[resourceKey]flowviz.ResourceDetails,
	newResources map[resourceKey]flowviz.ResourceDetails,
	isSameLanguage bool,
) {
	for _, key := range sortedResourceKeys(oldResources) {
		oldDetails := oldResources[key]
		oldElement := resourceElement(key.kind, oldDetails)
		newDetails, exists := newResources[key]
		if !exists {
			comparison.add(SeverityBreaking, "resource_removed", oldElement, key.name,
				fmt.Sprintf("%s removed or renamed; values that open Flows stored or will receive are no longer used", oldElement))
			continue
		}
		newElement := resourceElement(key.kind, newDetails)
		if oldElement != newElement {
			comparison.add(SeverityBreaking, "resource_kind_changed", newElement, key.name,
				fmt.Sprintf("changed from %s to %s", oldElement, newElement))
			continue
		}
		// valueType is the type as spelled in source, so an import alias change also differs.
		if isSameLanguage && oldDetails.ValueType != newDetails.ValueType {
			comparison.add(SeverityRisky, "resource_type_changed", newElement, key.name,
				fmt.Sprintf("value type spelled %s is now %s; check that stored and sent values still decode", oldDetails.ValueType, newDetails.ValueType))
		}
	}
	for _, key := range sortedResourceKeys(newResources) {
		if _, exists := oldResources[key]; !exists {
			element := resourceElement(key.kind, newResources[key])
			comparison.add(SeverityCompatible, "resource_added", element, key.name, "new "+element)
		}
	}
}

func sortedResourceKeys(resources map[resourceKey]flowviz.ResourceDetails) []resourceKey {
	keys := make([]resourceKey, 0, len(resources))
	for key := range resources {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left int, right int) bool {
		if keys[left].kind != keys[right].kind {
			return keys[left].kind < keys[right].kind
		}
		return keys[left].name < keys[right].name
	})
	return keys
}

// compareInputSchema compares the start input schema at a field path; "" is the whole input.
func (comparison *graphComparison) compareInputSchema(path string, oldSchema flowviz.StartInputSchema, newSchema flowviz.StartInputSchema) {
	name := path
	if name == "" {
		name = "(input)"
	}
	if oldSchema.Kind != newSchema.Kind {
		comparison.add(SeverityBreaking, "start_input_kind_changed", ElementStartInput, name,
			fmt.Sprintf("kind changed from %s to %s", oldSchema.Kind, newSchema.Kind))
		return
	}
	if oldSchema.Nullable && !newSchema.Nullable {
		comparison.add(SeverityRisky, "start_input_null_rejected", ElementStartInput, name,
			"no longer accepts null; Flow starters, including the Dex Web start form, must send a value")
	}
	if oldSchema.Format != newSchema.Format {
		comparison.add(SeverityRisky, "start_input_format_changed", ElementStartInput, name,
			fmt.Sprintf("format changed from %q to %q; Flow starters may send values in the old format", oldSchema.Format, newSchema.Format))
	}
	comparison.compareEnumValues(name, oldSchema.EnumValues, newSchema.EnumValues)
	if isBoundNarrowed(oldSchema.Minimum, newSchema.Minimum, 1) || isBoundNarrowed(oldSchema.Maximum, newSchema.Maximum, -1) {
		comparison.add(SeverityRisky, "start_input_range_narrowed", ElementStartInput, name,
			fmt.Sprintf("range narrowed from [%s, %s] to [%s, %s]; Flow starters may send values outside it",
				oldSchema.Minimum, oldSchema.Maximum, newSchema.Minimum, newSchema.Maximum))
	}
	if !equalFixedLength(oldSchema.FixedLength, newSchema.FixedLength) {
		comparison.add(SeverityRisky, "start_input_fixed_length_changed", ElementStartInput, name,
			"fixed array length changed; Flow starters may send the old length")
	}
	if oldSchema.Items != nil && newSchema.Items != nil {
		comparison.compareInputSchema(path+"[]", *oldSchema.Items, *newSchema.Items)
	}
	if oldSchema.Values != nil && newSchema.Values != nil {
		comparison.compareInputSchema(path+"{}", *oldSchema.Values, *newSchema.Values)
	}
	comparison.compareInputFields(path, oldSchema.Fields, newSchema.Fields)
}

func (comparison *graphComparison) compareInputFields(path string, oldFields []flowviz.StartInputField, newFields []flowviz.StartInputField) {
	newByName := make(map[string]flowviz.StartInputField, len(newFields))
	for _, field := range newFields {
		newByName[field.Name] = field
	}
	oldByName := make(map[string]bool, len(oldFields))
	for _, oldField := range oldFields {
		oldByName[oldField.Name] = true
		fieldPath := joinFieldPath(path, oldField.Name)
		newField, exists := newByName[oldField.Name]
		if !exists {
			comparison.add(SeverityRisky, "start_input_field_removed", ElementStartInput, fieldPath,
				"field removed; Flow starters that still send it may lose the value")
			continue
		}
		if !oldField.Required && newField.Required {
			comparison.add(SeverityRisky, "start_input_field_now_required", ElementStartInput, fieldPath,
				"field became required; Flow starters, including the Dex Web start form, that omit it may fail")
		}
		comparison.compareInputSchema(fieldPath, oldField.Schema, newField.Schema)
	}
	for _, newField := range newFields {
		if oldByName[newField.Name] {
			continue
		}
		fieldPath := joinFieldPath(path, newField.Name)
		if newField.Required {
			comparison.add(SeverityRisky, "start_input_required_field_added", ElementStartInput, fieldPath,
				"new required field; Flow starters, including the Dex Web start form, that omit it may fail")
		} else {
			comparison.add(SeverityCompatible, "start_input_field_added", ElementStartInput, fieldPath, "new optional field")
		}
	}
}

func joinFieldPath(path string, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (comparison *graphComparison) compareEnumValues(name string, oldValues []flowviz.StartInputEnumValue, newValues []flowviz.StartInputEnumValue) {
	if len(oldValues) == 0 && len(newValues) == 0 {
		return
	}
	if len(oldValues) == 0 {
		comparison.add(SeverityRisky, "start_input_enum_restricted", ElementStartInput, name,
			"now accepts only listed enum values; Flow starters may send other values")
		return
	}
	if len(newValues) == 0 {
		return
	}
	oldSet, newSet := enumValueSet(oldValues), enumValueSet(newValues)
	for _, value := range sortedKeys(oldSet) {
		if !newSet[value] {
			comparison.add(SeverityRisky, "start_input_enum_value_removed", ElementStartInput, name,
				fmt.Sprintf("enum value %s removed; Flow starters may still send it", value))
		}
	}
	for _, value := range sortedKeys(newSet) {
		if !oldSet[value] {
			comparison.add(SeverityCompatible, "start_input_enum_value_added", ElementStartInput, name,
				fmt.Sprintf("enum value %s added", value))
		}
	}
}

func enumValueSet(values []flowviz.StartInputEnumValue) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[fmt.Sprint(value.Value)] = true
	}
	return set
}

// isBoundNarrowed reports whether newBound excludes values oldBound allowed; direction 1 is a minimum, -1 a maximum.
func isBoundNarrowed(oldBound string, newBound string, direction int) bool {
	if newBound == "" {
		return false
	}
	newValue, isNewValid := new(big.Int).SetString(newBound, 10)
	if !isNewValid {
		return false
	}
	if oldBound == "" {
		return true
	}
	oldValue, isOldValid := new(big.Int).SetString(oldBound, 10)
	if !isOldValid {
		return false
	}
	return newValue.Cmp(oldValue) == direction
}

func equalFixedLength(oldLength *int64, newLength *int64) bool {
	if oldLength == nil || newLength == nil {
		return oldLength == nil && newLength == nil
	}
	return *oldLength == *newLength
}

func (comparison *graphComparison) compareActions(oldActions []flowviz.Action, newActions []flowviz.Action) {
	newByRPC := make(map[string]flowviz.Action, len(newActions))
	for _, action := range newActions {
		newByRPC[action.RPCName] = action
	}
	oldByRPC := make(map[string]bool, len(oldActions))
	for _, oldAction := range oldActions {
		oldByRPC[oldAction.RPCName] = true
		newAction, exists := newByRPC[oldAction.RPCName]
		if !exists {
			comparison.add(SeverityRisky, "action_removed", ElementAction, oldAction.RPCName,
				"Dex Web no longer offers this Action")
			continue
		}
		if oldAction.RequiredPermission != newAction.RequiredPermission {
			comparison.add(SeverityRisky, "action_permission_changed", ElementAction, oldAction.RPCName,
				fmt.Sprintf("required permission changed from %s to %s", oldAction.RequiredPermission, newAction.RequiredPermission))
		}
		if oldAction.Input.Kind != newAction.Input.Kind {
			comparison.add(SeverityBreaking, "action_input_kind_changed", ElementAction, oldAction.RPCName,
				fmt.Sprintf("RPC input kind changed from %s to %s", oldAction.Input.Kind, newAction.Input.Kind))
			continue
		}
		comparison.compareActionFields(oldAction.RPCName, oldAction.Input.Fields, newAction.Input.Fields)
	}
	for _, action := range newActions {
		if !oldByRPC[action.RPCName] {
			comparison.add(SeverityCompatible, "action_added", ElementAction, action.RPCName, "new Action")
		}
	}
}

func (comparison *graphComparison) compareActionFields(rpcName string, oldFields []flowviz.ActionInputField, newFields []flowviz.ActionInputField) {
	newByName := make(map[string]flowviz.ActionInputField, len(newFields))
	for _, field := range newFields {
		newByName[field.FieldName] = field
	}
	oldByName := make(map[string]bool, len(oldFields))
	for _, oldField := range oldFields {
		oldByName[oldField.FieldName] = true
		name := rpcName + "." + oldField.FieldName
		newField, exists := newByName[oldField.FieldName]
		if !exists {
			comparison.add(SeverityRisky, "action_input_field_removed", ElementAction, name,
				"RPC input field removed; callers that still send it may lose the value")
			continue
		}
		if oldField.ValueType != newField.ValueType {
			comparison.add(SeverityBreaking, "action_input_field_type_changed", ElementAction, name,
				fmt.Sprintf("RPC input field type changed from %s to %s", oldField.ValueType, newField.ValueType))
		}
		if !oldField.Required && newField.Required {
			comparison.add(SeverityRisky, "action_input_field_now_required", ElementAction, name,
				"RPC input field became required; RPC callers, including Dex Web, that omit it may fail")
		}
		if oldField.Source != newField.Source || oldField.AttributeKey != newField.AttributeKey {
			comparison.add(SeverityRisky, "action_input_field_source_changed", ElementAction, name,
				fmt.Sprintf("Dex Web fills it from %s instead of %s", describeFieldSource(newField), describeFieldSource(oldField)))
		}
	}
	for _, newField := range newFields {
		if oldByName[newField.FieldName] {
			continue
		}
		name := rpcName + "." + newField.FieldName
		if newField.Required {
			comparison.add(SeverityRisky, "action_input_required_field_added", ElementAction, name,
				"new required RPC input field; RPC callers, including Dex Web, that omit it may fail")
		} else {
			comparison.add(SeverityCompatible, "action_input_field_added", ElementAction, name, "new optional RPC input field")
		}
	}
}

func describeFieldSource(field flowviz.ActionInputField) string {
	if field.Source == "attribute" {
		return "Attribute " + field.AttributeKey
	}
	return field.Source
}

func (comparison *graphComparison) compareIndexedAttributes(oldAttributes []flowviz.IndexedAttribute, newAttributes []flowviz.IndexedAttribute) {
	newByKey := make(map[string]flowviz.IndexedAttribute, len(newAttributes))
	for _, attribute := range newAttributes {
		newByKey[attribute.AttributeKey] = attribute
	}
	oldByKey := make(map[string]bool, len(oldAttributes))
	for _, oldAttribute := range oldAttributes {
		oldByKey[oldAttribute.AttributeKey] = true
		newAttribute, exists := newByKey[oldAttribute.AttributeKey]
		if !exists {
			comparison.add(SeverityRisky, "indexed_attribute_removed", ElementIndexedAttribute, oldAttribute.AttributeKey,
				"no longer indexed; searches on it stop finding new Flows")
			continue
		}
		if oldAttribute.IndexKey != newAttribute.IndexKey ||
			oldAttribute.IndexType != newAttribute.IndexType ||
			oldAttribute.ValueType != newAttribute.ValueType {
			comparison.add(SeverityRisky, "indexed_attribute_changed", ElementIndexedAttribute, oldAttribute.AttributeKey,
				fmt.Sprintf("index changed from %s %s %s to %s %s %s; existing searches may stop matching",
					oldAttribute.IndexKey, oldAttribute.IndexType, oldAttribute.ValueType,
					newAttribute.IndexKey, newAttribute.IndexType, newAttribute.ValueType))
		}
	}
	for _, attribute := range newAttributes {
		if !oldByKey[attribute.AttributeKey] {
			comparison.add(SeverityCompatible, "indexed_attribute_added", ElementIndexedAttribute, attribute.AttributeKey, "newly indexed")
		}
	}
}

func (comparison *graphComparison) compareViewRPC(element string, code string, oldRPCName string, newRPCName string) {
	if oldRPCName == "" || oldRPCName == newRPCName {
		return
	}
	if newRPCName == "" {
		comparison.add(SeverityRisky, code, element, oldRPCName,
			fmt.Sprintf("Dex Web no longer reads it from RPC %s", oldRPCName))
		return
	}
	comparison.add(SeverityRisky, code, element, newRPCName,
		fmt.Sprintf("Dex Web now reads it from RPC %s instead of %s", newRPCName, oldRPCName))
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

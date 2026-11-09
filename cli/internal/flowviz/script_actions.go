// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// scriptAttribute is one Attribute or AttributeMap that a Python or TypeScript Flow file declares.
type scriptAttribute struct {
	Key       string `json:"key"`
	ValueType string `json:"valueType"`
	IsMap     bool   `json:"isMap"`
}

// scriptAction is one Action an RPC declares, as a Python or TypeScript analyzer read it.
type scriptAction struct {
	RPCNodeID          string                 `json:"rpcNodeId"`
	RPCName            string                 `json:"rpcName"`
	Span               *Span                  `json:"span"`
	Error              string                 `json:"error"`
	Label              string                 `json:"label"`
	RequiredPermission string                 `json:"requiredPermission"`
	Condition          *scriptActionCondition `json:"condition"`
	Directives         []string               `json:"directives"`
	Input              scriptActionInput      `json:"input"`
}

type scriptActionCondition struct {
	AttributeKey string            `json:"attributeKey"`
	Values       []json.RawMessage `json:"values"`
	Span         *Span             `json:"span"`
}

type scriptActionInput struct {
	Kind     string                   `json:"kind"`
	TypeName string                   `json:"typeName"`
	Error    string                   `json:"error"`
	Fields   []scriptActionInputField `json:"fields"`
}

type scriptActionInputField struct {
	FieldName string `json:"fieldName"`
	ValueType string `json:"valueType"`
	Required  bool   `json:"required"`
}

type scriptRPCLockError struct {
	RPCNodeID string `json:"rpcNodeId"`
	RPCName   string `json:"rpcName"`
	Message   string `json:"message"`
	Span      *Span  `json:"span"`
}

// applyActions validates script-read Actions with the rules the Go analyzer applies to RPCOptions.Action.
func (analysis *scriptAnalysis) applyActions() {
	if analysis.Graph.V2 == nil {
		analysis.Graph.V2 = newEmptyV2Definition()
	}
	attributes := make(map[string]scriptAttribute, len(analysis.Attributes))
	for _, attribute := range analysis.Attributes {
		attributes[attribute.Key] = attribute
	}
	seenRPCNames := make(map[string]bool, len(analysis.Actions))
	for _, declared := range analysis.Actions {
		if seenRPCNames[declared.RPCName] {
			analysis.Graph.AddDiagnostic("error", "v2_action", fmt.Sprintf("RPC %s declares more than one Action", declared.RPCName), declared.Span)
			continue
		}
		seenRPCNames[declared.RPCName] = true
		action, isValid := analysis.actionFromScript(declared, attributes)
		if !isValid {
			continue
		}
		input, isInputValid := analysis.actionInputFromScript(declared, attributes)
		if !isInputValid {
			continue
		}
		action.Input = input
		analysis.Graph.V2.Actions = append(analysis.Graph.V2.Actions, action)
	}
	for _, lockError := range analysis.RPCLockErrors {
		if seenRPCNames[lockError.RPCName] {
			analysis.Graph.AddDiagnostic("error", "rpc_lock_unresolved", lockError.Message, lockError.Span)
		}
	}
}

func (analysis *scriptAnalysis) actionFromScript(declared scriptAction, attributes map[string]scriptAttribute) (Action, bool) {
	if declared.Error != "" {
		analysis.Graph.AddDiagnostic("error", "v2_action", declared.Error, declared.Span)
		return Action{}, false
	}
	if strings.TrimSpace(declared.Label) == "" {
		analysis.Graph.AddDiagnostic("error", "v2_action", fmt.Sprintf("RPC %s Action label must be a non-empty static string", declared.RPCName), declared.Span)
		return Action{}, false
	}
	if !actionPermissionPattern.MatchString(declared.RequiredPermission) {
		analysis.Graph.AddDiagnostic("error", "v2_action", fmt.Sprintf("RPC %s Action requires exactly one valid permission", declared.RPCName), declared.Span)
		return Action{}, false
	}
	action := Action{RPCName: declared.RPCName, Label: declared.Label, RequiredPermission: declared.RequiredPermission}
	if declared.Condition == nil {
		return action, true
	}
	condition, isValid := analysis.actionConditionFromScript(declared, attributes)
	if !isValid {
		return Action{}, false
	}
	action.Condition = condition
	return action, true
}

func (analysis *scriptAnalysis) actionConditionFromScript(declared scriptAction, attributes map[string]scriptAttribute) (*ActionCondition, bool) {
	span := declared.Condition.Span
	if span == nil {
		span = declared.Span
	}
	attribute, isDeclared := attributes[declared.Condition.AttributeKey]
	if !isDeclared || attribute.IsMap {
		analysis.Graph.AddDiagnostic("error", "v2_action", "Action condition must reference a scalar Attribute declared in this file", span)
		return nil, false
	}
	if len(declared.Condition.Values) == 0 {
		analysis.Graph.AddDiagnostic("error", "v2_action", fmt.Sprintf("RPC %s Action condition requires at least one match", declared.RPCName), span)
		return nil, false
	}
	values := make([]any, 0, len(declared.Condition.Values))
	for _, raw := range declared.Condition.Values {
		value, isValid := scriptActionConditionValue(raw, attribute.ValueType)
		if !isValid {
			analysis.Graph.AddDiagnostic("error", "v2_action", fmt.Sprintf("Action condition value must be a matching compile-time %s", attribute.ValueType), span)
			return nil, false
		}
		values = append(values, value)
	}
	return &ActionCondition{AttributeKey: attribute.Key, Operator: "in", Values: values}, true
}

// scriptActionConditionValue decodes one condition value into the Go type the Go analyzer emits for it.
func scriptActionConditionValue(raw json.RawMessage, valueType string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !v2JSONValueMatchesType(value, valueType) {
		return nil, false
	}
	number, isNumber := value.(json.Number)
	if !isNumber {
		return value, true
	}
	if valueType == "int64" {
		integer, err := number.Int64()
		return integer, err == nil
	}
	double, err := number.Float64()
	return double, err == nil
}

func (analysis *scriptAnalysis) actionInputFromScript(declared scriptAction, attributes map[string]scriptAttribute) (ActionInput, bool) {
	directives := make([]v2Directive, 0, len(declared.Directives))
	for _, text := range declared.Directives {
		directive, err := parseV2Directive(text, nil)
		if err != nil {
			analysis.Graph.AddDiagnostic("error", "v2_directive", err.Error(), declared.Span)
			return ActionInput{}, false
		}
		switch directive.name {
		case "input":
			directives = append(directives, directive)
		case "action", "when":
			analysis.Graph.AddDiagnostic("error", "v2_action", "dex:action and dex:when are replaced by the RPC's Action declaration", declared.Span)
			return ActionInput{}, false
		}
	}
	if declared.Input.Error != "" {
		analysis.Graph.AddDiagnostic("error", "v2_action_input", fmt.Sprintf("Action RPC %s %s", declared.RPCName, declared.Input.Error), declared.Span)
		return ActionInput{}, false
	}
	if len(directives) == 0 {
		if declared.Input.Kind != "none" {
			analysis.Graph.AddDiagnostic("error", "v2_action_input", fmt.Sprintf("Action RPC %s takes input %s; declare one dex:input directive for each of its fields", declared.RPCName, declared.Input.TypeName), declared.Span)
			return ActionInput{}, false
		}
		return ActionInput{Kind: "none"}, true
	}
	if declared.Input.Kind != "object" {
		analysis.Graph.AddDiagnostic("error", "v2_action_input", fmt.Sprintf("Action RPC %s takes no input, so it cannot declare dex:input", declared.RPCName), declared.Span)
		return ActionInput{}, false
	}
	return analysis.actionInputFields(declared, directives, attributes)
}

// actionInputFields applies the Go analyzer's dex:input rules to a script RPC's input type fields.
func (analysis *scriptAnalysis) actionInputFields(declared scriptAction, directives []v2Directive, attributes map[string]scriptAttribute) (ActionInput, bool) {
	fieldsByName := make(map[string]scriptActionInputField, len(declared.Input.Fields))
	for _, field := range declared.Input.Fields {
		fieldsByName[field.FieldName] = field
	}
	input := ActionInput{Kind: "object", Fields: make([]ActionInputField, 0, len(directives))}
	seen := make(map[string]bool)
	isValid := true
	reportError := func(directive v2Directive, message string) {
		analysis.Graph.AddDiagnostic("error", "v2_directive", fmt.Sprintf("dex:%s %s", directive.name, message), declared.Span)
		isValid = false
	}
	for _, directive := range directives {
		if !analysis.validateScriptActionInputDirective(directive, reportError) {
			continue
		}
		fieldName := directive.arguments["field-name"].text
		field, isField := fieldsByName[fieldName]
		if !isField {
			reportError(directive, fmt.Sprintf("input field %q is not in the RPC input type %s", fieldName, declared.Input.TypeName))
			continue
		}
		if seen[fieldName] {
			reportError(directive, fmt.Sprintf("input field %q is duplicated", fieldName))
			continue
		}
		seen[fieldName] = true
		valueType := directive.arguments["value-type"].text
		if valueType != field.ValueType || !isV2EditableType(valueType) {
			reportError(directive, fmt.Sprintf("input field %q type %q does not match source type %q", fieldName, valueType, field.ValueType))
			continue
		}
		isRequired, err := strconv.ParseBool(directive.arguments["required"].text)
		if err != nil || isRequired != field.Required {
			reportError(directive, fmt.Sprintf("input field %q required must match whether the field is optional in source", fieldName))
			continue
		}
		source := directive.arguments["source"].text
		capture := ""
		if captureArgument, exists := directive.arguments["capture"]; exists {
			capture = captureArgument.text
			if capture != "qr-code" {
				reportError(directive, "capture must be qr-code")
				continue
			}
			if source != "user" || valueType != "string" {
				reportError(directive, "capture requires a source:user string input field")
				continue
			}
		}
		attributeKey := ""
		switch source {
		case "user":
			if _, exists := directive.arguments["attribute-key"]; exists {
				reportError(directive, "source:user forbids attribute-key")
				continue
			}
		case "attribute":
			argument, exists := directive.arguments["attribute-key"]
			if !exists {
				reportError(directive, "source:attribute requires attribute-key")
				continue
			}
			attributeKey = argument.text
			attribute, isDeclared := attributes[attributeKey]
			if !isDeclared || attribute.IsMap || attribute.ValueType != valueType {
				reportError(directive, fmt.Sprintf("source Attribute %q must be a matching scalar Attribute", attributeKey))
				continue
			}
		default:
			reportError(directive, "source must be user or attribute")
			continue
		}
		input.Fields = append(input.Fields, ActionInputField{
			FieldName:    fieldName,
			ValueType:    valueType,
			Source:       source,
			AttributeKey: attributeKey,
			Capture:      capture,
			Required:     isRequired,
			Description:  directive.arguments["description"].text,
		})
	}
	if len(seen) != len(declared.Input.Fields) {
		analysis.Graph.AddDiagnostic("error", "v2_action_input", fmt.Sprintf("every field of Action RPC %s input %s must have one dex:input directive", declared.RPCName, declared.Input.TypeName), declared.Span)
		return input, false
	}
	return input, isValid
}

func (analysis *scriptAnalysis) validateScriptActionInputDirective(directive v2Directive, reportError func(v2Directive, string)) bool {
	allowed := map[string]bool{"field-name": true, "value-type": true, "source": true, "attribute-key": true, "capture": true, "required": true, "description": true}
	isValid := true
	for name, argument := range directive.arguments {
		if !allowed[name] {
			reportError(directive, fmt.Sprintf("unknown argument %s", name))
			isValid = false
		} else if argument.array {
			reportError(directive, fmt.Sprintf("argument %s must not be a JSON array", name))
			isValid = false
		}
	}
	for _, name := range []string{"field-name", "value-type", "source", "required", "description"} {
		if _, found := directive.arguments[name]; !found {
			reportError(directive, fmt.Sprintf("missing required argument %s", name))
			isValid = false
		}
	}
	return isValid
}

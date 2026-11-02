// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package stepwise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// ToolCallContext gives a function tool the identity of its call and the session's live writers.
type ToolCallContext struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// TurnID is "turn-<n>" of the running turn.
	TurnID string
	// ToolCallID is the tool call this run answers.
	ToolCallID string
	// IdempotencyKey is "<flowID>/<turnID>/<toolCallID>", the same on every attempt. Pass it to
	// the external system when it supports idempotency.
	IdempotencyKey string
	// Attempt is the one-based Step attempt.
	Attempt int64
	// Events writes live events, bound to the tool call Step attempt. It is safe from any
	// goroutine.
	Events *dexagents.AgentEventWriter
	// Heartbeat records Step liveness. The session already heartbeats in the background.
	Heartbeat func() error
}

// FunctionToolExecute runs one call of a function tool with its arguments as validated JSON text.
//
// It returns the output the model sees. A *dexagents.ToolExecutionError, or
// an error wrapping one, records failed with its message; any other error
// fails the Step attempt.
type FunctionToolExecute func(ctx context.Context, arguments json.RawMessage, call ToolCallContext) (string, error)

// FunctionToolOption configures one FunctionTool.
type FunctionToolOption interface {
	applyFunctionTool(tool *FunctionTool)
}

type functionToolOptionFunc func(tool *FunctionTool)

func (option functionToolOptionFunc) applyFunctionTool(tool *FunctionTool) {
	option(tool)
}

// WithRequiresApproval sets whether each call of the tool waits for resolve_interrupt.
//
// Without it the effect decides: no approval for read and idempotent_write,
// approval for non_idempotent_write and unclassified.
func WithRequiresApproval(isApprovalRequired bool) FunctionToolOption {
	return functionToolOptionFunc(func(tool *FunctionTool) {
		tool.requiresApproval = &isApprovalRequired
	})
}

// FunctionTool runs one Go function as a Stepwise tool in its own Step.
//
// Create it with NewFunctionTool or NewTypedFunctionTool. The effect picks the
// tool call Step and the default approval, exactly as dexagents.ToolPolicy
// does for adapter tools. Before each call, the arguments are checked against
// a subset of JSON Schema that the Python core checks with the same messages:
// type, enum, anyOf, properties, required, additionalProperties, items,
// minimum, and maximum. A violation records failed with "invalid arguments for
// <name>: <problem>", which the model sees.
type FunctionTool struct {
	name             string
	description      string
	parameters       json.RawMessage
	parsedParameters any
	effect           dexagents.ToolEffect
	requiresApproval *bool
	execute          FunctionToolExecute
}

// NewFunctionTool creates a function tool from a JSON Schema and a function of the JSON arguments.
//
//	lookupOrder, err := stepwise.NewFunctionTool(
//		"lookup_order",
//		"Look up an order by its ID.",
//		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
//		dexagents.ToolEffectRead,
//		func(ctx context.Context, arguments json.RawMessage, call stepwise.ToolCallContext) (string, error) {
//			var input struct {
//				OrderID string `json:"order_id"`
//			}
//			if err := json.Unmarshal(arguments, &input); err != nil {
//				return "", err
//			}
//			return orders.Describe(ctx, input.OrderID)
//		},
//	)
//
// name must not be blank. parameters must be a JSON object. It returns an error
// for a blank name, invalid parameters, an unknown effect, or a nil execute.
func NewFunctionTool(
	name string,
	description string,
	parameters json.RawMessage,
	effect dexagents.ToolEffect,
	execute FunctionToolExecute,
	options ...FunctionToolOption,
) (*FunctionTool, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("function tool name must not be blank")
	}
	if !effect.IsValid() {
		return nil, fmt.Errorf("function tool %s has effect %q, which is not a tool effect", name, effect)
	}
	if execute == nil {
		return nil, fmt.Errorf("function tool %s needs an execute function", name)
	}
	parsed, err := pythonjson.ParseOrdered(parameters)
	if err != nil {
		return nil, fmt.Errorf("function tool %s parameters: %w", name, err)
	}
	if _, isObject := parsed.(pythonjson.Object); !isObject {
		return nil, fmt.Errorf("function tool %s parameters must be a JSON object", name)
	}
	tool := &FunctionTool{
		name: name, description: description, parameters: parameters, parsedParameters: parsed,
		effect: effect, execute: execute,
	}
	for _, option := range options {
		option.applyFunctionTool(tool)
	}
	return tool, nil
}

// NewTypedFunctionTool creates a function tool whose arguments decode into Arguments.
//
// A string Result is the output; any other Result is encoded as JSON with
// Python's default separators, as the Python core encodes a non-string
// result. Decoding failures record failed, so the model sees them.
//
//	type RefundArguments struct {
//		OrderID     string `json:"order_id"`
//		AmountCents int64  `json:"amount_cents"`
//	}
//	refundOrder, err := stepwise.NewTypedFunctionTool("refund_order", "Refund part of an order.",
//		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"},"amount_cents":{"type":"integer","minimum":1}},"required":["order_id","amount_cents"]}`),
//		dexagents.ToolEffectNonIdempotentWrite,
//		func(ctx context.Context, arguments RefundArguments, call stepwise.ToolCallContext) (string, error) {
//			return payments.Refund(ctx, arguments.OrderID, arguments.AmountCents, call.IdempotencyKey)
//		})
//
// It returns the errors of NewFunctionTool.
func NewTypedFunctionTool[Arguments any, Result any](
	name string,
	description string,
	parameters json.RawMessage,
	effect dexagents.ToolEffect,
	execute func(ctx context.Context, arguments Arguments, call ToolCallContext) (Result, error),
	options ...FunctionToolOption,
) (*FunctionTool, error) {
	if execute == nil {
		return nil, fmt.Errorf("function tool %s needs an execute function", name)
	}
	return NewFunctionTool(name, description, parameters, effect,
		func(ctx context.Context, rawArguments json.RawMessage, call ToolCallContext) (string, error) {
			var arguments Arguments
			if err := json.Unmarshal(rawArguments, &arguments); err != nil {
				return "", &dexagents.ToolExecutionError{Message: fmt.Sprintf("invalid arguments for %s: %v", name, err)}
			}
			result, err := execute(ctx, arguments, call)
			if err != nil {
				return "", err
			}
			if text, isText := any(result).(string); isText {
				return text, nil
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", fmt.Errorf("encode %s result: %w", name, err)
			}
			return pythonjson.Marshal(json.RawMessage(encoded), pythonjson.Options{})
		}, options...)
}

// Name returns the tool name the model calls.
func (tool *FunctionTool) Name() string {
	return tool.name
}

// Effect returns the tool's own effect.
func (tool *FunctionTool) Effect() dexagents.ToolEffect {
	return tool.effect
}

// Policy returns the tool's own policy, with the effect's default approval resolved.
func (tool *FunctionTool) Policy() dexagents.ToolPolicy {
	isApprovalRequired := dexagents.ToolPolicy{Effect: tool.effect, RequiresApproval: tool.requiresApproval}.IsApprovalRequired()
	return dexagents.ToolPolicy{Effect: tool.effect, RequiresApproval: &isApprovalRequired}
}

// Definition returns the definition the runner offers to the model.
func (tool *FunctionTool) Definition() ModelToolDefinition {
	return ModelToolDefinition{Name: tool.name, Description: tool.description, Parameters: tool.parameters}
}

// Invoke checks the arguments against the schema and calls the function once.
//
// It returns the output text, a *dexagents.ToolExecutionError when the
// arguments break the schema, or what the function returns.
func (tool *FunctionTool) Invoke(ctx context.Context, arguments string, call ToolCallContext) (string, error) {
	if arguments == "" {
		arguments = "{}"
	}
	values, err := pythonjson.ParseOrdered([]byte(arguments))
	if err != nil {
		// A fixed text: parser messages differ between languages and versions.
		return "", tool.invalidArguments("arguments are not valid JSON")
	}
	if _, isObject := values.(pythonjson.Object); !isObject {
		return "", tool.invalidArguments("expected a JSON object")
	}
	if problem := schemaViolation(values, tool.parsedParameters, "arguments"); problem != "" {
		return "", tool.invalidArguments(problem)
	}
	return tool.execute(ctx, json.RawMessage(arguments), call)
}

func (tool *FunctionTool) invalidArguments(problem string) error {
	return &dexagents.ToolExecutionError{Message: fmt.Sprintf("invalid arguments for %s: %s", tool.name, problem)}
}

// schemaViolation returns the first way value breaks the JSON Schema subset, or "".
func schemaViolation(value any, schemaValue any, path string) string {
	schema, isObject := schemaValue.(pythonjson.Object)
	if !isObject {
		return ""
	}
	if options, hasAnyOf := memberValue(schema, "anyOf"); hasAnyOf {
		if optionList, isList := options.([]any); isList {
			matchesAny := false
			for _, option := range optionList {
				if schemaViolation(value, option, path) == "" {
					matchesAny = true
					break
				}
			}
			if !matchesAny {
				return path + " matches none of the allowed types"
			}
		}
	}
	if expected, hasType := memberValue(schema, "type"); hasType && expected != nil {
		expectedTypes := typeNames(expected)
		matchesType := false
		for _, typeName := range expectedTypes {
			if hasJSONType(value, typeName) {
				matchesType = true
				break
			}
		}
		if !matchesType {
			return path + " must be " + strings.Join(expectedTypes, " or ")
		}
	}
	if enumValues, hasEnum := memberValue(schema, "enum"); hasEnum {
		enumList, _ := enumValues.([]any)
		if !containsPythonEqual(enumList, value) {
			enumText, err := pythonjson.Marshal(enumValues, pythonjson.Options{})
			if err != nil {
				enumText = "the enum"
			}
			return path + " must be one of " + enumText
		}
	}
	switch typed := value.(type) {
	case pythonjson.Object:
		return objectViolation(typed, schema, path)
	case []any:
		if itemSchema, hasItems := memberValue(schema, "items"); hasItems {
			if _, isSchema := itemSchema.(pythonjson.Object); isSchema {
				for index, item := range typed {
					if problem := schemaViolation(item, itemSchema, fmt.Sprintf("%s[%d]", path, index)); problem != "" {
						return problem
					}
				}
			}
		}
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return ""
		}
		if minimum, hasMinimum := numberMember(schema, "minimum"); hasMinimum && number < minimum.value {
			return path + " must be at least " + minimum.text
		}
		if maximum, hasMaximum := numberMember(schema, "maximum"); hasMaximum && number > maximum.value {
			return path + " must be at most " + maximum.text
		}
	}
	return ""
}

func objectViolation(value pythonjson.Object, schema pythonjson.Object, path string) string {
	propertiesValue, _ := memberValue(schema, "properties")
	properties, _ := propertiesValue.(pythonjson.Object)
	if requiredValue, hasRequired := memberValue(schema, "required"); hasRequired {
		requiredList, _ := requiredValue.([]any)
		for _, key := range requiredList {
			keyText, isText := key.(string)
			if !isText {
				continue
			}
			if _, isPresent := memberValue(value, keyText); !isPresent {
				return path + "." + keyText + " is required"
			}
		}
	}
	additional, hasAdditional := memberValue(schema, "additionalProperties")
	for _, member := range value {
		memberPath := path + "." + member.Key
		var problem string
		if propertySchema, isProperty := memberValue(properties, member.Key); isProperty {
			problem = schemaViolation(member.Value, propertySchema, memberPath)
		} else if hasAdditional && additional == false {
			problem = memberPath + " is not allowed"
		} else if additionalSchema, isSchema := additional.(pythonjson.Object); hasAdditional && isSchema {
			problem = schemaViolation(member.Value, additionalSchema, memberPath)
		}
		if problem != "" {
			return problem
		}
	}
	return ""
}

func typeNames(expected any) []string {
	if name, isName := expected.(string); isName {
		return []string{name}
	}
	names := []string{}
	if list, isList := expected.([]any); isList {
		for _, item := range list {
			if name, isName := item.(string); isName {
				names = append(names, name)
			}
		}
	}
	return names
}

func hasJSONType(value any, typeName string) bool {
	switch typeName {
	case "string":
		_, isString := value.(string)
		return isString
	case "boolean":
		_, isBool := value.(bool)
		return isBool
	case "null":
		return value == nil
	case "array":
		_, isArray := value.([]any)
		return isArray
	case "object":
		_, isObject := value.(pythonjson.Object)
		return isObject
	case "number":
		_, isNumber := value.(json.Number)
		return isNumber
	case "integer":
		number, isNumber := value.(json.Number)
		if !isNumber {
			return false
		}
		floatValue, err := number.Float64()
		// Python accepts 1.0 as an integer, as the other cores do.
		return err == nil && !math.IsInf(floatValue, 0) && floatValue == math.Trunc(floatValue)
	default:
		return false
	}
}

type schemaNumber struct {
	value float64
	text  string
}

func numberMember(schema pythonjson.Object, key string) (schemaNumber, bool) {
	value, isPresent := memberValue(schema, key)
	number, isNumber := value.(json.Number)
	if !isPresent || !isNumber {
		return schemaNumber{}, false
	}
	floatValue, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return schemaNumber{}, false
	}
	text, err := pythonjson.FormatNumber(number)
	if err != nil {
		text = number.String()
	}
	return schemaNumber{value: floatValue, text: text}, true
}

func memberValue(object pythonjson.Object, key string) (any, bool) {
	for _, member := range object {
		if member.Key == key {
			return member.Value, true
		}
	}
	return nil, false
}

// containsPythonEqual reports whether value is in items under Python's ==, where 1 == 1.0 == True.
func containsPythonEqual(items []any, value any) bool {
	for _, item := range items {
		if isPythonEqual(item, value) {
			return true
		}
	}
	return false
}

func isPythonEqual(left any, right any) bool {
	leftNumber, isLeftNumber := pythonNumber(left)
	rightNumber, isRightNumber := pythonNumber(right)
	if isLeftNumber || isRightNumber {
		return isLeftNumber && isRightNumber && leftNumber == rightNumber
	}
	leftText, err := pythonjson.Marshal(left, pythonjson.Options{IsCompact: true, ShouldSortKeys: true})
	if err != nil {
		return false
	}
	rightText, err := pythonjson.Marshal(right, pythonjson.Options{IsCompact: true, ShouldSortKeys: true})
	return err == nil && leftText == rightText
}

func pythonNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

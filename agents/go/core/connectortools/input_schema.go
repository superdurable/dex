// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

const definitionsPrefix = "#/$defs/"

// checkedSchemaKeywords constrain a value; an unlisted keyword refuses the tool, so nothing is unchecked.
var checkedSchemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "anyOf": true, "$ref": true, "$defs": true, "minimum": true, "maximum": true,
}

// annotationSchemaKeywords never constrain a value; the operation's input type checks format.
var annotationSchemaKeywords = map[string]bool{
	"description": true, "title": true, "format": true, "contentEncoding": true, "default": true,
	"$schema": true, "$id": true, "examples": true,
}

var jsonTypeChecks = map[string]func(value any) bool{
	"string":  func(value any) bool { _, isString := value.(string); return isString },
	"boolean": func(value any) bool { _, isBool := value.(bool); return isBool },
	"null":    func(value any) bool { return value == nil },
	"array":   func(value any) bool { _, isArray := value.([]any); return isArray },
	"object":  func(value any) bool { _, isObject := value.(pythonjson.Object); return isObject },
	"number":  func(value any) bool { _, isNumber := value.(json.Number); return isNumber },
	"integer": isJSONInteger,
}

// inputSchema is a parsed inputSchema whose objects keep their member order.
type inputSchema struct {
	root        pythonjson.Object
	definitions pythonjson.Object
}

// parseInputSchema refuses a schema with a keyword this checker cannot enforce.
func parseInputSchema(schemaJSON json.RawMessage) (inputSchema, error) {
	parsed, err := pythonjson.ParseOrdered(schemaJSON)
	if err != nil {
		return inputSchema{}, fmt.Errorf("inputSchema is not JSON: %w", err)
	}
	root, isObject := parsed.(pythonjson.Object)
	if !isObject {
		return inputSchema{}, errors.New("inputSchema must have type object")
	}
	if schemaType, _ := member(root, "type"); schemaType != "object" {
		return inputSchema{}, errors.New("inputSchema must have type object")
	}
	definitions := pythonjson.Object{}
	if rawDefinitions, hasDefinitions := member(root, "$defs"); hasDefinitions {
		typed, isDefinitions := rawDefinitions.(pythonjson.Object)
		if !isDefinitions {
			return inputSchema{}, errors.New("inputSchema $defs must be an object")
		}
		definitions = typed
	}
	if err := checkSchemaNode(root, definitions, "inputSchema"); err != nil {
		return inputSchema{}, err
	}
	return inputSchema{root: root, definitions: definitions}, nil
}

func checkSchemaNode(node any, definitions pythonjson.Object, path string) error {
	object, isObject := node.(pythonjson.Object)
	if !isObject {
		return fmt.Errorf("%s must be a schema object", path)
	}
	for _, keyword := range object {
		if annotationSchemaKeywords[keyword.Key] {
			continue
		}
		if !checkedSchemaKeywords[keyword.Key] {
			return fmt.Errorf("%s uses %s, which agent tools do not check", path, keyword.Key)
		}
		if err := checkSchemaKeyword(keyword, definitions, path); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaKeyword(keyword pythonjson.Member, definitions pythonjson.Object, path string) error {
	switch keyword.Key {
	case "type":
		if _, isKnown := schemaTypeNames(keyword.Value); !isKnown {
			return fmt.Errorf("%s.type is not a JSON type", path)
		}
	case "properties", "$defs":
		children, isObject := keyword.Value.(pythonjson.Object)
		if !isObject {
			return fmt.Errorf("%s.%s must be an object", path, keyword.Key)
		}
		for _, child := range children {
			if err := checkSchemaNode(child.Value, definitions, path+"."+keyword.Key+"."+child.Key); err != nil {
				return err
			}
		}
	case "items", "additionalProperties":
		if _, isBool := keyword.Value.(bool); keyword.Key == "additionalProperties" && isBool {
			return nil
		}
		return checkSchemaNode(keyword.Value, definitions, path+"."+keyword.Key)
	case "anyOf":
		options, isArray := keyword.Value.([]any)
		if !isArray || len(options) == 0 {
			return fmt.Errorf("%s.anyOf must be a non-empty array", path)
		}
		for index, option := range options {
			if err := checkSchemaNode(option, definitions, fmt.Sprintf("%s.anyOf[%d]", path, index)); err != nil {
				return err
			}
		}
	case "$ref":
		reference, isString := keyword.Value.(string)
		if !isString || !strings.HasPrefix(reference, definitionsPrefix) {
			return fmt.Errorf("%s.$ref must point into #/$defs/", path)
		}
		if _, isDefined := member(definitions, strings.TrimPrefix(reference, definitionsPrefix)); !isDefined {
			return fmt.Errorf("%s.$ref names a missing definition", path)
		}
	case "required":
		names, isArray := keyword.Value.([]any)
		isNames := isArray && !slices.ContainsFunc(names, func(name any) bool { _, isString := name.(string); return !isString })
		if !isNames {
			return fmt.Errorf("%s.required must be an array of names", path)
		}
	case "enum":
		if _, isArray := keyword.Value.([]any); !isArray {
			return fmt.Errorf("%s.enum must be an array", path)
		}
	}
	return nil
}

// schemaTypeNames returns a type keyword's names, and false when one is not a JSON type.
func schemaTypeNames(value any) ([]string, bool) {
	var names []any
	switch typed := value.(type) {
	case string:
		names = []any{typed}
	case []any:
		names = typed
	default:
		return nil, false
	}
	typeNames := make([]string, 0, len(names))
	for _, name := range names {
		text, isString := name.(string)
		if !isString || jsonTypeChecks[text] == nil {
			return nil, false
		}
		typeNames = append(typeNames, text)
	}
	return typeNames, true
}

// findInputViolation returns the first way the arguments break the schema, or "".
//
// The texts are the Python and TypeScript cores', such as "arguments.amount must be integer".
func (schema inputSchema) findInputViolation(arguments any) string {
	return schema.violation(arguments, schema.root, "arguments")
}

func (schema inputSchema) violation(value any, node pythonjson.Object, path string) string {
	if reference, isString := memberString(node, "$ref"); isString {
		definition, _ := member(schema.definitions, strings.TrimPrefix(reference, definitionsPrefix))
		if definitionObject, isObject := definition.(pythonjson.Object); isObject {
			if problem := schema.violation(value, definitionObject, path); problem != "" {
				return problem
			}
		}
	}
	if rawOptions, hasAnyOf := member(node, "anyOf"); hasAnyOf {
		options, _ := rawOptions.([]any)
		matchesNone := true
		for _, option := range options {
			optionObject, _ := option.(pythonjson.Object)
			if schema.violation(value, optionObject, path) == "" {
				matchesNone = false
				break
			}
		}
		if matchesNone {
			return path + " matches none of the allowed types"
		}
	}
	if rawType, hasType := member(node, "type"); hasType && rawType != nil {
		typeNames, _ := schemaTypeNames(rawType)
		if !slices.ContainsFunc(typeNames, func(name string) bool { return jsonTypeChecks[name](value) }) {
			return path + " must be " + strings.Join(typeNames, " or ")
		}
	}
	if rawEnum, hasEnum := member(node, "enum"); hasEnum {
		allowed, _ := rawEnum.([]any)
		if !slices.ContainsFunc(allowed, func(candidate any) bool { return isJSONEqual(value, candidate) }) {
			enumText, err := pythonjson.Marshal(allowed, pythonjson.Options{})
			if err != nil {
				enumText = fmt.Sprint(allowed)
			}
			return path + " must be one of " + enumText
		}
	}
	switch typed := value.(type) {
	case pythonjson.Object:
		return schema.objectViolation(typed, node, path)
	case []any:
		if items, isItems := member(node, "items"); isItems {
			if itemSchema, isObject := items.(pythonjson.Object); isObject {
				for index, item := range typed {
					if problem := schema.violation(item, itemSchema, fmt.Sprintf("%s[%d]", path, index)); problem != "" {
						return problem
					}
				}
			}
		}
	case json.Number:
		if minimum, isNumber := memberNumber(node, "minimum"); isNumber && compareJSONNumbers(typed, minimum) < 0 {
			return path + " must be at least " + formatSchemaNumber(minimum)
		}
		if maximum, isNumber := memberNumber(node, "maximum"); isNumber && compareJSONNumbers(typed, maximum) > 0 {
			return path + " must be at most " + formatSchemaNumber(maximum)
		}
	}
	return ""
}

func (schema inputSchema) objectViolation(value pythonjson.Object, node pythonjson.Object, path string) string {
	properties := pythonjson.Object{}
	if rawProperties, hasProperties := member(node, "properties"); hasProperties {
		properties, _ = rawProperties.(pythonjson.Object)
	}
	if rawRequired, hasRequired := member(node, "required"); hasRequired {
		required, _ := rawRequired.([]any)
		for _, rawName := range required {
			name, _ := rawName.(string)
			if _, isPresent := member(value, name); !isPresent {
				return path + "." + name + " is required"
			}
		}
	}
	var additional any = true
	if rawAdditional, hasAdditional := member(node, "additionalProperties"); hasAdditional {
		additional = rawAdditional
	}
	keys := make([]string, 0, len(value))
	for _, argument := range value {
		keys = append(keys, argument.Key)
	}
	// Python compares keys by code point, which byte order of UTF-8 keeps.
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		item, _ := member(value, key)
		problem := ""
		if propertySchema, isProperty := member(properties, key); isProperty {
			propertyObject, _ := propertySchema.(pythonjson.Object)
			problem = schema.violation(item, propertyObject, path+"."+key)
		} else if additional == false {
			problem = path + "." + key + " is not allowed"
		} else if additionalSchema, isSchema := additional.(pythonjson.Object); isSchema {
			problem = schema.violation(item, additionalSchema, path+"."+key)
		}
		if problem != "" {
			return problem
		}
	}
	return ""
}

// member returns an object's value for a key; the last one wins, as in Python's json.loads.
func member(object pythonjson.Object, key string) (any, bool) {
	for index := len(object) - 1; index >= 0; index-- {
		if object[index].Key == key {
			return object[index].Value, true
		}
	}
	return nil, false
}

func memberString(object pythonjson.Object, key string) (string, bool) {
	value, _ := member(object, key)
	text, isString := value.(string)
	return text, isString
}

func memberNumber(object pythonjson.Object, key string) (json.Number, bool) {
	value, _ := member(object, key)
	number, isNumber := value.(json.Number)
	return number, isNumber
}

// isJSONInteger reports a number with no fractional part, as Python's value == int(value).
func isJSONInteger(value any) bool {
	number, isNumber := value.(json.Number)
	if !isNumber {
		return false
	}
	if isIntegerLiteral(number) {
		return true
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	return err == nil && !math.IsInf(parsed, 0) && parsed == math.Trunc(parsed)
}

func isIntegerLiteral(number json.Number) bool {
	return !strings.ContainsAny(number.String(), ".eE")
}

// compareJSONNumbers compares as Python compares the int or float that json.loads reads.
func compareJSONNumbers(left json.Number, right json.Number) int {
	leftValue, rightValue := pythonNumber(left), pythonNumber(right)
	return leftValue.Cmp(rightValue)
}

// pythonNumber is the exact value of the int or float that Python's json.loads reads.
func pythonNumber(number json.Number) *big.Float {
	if isIntegerLiteral(number) {
		integer, isInteger := new(big.Int).SetString(number.String(), 10)
		if isInteger {
			return new(big.Float).SetPrec(uint(max(integer.BitLen(), 64))).SetInt(integer)
		}
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil && !math.IsInf(parsed, 0) {
		return new(big.Float)
	}
	return new(big.Float).SetFloat64(parsed)
}

// formatSchemaNumber returns Python's str of a schema bound, such as 1 or 1.5.
func formatSchemaNumber(number json.Number) string {
	text, err := pythonjson.FormatNumber(number)
	if err != nil {
		return number.String()
	}
	return text
}

// isJSONEqual is JSON equality: booleans never equal numbers, and 1 equals 1.0.
func isJSONEqual(left any, right any) bool {
	switch leftValue := left.(type) {
	case bool:
		rightValue, isBool := right.(bool)
		return isBool && leftValue == rightValue
	case json.Number:
		rightValue, isNumber := right.(json.Number)
		return isNumber && compareJSONNumbers(leftValue, rightValue) == 0
	case []any:
		rightValue, isArray := right.([]any)
		return isArray && slices.EqualFunc(leftValue, rightValue, isJSONEqual)
	case pythonjson.Object:
		rightValue, isObject := right.(pythonjson.Object)
		if !isObject || len(objectKeys(leftValue)) != len(objectKeys(rightValue)) {
			return false
		}
		for key := range objectKeys(leftValue) {
			leftItem, _ := member(leftValue, key)
			rightItem, isPresent := member(rightValue, key)
			if !isPresent || !isJSONEqual(leftItem, rightItem) {
				return false
			}
		}
		return true
	case string:
		rightValue, isString := right.(string)
		return isString && leftValue == rightValue
	case nil:
		return right == nil
	default:
		return false
	}
}

func objectKeys(object pythonjson.Object) map[string]bool {
	keys := make(map[string]bool, len(object))
	for _, item := range object {
		keys[item.Key] = true
	}
	return keys
}

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
	"math/big"
	"regexp"
)

const maximumStartInputDepth = 32

var startInputIntegerPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// v2StartInputFact carries a Start Step schema or extraction error. List enum values in declaration order.
type v2StartInputFact struct {
	StepType string `json:"stepType"`
	// Input uses int64 bounds for unbounded integers, decimal strings for integer enum values, and "date-time" as the sole format.
	Input *StartInputSchema `json:"input,omitempty"`
	Error string            `json:"error,omitempty"`
	Span  *Span             `json:"span,omitempty"`
}

func (graph *Graph) buildV2StartDefinition(fact v2StartInputFact) *StartDefinition {
	if fact.StepType == "" {
		return nil
	}
	if fact.Error == "" {
		if fact.Input == nil {
			graph.AddDiagnostic("warning", "v2_start_input", fmt.Sprintf("Start Step %s input type is not statically readable", fact.StepType), fact.Span)
			return nil
		}
		if err := validateStartInputSchema(*fact.Input, 0); err != nil {
			fact.Error = err.Error()
		}
	}
	if fact.Error != "" {
		graph.AddDiagnostic("warning", "v2_start_input", fmt.Sprintf("Start Step %s input cannot drive a form: %s", fact.StepType, fact.Error), fact.Span)
		return nil
	}
	return &StartDefinition{StepType: fact.StepType, Input: *fact.Input}
}

func validateStartInputSchema(schema StartInputSchema, depth int) error {
	if depth > maximumStartInputDepth {
		return fmt.Errorf("nesting exceeds %d levels", maximumStartInputDepth)
	}
	if (schema.Nullable && schema.Kind == "null") ||
		(schema.Format != "" && (schema.Kind != "string" || schema.Format != "date-time")) ||
		(len(schema.EnumValues) > 0 && schema.Kind != "string" && schema.Kind != "integer") ||
		((schema.Minimum != "" || schema.Maximum != "") && schema.Kind != "integer") ||
		(len(schema.Fields) > 0 && schema.Kind != "object") ||
		(schema.Items != nil && schema.Kind != "array") ||
		(schema.Values != nil && schema.Kind != "map") ||
		(schema.FixedLength != nil && (schema.Kind != "array" || *schema.FixedLength < 0)) {
		return fmt.Errorf("invalid schema options for type %s", schema.Kind)
	}
	switch schema.Kind {
	case "null", "number", "boolean":
		return nil
	case "string", "integer":
		return validateStartInputScalar(schema)
	case "object":
		return validateStartInputFields(schema.Fields, depth)
	case "array":
		if schema.Items == nil {
			return fmt.Errorf("array items schema is required")
		}
		return validateStartInputSchema(*schema.Items, depth+1)
	case "map":
		if schema.Values == nil {
			return fmt.Errorf("map values schema is required")
		}
		return validateStartInputSchema(*schema.Values, depth+1)
	default:
		return fmt.Errorf("type %s is unsupported", schema.Kind)
	}
}

func validateStartInputFields(fields []StartInputField, depth int) error {
	fieldNames := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			return fmt.Errorf("JSON field name must not be empty")
		}
		if fieldNames[field.Name] {
			return fmt.Errorf("JSON field %q is repeated", field.Name)
		}
		fieldNames[field.Name] = true
		if err := validateStartInputSchema(field.Schema, depth+1); err != nil {
			return fmt.Errorf("field %s: %w", field.Name, err)
		}
	}
	return nil
}

func validateStartInputScalar(schema StartInputSchema) error {
	var minimum, maximum *big.Int
	if schema.Kind == "integer" {
		minimum = parseStartInputInteger(schema.Minimum)
		maximum = parseStartInputInteger(schema.Maximum)
		if minimum == nil || maximum == nil || minimum.Cmp(maximum) > 0 {
			return fmt.Errorf("integer requires valid minimum and maximum bounds")
		}
	}
	seenNames := make(map[string]bool, len(schema.EnumValues))
	seenValues := make(map[string]bool, len(schema.EnumValues))
	for _, enumValue := range schema.EnumValues {
		if enumValue.Name == "" || seenNames[enumValue.Name] {
			return fmt.Errorf("enum value name %q must be non-empty and unique", enumValue.Name)
		}
		value, isString := enumValue.Value.(string)
		if !isString {
			return fmt.Errorf("enum value %q must be a string", enumValue.Name)
		}
		if schema.Kind == "integer" {
			integer := parseStartInputInteger(value)
			if integer == nil || integer.Cmp(minimum) < 0 || integer.Cmp(maximum) > 0 {
				return fmt.Errorf("enum value %q must be an integer within bounds", enumValue.Name)
			}
			value = integer.String()
		}
		if seenValues[value] {
			return fmt.Errorf("enum value %q is repeated", value)
		}
		seenNames[enumValue.Name] = true
		seenValues[value] = true
	}
	return nil
}

func parseStartInputInteger(value string) *big.Int {
	if !startInputIntegerPattern.MatchString(value) {
		return nil
	}
	integer, _ := new(big.Int).SetString(value, 10)
	return integer
}

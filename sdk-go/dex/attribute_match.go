// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"fmt"
	"math"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// AttributeMatch describes a scalar Attribute predicate.
//
// Create a match with an AttributeMatch factory and pass it to
// [Client.WaitForAttributeMatch] or [Client.WaitForAttributeMapInstanceMatch].
// The operand type must match the registered Attribute type. String and boolean
// Attributes support equality operators. Integer and floating-point Attributes
// support every operator.
//
// Example:
//
//	match := dex.AttributeMatchGreaterThan(int64(3))
type AttributeMatch[T any] struct {
	operator dexpb.AttributeMatchOperator
	operand  T
}

// AttributeMatchDef is the schema-erased form of an AttributeMatch.
//
// Applications create matches with the AttributeMatch factory functions. This
// interface is sealed so only SDK-defined match values can implement it.
type AttributeMatchDef interface {
	attributeMatchOperator() dexpb.AttributeMatchOperator
	attributeMatchOperand() any
}

// AttributeMatchEqual creates a match that succeeds when the Attribute equals operand.
//
// String, boolean, integer, and floating-point operands are supported.
func AttributeMatchEqual[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand)
}

// AttributeMatchNotEqual creates a match that succeeds when the Attribute differs from operand.
//
// A missing Attribute does not satisfy this match. String, boolean, integer,
// and floating-point operands are supported.
func AttributeMatchNotEqual[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand)
}

// AttributeMatchGreaterThan creates a match that succeeds when the Attribute is greater than operand.
//
// The operand must be an integer or finite floating-point value.
func AttributeMatchGreaterThan[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand)
}

// AttributeMatchGreaterThanOrEqual creates a greater-than-or-equal Attribute match.
//
// The operand must be an integer or finite floating-point value.
func AttributeMatchGreaterThanOrEqual[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(
		dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL,
		operand,
	)
}

// AttributeMatchLessThan creates a match that succeeds when the Attribute is less than operand.
//
// The operand must be an integer or finite floating-point value.
func AttributeMatchLessThan[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand)
}

// AttributeMatchLessThanOrEqual creates a less-than-or-equal Attribute match.
//
// The operand must be an integer or finite floating-point value.
func AttributeMatchLessThanOrEqual[T any](operand T) AttributeMatch[T] {
	return newAttributeMatch(
		dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL,
		operand,
	)
}

func newAttributeMatch[T any](operator dexpb.AttributeMatchOperator, operand T) AttributeMatch[T] {
	return AttributeMatch[T]{operator: operator, operand: operand}
}

func (match AttributeMatch[T]) attributeMatchOperator() dexpb.AttributeMatchOperator {
	return match.operator
}

func (match AttributeMatch[T]) attributeMatchOperand() any {
	return match.operand
}

func validateEncodedAttributeMatch(operator dexpb.AttributeMatchOperator, operand *dexpb.Value) error {
	if operator < dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL ||
		operator > dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL {
		return fmt.Errorf("dex: AttributeMatch operator is invalid")
	}
	switch operand.GetKind().(type) {
	case *dexpb.Value_StringValue, *dexpb.Value_BoolValue:
		if operator >= dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN {
			return fmt.Errorf("dex: AttributeMatch ordering requires an integer or float64 operand")
		}
	case *dexpb.Value_IntValue:
	case *dexpb.Value_DoubleValue:
		if math.IsNaN(operand.GetDoubleValue()) || math.IsInf(operand.GetDoubleValue(), 0) {
			return fmt.Errorf("dex: AttributeMatch float64 operand must be finite")
		}
	default:
		return fmt.Errorf("dex: AttributeMatch supports only string, boolean, integer, or float64 operands")
	}
	return nil
}

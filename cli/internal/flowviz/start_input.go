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
	"go/ast"
	"go/constant"
	"go/types"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/superdurable/dex/service/common/ptr"
)

const maximumStartInputDepth = 32

func (analyzer *goAnalyzer) collectV2StartDefinition() *StartDefinition {
	if analyzer.startStepType == "" {
		return nil
	}
	if analyzer.startInputType == nil {
		analyzer.graph.AddDiagnostic(
			"warning",
			"v2_start_input",
			fmt.Sprintf("Start Step %s input type is not statically readable", analyzer.startStepType),
			nil,
		)
		return nil
	}
	schema, err := analyzer.startInputSchema(analyzer.startInputType, 0, make(map[types.Type]bool))
	if err != nil {
		analyzer.graph.AddDiagnostic(
			"warning",
			"v2_start_input",
			fmt.Sprintf("Start Step %s input cannot drive a form: %v", analyzer.startStepType, err),
			nil,
		)
		return nil
	}
	return &StartDefinition{StepType: analyzer.startStepType, Input: schema}
}

func (analyzer *goAnalyzer) registeredStepInputType(expression ast.Expr) types.Type {
	typeAndValue, found := analyzer.typeInfo.Types[expression]
	if !found || typeAndValue.Type == nil {
		return nil
	}
	methodSelection := types.NewMethodSet(typeAndValue.Type).Lookup(nil, "Execute")
	if methodSelection == nil {
		return nil
	}
	signature, ok := methodSelection.Obj().Type().(*types.Signature)
	if !ok || signature.Params().Len() != 2 {
		return nil
	}
	return signature.Params().At(1).Type()
}

func (analyzer *goAnalyzer) startInputSchema(
	valueType types.Type,
	depth int,
	ancestors map[types.Type]bool,
) (StartInputSchema, error) {
	if depth > maximumStartInputDepth {
		return StartInputSchema{}, fmt.Errorf("nesting exceeds %d levels", maximumStartInputDepth)
	}
	valueType = types.Unalias(valueType)
	if isDexNoneType(valueType) {
		return StartInputSchema{Kind: "null"}, nil
	}
	if pointer, ok := valueType.(*types.Pointer); ok {
		schema, err := analyzer.startInputSchema(pointer.Elem(), depth+1, ancestors)
		if err != nil {
			return StartInputSchema{}, err
		}
		if schema.Kind != "null" {
			schema.Nullable = true
		}
		return schema, nil
	}
	if named, ok := valueType.(*types.Named); ok {
		if isTimeType(named) {
			return StartInputSchema{Kind: "string", Format: "date-time"}, nil
		}
		if hasCustomJSONMethods(named) {
			return StartInputSchema{}, fmt.Errorf("type %s has custom JSON encoding", named.Obj().Name())
		}
		if ancestors[named] {
			return StartInputSchema{}, fmt.Errorf("type %s is recursive", named.Obj().Name())
		}
		nextAncestors := cloneTypeSet(ancestors)
		nextAncestors[named] = true
		schema, err := analyzer.startInputSchema(named.Underlying(), depth, nextAncestors)
		if err != nil {
			return StartInputSchema{}, err
		}
		schema.EnumValues = startInputEnumValues(named)
		return schema, nil
	}
	switch underlying := valueType.Underlying().(type) {
	case *types.Basic:
		return analyzer.startInputBasicSchema(underlying)
	case *types.Struct:
		return analyzer.startInputStructSchema(underlying, depth, ancestors)
	case *types.Slice:
		if basic, ok := types.Unalias(underlying.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Byte {
			return StartInputSchema{}, fmt.Errorf("[]byte uses base64 JSON encoding")
		}
		items, err := analyzer.startInputSchema(underlying.Elem(), depth+1, ancestors)
		if err != nil {
			return StartInputSchema{}, err
		}
		return StartInputSchema{Kind: "array", Nullable: true, Items: &items}, nil
	case *types.Array:
		items, err := analyzer.startInputSchema(underlying.Elem(), depth+1, ancestors)
		if err != nil {
			return StartInputSchema{}, err
		}
		return StartInputSchema{
			Kind: "array", Items: &items, FixedLength: ptr.Any(underlying.Len()),
		}, nil
	case *types.Map:
		key, ok := types.Unalias(underlying.Key()).Underlying().(*types.Basic)
		if !ok || key.Kind() != types.String {
			return StartInputSchema{}, fmt.Errorf("map keys must be strings")
		}
		values, err := analyzer.startInputSchema(underlying.Elem(), depth+1, ancestors)
		if err != nil {
			return StartInputSchema{}, err
		}
		return StartInputSchema{Kind: "map", Nullable: true, Values: &values}, nil
	default:
		return StartInputSchema{}, fmt.Errorf("type %s is unsupported", valueType.String())
	}
}

func (analyzer *goAnalyzer) startInputBasicSchema(basic *types.Basic) (StartInputSchema, error) {
	switch basic.Kind() {
	case types.String:
		return StartInputSchema{Kind: "string"}, nil
	case types.Bool:
		return StartInputSchema{Kind: "boolean"}, nil
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		minimum, maximum := signedIntegerBounds(int(analyzer.typeSizes.Sizeof(basic) * 8))
		return StartInputSchema{Kind: "integer", Minimum: minimum, Maximum: maximum}, nil
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		minimum, maximum := unsignedIntegerBounds(int(analyzer.typeSizes.Sizeof(basic) * 8))
		return StartInputSchema{Kind: "integer", Minimum: minimum, Maximum: maximum}, nil
	case types.Float32, types.Float64:
		return StartInputSchema{Kind: "number"}, nil
	default:
		return StartInputSchema{}, fmt.Errorf("basic type %s is unsupported", basic.Name())
	}
}

func (analyzer *goAnalyzer) startInputStructSchema(
	structure *types.Struct,
	depth int,
	ancestors map[types.Type]bool,
) (StartInputSchema, error) {
	fields := make([]StartInputField, 0, structure.NumFields())
	fieldNames := make(map[string]bool)
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() {
			continue
		}
		tagName, options := parseJSONTag(structure.Tag(index))
		if tagName == "-" {
			continue
		}
		if options["string"] {
			return StartInputSchema{}, fmt.Errorf("field %s uses json string encoding", field.Name())
		}
		_, isPointer := types.Unalias(field.Type()).(*types.Pointer)
		if field.Anonymous() && tagName == "" {
			promotedSchema, err := analyzer.startInputSchema(field.Type(), depth+1, ancestors)
			if err != nil {
				return StartInputSchema{}, fmt.Errorf("anonymous field %s: %w", field.Name(), err)
			}
			if promotedSchema.Kind != "object" {
				return StartInputSchema{}, fmt.Errorf("anonymous field %s must encode as an object", field.Name())
			}
			for _, promotedField := range promotedSchema.Fields {
				if fieldNames[promotedField.Name] {
					return StartInputSchema{}, fmt.Errorf("anonymous field %s conflicts on JSON field %q", field.Name(), promotedField.Name)
				}
				fieldNames[promotedField.Name] = true
				promotedField.Required = promotedField.Required && !isPointer && !options["omitempty"]
				fields = append(fields, promotedField)
			}
			continue
		}
		fieldName := tagName
		if fieldName == "" {
			fieldName = field.Name()
		}
		if fieldNames[fieldName] {
			return StartInputSchema{}, fmt.Errorf("JSON field %q is repeated", fieldName)
		}
		fieldNames[fieldName] = true
		fieldSchema, err := analyzer.startInputSchema(field.Type(), depth+1, ancestors)
		if err != nil {
			return StartInputSchema{}, fmt.Errorf("field %s: %w", fieldName, err)
		}
		fields = append(fields, StartInputField{
			Name: fieldName, Required: !isPointer && !options["omitempty"], Schema: fieldSchema,
		})
	}
	return StartInputSchema{Kind: "object", Fields: fields}, nil
}

func parseJSONTag(tag string) (string, map[string]bool) {
	value, found := reflect.StructTag(tag).Lookup("json")
	if !found {
		return "", map[string]bool{}
	}
	parts := strings.Split(value, ",")
	if !validJSONTagName(parts[0]) {
		parts[0] = ""
	}
	options := make(map[string]bool, len(parts)-1)
	for _, option := range parts[1:] {
		options[option] = true
	}
	return parts[0], options
}

func validJSONTagName(name string) bool {
	if name == "" {
		return true
	}
	for _, character := range name {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", character) {
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func startInputEnumValues(named *types.Named) []StartInputEnumValue {
	object := named.Obj()
	if object == nil || object.Pkg() == nil {
		return nil
	}
	underlying, ok := named.Underlying().(*types.Basic)
	if !ok || underlying.Info()&(types.IsString|types.IsInteger) == 0 {
		return nil
	}
	constants := make([]*types.Const, 0)
	for _, name := range object.Pkg().Scope().Names() {
		constantObject, isConstant := object.Pkg().Scope().Lookup(name).(*types.Const)
		if isConstant && types.Identical(types.Unalias(constantObject.Type()), named) {
			constants = append(constants, constantObject)
		}
	}
	sort.SliceStable(constants, func(left int, right int) bool {
		return constants[left].Pos() < constants[right].Pos()
	})
	values := make([]StartInputEnumValue, 0, len(constants))
	seenValues := make(map[string]bool)
	for _, constantObject := range constants {
		value, key, ok := startInputEnumValue(constantObject.Val(), underlying)
		if !ok || seenValues[key] {
			continue
		}
		seenValues[key] = true
		values = append(values, StartInputEnumValue{Name: constantObject.Name(), Value: value})
	}
	return values
}

func startInputEnumValue(value constant.Value, underlying *types.Basic) (any, string, bool) {
	if underlying.Info()&types.IsString != 0 {
		text := constant.StringVal(value)
		return text, "string:" + text, true
	}
	integer := constant.ToInt(value)
	if integer.Kind() != constant.Int {
		return nil, "", false
	}
	exact := integer.ExactString()
	return exact, "integer:" + exact, true
}

func isDexNoneType(valueType types.Type) bool {
	pointer, ok := types.Unalias(valueType).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == goSDKPackage && named.Obj().Name() == "none"
}

func isTimeType(named *types.Named) bool {
	return named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "time" && named.Obj().Name() == "Time"
}

func hasCustomJSONMethods(named *types.Named) bool {
	for _, candidate := range []types.Type{named, types.NewPointer(named)} {
		methods := types.NewMethodSet(candidate)
		for index := 0; index < methods.Len(); index++ {
			switch methods.At(index).Obj().Name() {
			case "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText":
				return true
			}
		}
	}
	return false
}

func cloneTypeSet(source map[types.Type]bool) map[types.Type]bool {
	clone := make(map[types.Type]bool, len(source)+1)
	for valueType := range source {
		clone[valueType] = true
	}
	return clone
}

func signedIntegerBounds(bits int) (string, string) {
	if bits >= 64 {
		return strconv.FormatInt(math.MinInt64, 10), strconv.FormatInt(math.MaxInt64, 10)
	}
	maximum := int64(1)<<(bits-1) - 1
	return strconv.FormatInt(-maximum-1, 10), strconv.FormatInt(maximum, 10)
}

func unsignedIntegerBounds(bits int) (string, string) {
	if bits >= 64 {
		return "0", strconv.FormatUint(math.MaxUint64, 10)
	}
	maximum := uint64(1)<<bits - 1
	return "0", strconv.FormatUint(maximum, 10)
}

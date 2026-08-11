// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	jsonEncoding     = "json"
	rawBytesEncoding = "rawbytes"
	dateTimeFormat   = time.RFC3339Nano
	internalIDPrefix = "__dex_internal_condition_"
)

var (
	byteSliceType = reflect.TypeFor[[]byte]()
	noneType      = reflect.TypeFor[None]()
	timeType      = reflect.TypeFor[time.Time]()
)

// Value is an opaque Dex value. Decoded string values contain valid UTF-8.
//
// Values returned by bulk Client reads are already hydrated. Use Decode with the
// same Go type used by the matching Attribute. Primitive
// values preserve their wire kind; structs, maps, and non-byte slices use JSON;
// byte slices use the raw-bytes encoding.
type Value struct {
	value *dexpb.Value
}

// Decode decodes Value into a non-nil pointer.
//
// valuePtr must be a non-nil pointer compatible with the encoded wire value. Decode
// replaces the pointed-to value on success and returns a ValueMappingError for an
// incompatible target, malformed payload, unsupported encoding, or numeric overflow.
//
//	values, err := client.GetAttributes(ctx, flowID, statusAttribute)
//	if err != nil {
//		return err
//	}
//	var status string
//	err = values[statusAttribute.AttributeName()].Decode(&status)
func (value Value) Decode(valuePtr any) error {
	return decodeValue(value.value, valuePtr)
}

func encodeValue(value any) (encoded *dexpb.Value, err error) {
	defer func() {
		err = wrapValueMappingError("encode", err)
	}()
	if value == nil {
		return encodeJSONObject(value)
	}

	reflected := reflect.ValueOf(value)
	if reflected.Type() == noneType {
		if reflected.IsNil() {
			return encodeJSONObject(nil)
		}
		return nil, fmt.Errorf("dex: None payload must be nil")
	}
	if isNilValue(reflected) {
		return encodeJSONObject(nil)
	}

	switch reflected.Kind() {
	case reflect.String:
		stringValue := reflected.String()
		// Protobuf string fields require valid UTF-8.
		if !utf8.ValidString(stringValue) {
			return nil, fmt.Errorf("dex: string value is not valid UTF-8")
		}
		return &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: stringValue},
		}, nil
	case reflect.Bool:
		return &dexpb.Value{
			Kind: &dexpb.Value_BoolValue{BoolValue: reflected.Bool()},
		}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: reflected.Int()},
		}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr:
		unsigned := reflected.Uint()
		if unsigned > math.MaxInt64 {
			return nil, fmt.Errorf("dex: integer %d exceeds int64", unsigned)
		}
		return &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: int64(unsigned)},
		}, nil
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("dex: non-finite floating-point values are unsupported")
		}
		return &dexpb.Value{
			Kind: &dexpb.Value_DoubleValue{DoubleValue: number},
		}, nil
	case reflect.Slice:
		if reflected.Type().ConvertibleTo(byteSliceType) {
			return encodeRawBytes(reflected), nil
		}
		return encodeJSONObject(value)
	default:
		return encodeJSONObject(value)
	}
}

func encodeRawBytes(value reflect.Value) *dexpb.Value {
	rawBytes := value.Convert(byteSliceType).Interface().([]byte)
	payload := make([]byte, len(rawBytes))
	copy(payload, rawBytes)
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: rawBytesEncoding,
				Payload:  payload,
			},
		},
	}
}

func encodeJSONObject(value any) (*dexpb.Value, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("dex: encode JSON value: %w", err)
	}
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: jsonEncoding,
				Payload:  payload,
			},
		},
	}, nil
}

func encodeAttributeValue(
	value any,
	index *AttributeIndex,
) (encoded *dexpb.Value, config *dexpb.IndexConfig, err error) {
	defer func() {
		err = wrapValueMappingError("encode indexed", err)
	}()
	if index == nil {
		encoded, err = encodeValue(value)
		return encoded, nil, err
	}

	encoded, err = encodeIndexedValue(value, index.Type)
	if err != nil {
		return nil, nil, err
	}
	indexType, err := mapIndexType(index.Type)
	if err != nil {
		return nil, nil, err
	}
	return encoded, &dexpb.IndexConfig{
		Enable:   true,
		Type:     indexType,
		IndexKey: index.IndexKey,
	}, nil
}

func encodeIndexedValue(value any, indexType IndexType) (*dexpb.Value, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || isNilValue(reflected) {
		return nil, fmt.Errorf("dex: indexed attribute value must not be nil")
	}

	switch indexType {
	case IndexKeyword, IndexFullText:
		if reflected.Kind() != reflect.String {
			return nil, incompatibleIndexValue(indexType, reflected.Type())
		}
		return encodeValue(value)
	case IndexKeywordArray:
		if !isStringSlice(reflected.Type()) {
			return nil, incompatibleIndexValue(indexType, reflected.Type())
		}
		for index := range reflected.Len() {
			if !utf8.ValidString(reflected.Index(index).String()) {
				return nil, fmt.Errorf(
					"dex: keyword array value at index %d is not valid UTF-8",
					index,
				)
			}
		}
		return encodeJSONObject(value)
	case IndexInt:
		switch reflected.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Uintptr:
			return encodeValue(value)
		default:
			return nil, incompatibleIndexValue(indexType, reflected.Type())
		}
	case IndexDouble:
		if reflected.Kind() != reflect.Float32 && reflected.Kind() != reflect.Float64 {
			return nil, incompatibleIndexValue(indexType, reflected.Type())
		}
		return encodeValue(value)
	case IndexBool:
		if reflected.Kind() != reflect.Bool {
			return nil, incompatibleIndexValue(indexType, reflected.Type())
		}
		return encodeValue(value)
	case IndexDatetime:
		return encodeDatetimeIndex(value, reflected)
	default:
		return nil, fmt.Errorf("dex: unsupported index type %d", indexType)
	}
}

func encodeDatetimeIndex(value any, reflected reflect.Value) (*dexpb.Value, error) {
	if reflected.Type().ConvertibleTo(timeType) {
		dateTime := reflected.Convert(timeType).Interface().(time.Time)
		return encodeValue(dateTime.Format(dateTimeFormat))
	}
	if reflected.Kind() != reflect.String {
		return nil, incompatibleIndexValue(IndexDatetime, reflected.Type())
	}
	dateTime := reflected.String()
	if _, err := parseDatetime(dateTime); err != nil {
		return nil, err
	}
	return encodeValue(dateTime)
}

func decodeValue(value *dexpb.Value, valuePtr any) error {
	target, err := decodeTarget(valuePtr)
	if err != nil {
		return err
	}
	return decodeValueInto(value, target, valuePtr)
}

func decodeValueInto(
	value *dexpb.Value,
	target reflect.Value,
	valuePtr any,
) (err error) {
	defer func() {
		err = wrapValueMappingError("decode", err)
	}()
	if value == nil || value.Kind == nil {
		return fmt.Errorf("dex: value has no concrete kind")
	}

	switch kind := value.Kind.(type) {
	case *dexpb.Value_StringValue:
		return assignString(target, kind.StringValue)
	case *dexpb.Value_IntValue:
		return assignInt(target, kind.IntValue)
	case *dexpb.Value_DoubleValue:
		return assignFloat(target, kind.DoubleValue)
	case *dexpb.Value_BoolValue:
		return assignBool(target, kind.BoolValue)
	case *dexpb.Value_ObjValue:
		return decodeObject(kind.ObjValue, target, valuePtr)
	case *dexpb.Value_InternalBlobIdForStringValue,
		*dexpb.Value_InternalBlobIdForObjValue:
		return fmt.Errorf("dex: blob-backed value must be hydrated before decoding")
	case *dexpb.Value_NullValue:
		return fmt.Errorf("dex: attribute deletion marker cannot be decoded")
	default:
		return fmt.Errorf("dex: unsupported value kind %T", kind)
	}
}

func decodeTarget(valuePtr any) (reflect.Value, error) {
	if valuePtr == nil {
		return reflect.Value{}, fmt.Errorf("dex: decode target must be a non-nil pointer")
	}
	target := reflect.ValueOf(valuePtr)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return reflect.Value{}, fmt.Errorf("dex: decode target must be a non-nil pointer")
	}
	return target.Elem(), nil
}

func decodeObject(
	object *dexpb.EncodedObject,
	target reflect.Value,
	valuePtr any,
) error {
	if object == nil {
		return fmt.Errorf("dex: object value is missing")
	}
	switch object.Encoding {
	case jsonEncoding:
		if target.Type() == noneType &&
			!bytes.Equal(bytes.TrimSpace(object.Payload), []byte("null")) {
			return fmt.Errorf("dex: None payload must be JSON null")
		}
		if err := json.Unmarshal(object.Payload, valuePtr); err != nil {
			return fmt.Errorf("dex: decode JSON value: %w", err)
		}
		return nil
	case rawBytesEncoding:
		return assignRawBytes(target, object.Payload)
	default:
		return fmt.Errorf("dex: unsupported object encoding %q", object.Encoding)
	}
}

func assignRawBytes(target reflect.Value, payload []byte) error {
	rawBytes := make([]byte, len(payload))
	copy(rawBytes, payload)
	source := reflect.ValueOf(rawBytes)
	if target.Kind() == reflect.Interface {
		if !source.Type().AssignableTo(target.Type()) {
			return decodeTypeError("raw bytes", target.Type())
		}
		target.Set(source)
		return nil
	}
	if target.Kind() != reflect.Slice || !source.Type().ConvertibleTo(target.Type()) {
		return decodeTypeError("raw bytes", target.Type())
	}
	target.Set(source.Convert(target.Type()))
	return nil
}

func assignString(target reflect.Value, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("dex: string value is not valid UTF-8")
	}
	if target.Type() == timeType {
		dateTime, err := parseDatetime(value)
		if err != nil {
			return err
		}
		target.Set(reflect.ValueOf(dateTime))
		return nil
	}
	if target.Kind() == reflect.Interface {
		return assignInterface(target, value, "string")
	}
	if target.Kind() != reflect.String {
		return decodeTypeError("string", target.Type())
	}
	target.SetString(value)
	return nil
}

func assignInt(target reflect.Value, value int64) error {
	if target.Kind() == reflect.Interface {
		return assignInterface(target, value, "integer")
	}
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if target.OverflowInt(value) {
			return fmt.Errorf("dex: integer %d overflows %s", value, target.Type())
		}
		target.SetInt(value)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr:
		if value < 0 || target.OverflowUint(uint64(value)) {
			return fmt.Errorf("dex: integer %d overflows %s", value, target.Type())
		}
		target.SetUint(uint64(value))
		return nil
	default:
		return decodeTypeError("integer", target.Type())
	}
}

func assignFloat(target reflect.Value, value float64) error {
	if target.Kind() == reflect.Interface {
		return assignInterface(target, value, "double")
	}
	if target.Kind() != reflect.Float32 && target.Kind() != reflect.Float64 {
		return decodeTypeError("double", target.Type())
	}
	if target.OverflowFloat(value) {
		return fmt.Errorf("dex: double %v overflows %s", value, target.Type())
	}
	target.SetFloat(value)
	return nil
}

func assignBool(target reflect.Value, value bool) error {
	if target.Kind() == reflect.Interface {
		return assignInterface(target, value, "boolean")
	}
	if target.Kind() != reflect.Bool {
		return decodeTypeError("boolean", target.Type())
	}
	target.SetBool(value)
	return nil
}

func assignInterface(target reflect.Value, value any, source string) error {
	reflected := reflect.ValueOf(value)
	if !reflected.Type().AssignableTo(target.Type()) {
		return decodeTypeError(source, target.Type())
	}
	target.Set(reflected)
	return nil
}

func mapIndexType(indexType IndexType) (dexpb.IndexType, error) {
	switch indexType {
	case IndexKeyword:
		return dexpb.IndexType_INDEX_TYPE_KEYWORD, nil
	case IndexFullText:
		return dexpb.IndexType_INDEX_TYPE_TEXT, nil
	case IndexKeywordArray:
		return dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, nil
	case IndexInt:
		return dexpb.IndexType_INDEX_TYPE_INT, nil
	case IndexDouble:
		return dexpb.IndexType_INDEX_TYPE_DOUBLE, nil
	case IndexBool:
		return dexpb.IndexType_INDEX_TYPE_BOOL, nil
	case IndexDatetime:
		return dexpb.IndexType_INDEX_TYPE_DATETIME, nil
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED,
			fmt.Errorf("dex: unsupported index type %d", indexType)
	}
}

func newDeleteValue(index *AttributeIndex) (*dexpb.Value, *dexpb.IndexConfig, error) {
	value := &dexpb.Value{
		Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
	}
	if index == nil {
		return value, nil, nil
	}
	indexType, err := mapIndexType(index.Type)
	if err != nil {
		return nil, nil, err
	}
	return value, &dexpb.IndexConfig{
		Enable:   true,
		Type:     indexType,
		IndexKey: index.IndexKey,
	}, nil
}

func parseDatetime(value string) (time.Time, error) {
	dateTime, err := time.Parse(dateTimeFormat, value)
	if err == nil {
		return dateTime, nil
	}
	return time.Time{}, fmt.Errorf("dex: invalid absolute datetime %q", value)
}

func isNilValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func isStringSlice(valueType reflect.Type) bool {
	return valueType.Kind() == reflect.Slice &&
		valueType.Elem().Kind() == reflect.String
}

func incompatibleIndexValue(indexType IndexType, valueType reflect.Type) error {
	return fmt.Errorf(
		"dex: value type %s is incompatible with index type %d",
		valueType,
		indexType,
	)
}

func decodeTypeError(source string, target reflect.Type) error {
	return fmt.Errorf("dex: cannot decode %s value into %s", source, target)
}

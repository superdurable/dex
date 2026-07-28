// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package index

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/timeparser"
	"github.com/superdurable/iwf/service/common/utils"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/client"
)

// ConvertAttributeWritesToSearchAttributeUpsertMap encodes indexed AttributeWrites for Temporal/Cadence upsert.
// Writes without IndexConfig.enable (or with enable=false) are skipped.
func ConvertAttributeWritesToSearchAttributeUpsertMap(writes []*iwfpb.AttributeWrite) map[string]interface{} {
	res := map[string]interface{}{}
	for _, write := range writes {
		if write == nil {
			continue
		}
		cfg := write.GetIndexConfig()
		if cfg == nil || !cfg.GetEnable() {
			continue
		}

		key := getIndexKeyWithFallback(write)
		indexType := getIndexTypeWithFallback(write)
		if utils.IsNullValue(write.GetValue()) {
			res[key] = nil
			continue
		}
		nonNilVal := resolveNonNilIndexValue(write.GetValue(), indexType)
		if nonNilVal == nil {
			// nil means invalid
			continue
		}
		res[key] = nonNilVal
	}
	return res
}

// getIndexKeyWithFallback returns IndexConfig.index_key when set, else the attribute key.
func getIndexKeyWithFallback(write *iwfpb.AttributeWrite) string {
	if write.GetIndexConfig() != nil && write.GetIndexConfig().GetIndexKey() != "" {
		return write.GetIndexConfig().GetIndexKey()
	}
	return write.GetKey()
}

// getIndexTypeWithFallback returns the IndexType for an AttributeWrite, preferring IndexConfig then Value kind.
func getIndexTypeWithFallback(write *iwfpb.AttributeWrite) iwfpb.IndexType {
	if write.GetIndexConfig() != nil && write.GetIndexConfig().GetType() != iwfpb.IndexType_INDEX_TYPE_UNSPECIFIED {
		return write.GetIndexConfig().GetType()
	}
	switch write.GetValue().GetKind().(type) {
	case *iwfpb.Value_StringValue, *iwfpb.Value_ObjValue, *iwfpb.Value_InternalBlobIdForStringValue, *iwfpb.Value_InternalBlobIdForObjValue:
		return iwfpb.IndexType_INDEX_TYPE_KEYWORD
	case *iwfpb.Value_IntValue:
		return iwfpb.IndexType_INDEX_TYPE_INT
	case *iwfpb.Value_DoubleValue:
		return iwfpb.IndexType_INDEX_TYPE_DOUBLE
	case *iwfpb.Value_BoolValue:
		return iwfpb.IndexType_INDEX_TYPE_BOOL
	case *iwfpb.Value_NullValue:
		return iwfpb.IndexType_INDEX_TYPE_UNSPECIFIED
	default:
		return iwfpb.IndexType_INDEX_TYPE_UNSPECIFIED
	}
}

func resolveNonNilIndexValue(value *iwfpb.Value, indexType iwfpb.IndexType) interface{} {

	switch indexType {
	case iwfpb.IndexType_INDEX_TYPE_KEYWORD, iwfpb.IndexType_INDEX_TYPE_TEXT:
		switch value := value.GetKind().(type) {
		case *iwfpb.Value_StringValue:
			return value.StringValue
		case *iwfpb.Value_ObjValue:
			return string(value.ObjValue.GetPayload())
		case *iwfpb.Value_IntValue:
			return strconv.FormatInt(value.IntValue, 10)
		case *iwfpb.Value_DoubleValue:
			return strconv.FormatFloat(value.DoubleValue, 'g', -1, 64)
		case *iwfpb.Value_BoolValue:
			return strconv.FormatBool(value.BoolValue)
		default:
			return nil
		}
	case iwfpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		var serialized []byte
		switch value := value.GetKind().(type) {
		case *iwfpb.Value_StringValue:
			serialized = []byte(value.StringValue)
		case *iwfpb.Value_ObjValue:
			serialized = value.ObjValue.GetPayload()
		default:
			return nil
		}
		var values []string
		if err := json.Unmarshal(serialized, &values); err != nil {
			return nil
		}
		return values
	case iwfpb.IndexType_INDEX_TYPE_INT:
		switch value := value.GetKind().(type) {
		case *iwfpb.Value_IntValue:
			return value.IntValue
		case *iwfpb.Value_StringValue:
			parsed, err := strconv.ParseInt(value.StringValue, 10, 64)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case iwfpb.IndexType_INDEX_TYPE_DOUBLE:
		switch value := value.GetKind().(type) {
		case *iwfpb.Value_DoubleValue:
			return value.DoubleValue
		case *iwfpb.Value_StringValue:
			parsed, err := strconv.ParseFloat(value.StringValue, 64)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case iwfpb.IndexType_INDEX_TYPE_BOOL:
		switch value := value.GetKind().(type) {
		case *iwfpb.Value_BoolValue:
			return value.BoolValue
		case *iwfpb.Value_StringValue:
			parsed, err := strconv.ParseBool(value.StringValue)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case iwfpb.IndexType_INDEX_TYPE_DATETIME:
		value, ok := value.GetKind().(*iwfpb.Value_StringValue)
		if !ok {
			return nil
		}
		timestamp, err := timeparser.ParseTime(value.StringValue)
		if err != nil {
			return nil
		}
		return time.Unix(0, timestamp)
	default:
		return nil
	}
}

// MapCadenceSearchAttributeFieldsToAttrValues decodes requested Cadence indexed fields into Values.
func MapCadenceSearchAttributeFieldsToAttrValues(
	searchAttributes *shared.SearchAttributes, indexedAttrTypes map[string]iwfpb.IndexType,
) (map[string]*iwfpb.Value, error) {
	if searchAttributes == nil || len(indexedAttrTypes) == 0 {
		return nil, nil
	}
	result := make(map[string]*iwfpb.Value, len(indexedAttrTypes))
	for key, indexType := range indexedAttrTypes {
		field, ok := searchAttributes.IndexedFields[key]
		if !ok {
			continue
		}
		var object interface{}
		if err := client.NewValue(field).Get(&object); err != nil {
			return nil, err
		}
		if object == nil {
			continue
		}
		val, err := backendObjectToValue(object, indexType, true)
		if err != nil {
			return nil, err
		}
		result[key] = val
	}
	return result, nil
}

// MapTemporalSearchAttributeFieldsToAttrValues decodes requested Temporal indexed fields into Values.
func MapTemporalSearchAttributeFieldsToAttrValues(
	searchAttributes *common.SearchAttributes, indexedAttrTypes map[string]iwfpb.IndexType,
) (map[string]*iwfpb.Value, error) {
	if searchAttributes == nil || len(indexedAttrTypes) == 0 {
		return nil, nil
	}
	result := make(map[string]*iwfpb.Value, len(indexedAttrTypes))
	for key, indexType := range indexedAttrTypes {
		field, ok := searchAttributes.IndexedFields[key]
		if !ok {
			continue
		}
		var object interface{}
		if err := converter.GetDefaultDataConverter().FromPayload(field, &object); err != nil {
			return nil, err
		}
		if object == nil {
			continue
		}
		val, err := backendObjectToValue(object, indexType, false)
		if err != nil {
			return nil, err
		}
		result[key] = val
	}
	return result, nil
}

func backendObjectToValue(object interface{}, indexType iwfpb.IndexType, cadenceJSONNumber bool) (*iwfpb.Value, error) {
	switch indexType {
	case iwfpb.IndexType_INDEX_TYPE_KEYWORD, iwfpb.IndexType_INDEX_TYPE_TEXT, iwfpb.IndexType_INDEX_TYPE_DATETIME:
		s, ok := object.(string)
		if !ok {
			return nil, fmt.Errorf("expected string for %v", indexType)
		}
		return &iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: s}}, nil
	case iwfpb.IndexType_INDEX_TYPE_INT:
		switch v := object.(type) {
		case int64:
			return &iwfpb.Value{Kind: &iwfpb.Value_IntValue{IntValue: v}}, nil
		case float64:
			return &iwfpb.Value{Kind: &iwfpb.Value_IntValue{IntValue: int64(v)}}, nil
		default:
			return nil, fmt.Errorf("expected int for INT")
		}
	case iwfpb.IndexType_INDEX_TYPE_DOUBLE:
		switch v := object.(type) {
		case float64:
			return &iwfpb.Value{Kind: &iwfpb.Value_DoubleValue{DoubleValue: v}}, nil
		case int64:
			return &iwfpb.Value{Kind: &iwfpb.Value_DoubleValue{DoubleValue: float64(v)}}, nil
		default:
			return nil, fmt.Errorf("expected float for DOUBLE")
		}
	case iwfpb.IndexType_INDEX_TYPE_BOOL:
		b, ok := object.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool for BOOL")
		}
		return &iwfpb.Value{Kind: &iwfpb.Value_BoolValue{BoolValue: b}}, nil
	case iwfpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		_ = cadenceJSONNumber
		values, err := keywordArrayObjectToStrings(object)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		return &iwfpb.Value{
			Kind: &iwfpb.Value_ObjValue{
				ObjValue: &iwfpb.EncodedObject{
					Encoding: "json",
					Payload:  payload,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported index type %v", indexType)
	}
}

func keywordArrayObjectToStrings(object interface{}) ([]string, error) {
	switch v := object.(type) {
	case []string:
		return v, nil
	case []interface{}:
		values := make([]string, 0, len(v))
		for _, element := range v {
			s, ok := element.(string)
			if !ok {
				return nil, fmt.Errorf("KEYWORD_ARRAY element not string")
			}
			values = append(values, s)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("expected string array for KEYWORD_ARRAY")
	}
}

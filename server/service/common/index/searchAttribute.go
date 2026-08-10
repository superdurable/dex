// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/timeparser"
	"github.com/superdurable/dex/service/common/utils"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/client"
	"google.golang.org/protobuf/types/known/structpb"
)

// ConvertAttributeWritesToSearchAttributeUpsertMap encodes indexed AttributeWrites for Temporal/Cadence upsert.
// Writes without IndexConfig.enable (or with enable=false) are skipped.
func ConvertAttributeWritesToSearchAttributeUpsertMap(writes []*dexpb.AttributeWrite) map[string]interface{} {
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
func getIndexKeyWithFallback(write *dexpb.AttributeWrite) string {
	if write.GetIndexConfig() != nil && write.GetIndexConfig().GetIndexKey() != "" {
		return write.GetIndexConfig().GetIndexKey()
	}
	return write.GetKey()
}

// getIndexTypeWithFallback returns the IndexType for an AttributeWrite, preferring IndexConfig then Value kind.
func getIndexTypeWithFallback(write *dexpb.AttributeWrite) dexpb.IndexType {
	if write.GetIndexConfig() != nil && write.GetIndexConfig().GetType() != dexpb.IndexType_INDEX_TYPE_UNSPECIFIED {
		return write.GetIndexConfig().GetType()
	}
	switch write.GetValue().GetKind().(type) {
	case *dexpb.Value_StringValue, *dexpb.Value_ObjValue, *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
		return dexpb.IndexType_INDEX_TYPE_KEYWORD
	case *dexpb.Value_IntValue:
		return dexpb.IndexType_INDEX_TYPE_INT
	case *dexpb.Value_DoubleValue:
		return dexpb.IndexType_INDEX_TYPE_DOUBLE
	case *dexpb.Value_BoolValue:
		return dexpb.IndexType_INDEX_TYPE_BOOL
	case *dexpb.Value_NullValue:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED
	}
}

func resolveNonNilIndexValue(value *dexpb.Value, indexType dexpb.IndexType) interface{} {

	switch indexType {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_TEXT:
		switch value := value.GetKind().(type) {
		case *dexpb.Value_StringValue:
			return value.StringValue
		case *dexpb.Value_ObjValue:
			return string(value.ObjValue.GetPayload())
		case *dexpb.Value_IntValue:
			return strconv.FormatInt(value.IntValue, 10)
		case *dexpb.Value_DoubleValue:
			return strconv.FormatFloat(value.DoubleValue, 'g', -1, 64)
		case *dexpb.Value_BoolValue:
			return strconv.FormatBool(value.BoolValue)
		default:
			return nil
		}
	case dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		var serialized []byte
		switch value := value.GetKind().(type) {
		case *dexpb.Value_StringValue:
			serialized = []byte(value.StringValue)
		case *dexpb.Value_ObjValue:
			serialized = value.ObjValue.GetPayload()
		default:
			return nil
		}
		var values []string
		if err := json.Unmarshal(serialized, &values); err != nil {
			return nil
		}
		return values
	case dexpb.IndexType_INDEX_TYPE_INT:
		switch value := value.GetKind().(type) {
		case *dexpb.Value_IntValue:
			return value.IntValue
		case *dexpb.Value_StringValue:
			parsed, err := strconv.ParseInt(value.StringValue, 10, 64)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case dexpb.IndexType_INDEX_TYPE_DOUBLE:
		switch value := value.GetKind().(type) {
		case *dexpb.Value_DoubleValue:
			return value.DoubleValue
		case *dexpb.Value_StringValue:
			parsed, err := strconv.ParseFloat(value.StringValue, 64)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case dexpb.IndexType_INDEX_TYPE_BOOL:
		switch value := value.GetKind().(type) {
		case *dexpb.Value_BoolValue:
			return value.BoolValue
		case *dexpb.Value_StringValue:
			parsed, err := strconv.ParseBool(value.StringValue)
			if err != nil {
				return nil
			}
			return parsed
		default:
			return nil
		}
	case dexpb.IndexType_INDEX_TYPE_DATETIME:
		value, ok := value.GetKind().(*dexpb.Value_StringValue)
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

// MapTemporalSearchAttributeFieldsToKVs decodes all Temporal search attributes returned by visibility.
func MapTemporalSearchAttributeFieldsToKVs(
	searchAttributes *common.SearchAttributes,
) ([]*dexpb.KV, error) {
	if searchAttributes == nil {
		return nil, nil
	}
	keys := sortedKeys(searchAttributes.GetIndexedFields())
	result := make([]*dexpb.KV, 0, len(keys))
	for _, key := range keys {
		value, err := temporalSearchAttributePayloadToValue(searchAttributes.GetIndexedFields()[key])
		if err != nil {
			return nil, fmt.Errorf("decode search attribute %q: %w", key, err)
		}
		result = append(result, &dexpb.KV{Key: key, Value: value})
	}
	return result, nil
}

// MapCadenceSearchAttributeFieldsToKVs decodes all Cadence search attributes returned by visibility.
func MapCadenceSearchAttributeFieldsToKVs(searchAttributes *shared.SearchAttributes) []*dexpb.KV {
	if searchAttributes == nil {
		return nil
	}
	keys := sortedKeys(searchAttributes.GetIndexedFields())
	result := make([]*dexpb.KV, 0, len(keys))
	for _, key := range keys {
		result = append(result, &dexpb.KV{
			Key:   key,
			Value: jsonPayloadToBestEffortValue(searchAttributes.GetIndexedFields()[key], "json"),
		})
	}
	return result
}

func sortedKeys[Value any](values map[string]Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func temporalSearchAttributePayloadToValue(payload *common.Payload) (*dexpb.Value, error) {
	if payload == nil {
		return rawPayloadValue(nil, ""), nil
	}
	if indexType, ok := temporalPayloadIndexType(payload); ok {
		var object interface{}
		if err := converter.GetDefaultDataConverter().FromPayload(payload, &object); err != nil {
			return rawTemporalPayloadValue(payload), nil
		}
		if object == nil {
			return nullValue(), nil
		}
		value, err := backendObjectToValue(object, indexType, false)
		if err != nil {
			return rawTemporalPayloadValue(payload), nil
		}
		return value, nil
	}
	return jsonPayloadToBestEffortValue(payload.GetData(), temporalPayloadEncoding(payload)), nil
}

func temporalPayloadIndexType(payload *common.Payload) (dexpb.IndexType, bool) {
	valueType, err := enums.IndexedValueTypeFromString(string(payload.GetMetadata()["type"]))
	if err != nil {
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED, false
	}
	switch valueType {
	case enums.INDEXED_VALUE_TYPE_KEYWORD:
		return dexpb.IndexType_INDEX_TYPE_KEYWORD, true
	case enums.INDEXED_VALUE_TYPE_TEXT:
		return dexpb.IndexType_INDEX_TYPE_TEXT, true
	case enums.INDEXED_VALUE_TYPE_KEYWORD_LIST:
		return dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, true
	case enums.INDEXED_VALUE_TYPE_INT:
		return dexpb.IndexType_INDEX_TYPE_INT, true
	case enums.INDEXED_VALUE_TYPE_DOUBLE:
		return dexpb.IndexType_INDEX_TYPE_DOUBLE, true
	case enums.INDEXED_VALUE_TYPE_BOOL:
		return dexpb.IndexType_INDEX_TYPE_BOOL, true
	case enums.INDEXED_VALUE_TYPE_DATETIME:
		return dexpb.IndexType_INDEX_TYPE_DATETIME, true
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED, false
	}
}

func jsonPayloadToBestEffortValue(payload []byte, encoding string) *dexpb.Value {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object interface{}
	if err := decoder.Decode(&object); err != nil {
		return rawPayloadValue(payload, encoding)
	}
	return jsonObjectToValue(object, payload, encoding)
}

func jsonObjectToValue(object interface{}, payload []byte, encoding string) *dexpb.Value {
	switch value := object.(type) {
	case nil:
		return nullValue()
	case string:
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
	case json.Number:
		if intValue, err := value.Int64(); err == nil {
			return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: intValue}}
		}
		if doubleValue, err := value.Float64(); err == nil {
			return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: doubleValue}}
		}
		return rawPayloadValue(payload, encoding)
	case bool:
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value}}
	default:
		return rawPayloadValue(payload, encoding)
	}
}

func nullValue() *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
	}
}

func rawPayloadValue(payload []byte, encoding string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: encoding,
				Payload:  payload,
			},
		},
	}
}

func rawTemporalPayloadValue(payload *common.Payload) *dexpb.Value {
	return rawPayloadValue(payload.GetData(), temporalPayloadEncoding(payload))
}

func temporalPayloadEncoding(payload *common.Payload) string {
	return string(payload.GetMetadata()[converter.MetadataEncoding])
}

// MapCadenceSearchAttributeFieldsToAttrValues decodes requested Cadence indexed fields into Values.
func MapCadenceSearchAttributeFieldsToAttrValues(
	searchAttributes *shared.SearchAttributes, indexedAttrTypes map[string]dexpb.IndexType,
) (map[string]*dexpb.Value, error) {
	if searchAttributes == nil || len(indexedAttrTypes) == 0 {
		return nil, nil
	}
	result := make(map[string]*dexpb.Value, len(indexedAttrTypes))
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
	searchAttributes *common.SearchAttributes, indexedAttrTypes map[string]dexpb.IndexType,
) (map[string]*dexpb.Value, error) {
	if searchAttributes == nil || len(indexedAttrTypes) == 0 {
		return nil, nil
	}
	result := make(map[string]*dexpb.Value, len(indexedAttrTypes))
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

func backendObjectToValue(object interface{}, indexType dexpb.IndexType, cadenceJSONNumber bool) (*dexpb.Value, error) {
	switch indexType {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_TEXT, dexpb.IndexType_INDEX_TYPE_DATETIME:
		s, ok := object.(string)
		if !ok {
			return nil, fmt.Errorf("expected string for %v", indexType)
		}
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: s}}, nil
	case dexpb.IndexType_INDEX_TYPE_INT:
		switch v := object.(type) {
		case int64:
			return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: v}}, nil
		case float64:
			return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: int64(v)}}, nil
		default:
			return nil, fmt.Errorf("expected int for INT")
		}
	case dexpb.IndexType_INDEX_TYPE_DOUBLE:
		switch v := object.(type) {
		case float64:
			return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: v}}, nil
		case int64:
			return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: float64(v)}}, nil
		default:
			return nil, fmt.Errorf("expected float for DOUBLE")
		}
	case dexpb.IndexType_INDEX_TYPE_BOOL:
		b, ok := object.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool for BOOL")
		}
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: b}}, nil
	case dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		_ = cadenceJSONNumber
		values, err := keywordArrayObjectToStrings(object)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		return &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
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

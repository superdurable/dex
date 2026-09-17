// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package attributestore

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
)

func (c columnSchema) convert(value *dexpb.Value, storeType config.AttributeStoreType) (any, error) {
	if value == nil || value.GetKind() == nil {
		return nil, fmt.Errorf("value is missing")
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_NullValue:
		if !c.nullable {
			return nil, fmt.Errorf("column is not nullable")
		}
		return nil, nil
	case *dexpb.Value_StringValue:
		if c.acceptsTemporal(storeType) {
			return c.convertDatetime(kind.StringValue, storeType)
		}
		if !c.acceptsString() {
			return nil, fmt.Errorf("column does not accept strings")
		}
		if c.characterMaximum != nil && int64(utf8.RuneCountInString(kind.StringValue)) > *c.characterMaximum {
			return nil, fmt.Errorf("string exceeds column length")
		}
		return kind.StringValue, nil
	case *dexpb.Value_IntValue:
		if !c.acceptsInt(kind.IntValue, storeType) {
			return nil, fmt.Errorf("column cannot store int64 value")
		}
		return kind.IntValue, nil
	case *dexpb.Value_DoubleValue:
		if !c.acceptsDouble(storeType) || math.IsNaN(kind.DoubleValue) || math.IsInf(kind.DoubleValue, 0) {
			return nil, fmt.Errorf("column cannot store double value")
		}
		return kind.DoubleValue, nil
	case *dexpb.Value_BoolValue:
		if !c.acceptsBool(storeType) {
			return nil, fmt.Errorf("column cannot store bool value")
		}
		return kind.BoolValue, nil
	case *dexpb.Value_ObjValue:
		return c.convertObject(kind.ObjValue, storeType)
	case *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
		return nil, fmt.Errorf("blob-backed value was not hydrated")
	default:
		return nil, fmt.Errorf("unsupported Attribute value")
	}
}

func (c columnSchema) acceptsString() bool {
	switch c.dataType {
	case "character", "character varying", "text", "char", "varchar", "tinytext", "mediumtext",
		"longtext", "string":
		return true
	default:
		return false
	}
}

func (c columnSchema) acceptsInt(value int64, storeType config.AttributeStoreType) bool {
	unsigned := storeType == config.AttributeStoreTypeMySQL && strings.Contains(c.columnType, "unsigned")
	if unsigned && value < 0 {
		return false
	}
	switch c.dataType {
	case "tinyint", "byteint":
		return intInRange(value, unsigned, math.MinInt8, math.MaxInt8, math.MaxUint8)
	case "smallint":
		return intInRange(value, unsigned, math.MinInt16, math.MaxInt16, math.MaxUint16)
	case "mediumint":
		return intInRange(value, unsigned, -8388608, 8388607, 16777215)
	case "integer", "int":
		return intInRange(value, unsigned, math.MinInt32, math.MaxInt32, math.MaxUint32)
	case "bigint":
		return true
	case "number", "numeric", "decimal", "fixed":
		if c.numericScale != nil && *c.numericScale != 0 {
			return false
		}
		if c.numericPrecision == nil {
			return true
		}
		digits := len(strconv.FormatInt(value, 10))
		if value < 0 {
			digits--
		}
		return int64(digits) <= *c.numericPrecision
	default:
		return false
	}
}

func intInRange(value int64, unsigned bool, signedMinimum, signedMaximum, unsignedMaximum int64) bool {
	if unsigned {
		return value <= unsignedMaximum
	}
	return value >= signedMinimum && value <= signedMaximum
}

func (c columnSchema) acceptsDouble(storeType config.AttributeStoreType) bool {
	if storeType.IsWarehouse() {
		switch c.dataType {
		case "real", "double precision", "float", "float4", "float8", "double":
			return true
		default:
			return false
		}
	}
	switch c.dataType {
	case "real", "double precision", "float", "float4", "float8", "double", "number", "numeric",
		"decimal", "fixed":
		return true
	default:
		return false
	}
}

func (c columnSchema) acceptsBool(storeType config.AttributeStoreType) bool {
	if storeType != config.AttributeStoreTypeMySQL {
		return c.dataType == "boolean" || c.dataType == "bool"
	}
	return c.columnType == "tinyint(1)" || c.columnType == "bit(1)" ||
		c.dataType == "boolean" || c.dataType == "bool"
}

func (c columnSchema) acceptsTemporal(storeType config.AttributeStoreType) bool {
	switch storeType {
	case config.AttributeStoreTypePostgres:
		return c.dataType == "timestamp with time zone"
	case config.AttributeStoreTypeMySQL:
		return c.dataType == "datetime" || c.dataType == "timestamp"
	case config.AttributeStoreTypeDatabricks:
		return c.dataType == "timestamp" || c.dataType == "timestamp_ntz" ||
			c.dataType == "timestamp without time zone"
	case config.AttributeStoreTypeSnowflake:
		return strings.HasPrefix(c.dataType, "timestamp")
	default:
		return false
	}
}

func (c columnSchema) convertDatetime(value string, storeType config.AttributeStoreType) (any, error) {
	datetime, err := parseDatetime(value)
	if err != nil {
		return nil, err
	}
	if storeType.IsWarehouse() {
		return datetime.UTC().Format(time.RFC3339Nano), nil
	}
	return datetime, nil
}

func (c columnSchema) convertObject(
	object *dexpb.EncodedObject,
	storeType config.AttributeStoreType,
) (any, error) {
	if object == nil {
		return nil, fmt.Errorf("object is missing")
	}
	if object.GetEncoding() == "json" {
		if !json.Valid(object.GetPayload()) {
			return nil, fmt.Errorf("JSON payload is invalid")
		}
		if c.acceptsTemporal(storeType) {
			datetime, err := parseJSONDatetime(object.GetPayload())
			if err != nil {
				return nil, err
			}
			if storeType.IsWarehouse() {
				return datetime.UTC().Format(time.RFC3339Nano), nil
			}
			return datetime, nil
		}
		if !c.acceptsJSON(storeType) {
			return nil, fmt.Errorf("column does not accept JSON")
		}
		if storeType == config.AttributeStoreTypeMySQL {
			return object.GetPayload(), nil
		}
		return string(object.GetPayload()), nil
	}
	if !c.acceptsBinary(storeType) {
		return nil, fmt.Errorf("column does not accept binary objects")
	}
	if c.characterMaximum != nil && int64(len(object.GetPayload())) > *c.characterMaximum {
		return nil, fmt.Errorf("object exceeds column length")
	}
	return object.GetPayload(), nil
}

func (c columnSchema) acceptsJSON(storeType config.AttributeStoreType) bool {
	if storeType.IsWarehouse() {
		return c.dataType == "variant"
	}
	return c.dataType == "json" || c.dataType == "jsonb"
}

func parseDatetime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("datetime must use RFC3339: %w", err)
	}
	return parsed, nil
}

func parseJSONDatetime(payload []byte) (time.Time, error) {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return time.Time{}, fmt.Errorf("decode JSON datetime: %w", err)
	}
	switch datetime := value.(type) {
	case string:
		return parseDatetime(datetime)
	case json.Number:
		return parseUnixDatetime(datetime.String())
	default:
		return time.Time{}, fmt.Errorf("datetime payload must be a JSON string or epoch-seconds number")
	}
}

func parseUnixDatetime(value string) (time.Time, error) {
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	parts := strings.Split(unsigned, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return time.Time{}, fmt.Errorf("datetime epoch seconds are invalid")
	}
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("datetime epoch seconds are invalid: %w", err)
	}
	var nanoseconds int64
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 9 {
			return time.Time{}, fmt.Errorf("datetime epoch fraction must contain one to nine digits")
		}
		fraction := parts[1] + strings.Repeat("0", 9-len(parts[1]))
		nanoseconds, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("datetime epoch fraction is invalid: %w", err)
		}
	}
	if negative {
		seconds = -seconds
		nanoseconds = -nanoseconds
	}
	return time.Unix(seconds, nanoseconds), nil
}

func (c columnSchema) acceptsBinary(storeType config.AttributeStoreType) bool {
	if storeType == config.AttributeStoreTypePostgres {
		return c.dataType == "bytea"
	}
	if storeType.IsWarehouse() {
		return c.dataType == "binary" || c.dataType == "varbinary"
	}
	switch c.dataType {
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return true
	default:
		return false
	}
}

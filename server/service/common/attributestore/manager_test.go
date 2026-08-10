// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package attributestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestJitterIntervalUsesConfiguredRange(t *testing.T) {
	interval := time.Minute
	require.Equal(t, 54*time.Second, jitterInterval(interval, 0))
	require.Equal(t, 60*time.Second, jitterInterval(interval, 0.5))
	require.Equal(t, 66*time.Second, jitterInterval(interval, 1))
}

func TestColumnConversionBoundaries(t *testing.T) {
	maximum := int64(3)
	textColumn := columnSchema{dataType: "varchar", characterMaximum: &maximum}
	value, err := textColumn.convert(stringValue("三字好"), config.AttributeStoreTypePostgres)
	require.NoError(t, err)
	require.Equal(t, "三字好", value)
	_, err = textColumn.convert(stringValue("four"), config.AttributeStoreTypePostgres)
	require.ErrorContains(t, err, "length")

	integerColumn := columnSchema{dataType: "smallint"}
	_, err = integerColumn.convert(intValue(32767), config.AttributeStoreTypePostgres)
	require.NoError(t, err)
	_, err = integerColumn.convert(intValue(32768), config.AttributeStoreTypePostgres)
	require.ErrorContains(t, err, "int64")

	mediumIntegerColumn := columnSchema{dataType: "mediumint", columnType: "mediumint"}
	_, err = mediumIntegerColumn.convert(intValue(8388607), config.AttributeStoreTypeMySQL)
	require.NoError(t, err)
	_, err = mediumIntegerColumn.convert(intValue(8388608), config.AttributeStoreTypeMySQL)
	require.ErrorContains(t, err, "int64")

	unsignedIntegerColumn := columnSchema{dataType: "mediumint", columnType: "mediumint unsigned"}
	_, err = unsignedIntegerColumn.convert(intValue(16777215), config.AttributeStoreTypeMySQL)
	require.NoError(t, err)
	_, err = unsignedIntegerColumn.convert(intValue(-1), config.AttributeStoreTypeMySQL)
	require.ErrorContains(t, err, "int64")
	_, err = unsignedIntegerColumn.convert(intValue(16777216), config.AttributeStoreTypeMySQL)
	require.ErrorContains(t, err, "int64")

	boolColumn := columnSchema{dataType: "tinyint", columnType: "tinyint(1)"}
	_, err = boolColumn.convert(boolValue(true), config.AttributeStoreTypeMySQL)
	require.NoError(t, err)
	_, err = boolColumn.convert(boolValue(true), config.AttributeStoreTypePostgres)
	require.ErrorContains(t, err, "bool")

	jsonColumn := columnSchema{dataType: "jsonb"}
	_, err = jsonColumn.convert(objectValue("json", `{"valid":true}`), config.AttributeStoreTypePostgres)
	require.NoError(t, err)
	_, err = jsonColumn.convert(objectValue("json", `{invalid`), config.AttributeStoreTypePostgres)
	require.ErrorContains(t, err, "invalid")

	nonNullable := columnSchema{dataType: "text"}
	_, err = nonNullable.convert(nullValue(), config.AttributeStoreTypePostgres)
	require.ErrorContains(t, err, "nullable")
}

func TestBuildUpsertQuotesIdentifiersAndBindsValues(t *testing.T) {
	snapshot := &tableSchema{
		reference:  tableReference{namespace: "reporting", table: "flow attributes"},
		primaryKey: "FlowID",
	}
	values := map[string]filteredValue{
		"select": {value: int64(7)},
		"name":   {value: "flow"},
	}
	postgres := &storeEntry{cfg: config.AttributeStoreConfigEntry{Type: config.AttributeStoreTypePostgres}}
	query, arguments := postgres.buildUpsert(snapshot, "flow-id", values)
	require.Equal(t, `INSERT INTO "reporting"."flow attributes" ("FlowID", "name", "select") VALUES ($1, $2, $3) ON CONFLICT ("FlowID") DO UPDATE SET "name" = EXCLUDED."name", "select" = EXCLUDED."select"`, query)
	require.Equal(t, []any{"flow-id", "flow", int64(7)}, arguments)

	mysql := &storeEntry{cfg: config.AttributeStoreConfigEntry{Type: config.AttributeStoreTypeMySQL}}
	query, arguments = mysql.buildUpsert(snapshot, "flow-id", values)
	require.Equal(t, "INSERT INTO `reporting`.`flow attributes` (`FlowID`, `name`, `select`) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `select` = VALUES(`select`)", query)
	require.Equal(t, []any{"flow-id", "flow", int64(7)}, arguments)
}

func stringValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
}

func intValue(value int64) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: value}}
}

func boolValue(value bool) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value}}
}

func objectValue(encoding, payload string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: encoding,
		Payload:  []byte(payload),
	}}}
}

func nullValue() *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE}}
}

// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type namedString string
type namedInt int32
type namedStrings []namedString

type codecRecord struct {
	Name  string
	Count int
}

func TestValueCodecNativeAndJSONRoundTrips(t *testing.T) {
	testCases := []struct {
		name   string
		input  any
		target any
		assert func(*testing.T, *dexpb.Value, any)
	}{
		{
			name:   "named string",
			input:  namedString("value"),
			target: new(namedString),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, "value", value.GetStringValue())
				require.Equal(t, namedString("value"), *target.(*namedString))
			},
		},
		{
			name:   "named integer",
			input:  namedInt(42),
			target: new(namedInt),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, int64(42), value.GetIntValue())
				require.Equal(t, namedInt(42), *target.(*namedInt))
			},
		},
		{
			name:   "boolean",
			input:  true,
			target: new(bool),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.True(t, value.GetBoolValue())
				require.True(t, *target.(*bool))
			},
		},
		{
			name:   "double",
			input:  4.5,
			target: new(float64),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, 4.5, value.GetDoubleValue())
				require.Equal(t, 4.5, *target.(*float64))
			},
		},
		{
			name:   "object",
			input:  codecRecord{Name: "item", Count: 3},
			target: new(codecRecord),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, jsonEncoding, value.GetObjValue().Encoding)
				require.Equal(
					t,
					codecRecord{Name: "item", Count: 3},
					*target.(*codecRecord),
				)
			},
		},
		{
			name:   "bytes",
			input:  []byte("bytes"),
			target: new([]byte),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, jsonEncoding, value.GetObjValue().Encoding)
				require.Equal(t, []byte("bytes"), *target.(*[]byte))
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := encodeValue(testCase.input)
			require.NoError(t, err)
			require.NoError(t, decodeValue(value, testCase.target))
			testCase.assert(t, value, testCase.target)
		})
	}
}

func TestValueCodecRejectsInvalidValues(t *testing.T) {
	_, err := encodeValue(uint64(math.MaxInt64) + 1)
	require.ErrorContains(t, err, "exceeds int64")

	_, err = encodeValue(math.NaN())
	require.ErrorContains(t, err, "non-finite")

	_, err = encodeValue(make(chan int))
	require.ErrorContains(t, err, "unsupported type")

	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, err = encodeValue(cyclic)
	require.ErrorContains(t, err, "cycle")
}

func TestValueCodecNullAndDecodeValidation(t *testing.T) {
	var input *codecRecord
	value, err := encodeValue(input)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(value.GetObjValue().Payload))

	var channel chan int
	value, err = encodeValue(channel)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(value.GetObjValue().Payload))

	value, err = encodeValue(uintptr(9))
	require.NoError(t, err)
	require.Equal(t, int64(9), value.GetIntValue())

	value, err = encodeValue(input)
	require.NoError(t, err)
	target := &codecRecord{Name: "old"}
	require.NoError(t, decodeValue(value, &target))
	require.Nil(t, target)

	require.ErrorContains(t, decodeValue(value, nil), "non-nil pointer")
	require.ErrorContains(t, decodeValue(value, codecRecord{}), "non-nil pointer")
	require.ErrorContains(t, decodeValue(value, (*codecRecord)(nil)), "non-nil pointer")

	var output codecRecord
	require.ErrorContains(t, decodeValue(&dexpb.Value{}, &output), "no concrete kind")
	require.ErrorContains(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{
			InternalBlobIdForObjValue: "blob",
		}},
		&output,
	), "hydrated")
	require.ErrorContains(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "other",
				Payload:  []byte("{}"),
			},
		}},
		&output,
	), "unsupported object encoding")
}

func TestIndexedAttributeEncoding(t *testing.T) {
	dateTime := time.Date(2026, 7, 30, 10, 0, 0, 0, time.FixedZone("PDT", -7*3600))
	testCases := []struct {
		name      string
		value     any
		indexType IndexType
	}{
		{name: "keyword", value: namedString("key"), indexType: IndexKeyword},
		{name: "text", value: "text", indexType: IndexText},
		{
			name:      "keyword array",
			value:     namedStrings{"one", "two"},
			indexType: IndexKeywordArray,
		},
		{name: "integer", value: namedInt(8), indexType: IndexInt},
		{name: "double", value: float32(2.5), indexType: IndexDouble},
		{name: "boolean", value: true, indexType: IndexBool},
		{name: "datetime", value: dateTime, indexType: IndexDatetime},
		{
			name:      "datetime string",
			value:     dateTime.Format(dateTimeFormat),
			indexType: IndexDatetime,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			value, config, err := encodeAttributeValue(testCase.value, &AttributeIndex{
				Type:     testCase.indexType,
				IndexKey: "shared",
			})
			require.NoError(t, err)
			require.NotNil(t, value.Kind)
			require.True(t, config.Enable)
			require.Equal(t, "shared", config.IndexKey)
		})
	}

	_, _, err := encodeAttributeValue(1, &AttributeIndex{Type: IndexKeyword})
	require.ErrorContains(t, err, "incompatible")
	_, _, err = encodeAttributeValue("1", &AttributeIndex{Type: IndexInt})
	require.ErrorContains(t, err, "incompatible")
	_, _, err = encodeAttributeValue("tomorrow", &AttributeIndex{Type: IndexDatetime})
	require.ErrorContains(t, err, "invalid absolute datetime")
}

func TestInitialAttributeValidatesEncoding(t *testing.T) {
	attribute := DefineAttribute[string](
		"status",
		Indexed(AttributeIndex{Type: IndexKeyword}),
	)
	initial, err := Initial(attribute, "ready")
	require.NoError(t, err)
	mapped, err := mapInitialAttributes([]InitialAttribute{initial})
	require.NoError(t, err)
	require.Equal(t, "status", mapped[0].Key)
	require.Equal(t, "ready", mapped[0].Value.GetStringValue())
	require.Equal(t, dexpb.IndexType_INDEX_TYPE_KEYWORD, mapped[0].IndexConfig.Type)

	invalid := DefineAttribute[int](
		"invalid",
		Indexed(AttributeIndex{Type: IndexKeyword}),
	)
	_, err = Initial(invalid, 1)
	require.ErrorContains(t, err, "incompatible")
}

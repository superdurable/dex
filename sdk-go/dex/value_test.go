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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type namedString string
type namedInt int32
type namedStrings []namedString
type namedBytes []byte

type codecRecord struct {
	Name  string
	Count int
}

type codecInterface interface {
	Foo()
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
			input:  []byte{0xff, 0xfe},
			target: new([]byte),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, rawBytesEncoding, value.GetObjValue().Encoding)
				require.Equal(t, []byte{0xff, 0xfe}, value.GetObjValue().Payload)
				require.Equal(t, []byte{0xff, 0xfe}, *target.(*[]byte))
			},
		},
		{
			name:   "named bytes",
			input:  namedBytes{0x00, 0xff},
			target: new(namedBytes),
			assert: func(t *testing.T, value *dexpb.Value, target any) {
				require.Equal(t, rawBytesEncoding, value.GetObjValue().Encoding)
				require.Equal(t, []byte{0x00, 0xff}, value.GetObjValue().Payload)
				require.Equal(t, namedBytes{0x00, 0xff}, *target.(*namedBytes))
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

	_, err = encodeValue(string([]byte{0xff}))
	require.ErrorContains(t, err, "valid UTF-8")

	_, err = encodeValue(namedString(string([]byte{0xfe})))
	require.ErrorContains(t, err, "valid UTF-8")
	var decodedString string
	require.ErrorContains(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: string([]byte{0xfd}),
		}},
		&decodedString,
	), "valid UTF-8")

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
	var noneInput None
	noneValue, err := encodeValue(noneInput)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(noneValue.GetObjValue().Payload))
	var noneOutput None
	require.NoError(t, decodeValue(noneValue, &noneOutput))
	require.Nil(t, noneOutput)
	_, err = encodeValue(None(&none{}))
	require.ErrorContains(t, err, "must be nil")
	require.ErrorContains(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: jsonEncoding,
			Payload:  []byte("{}"),
		}}},
		&noneOutput,
	), "JSON null")

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

func TestValueDecodeRejectsIncompatibleInterfaces(t *testing.T) {
	testCases := []struct {
		name   string
		value  *dexpb.Value
		target any
	}{
		{
			name: "string to error",
			value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: "value",
			}},
			target: new(error),
		},
		{
			name: "integer to method interface",
			value: &dexpb.Value{Kind: &dexpb.Value_IntValue{
				IntValue: 1,
			}},
			target: new(codecInterface),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				err := decodeValue(testCase.value, testCase.target)
				require.ErrorContains(t, err, "cannot decode")
			})
		})
	}

	var target any
	require.NoError(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "value"}},
		&target,
	))
	require.Equal(t, "value", target)

	require.NoError(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: rawBytesEncoding,
			Payload:  []byte{0xff},
		}}},
		&target,
	))
	require.Equal(t, []byte{0xff}, target)

	var stringTarget string
	require.ErrorContains(t, decodeValue(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: rawBytesEncoding,
			Payload:  []byte{0xff},
		}}},
		&stringTarget,
	), "cannot decode raw bytes")
}

func TestIndexedAttributeEncoding(t *testing.T) {
	dateTime := time.Date(2026, 7, 30, 10, 0, 0, 123456789, time.UTC)
	testCases := []struct {
		name      string
		value     any
		indexType IndexType
	}{
		{name: "keyword", value: namedString("key"), indexType: IndexKeyword},
		{name: "full text", value: "text", indexType: IndexFullText},
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
			value:     "2026-07-30T10:00:00.123456789Z",
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
			if testCase.indexType == IndexDatetime {
				require.Equal(t, dateTime.Format(time.RFC3339Nano), value.GetStringValue())
				var decoded time.Time
				require.NoError(t, decodeValue(value, &decoded))
				require.Equal(t, dateTime, decoded)
			}
		})
	}

	_, _, err := encodeAttributeValue(1, &AttributeIndex{Type: IndexKeyword})
	require.ErrorContains(t, err, "incompatible")
	_, _, err = encodeAttributeValue("1", &AttributeIndex{Type: IndexInt})
	require.ErrorContains(t, err, "incompatible")
	_, _, err = encodeAttributeValue("tomorrow", &AttributeIndex{Type: IndexDatetime})
	require.ErrorContains(t, err, "invalid absolute datetime")
	_, _, err = encodeAttributeValue("20240101", &AttributeIndex{Type: IndexDatetime})
	require.ErrorContains(t, err, "invalid absolute datetime")
	_, _, err = encodeAttributeValue(
		[]string{"valid", string([]byte{0xff})},
		&AttributeIndex{Type: IndexKeywordArray},
	)
	require.ErrorContains(t, err, "index 1 is not valid UTF-8")
}

func TestInitialAttributeValidatesEncoding(t *testing.T) {
	attribute := DefineAttribute[string](
		"status",
		Indexed(AttributeIndex{Type: IndexKeyword}),
	)
	initial, err := InitialAttribute(attribute, "ready")
	require.NoError(t, err)
	mapped, err := mapInitialAttributes([]InitialAttributeDef{initial})
	require.NoError(t, err)
	require.Equal(t, "status", mapped[0].Key)
	require.Equal(t, "ready", mapped[0].Value.GetStringValue())
	require.Equal(t, dexpb.IndexType_INDEX_TYPE_KEYWORD, mapped[0].IndexConfig.Type)

	invalid := DefineAttribute[int](
		"invalid",
		Indexed(AttributeIndex{Type: IndexKeyword}),
	)
	_, err = InitialAttribute(invalid, 1)
	require.ErrorContains(t, err, "incompatible")
}

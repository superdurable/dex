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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/cadence/.gen/go/shared"
	"google.golang.org/protobuf/proto"
)

func TestMapTemporalSearchAttributeFieldsToKVs(t *testing.T) {
	searchAttributes := &common.SearchAttributes{
		IndexedFields: map[string]*common.Payload{
			"Double": {
				Metadata: map[string][]byte{
					converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
					"type":                     []byte("Double"),
				},
				Data: []byte("1"),
			},
			"Future": {
				Metadata: map[string][]byte{
					converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
					"type":                     []byte("Future"),
				},
				Data: []byte(`["a","b"]`),
			},
			"Missing": {
				Metadata: map[string][]byte{
					converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
				},
				Data: []byte("42"),
			},
			"Opaque": {
				Metadata: map[string][]byte{
					converter.MetadataEncoding: []byte(converter.MetadataEncodingBinary),
				},
				Data: []byte{0xff, 0x01},
			},
			"Wrong": {
				Metadata: map[string][]byte{
					converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
					"type":                     []byte("Bool"),
				},
				Data: []byte(`"not-bool"`),
			},
		},
	}

	actual, err := MapTemporalSearchAttributeFieldsToKVs(searchAttributes)
	require.NoError(t, err)
	require.Len(t, actual, 5)
	require.True(t, proto.Equal(&dexpb.KV{
		Key: "Double",
		Value: &dexpb.Value{
			Kind: &dexpb.Value_DoubleValue{DoubleValue: 1},
		},
	}, actual[0]))
	require.True(t, proto.Equal(&dexpb.KV{
		Key: "Future",
		Value: &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
					Encoding: converter.MetadataEncodingJSON,
					Payload:  []byte(`["a","b"]`),
				},
			},
		},
	}, actual[1]))
	require.True(t, proto.Equal(&dexpb.KV{
		Key: "Missing",
		Value: &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: 42},
		},
	}, actual[2]))
	require.True(t, proto.Equal(&dexpb.KV{
		Key: "Opaque",
		Value: &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
					Encoding: converter.MetadataEncodingBinary,
					Payload:  []byte{0xff, 0x01},
				},
			},
		},
	}, actual[3]))
	require.True(t, proto.Equal(&dexpb.KV{
		Key: "Wrong",
		Value: &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
					Encoding: converter.MetadataEncodingJSON,
					Payload:  []byte(`"not-bool"`),
				},
			},
		},
	}, actual[4]))
}

func TestMapCadenceSearchAttributeFieldsToKVs(t *testing.T) {
	searchAttributes := &shared.SearchAttributes{
		IndexedFields: map[string][]byte{
			"Array":  []byte(`["a","b"]`),
			"Bool":   []byte("true"),
			"Double": []byte("0.5"),
			"Int":    []byte("1"),
			"String": []byte(`"value"`),
		},
	}

	actual := MapCadenceSearchAttributeFieldsToKVs(searchAttributes)
	require.Len(t, actual, 5)
	require.Equal(t, "Array", actual[0].GetKey())
	require.Equal(t, "json", actual[0].GetValue().GetObjValue().GetEncoding())
	require.Equal(t, []byte(`["a","b"]`), actual[0].GetValue().GetObjValue().GetPayload())
	require.Equal(t, true, actual[1].GetValue().GetBoolValue())
	require.Equal(t, 0.5, actual[2].GetValue().GetDoubleValue())
	require.Equal(t, int64(1), actual[3].GetValue().GetIntValue())
	require.Equal(t, "value", actual[4].GetValue().GetStringValue())
}

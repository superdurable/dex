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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

type fakeValueHydrator struct {
	requests []*dexpb.Value
	values   []*dexpb.Value
	err      error
}

func (hydrator *fakeValueHydrator) Hydrate(
	_ context.Context,
	requests []*dexpb.Value,
) ([]*dexpb.Value, error) {
	hydrator.requests = requests
	return hydrator.values, hydrator.err
}

func TestHydrateValuesDeduplicatesAndPreservesOrder(t *testing.T) {
	stringBlob := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "string-blob",
		},
	}
	objectBlob := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForObjValue{
			InternalBlobIdForObjValue: "object-blob",
		},
	}
	concrete := &dexpb.Value{
		Kind: &dexpb.Value_IntValue{IntValue: 7},
	}
	hydrator := &fakeValueHydrator{
		values: []*dexpb.Value{
			{Kind: &dexpb.Value_StringValue{StringValue: "loaded"}},
			{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
				Encoding: jsonEncoding,
				Payload:  []byte(`{"value":1}`),
			}}},
		},
	}

	values, err := hydrateValues(
		context.Background(),
		hydrator,
		[]*dexpb.Value{stringBlob, concrete, stringBlob, objectBlob},
	)
	require.NoError(t, err)
	require.Len(t, hydrator.requests, 2)
	require.Equal(t, "loaded", values[0].GetStringValue())
	require.Same(t, concrete, values[1])
	require.Equal(t, "loaded", values[2].GetStringValue())
	require.JSONEq(t, `{"value":1}`, string(values[3].GetObjValue().Payload))
}

func TestHydrateValuesValidatesResponses(t *testing.T) {
	stringBlob := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "blob",
		},
	}
	_, err := hydrateValues(context.Background(), nil, []*dexpb.Value{stringBlob})
	require.ErrorContains(t, err, "require a hydrator")

	_, err = hydrateValues(
		context.Background(),
		&fakeValueHydrator{},
		[]*dexpb.Value{stringBlob},
	)
	require.ErrorContains(t, err, "returned 0 values")

	_, err = hydrateValues(
		context.Background(),
		&fakeValueHydrator{values: []*dexpb.Value{{
			Kind: &dexpb.Value_IntValue{IntValue: 1},
		}}},
		[]*dexpb.Value{stringBlob},
	)
	require.ErrorContains(t, err, "hydrated to")

	_, err = hydrateValues(
		context.Background(),
		&fakeValueHydrator{err: errors.New("load failed")},
		[]*dexpb.Value{stringBlob},
	)
	require.ErrorContains(t, err, "load failed")
}

func TestBlobCachePayloadRoundTrip(t *testing.T) {
	stringReference := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "string",
		},
	}
	stringValue := &dexpb.Value{
		Kind: &dexpb.Value_StringValue{StringValue: "payload"},
	}
	payload, err := marshalBlobCachePayload(stringReference, stringValue)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), payload)
	decoded, err := unmarshalBlobCachePayload(stringReference, payload)
	require.NoError(t, err)
	require.Equal(t, "payload", decoded.GetStringValue())
	_, err = unmarshalBlobCachePayload(stringReference, []byte{0xff})
	require.ErrorContains(t, err, "UTF-8")

	objectReference := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForObjValue{
			InternalBlobIdForObjValue: "object",
		},
	}
	objectValue := &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: jsonEncoding,
			Payload:  []byte(`{"value":1}`),
		}},
	}
	first, err := marshalBlobCachePayload(objectReference, objectValue)
	require.NoError(t, err)
	second, err := marshalBlobCachePayload(objectReference, objectValue)
	require.NoError(t, err)
	require.Equal(t, first, second)
	decoded, err = unmarshalBlobCachePayload(objectReference, first)
	require.NoError(t, err)
	require.True(t, proto.Equal(objectValue.GetObjValue(), decoded.GetObjValue()))
}

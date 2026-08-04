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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type fakeHydrationFlowServiceClient struct {
	dexpb.FlowServiceClient
	requests []*dexpb.Value
	values   map[string]*dexpb.Value
	err      error
}

func (client *fakeHydrationFlowServiceClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	client.requests = request.Values
	if client.err != nil {
		return nil, client.err
	}
	return &dexpb.LoadBlobsResponse{Values: client.values}, nil
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
	client := &fakeHydrationFlowServiceClient{
		values: map[string]*dexpb.Value{
			"string-blob": {Kind: &dexpb.Value_StringValue{StringValue: "loaded"}},
			"object-blob": {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
				Encoding: jsonEncoding,
				Payload:  []byte(`{"value":1}`),
			}}},
		},
	}
	hydrator := newValueHydrator(client, nil)
	values := []*dexpb.Value{stringBlob, concrete, stringBlob, objectBlob}

	err := hydrator.HydrateValuesInPlace(
		context.Background(),
		valuePointers(values),
	)
	require.NoError(t, err)
	require.Len(t, client.requests, 2)
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
	require.Panics(t, func() { newValueHydrator(nil, nil) })

	values := []*dexpb.Value{stringBlob}
	err := newValueHydrator(
		&fakeHydrationFlowServiceClient{},
		nil,
	).HydrateValuesInPlace(
		context.Background(), valuePointers(values),
	)
	require.ErrorContains(t, err, "omitted blob")
	require.Same(t, stringBlob, values[0])

	values = []*dexpb.Value{stringBlob}
	err = newValueHydrator(
		&fakeHydrationFlowServiceClient{values: map[string]*dexpb.Value{
			"blob": {Kind: &dexpb.Value_IntValue{IntValue: 1}},
		}},
		nil,
	).HydrateValuesInPlace(
		context.Background(), valuePointers(values),
	)
	require.ErrorContains(t, err, "hydrated to")
	require.Same(t, stringBlob, values[0])

	values = []*dexpb.Value{stringBlob}
	err = newValueHydrator(
		&fakeHydrationFlowServiceClient{err: errors.New("load failed")},
		nil,
	).HydrateValuesInPlace(
		context.Background(), valuePointers(values),
	)
	require.ErrorContains(t, err, "load failed")
	require.Same(t, stringBlob, values[0])
}

func TestBlobCachePayloadRoundTrip(t *testing.T) {
	stringBlobID := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "string",
		},
	}
	stringValue := &dexpb.Value{
		Kind: &dexpb.Value_StringValue{StringValue: "payload"},
	}
	payload, err := marshalBlobCachePayload(stringBlobID, stringValue)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), payload)
	decoded, err := unmarshalBlobCachePayload(stringBlobID, payload)
	require.NoError(t, err)
	require.Equal(t, "payload", decoded.GetStringValue())
	_, err = unmarshalBlobCachePayload(stringBlobID, []byte{0xff})
	require.ErrorContains(t, err, "UTF-8")

	objectBlobID := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForObjValue{
			InternalBlobIdForObjValue: "object",
		},
	}
	objectValue := &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: rawBytesEncoding,
			Payload:  []byte{0x00, 0xff},
		}},
	}
	first, err := marshalBlobCachePayload(objectBlobID, objectValue)
	require.NoError(t, err)
	second, err := marshalBlobCachePayload(objectBlobID, objectValue)
	require.NoError(t, err)
	require.Equal(t, first, second)
	decoded, err = unmarshalBlobCachePayload(objectBlobID, first)
	require.NoError(t, err)
	require.True(t, proto.Equal(objectValue.GetObjValue(), decoded.GetObjValue()))
}

func valuePointers(values []*dexpb.Value) []**dexpb.Value {
	pointers := make([]**dexpb.Value, len(values))
	for index := range values {
		pointers[index] = &values[index]
	}
	return pointers
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fakeHydrationFlowServiceClient struct {
	dexpb.FlowServiceClient
	requests []*dexpb.LoadBlobRequestEntry
	values   map[string]*dexpb.Value
	err      error
}

func (client *fakeHydrationFlowServiceClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	client.requests = request.Entries
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
	hydrator := newValueHydrator(client, newHydrationCache(t), nil, "flow-test")
	values := []*dexpb.Value{stringBlob, concrete, stringBlob, objectBlob}

	err := hydrator.HydrateValuesInPlace(
		context.Background(),
		valuePointers("flow-1", values),
	)
	require.NoError(t, err)
	require.Len(t, client.requests, 2)
	require.Equal(t, "loaded", values[0].GetStringValue())
	require.Same(t, concrete, values[1])
	require.Equal(t, "loaded", values[2].GetStringValue())
	require.JSONEq(t, `{"value":1}`, string(values[3].GetObjValue().Payload))
}

func TestHydrateHardDeleteAttributeUsesUpdatedValue(t *testing.T) {
	for _, blobID := range []string{"hd/attribute-object", "hd1|run-id|attribute-object"} {
		t.Run(blobID, func(t *testing.T) {
			cache := newHydrationCache(t)
			client := &fakeHydrationFlowServiceClient{values: map[string]*dexpb.Value{
				blobID: {Kind: &dexpb.Value_StringValue{StringValue: "original"}},
			}}
			hydrator := newValueHydrator(client, cache, nil, "flow-test")
			blobReference := &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: blobID,
			}}
			first := []*dexpb.Value{blobReference}
			require.NoError(t, hydrator.HydrateValuesInPlace(context.Background(), valuePointers("flow-1", first)))
			require.Equal(t, "original", first[0].GetStringValue())

			client.values[blobID] = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "updated"}}
			second := []*dexpb.Value{blobReference}
			require.NoError(t, hydrator.HydrateValuesInPlace(context.Background(), valuePointers("flow-1", second)))
			require.Equal(t, "updated", second[0].GetStringValue())
			require.Len(t, client.requests, 1)
			_, found, err := cache.Get(flowBlobCacheKey("flow-1", blobID))
			require.NoError(t, err)
			require.False(t, found)
		})
	}
}

func TestHydrateValuesValidatesResponses(t *testing.T) {
	stringBlob := &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "blob",
		},
	}
	cache := newHydrationCache(t)
	require.Panics(t, func() { newValueHydrator(nil, cache, nil, "flow-test") })
	require.Panics(t, func() {
		newValueHydrator(&fakeHydrationFlowServiceClient{}, nil, nil, "flow-test")
	})

	values := []*dexpb.Value{stringBlob}
	err := newValueHydrator(
		&fakeHydrationFlowServiceClient{},
		cache,
		nil,
		"flow-test",
	).HydrateValuesInPlace(
		context.Background(), valuePointers("flow-1", values),
	)
	require.ErrorContains(t, err, "omitted blob")
	require.Same(t, stringBlob, values[0])

	values = []*dexpb.Value{stringBlob}
	err = newValueHydrator(
		&fakeHydrationFlowServiceClient{values: map[string]*dexpb.Value{
			"blob": {Kind: &dexpb.Value_IntValue{IntValue: 1}},
		}},
		cache,
		nil,
		"flow-test",
	).HydrateValuesInPlace(
		context.Background(), valuePointers("flow-1", values),
	)
	require.ErrorContains(t, err, "hydrated to")
	require.Same(t, stringBlob, values[0])

	values = []*dexpb.Value{stringBlob}
	err = newValueHydrator(
		&fakeHydrationFlowServiceClient{err: errors.New("load failed")},
		cache,
		nil,
		"flow-test",
	).HydrateValuesInPlace(
		context.Background(), valuePointers("flow-1", values),
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

const (
	testReceiveLimitMessage    = "grpc: received message larger than max (9 vs. 5)"
	testServerSendLimitMessage = "Error, encoded message length too large: found 37748736 bytes, the limit is: 33554432 bytes"
)

var testMessageSizeFailures = []struct {
	name    string
	code    codes.Code
	message string
}{
	{name: "receive limit", code: codes.ResourceExhausted, message: testReceiveLimitMessage},
	{name: "server send limit", code: codes.OutOfRange, message: testServerSendLimitMessage},
}

// limitedHydrationFlowServiceClient fails LoadBlobs calls above maxBlobs entries, or with oversizedBlobID.
type limitedHydrationFlowServiceClient struct {
	dexpb.FlowServiceClient
	maxBlobs         int
	oversizedBlobID  string
	failure          error
	requestedBlobIDs [][]string
}

func (client *limitedHydrationFlowServiceClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	blobIDs := make([]string, 0, len(request.Entries))
	for _, entry := range request.Entries {
		blobIDs = append(blobIDs, entry.GetBlobValue().GetInternalBlobIdForStringValue())
	}
	client.requestedBlobIDs = append(client.requestedBlobIDs, blobIDs)
	if len(blobIDs) > client.maxBlobs || slices.Contains(blobIDs, client.oversizedBlobID) {
		return nil, client.failure
	}
	values := make(map[string]*dexpb.Value, len(blobIDs))
	for _, blobID := range blobIDs {
		values[blobID] = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "value-" + blobID}}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func TestIsGrpcMessageSizeErrorRequiresCodeDiagnosticAndNoDetails(t *testing.T) {
	sizeFailures := []*status.Status{
		status.New(codes.ResourceExhausted, testReceiveLimitMessage),
		status.New(codes.ResourceExhausted, "grpc: trying to send message larger than max (9 vs. 5)"),
		status.New(codes.ResourceExhausted, "grpc: received message after decompression larger than max 5"),
		status.New(codes.ResourceExhausted, "Received message larger than max (9 vs. 5)"),
		status.New(codes.ResourceExhausted, "Sent message larger than max (9 vs. 5)"),
		status.New(codes.ResourceExhausted, "Error, decoded message length too large: found 9 bytes, the limit is: 5 bytes"),
		status.New(codes.OutOfRange, testServerSendLimitMessage),
	}
	for _, sizeFailure := range sizeFailures {
		require.True(t, isGrpcMessageSizeError(sizeFailure.Err()), sizeFailure.Message())
	}
	otherFailures := []error{
		status.Error(codes.ResourceExhausted, "Bandwidth exhausted"),
		status.Error(codes.ResourceExhausted, "Frame size 20000 is larger than max frame size 16384"),
		status.Error(codes.OutOfRange, "page token out of range"),
		status.Error(codes.Internal, testReceiveLimitMessage),
		status.Error(codes.Unavailable, "down"),
		testDexStatusError(t, codes.OutOfRange, testServerSendLimitMessage),
		errors.New(testReceiveLimitMessage),
	}
	for _, otherFailure := range otherFailures {
		require.False(t, isGrpcMessageSizeError(otherFailure), otherFailure.Error())
	}
}

func TestHydrateValuesSplitsOversizedBatchInOrder(t *testing.T) {
	for _, sizeFailure := range testMessageSizeFailures {
		t.Run(sizeFailure.name, func(t *testing.T) {
			client := &limitedHydrationFlowServiceClient{
				maxBlobs: 2,
				failure:  status.Error(sizeFailure.code, sizeFailure.message),
			}
			hydrator := newValueHydrator(client, newHydrationCache(t), nil, "flow-test")
			values := testStringBlobValues(5)

			require.NoError(t, hydrator.HydrateValuesInPlace(context.Background(), valuePointers("flow-1", values)))
			for index, value := range values {
				require.Equal(t, fmt.Sprintf("value-blob-%d", index), value.GetStringValue())
			}
			require.Equal(t, [][]string{
				{"blob-0", "blob-1", "blob-2", "blob-3", "blob-4"},
				{"blob-0", "blob-1"},
				{"blob-2", "blob-3", "blob-4"},
				{"blob-2"},
				{"blob-3", "blob-4"},
			}, client.requestedBlobIDs)
		})
	}
}

func TestHydrateValuesReportsSingleOversizedBlob(t *testing.T) {
	for _, sizeFailure := range testMessageSizeFailures {
		t.Run(sizeFailure.name, func(t *testing.T) {
			client := &limitedHydrationFlowServiceClient{
				maxBlobs:        3,
				oversizedBlobID: "blob-1",
				failure:         status.Error(sizeFailure.code, sizeFailure.message),
			}
			hydrator := newValueHydrator(client, newHydrationCache(t), nil, "flow-test")
			values := testStringBlobValues(3)

			err := hydrator.HydrateValuesInPlace(context.Background(), valuePointers("flow-1", values))
			var tooLarge *BlobMessageTooLargeError
			require.ErrorAs(t, err, &tooLarge)
			require.Equal(t, "flow-1", tooLarge.FlowID)
			require.Equal(t, "blob-1", tooLarge.BlobID)
			require.Equal(t, sizeFailure.code, status.Code(tooLarge.Err))
			require.ErrorContains(t, err, `blob "blob-1"`)
			require.ErrorContains(t, err, sizeFailure.message)
			require.Equal(t, [][]string{
				{"blob-0", "blob-1", "blob-2"},
				{"blob-0"},
				{"blob-1", "blob-2"},
				{"blob-1"},
			}, client.requestedBlobIDs)
		})
	}
}

func TestHydrateValuesDoesNotSplitOtherFailures(t *testing.T) {
	otherFailures := map[string]error{
		"unavailable":                 status.Error(codes.Unavailable, "down"),
		"non-size resource exhausted": status.Error(codes.ResourceExhausted, "Bandwidth exhausted"),
		"Dex out of range":            testDexStatusError(t, codes.OutOfRange, testServerSendLimitMessage),
	}
	for name, otherFailure := range otherFailures {
		t.Run(name, func(t *testing.T) {
			client := &limitedHydrationFlowServiceClient{maxBlobs: 0, failure: otherFailure}
			hydrator := newValueHydrator(client, newHydrationCache(t), nil, "flow-test")

			err := hydrator.HydrateValuesInPlace(context.Background(), valuePointers("flow-1", testStringBlobValues(3)))
			require.ErrorIs(t, err, otherFailure)
			var tooLarge *BlobMessageTooLargeError
			require.False(t, errors.As(err, &tooLarge))
			require.Len(t, client.requestedBlobIDs, 1)
		})
	}
}

func testDexStatusError(t *testing.T, code codes.Code, message string) error {
	t.Helper()
	withDetails, err := status.New(code, message).WithDetails(&dexpb.ServiceErrorResponse{Detail: message})
	require.NoError(t, err)
	return withDetails.Err()
}

func testStringBlobValues(count int) []*dexpb.Value {
	values := make([]*dexpb.Value, count)
	for index := range values {
		values[index] = &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: fmt.Sprintf("blob-%d", index),
		}}
	}
	return values
}

func valuePointers(flowID string, values []*dexpb.Value) []flowValuePointer {
	pointers := make([]flowValuePointer, len(values))
	for index := range values {
		pointers[index] = flowValuePointer{flowID: flowID, valuePointer: &values[index]}
	}
	return pointers
}

func newHydrationCache(t *testing.T) *blobcache.Cache {
	t.Helper()
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	return cache
}

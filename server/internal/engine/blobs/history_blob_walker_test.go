package blobs

import (
	"context"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

func encodedObject(encoding string, payload []byte) *pb.Value {
	return &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
		Encoding: encoding,
		Payload:  payload,
	}}}
}

func intValuePb(v int64) *pb.Value { return &pb.Value{Kind: &pb.Value_IntValue{IntValue: v}} }

// fakeBlobStore captures every BatchInsertBlobs call so converter tests can
// assert on both the exact set of blobs written AND how many calls happened.
// Read methods return whatever was previously written.
type fakeBlobStore struct {
	calls   int
	written []p.BlobEntry
}

func (f *fakeBlobStore) BatchInsertBlobs(_ context.Context, _ int32, _, _ string, entries []p.BlobEntry) errors.CategorizedError {
	f.calls++
	f.written = append(f.written, entries...)
	return nil
}

func (f *fakeBlobStore) BatchGetBlobs(_ context.Context, _ int32, _, _ string, blobIDs []ids.BlobID) ([]p.BlobEntry, errors.CategorizedError) {
	out := make([]p.BlobEntry, 0, len(blobIDs))
	want := make(map[ids.BlobID]struct{}, len(blobIDs))
	for _, id := range blobIDs {
		want[id] = struct{}{}
	}
	for _, b := range f.written {
		if _, ok := want[b.BlobID]; ok {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeBlobStore) Close() error { return nil }

// newTestConverter builds an Uploader wired to the supplied fake BlobStore
// (test-only; engine code builds uploaders via the blobs.Blobs factory).
func newTestConverter(blobStore p.BlobStore) Uploader {
	return New(blobStore).NewUploader(0, "ns", "run")
}

// TestPbValueConverter_SingleStagesAndMutates: the Single method stages
// into the converter (no immediate write) AND mutates source pb.Value in
// place to BlobIdInternalOnly. After Done there's exactly one
// BatchInsertBlobs call.
func TestPbValueConverter_SingleStagesAndMutates(t *testing.T) {
	bs := &fakeBlobStore{}
	conv := newTestConverter(bs)
	src := encodedObject("application/json", []byte("hello"))

	got := conv.Single(src)

	require.Equal(t, p.ValueTypeBlobRef, got.Type)
	require.NotEmpty(t, got.BlobID)
	assert.Equal(t, 0, bs.calls, "no BlobStore call before Done")

	require.NoError(t, conv.SubmitOnce(context.Background()))
	require.Equal(t, 1, bs.calls)
	require.Len(t, bs.written, 1)
	assert.Equal(t, got.BlobID, bs.written[0].BlobID)
	assert.Equal(t, []byte("hello"), bs.written[0].Payload)

	ref, ok := src.Kind.(*pb.Value_EncodedObjectBlobIdInternalOnly)
	require.True(t, ok, "source pb.Value must have been mutated to BlobIdInternalOnly")
	assert.Equal(t, got.BlobID.String(), ref.EncodedObjectBlobIdInternalOnly,
		"in-place rewrite must reference the same blob_id as the returned p.Value")
}

// TestPbValueConverter_OneDoneAcrossManyMethodCalls proves the optimization
// rationale: a realistic engine call site (state map + N next steps + M
// channel publishes, each with EncodedObjects) results in EXACTLY ONE
// BatchInsertBlobs call after Done, regardless of how many converter calls
// happened.
func TestPbValueConverter_OneDoneAcrossManyMethodCalls(t *testing.T) {
	bs := &fakeBlobStore{}
	conv := newTestConverter(bs)

	// Simulates ProcessStepExecuteCompleted: 2 state entries + 3 NextSteps +
	// 2 ChannelPublishes (each with 2 values). Total 9 EncodedObjects across
	// 8 converter invocations.
	state := map[string]*pb.Value{
		"a": encodedObject("text/plain", []byte("state-a")),
		"b": encodedObject("text/plain", []byte("state-b")),
	}
	nextStepInputs := []*pb.Value{
		encodedObject("text/plain", []byte("ns-1")),
		encodedObject("text/plain", []byte("ns-2")),
		encodedObject("text/plain", []byte("ns-3")),
	}
	channelPubs := [][]*pb.Value{
		{encodedObject("text/plain", []byte("p1-a")), encodedObject("text/plain", []byte("p1-b"))},
		{encodedObject("text/plain", []byte("p2-a")), encodedObject("text/plain", []byte("p2-b"))},
	}

	_ = conv.Map(state)
	for _, in := range nextStepInputs {
		_ = conv.Single(in)
	}
	for _, vals := range channelPubs {
		_ = conv.Slice(vals)
	}

	assert.Equal(t, 0, bs.calls, "still no BlobStore call before Done")

	require.NoError(t, conv.SubmitOnce(context.Background()))
	assert.Equal(t, 1, bs.calls, "Done must consolidate into a SINGLE BatchInsertBlobs call")
	assert.Len(t, bs.written, 9, "all 9 EncodedObjects written in the same batch")
}

// TestEndToEnd_ConvertThenMarshalThenHydrate exercises the 1-pass design
// across a realistic shape: convert (in place) → proto-marshal → unmarshal →
// hydrate. Marshaled bytes are tiny (no inline blob payloads); hydrated
// result is byte-identical to original.
func TestEndToEnd_ConvertThenMarshalThenHydrate(t *testing.T) {
	bigPayload := make([]byte, 64*1024)
	for i := range bigPayload {
		bigPayload[i] = byte(i % 251)
	}
	smallPayload := []byte("smaller body")

	bs := &fakeBlobStore{}
	conv := newTestConverter(bs)
	payload := &pb.HistoryStepExecuteCompletedPayload{
		StepExeId: "step-1",
		StateToUpsert: map[string]*pb.Value{
			"big":   encodedObject("application/json", bigPayload),
			"plain": intValuePb(42),
		},
		ChannelPublish: []*pb.ChannelPublish{
			{ChannelName: "ch", Values: []*pb.Value{
				encodedObject("text/plain", smallPayload),
				intValuePb(7),
			}},
		},
	}

	_ = conv.Map(payload.StateToUpsert)
	for _, pub := range payload.ChannelPublish {
		_ = conv.Slice(pub.Values)
	}
	require.NoError(t, conv.SubmitOnce(context.Background()))

	require.Equal(t, 1, bs.calls, "single BatchInsertBlobs call")
	require.Len(t, bs.written, 2, "2 blob entries (one big, one small) in that one call")

	marshaled, marshalErr := proto.Marshal(payload)
	require.NoError(t, marshalErr)
	assert.Less(t, len(marshaled), 1024,
		"marshaled history payload should be <1KB (was bigPayload=%d bytes); got %d",
		len(bigPayload), len(marshaled))

	got := &pb.HistoryStepExecuteCompletedPayload{}
	require.NoError(t, proto.Unmarshal(marshaled, got))

	var blobIDs []ids.BlobID
	walkPbValues(got.ProtoReflect(), func(v *pb.Value) {
		if ref, ok := v.Kind.(*pb.Value_EncodedObjectBlobIdInternalOnly); ok {
			blobIDs = append(blobIDs, ids.MustParseBlobID(ref.EncodedObjectBlobIdInternalOnly))
		}
	})
	require.Len(t, blobIDs, 2)

	entries, _ := bs.BatchGetBlobs(context.Background(), 0, "ns", "run", blobIDs)
	blobMap := make(map[ids.BlobID]p.BlobEntry, len(entries))
	for _, e := range entries {
		blobMap[e.BlobID] = e
	}
	require.Nil(t, HydrateBlobRefsToEncodedObjects(got, blobMap))

	rehydratedBig := got.StateToUpsert["big"].GetEncodedObject()
	require.NotNil(t, rehydratedBig)
	assert.Equal(t, "application/json", rehydratedBig.Encoding)
	assert.Equal(t, bigPayload, rehydratedBig.Payload)

	rehydratedSmall := got.ChannelPublish[0].Values[0].GetEncodedObject()
	require.NotNil(t, rehydratedSmall)
	assert.Equal(t, "text/plain", rehydratedSmall.Encoding)
	assert.Equal(t, smallPayload, rehydratedSmall.Payload)
}

// TestPbValueConverter_DoneNoEntriesIsNoop confirms an empty converter's
// Done doesn't call BatchInsertBlobs (avoids a useless mongo round trip
// when a request has no EncodedObjects).
func TestPbValueConverter_DoneNoEntriesIsNoop(t *testing.T) {
	bs := &fakeBlobStore{}
	conv := newTestConverter(bs)

	_ = conv.Single(intValuePb(1))
	_ = conv.Slice([]*pb.Value{intValuePb(2), intValuePb(3)})
	_ = conv.Map(map[string]*pb.Value{"k": intValuePb(4)})

	require.NoError(t, conv.SubmitOnce(context.Background()))
	assert.Equal(t, 0, bs.calls, "no BatchInsertBlobs call when no EncodedObjects were staged")
}

// TestHydrate_MissingBlobReturnsError confirms the read walker fails loudly
// when a referenced blob_id is missing from the supplied map (TTL'd, lost, or
// never written): it returns an InternalError and does not mask the gap by
// rewriting the offending Value to Null.
func TestHydrate_MissingBlobReturnsError(t *testing.T) {
	missingBlobID := ids.NewBlobID()
	payload := &pb.HistoryChannelPublishPayload{
		ChannelName: "ch",
		Values: []*pb.Value{
			{Kind: &pb.Value_EncodedObjectBlobIdInternalOnly{EncodedObjectBlobIdInternalOnly: missingBlobID.String()}},
			intValuePb(99),
		},
	}
	err := HydrateBlobRefsToEncodedObjects(payload, map[ids.BlobID]p.BlobEntry{})
	require.NotNil(t, err)
	assert.True(t, err.IsInternalError())
	assert.IsType(t, (*pb.Value_EncodedObjectBlobIdInternalOnly)(nil), payload.Values[0].Kind,
		"missing ref must be left untouched, not masked as null")
	assert.IsType(t, (*pb.Value_IntValue)(nil), payload.Values[1].Kind)
}

// TestCollectBlobIDsFromHistoryEvents verifies page-level dedup: a blob_id
// referenced from N different events is collected once.
func TestCollectBlobIDsFromHistoryEvents(t *testing.T) {
	blobID1 := ids.NewBlobID()
	blobID2 := ids.NewBlobID()
	blobID3 := ids.NewBlobID()
	mkRef := func(id ids.BlobID) *pb.Value {
		return &pb.Value{Kind: &pb.Value_EncodedObjectBlobIdInternalOnly{EncodedObjectBlobIdInternalOnly: id.String()}}
	}
	events := []*pb.HistoryEvent{
		{Payload: &pb.HistoryEvent_StepExecuteCompleted{StepExecuteCompleted: &pb.HistoryStepExecuteCompletedPayload{
			StateToUpsert: map[string]*pb.Value{"a": mkRef(blobID1), "b": mkRef(blobID2)},
		}}},
		{Payload: &pb.HistoryEvent_ChannelPublish{ChannelPublish: &pb.HistoryChannelPublishPayload{
			Values: []*pb.Value{mkRef(blobID2), mkRef(blobID3)},
		}}},
	}
	var out []ids.BlobID
	CollectBlobIDsFromHistoryEvents(events, &out)
	assert.ElementsMatch(t, []ids.BlobID{blobID1, blobID2, blobID3}, out)
}

// TestHistoryEventPayload_BSONRoundTripPreservesPbValueKind is a regression
// test for the OpsFIFO row encoding pipeline: pb.Value's `Kind isValue_Kind`
// oneof interface is not round-trippable by the default mongo BSON codec
// (interface fields decode to nil). HistoryEventPayload.MarshalBSON +
// UnmarshalBSON go through proto.Marshal/Unmarshal so the variant
// (including BlobIdInternalOnly + EncodedObject) survives.
//
// Without the custom codec, this test would panic when calling
// GetEncodedObject() / GetEncodedObjectBlobIdInternalOnly() on a Value
// whose Kind decoded as nil.
func TestHistoryEventPayload_BSONRoundTripPreservesPbValueKind(t *testing.T) {
	blobID := ids.NewBlobID()
	// Build a payload that exercises every Kind variant the engine produces
	// for history payloads: BlobIdInternalOnly (the post-walker storage
	// form), primitive int, and Null. EncodedObject itself is not normally
	// stored at this layer (the converter rewrites it before BSON encode),
	// but we include it to verify the codec doesn't lose it either.
	payload := p.HistoryEventPayload{
		ChannelPublish: &pb.HistoryChannelPublishPayload{
			ChannelName: "ch",
			Values: []*pb.Value{
				{Kind: &pb.Value_EncodedObjectBlobIdInternalOnly{EncodedObjectBlobIdInternalOnly: blobID.String()}},
				intValuePb(7),
				{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}},
				{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{Encoding: "x", Payload: []byte("y")}}},
			},
		},
	}

	// Round-trip through a wrapper struct (matches OpsFIFOTaskRow.HistoryPayload).
	type wrap struct {
		P p.HistoryEventPayload `bson:"p"`
	}
	raw, err := bson.Marshal(wrap{P: payload})
	require.NoError(t, err)

	var got wrap
	require.NoError(t, bson.Unmarshal(raw, &got))

	require.NotNil(t, got.P.ChannelPublish, "ChannelPublish variant must survive BSON round-trip")
	require.Len(t, got.P.ChannelPublish.Values, 4)

	v0, ok := got.P.ChannelPublish.Values[0].Kind.(*pb.Value_EncodedObjectBlobIdInternalOnly)
	require.True(t, ok, "Values[0] Kind must round-trip as BlobIdInternalOnly")
	assert.Equal(t, blobID.String(), v0.EncodedObjectBlobIdInternalOnly)

	v1, ok := got.P.ChannelPublish.Values[1].Kind.(*pb.Value_IntValue)
	require.True(t, ok, "Values[1] Kind must round-trip as IntValue")
	assert.Equal(t, int64(7), v1.IntValue)

	_, ok = got.P.ChannelPublish.Values[2].Kind.(*pb.Value_NullValue)
	require.True(t, ok, "Values[2] Kind must round-trip as NullValue")

	v3, ok := got.P.ChannelPublish.Values[3].Kind.(*pb.Value_EncodedObject)
	require.True(t, ok, "Values[3] Kind must round-trip as EncodedObject")
	assert.Equal(t, "x", v3.EncodedObject.Encoding)
	assert.Equal(t, []byte("y"), v3.EncodedObject.Payload)
}

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
)

// fakeResolverBlobStore serves preloaded blobs and, unlike the shared
// fakeBlobStore, counts BatchGetBlobs calls and can inject a read error.
type fakeResolverBlobStore struct {
	blobs    map[ids.BlobID]p.BlobEntry
	getCalls int
	getErr   errors.CategorizedError
}

func (f *fakeResolverBlobStore) BatchInsertBlobs(context.Context, int32, string, string, []p.BlobEntry) errors.CategorizedError {
	return nil
}

func (f *fakeResolverBlobStore) BatchGetBlobs(_ context.Context, _ int32, _, _ string, blobIDs []ids.BlobID) ([]p.BlobEntry, errors.CategorizedError) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make([]p.BlobEntry, 0, len(blobIDs))
	for _, id := range blobIDs {
		if b, ok := f.blobs[id]; ok {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeResolverBlobStore) Close() error { return nil }

func blobRefVal(id ids.BlobID) p.Value { return p.Value{Type: p.ValueTypeBlobRef, BlobID: id} }
func intPersistVal(v int64) p.Value {
	return p.Value{Type: p.ValueTypeInt, IntVal: &v}
}

func newResolverForTest(store p.BlobStore, run *p.RunRow) Resolver {
	return New(store).NewResolver(run)
}

func TestResolver_ResolveStateMap_ResolvesBlobAndPrimitive(t *testing.T) {
	blobID := ids.NewBlobID()
	store := &fakeResolverBlobStore{blobs: map[ids.BlobID]p.BlobEntry{
		blobID: {BlobID: blobID, Encoding: "json", Payload: []byte(`"hi"`)},
	}}
	run := &p.RunRow{
		ShardID: 1, Namespace: "ns", ID: "run-1",
		StateMap: map[string]p.Value{"obj": blobRefVal(blobID), "n": intPersistVal(7)},
	}
	r := newResolverForTest(store, run)
	require.Nil(t, r.LoadAllForRunRow(context.Background()))

	state := r.ResolveStateMap()
	require.Len(t, state, 2)
	enc := state["obj"].GetEncodedObject()
	require.NotNil(t, enc)
	assert.Equal(t, "json", enc.Encoding)
	assert.Equal(t, []byte(`"hi"`), enc.Payload)
	assert.Equal(t, int64(7), state["n"].GetIntValue())
}

// Finding 2: empty resolutions return non-nil empty maps, consistently.
func TestResolver_EmptyResolutionsAreNonNilMaps(t *testing.T) {
	r := newResolverForTest(&fakeResolverBlobStore{}, &p.RunRow{ID: "run-1"})
	require.Nil(t, r.LoadAllForRunRow(context.Background()))

	assert.NotNil(t, r.ResolveStateMap())
	assert.Empty(t, r.ResolveStateMap())
	assert.NotNil(t, r.ResolveUnconsumedChannelMessages())
	assert.NotNil(t, r.ResolveActiveStepExecutions())
}

// A blob-ref missing from the loaded map degrades to Null, not a crash.
func TestResolver_MissingBlobResolvesToNull(t *testing.T) {
	missingBlobID := ids.NewBlobID()
	run := &p.RunRow{ID: "run-1", StateMap: map[string]p.Value{"x": blobRefVal(missingBlobID)}}
	r := newResolverForTest(&fakeResolverBlobStore{blobs: map[ids.BlobID]p.BlobEntry{}}, run)
	require.Nil(t, r.LoadAllForRunRow(context.Background()))

	_, isNull := r.ResolveSingleValue(blobRefVal(missingBlobID)).Kind.(*pb.Value_NullValue)
	assert.True(t, isNull)
}

func TestResolver_UnreceivedChannelMessages_SortedAndFiltered(t *testing.T) {
	blobID := ids.NewBlobID()
	store := &fakeResolverBlobStore{blobs: map[ids.BlobID]p.BlobEntry{
		blobID: {BlobID: blobID, Encoding: "json", Payload: []byte(`"five"`)},
	}}
	run := &p.RunRow{
		ShardID: 1, Namespace: "ns", ID: "run-1",
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{
			"chA": {{ID: 2, Value: intPersistVal(2)}, {ID: 5, Value: blobRefVal(blobID)}},
			"chB": {{ID: 3, Value: intPersistVal(3)}, {ID: 4, Value: intPersistVal(4)}},
		},
	}
	r := newResolverForTest(store, run)

	missed, err := r.LoadAndResolveUnreceivedChannelMessagesSorted(context.Background(), 2)
	require.Nil(t, err)
	require.Len(t, missed, 3) // ids 3,4,5 (id 2 filtered)
	assert.Equal(t, int64(3), missed[0].Id)
	assert.Equal(t, int64(4), missed[1].Id)
	assert.Equal(t, int64(5), missed[2].Id)
	assert.Equal(t, "chA", missed[2].ChannelName)
	assert.NotNil(t, missed[2].Value.GetEncodedObject())
}

// Hot path: when the worker is caught up, no blob fetch is issued.
func TestResolver_UnreceivedChannelMessages_CaughtUpNoFetch(t *testing.T) {
	store := &fakeResolverBlobStore{}
	run := &p.RunRow{
		ID: "run-1",
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{
			"chA": {{ID: 1, Value: intPersistVal(1)}, {ID: 2, Value: intPersistVal(2)}},
		},
	}
	r := newResolverForTest(store, run)

	missed, err := r.LoadAndResolveUnreceivedChannelMessagesSorted(context.Background(), 2)
	require.Nil(t, err)
	assert.Nil(t, missed)
	assert.Equal(t, 0, store.getCalls, "caught-up path must not fetch blobs")
}

// A blob fetch error must surface, NOT degrade to a null payload (guards the
// original cross-run multi-message bug).
func TestResolver_UnreceivedChannelMessages_FetchErrorSurfaced(t *testing.T) {
	store := &fakeResolverBlobStore{getErr: errors.NewInternalError("boom", nil)}
	blobID := ids.NewBlobID()
	run := &p.RunRow{
		ShardID: 1, Namespace: "ns", ID: "run-1",
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{
			"chA": {{ID: 5, Value: blobRefVal(blobID)}},
		},
	}
	r := newResolverForTest(store, run)

	missed, err := r.LoadAndResolveUnreceivedChannelMessagesSorted(context.Background(), 0)
	require.NotNil(t, err, "blob fetch error must surface, not degrade to null")
	assert.Nil(t, missed)
}

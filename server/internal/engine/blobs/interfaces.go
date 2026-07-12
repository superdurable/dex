// Package blobs encapsulates the EncodedObject blob lifecycle for one run:
// the write path (Uploader) stages and batch-flushes blob payloads, and the
// read path (Resolver) batch-fetches blobs and resolves persistence values
// back into proto. Both are constructed from a single Blobs factory bound to
// one BlobStore.
package blobs

import (
	"context"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// BlobsFactory constructs Uploaders and Resolvers bound to one BlobStore.
type BlobsFactory struct {
	blobStore p.BlobStore
}

func New(blobStore p.BlobStore) *BlobsFactory {
	return &BlobsFactory{blobStore: blobStore}
}

func (b *BlobsFactory) NewUploader(shardID int32, namespace, runID string) Uploader {
	return &uploaderImpl{blobStore: b.blobStore, shardID: shardID, namespace: namespace, runID: runID}
}

// NewResolver binds a resolver to run; shard/namespace/run-id are read from it.
func (b *BlobsFactory) NewResolver(run *p.RunRow) Resolver {
	return &resolverImpl{blobStore: b.blobStore, run: run}
}

// Uploader converts inbound pb.Values into persistence Values, staging
// EncodedObject payloads for a single batched blob write per RPC.
type Uploader interface {
	// Single converts one pb.Value, staging its EncodedObject (if any) and
	// mutating it in place to the internal blob-id variant.
	Single(pbVal *pb.Value) p.Value
	// Slice converts a slice of pb.Value, mutating each element in place.
	Slice(pbValues []*pb.Value) []p.Value
	// Map converts a map of pb.Value, mutating each value in place.
	Map(pbValues map[string]*pb.Value) map[string]p.Value
	// ChannelPublish converts publish payloads into per-channel messages.
	ChannelPublish(publish []*pb.ChannelPublish) map[string][]p.ChannelMessage
	// SubmitOnce flushes staged blobs in one round trip. Idempotent across
	// CAS retries within an RPC.
	SubmitOnce(ctx context.Context) errors.CategorizedError
}

// Resolver resolves a run's blob-backed persistence values into proto.
// Load batch-fetches every blob the run references; the Resolve* methods are
// then pure, synchronous transforms over the cached blob map.
type Resolver interface {
	// Load batch-fetches all blobs referenced by the run. Surfaces fetch
	// errors instead of degrading to null payloads. Idempotent.
	LoadAllForRunRow(ctx context.Context) errors.CategorizedError
	// ResolveUnreceivedChannelMessages builds the catch-up payload of channel
	// messages with id > lastReceivedID, sorted by id ASC. Self-contained: it
	// fetches only the unreceived messages' blobs (not Load's full set) and
	// returns early without any fetch when nothing is unreceived — this runs
	// on every worker call.
	LoadAndResolveUnreceivedChannelMessagesSorted(ctx context.Context, lastReceivedID int64) ([]*pb.UnreceivedChannelMessage, errors.CategorizedError)

	// ResolveStateMap resolves run.StateMap into proto values.
	ResolveStateMap() map[string]*pb.Value
	// ResolveUnconsumedChannelMessages resolves buffered channel messages.
	ResolveUnconsumedChannelMessages() map[string]*pb.ChannelMessages
	// ResolveActiveStepExecutions resolves active step executions, including
	// their non-blob proto fields (wait-for condition, retry state).
	ResolveActiveStepExecutions() map[string]*pb.ActiveStepExecution
	// ResolveSingleValue resolves one persistence value against the cache.
	ResolveSingleValue(val p.Value) *pb.Value
}

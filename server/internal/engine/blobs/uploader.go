package blobs

import (
	"context"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// uploaderImpl converts pb.Value into persistence.Value while staging
// EncodedObject blob writes into a single batch.
type uploaderImpl struct {
	blobStore p.BlobStore
	shardID   int32
	namespace string
	runID     string
	entries   []p.BlobEntry
	submitted bool
}

// Single converts a single pb.Value. See uploaderImpl docstring for the
// in-place mutation contract.
func (c *uploaderImpl) Single(pbVal *pb.Value) p.Value {
	if !isEncodedObject(pbVal) {
		return pbValueToPersistence(pbVal, ids.BlobID{})
	}
	enc := pbVal.Kind.(*pb.Value_EncodedObject).EncodedObject
	blobID := c.stage(enc)
	pbVal.Kind = &pb.Value_EncodedObjectBlobIdInternalOnly{EncodedObjectBlobIdInternalOnly: blobID.String()}
	return p.Value{Type: p.ValueTypeBlobRef, BlobID: blobID}
}

// Slice converts a slice of pb.Value. Mutates each EncodedObject element in
// place; see uploaderImpl docstring.
func (c *uploaderImpl) Slice(pbValues []*pb.Value) []p.Value {
	if len(pbValues) == 0 {
		return nil
	}
	out := make([]p.Value, len(pbValues))
	for i, val := range pbValues {
		out[i] = c.Single(val)
	}
	return out
}

// Map converts a map of pb.Value. Mutates each EncodedObject value in
// place; see uploaderImpl docstring.
func (c *uploaderImpl) Map(pbValues map[string]*pb.Value) map[string]p.Value {
	if len(pbValues) == 0 {
		return nil
	}
	out := make(map[string]p.Value, len(pbValues))
	for k, v := range pbValues {
		out[k] = c.Single(v)
	}
	return out
}

func (c *uploaderImpl) ChannelPublish(publish []*pb.ChannelPublish) map[string][]p.ChannelMessage {
	channelPubs := make(map[string][]p.ChannelMessage)
	for _, pub := range publish {
		vals := c.Slice(pub.Values)
		msgs := make([]p.ChannelMessage, len(vals))
		for i, v := range vals {
			msgs[i] = p.ChannelMessage{ID: 0, Value: v}
		}
		channelPubs[pub.ChannelName] = msgs
	}
	return channelPubs
}

// SubmitOnce commits staged blobs in one BatchInsertBlobs round trip.
// Safe to call multiple times per RPC — only the first call with pending
// entries writes; later calls are no-ops.
func (c *uploaderImpl) SubmitOnce(ctx context.Context) errors.CategorizedError {
	if c.submitted || len(c.entries) == 0 || c.blobStore == nil {
		return nil
	}
	if err := c.blobStore.BatchInsertBlobs(ctx, c.shardID, c.namespace, c.runID, c.entries); err != nil {
		return err
	}
	c.entries = nil
	c.submitted = true
	return nil
}

// stage allocates a fresh blob_id for enc and accumulates a BlobEntry to be
// flushed by SubmitOnce.
func (c *uploaderImpl) stage(enc *pb.EncodedObject) ids.BlobID {
	blobID := ids.NewBlobID()
	c.entries = append(c.entries, p.BlobEntry{
		BlobID:   blobID,
		Encoding: enc.GetEncoding(),
		Payload:  enc.GetPayload(),
	})
	return blobID
}

// pbValueToPersistence converts a proto Value to a persistence Value.
// EncodedObjects become BlobRefs (blobID supplied by the caller).
// Primitives are stored inline.
//
// EncodedObjectBlobIdInternalOnly is silently mapped to Null: the variant is
// SERVER-INTERNAL, only ever produced by Single / Slice / Map in this file,
// and must never appear on an inbound RPC. If a client maliciously sends one,
// the converter pipeline drops it here — no blob lookup, no DoS, no data leak.
func pbValueToPersistence(pbVal *pb.Value, blobID ids.BlobID) p.Value {
	if pbVal == nil {
		return p.Value{Type: p.ValueTypeNull}
	}
	switch v := pbVal.Kind.(type) {
	case *pb.Value_IntValue:
		val := v.IntValue
		return p.Value{Type: p.ValueTypeInt, IntVal: &val}
	case *pb.Value_DoubleValue:
		val := v.DoubleValue
		return p.Value{Type: p.ValueTypeDouble, DoubleVal: &val}
	case *pb.Value_BoolValue:
		val := v.BoolValue
		return p.Value{Type: p.ValueTypeBool, BoolVal: &val}
	case *pb.Value_NullValue:
		return p.Value{Type: p.ValueTypeNull}
	case *pb.Value_EncodedObject:
		return p.Value{Type: p.ValueTypeBlobRef, BlobID: blobID}
	case *pb.Value_EncodedObjectBlobIdInternalOnly:
		return p.Value{Type: p.ValueTypeNull}
	default:
		return p.Value{Type: p.ValueTypeNull}
	}
}

func isEncodedObject(pbVal *pb.Value) bool {
	if pbVal == nil {
		return false
	}
	_, ok := pbVal.Kind.(*pb.Value_EncodedObject)
	return ok
}

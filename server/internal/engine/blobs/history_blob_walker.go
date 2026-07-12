package blobs

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// valueMessageName is the fully-qualified proto name of dex.Value.
// Comparing FullName is the cheapest reliable way to detect a Value
// sub-message during a protoreflect walk.
const valueMessageName = "dex.Value"

// CollectBlobIDsFromHistoryEvents appends distinct typed blob IDs from history events.
func CollectBlobIDsFromHistoryEvents(events []*pb.HistoryEvent, out *[]ids.BlobID) {
	if len(events) == 0 || out == nil {
		return
	}
	seen := make(map[ids.BlobID]struct{})
	for _, ev := range events {
		walkPbValues(ev.ProtoReflect(), func(v *pb.Value) {
			ref, ok := v.Kind.(*pb.Value_EncodedObjectBlobIdInternalOnly)
			if !ok {
				return
			}
			// Trusted: these refs were server-minted (uploader) and round-tripped
			// through our own store; a non-UUID here is corruption → fail fast.
			blobID := ids.MustParseBlobID(ref.EncodedObjectBlobIdInternalOnly)
			if blobID.IsZero() {
				return
			}
			if _, dup := seen[blobID]; dup {
				return
			}
			seen[blobID] = struct{}{}
			*out = append(*out, blobID)
		})
	}
}

// HydrateBlobRefsToEncodedObjects walks msg recursively and rewrites every
// pb.Value_EncodedObjectBlobIdInternalOnly into pb.Value_EncodedObject
// using blobMap (blob_id -> BlobEntry). A blob_id missing from the map is
// unrecoverable data loss (TTL'd, evicted, or store data lost); it fails the
// read with an InternalError rather than silently masking the gap as null.
func HydrateBlobRefsToEncodedObjects(msg proto.Message, blobMap map[ids.BlobID]p.BlobEntry) errors.CategorizedError {
	if msg == nil {
		return nil
	}
	var missing []string
	walkPbValues(msg.ProtoReflect(), func(v *pb.Value) {
		ref, ok := v.Kind.(*pb.Value_EncodedObjectBlobIdInternalOnly)
		if !ok {
			return
		}
		// Trusted server-minted ref (see CollectBlobIDsFromHistoryEvents).
		blobID := ids.MustParseBlobID(ref.EncodedObjectBlobIdInternalOnly)
		blob, hit := blobMap[blobID]
		if !hit {
			missing = append(missing, ref.EncodedObjectBlobIdInternalOnly)
			return
		}
		v.Kind = &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
			Encoding: blob.Encoding,
			Payload:  blob.Payload,
		}}
	})
	if len(missing) > 0 {
		return errors.NewInternalError("history blob reference missing from BlobStore", nil)
	}
	return nil
}

// walkPbValues invokes fn on every concrete *pb.Value found anywhere inside
// the message tree rooted at m. It walks regular message fields, repeated
// fields, and map values, recursing into sub-messages that are not
// themselves dex.Value (which are leaves for this walker's purposes).
//
// Implementation note: protoreflect.Message gives us a generic walk; we use
// a type assertion back to the concrete Go *pb.Value at the leaf because
// the only mutation we do here (rewriting Kind) is on the concrete type.
// proto.MessageOf(...).Interface() returns the concrete Go message backing
// the protoreflect.Message, so the assertion is always safe for dex.Value.
func walkPbValues(m protoreflect.Message, fn func(*pb.Value)) {
	if m == nil || !m.IsValid() {
		return
	}
	if string(m.Descriptor().FullName()) == valueMessageName {
		// Concrete *pb.Value leaf — invoke fn and stop (Value's only nested
		// message is EncodedObject, which has no Values inside it).
		if v, ok := m.Interface().(*pb.Value); ok {
			fn(v)
		}
		return
	}
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		switch {
		case fd.IsList():
			list := val.List()
			if fd.Kind() != protoreflect.MessageKind {
				return true
			}
			for i := 0; i < list.Len(); i++ {
				walkPbValues(list.Get(i).Message(), fn)
			}
		case fd.IsMap():
			mp := val.Map()
			if fd.MapValue().Kind() != protoreflect.MessageKind {
				return true
			}
			mp.Range(func(_ protoreflect.MapKey, vv protoreflect.Value) bool {
				walkPbValues(vv.Message(), fn)
				return true
			})
		case fd.Kind() == protoreflect.MessageKind:
			walkPbValues(val.Message(), fn)
		}
		return true
	})
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package workerclient

import (
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
)

// ValidateAttributeWrites validates untrusted start Attributes and rejects server-owned blob IDs.
func ValidateAttributeWrites(attributes []*dexpb.AttributeWrite) error {
	seenKeys := make(map[string]bool, len(attributes))
	for index, attribute := range attributes {
		if attribute == nil || attribute.GetKey() == "" || attribute.GetValue() == nil ||
			attribute.GetValue().GetKind() == nil {
			return fmt.Errorf("attribute %d is invalid", index)
		}
		if seenKeys[attribute.GetKey()] {
			return fmt.Errorf("attribute keys must be unique")
		}
		seenKeys[attribute.GetKey()] = true
		if err := RejectWorkerBlobIDs(attribute.GetValue()); err != nil {
			return err
		}
	}
	return nil
}

// RejectWorkerBlobIDs rejects server-minted blob-id arms on worker responses (untrusted).
func RejectWorkerBlobIDs(values ...*dexpb.Value) error {
	for _, value := range values {
		if value == nil {
			continue
		}
		switch value.GetKind().(type) {
		case *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
			return fmt.Errorf("worker response must not contain internal_blob_id arms")
		}
	}
	return nil
}

// RejectWorkerAttributeWriteBlobIDs rejects blob-id arms on AttributeWrite values.
func RejectWorkerAttributeWriteBlobIDs(writes []*dexpb.AttributeWrite) error {
	for _, write := range writes {
		if write == nil {
			continue
		}
		if err := RejectWorkerBlobIDs(write.GetValue()); err != nil {
			return err
		}
	}
	return nil
}

// RejectWorkerKVBlobIDs rejects blob-id arms on KV values.
func RejectWorkerKVBlobIDs(kvs []*dexpb.KV) error {
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		if err := RejectWorkerBlobIDs(kv.GetValue()); err != nil {
			return err
		}
	}
	return nil
}

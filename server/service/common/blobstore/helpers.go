// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
)

// OffloadLargeAttributeWrites replaces oversized string/object Value arms with server-minted blob ids.
func OffloadLargeAttributeWrites(
	ctx context.Context,
	writes []*dexpb.AttributeWrite,
	flowId string,
	invocationId string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if !enabled || threshold <= 0 {
		return nil
	}
	for _, write := range writes {
		if write == nil || write.GetValue() == nil {
			continue
		}
		if err := offloadValue(ctx, write.Value, flowId, invocationId, threshold, blobStore); err != nil {
			return err
		}
	}
	return nil
}

// OffloadLargeKVs replaces oversized KV values with server-minted blob ids.
func OffloadLargeKVs(
	ctx context.Context,
	values []*dexpb.KV,
	flowId string,
	invocationId string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if !enabled || threshold <= 0 {
		return nil
	}
	for _, value := range values {
		if value == nil || value.GetValue() == nil {
			continue
		}
		if err := offloadValue(ctx, value.Value, flowId, invocationId, threshold, blobStore); err != nil {
			return err
		}
	}
	return nil
}

// OffloadLargeChannelMessages replaces oversized channel values with blob ids.
func OffloadLargeChannelMessages(
	ctx context.Context,
	messages []*dexpb.ChannelMessage,
	flowId string,
	invocationId string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if !enabled || threshold <= 0 {
		return nil
	}
	for _, message := range messages {
		if message == nil || message.GetValue() == nil {
			continue
		}
		if err := offloadValue(ctx, message.Value, flowId, invocationId, threshold, blobStore); err != nil {
			return err
		}
	}
	return nil
}

// OffloadLargeValue offloads a single Value when over threshold.
func OffloadLargeValue(
	ctx context.Context,
	value *dexpb.Value,
	flowId string,
	invocationId string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if !enabled || threshold <= 0 || value == nil {
		return nil
	}
	return offloadValue(ctx, value, flowId, invocationId, threshold, blobStore)
}

func offloadValue(
	ctx context.Context,
	value *dexpb.Value,
	flowId string,
	invocationId string,
	threshold int,
	blobStore BlobStore,
) error {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		if len(kind.StringValue) <= threshold {
			return nil
		}
		storeId, path, err := blobStore.WriteObject(ctx, flowId, invocationId, []byte(kind.StringValue))
		if err != nil {
			return err
		}
		blobId := formatStringBlobId(storeId, path)
		value.Kind = &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: blobId}
		return nil
	case *dexpb.Value_ObjValue:
		if kind.ObjValue == nil || len(kind.ObjValue.GetPayload()) <= threshold {
			return nil
		}
		storeId, path, err := blobStore.WriteObject(ctx, flowId, invocationId, kind.ObjValue.GetPayload())
		if err != nil {
			return err
		}
		blobId := formatObjBlobId(storeId, path, kind.ObjValue.GetEncoding())
		value.Kind = &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: blobId}
		return nil
	default:
		return nil
	}
}

// HydrateValues replaces internal_blob_id_for_* arms with concrete string/object values.
func HydrateValues(ctx context.Context, values []*dexpb.Value, blobStore BlobStore) error {
	for _, value := range values {
		if err := HydrateValue(ctx, value, blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateAttributeWrites hydrates Value arms on AttributeWrites / KVs.
func HydrateAttributeWrites(ctx context.Context, writes []*dexpb.AttributeWrite, blobStore BlobStore) error {
	for _, write := range writes {
		if write == nil {
			continue
		}
		if err := HydrateValue(ctx, write.GetValue(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateKVs hydrates Value arms on KV pairs.
func HydrateKVs(ctx context.Context, kvs []*dexpb.KV, blobStore BlobStore) error {
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		if err := HydrateValue(ctx, kv.GetValue(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

func HydrateConditionResults(
	ctx context.Context,
	results *dexpb.ConditionResults,
	blobStore BlobStore,
) error {
	for _, result := range results.GetChannelResults() {
		if err := HydrateValues(ctx, result.GetValues(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateValue hydrates a single Value in place.
func HydrateValue(ctx context.Context, value *dexpb.Value, blobStore BlobStore) error {
	if value == nil {
		return nil
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		storeId, path, _, err := parseBlobId(kind.InternalBlobIdForStringValue)
		if err != nil {
			return err
		}
		data, err := blobStore.ReadObject(ctx, storeId, path)
		if err != nil {
			return err
		}
		value.Kind = &dexpb.Value_StringValue{StringValue: string(data)}
		return nil
	case *dexpb.Value_InternalBlobIdForObjValue:
		storeId, path, encoding, err := parseBlobId(kind.InternalBlobIdForObjValue)
		if err != nil {
			return err
		}
		data, err := blobStore.ReadObject(ctx, storeId, path)
		if err != nil {
			return err
		}
		value.Kind = &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: encoding,
			Payload:  data,
		}}
		return nil
	default:
		return nil
	}
}

func formatStringBlobId(storeId, path string) string {
	return storeId + "|" + path
}

func formatObjBlobId(storeId, path, encoding string) string {
	return storeId + "|" + path + "|" + encoding
}

func parseBlobId(blobId string) (storeId, path, encoding string, err error) {
	first := -1
	for i := 0; i < len(blobId); i++ {
		if blobId[i] == '|' {
			first = i
			break
		}
	}
	if first < 0 {
		return "", "", "", fmt.Errorf("invalid blob id %q", blobId)
	}
	storeId = blobId[:first]
	rest := blobId[first+1:]
	second := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '|' {
			second = i
			break
		}
	}
	if second < 0 {
		return storeId, rest, "", nil
	}
	return storeId, rest[:second], rest[second+1:], nil
}

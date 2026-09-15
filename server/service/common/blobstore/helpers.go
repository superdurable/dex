// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
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

// HydrateFlowValues replaces Blob references with values owned by flowID.
func HydrateFlowValues(ctx context.Context, flowID string, values []*dexpb.Value, blobStore BlobStore) error {
	for _, value := range values {
		if err := HydrateValue(ctx, flowID, value, blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateAttributeWrites hydrates Value arms on AttributeWrites / KVs.
func HydrateAttributeWrites(
	ctx context.Context,
	flowID string,
	writes []*dexpb.AttributeWrite,
	blobStore BlobStore,
) error {
	for _, write := range writes {
		if write == nil {
			continue
		}
		if err := HydrateValue(ctx, flowID, write.GetValue(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateKVs hydrates Value arms on KV pairs.
func HydrateKVs(ctx context.Context, flowID string, kvs []*dexpb.KV, blobStore BlobStore) error {
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		if err := HydrateValue(ctx, flowID, kv.GetValue(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateChannelValues hydrates every pending Channel message Value.
func HydrateChannelValues(
	ctx context.Context,
	flowID string,
	channels map[string]*dexpb.ChannelValues,
	blobStore BlobStore,
) error {
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		for _, message := range channel.GetMessages() {
			if message == nil {
				continue
			}
			if err := HydrateValue(ctx, flowID, message.GetValue(), blobStore); err != nil {
				return err
			}
		}
	}
	return nil
}

func HydrateConditionResults(
	ctx context.Context,
	flowID string,
	results *dexpb.ConditionResults,
	blobStore BlobStore,
) error {
	for _, result := range results.GetChannelResults() {
		if err := HydrateFlowValues(ctx, flowID, result.GetValues(), blobStore); err != nil {
			return err
		}
	}
	return nil
}

// HydrateValue hydrates a Flow-owned Value in place.
func HydrateValue(ctx context.Context, flowID string, value *dexpb.Value, blobStore BlobStore) error {
	if value == nil {
		return nil
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		storeId, path, _, err := parseBlobId(kind.InternalBlobIdForStringValue)
		if err != nil {
			return err
		}
		data, err := blobStore.ReadObject(ctx, storeId, flowID, path)
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
		data, err := blobStore.ReadObject(ctx, storeId, flowID, path)
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

// TransferValueBlobOwnership ensures value is inline or owned by destinationFlowID.
func TransferValueBlobOwnership(
	ctx context.Context,
	value *dexpb.Value,
	sourceFlowID string,
	destinationFlowID string,
	invocationID string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if value == nil {
		return nil
	}
	if destinationFlowID == "" {
		return fmt.Errorf("destination Flow ID is required to transfer Blob ownership")
	}
	_, isBlobReference, err := blobIDFromValue(value)
	if err != nil {
		return err
	}
	if isBlobReference {
		if sourceFlowID == "" {
			return fmt.Errorf("source Flow ID is required to transfer Blob ownership")
		}
		if sourceFlowID == destinationFlowID {
			return nil
		}
		if err := HydrateValue(ctx, sourceFlowID, value, blobStore); err != nil {
			return err
		}
	}
	return OffloadLargeValue(ctx, value, destinationFlowID, invocationID, threshold, blobStore, enabled)
}

// TransferFlowResultBlobOwnership transfers every completed Step output in result.
func TransferFlowResultBlobOwnership(
	ctx context.Context,
	result *dexpb.FlowResult,
	sourceFlowID string,
	destinationFlowID string,
	invocationID string,
	threshold int,
	blobStore BlobStore,
	enabled bool,
) error {
	if result == nil {
		return nil
	}
	for _, completion := range result.GetResults() {
		if completion == nil {
			continue
		}
		if err := TransferValueBlobOwnership(
			ctx,
			completion.GetCompletedStepOutput(),
			sourceFlowID,
			destinationFlowID,
			invocationID,
			threshold,
			blobStore,
			enabled,
		); err != nil {
			return err
		}
	}
	return nil
}

func blobIDFromValue(value *dexpb.Value) (string, bool, error) {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		if kind.InternalBlobIdForStringValue == "" {
			return "", false, fmt.Errorf("Blob ID is required")
		}
		return kind.InternalBlobIdForStringValue, true, nil
	case *dexpb.Value_InternalBlobIdForObjValue:
		if kind.InternalBlobIdForObjValue == "" {
			return "", false, fmt.Errorf("Blob ID is required")
		}
		return kind.InternalBlobIdForObjValue, true, nil
	default:
		return "", false, nil
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

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package common

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
)

// BlobIdFromValue returns the internal blob id arm, if any.
func BlobIdFromValue(value *dexpb.Value) string {
	if value == nil {
		return ""
	}
	if blobId := value.GetInternalBlobIdForObjValue(); blobId != "" {
		return blobId
	}
	return value.GetInternalBlobIdForStringValue()
}

// LoadBlobsValue resolves a blob-arm Value via FlowService.LoadBlobs into a new
// concrete Value. Does not mutate value. Concrete values are returned as-is.
func LoadBlobsValue(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	value *dexpb.Value,
) (*dexpb.Value, error) {
	if value == nil {
		return nil, nil
	}
	blobId := BlobIdFromValue(value)
	if blobId == "" {
		return value, nil
	}
	if client == nil {
		return nil, fmt.Errorf("FlowServiceClient is required to LoadBlobs")
	}
	resp, err := client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Values: []*dexpb.Value{blobArmCopy(value)},
	})
	if err != nil {
		return nil, err
	}
	loaded := resp.GetValues()[blobId]
	if loaded == nil {
		return nil, fmt.Errorf("LoadBlobs returned no value for blob id %q", blobId)
	}
	return loaded, nil
}

func blobArmCopy(value *dexpb.Value) *dexpb.Value {
	if blobId := value.GetInternalBlobIdForObjValue(); blobId != "" {
		return &dexpb.Value{
			Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: blobId},
		}
	}
	return &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: value.GetInternalBlobIdForStringValue(),
		},
	}
}

// ObjPayloadString returns the obj payload as a string, resolving blob arms via LoadBlobs.
func ObjPayloadString(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	value *dexpb.Value,
) (string, error) {
	resolved, err := LoadBlobsValue(ctx, client, value)
	if err != nil {
		return "", err
	}
	if resolved == nil {
		return "", nil
	}
	if obj := resolved.GetObjValue(); obj != nil {
		return string(obj.GetPayload()), nil
	}
	return resolved.GetStringValue(), nil
}

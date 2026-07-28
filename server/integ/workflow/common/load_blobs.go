// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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

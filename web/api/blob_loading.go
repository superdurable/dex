// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"context"
	"fmt"
	"maps"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// loadBlobValues loads every entry through LoadBlobs. A batch whose response exceeds a gRPC
// message size limit is split in half, in order, until it fits or holds one entry.
//
// It returns the loaded values by blob ID, and the reason for each blob ID whose value does not
// fit in a response by itself. Any other LoadBlobs error ends the load and is returned.
func loadBlobValues(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	entries []*dexpb.LoadBlobRequestEntry,
) (map[string]*dexpb.Value, map[string]string, error) {
	values := make(map[string]*dexpb.Value, len(entries))
	unavailable := make(map[string]string)
	if err := loadBlobBatch(ctx, client, entries, values, unavailable); err != nil {
		return nil, nil, err
	}
	return values, unavailable, nil
}

func loadBlobBatch(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	entries []*dexpb.LoadBlobRequestEntry,
	values map[string]*dexpb.Value,
	unavailable map[string]string,
) error {
	if len(entries) == 0 {
		return nil
	}
	result, err := client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Entries: entries})
	if err == nil {
		maps.Copy(values, result.GetValues())
		return nil
	}
	if !isGRPCMessageSizeError(err) {
		return err
	}
	if len(entries) == 1 {
		unavailable[v2BlobID(entries[0].GetBlobValue())] = fmt.Sprintf(
			"the stored value is larger than the gRPC message limit: %s",
			status.Convert(err).Message(),
		)
		return nil
	}
	half := len(entries) / 2
	if err := loadBlobBatch(ctx, client, entries[:half], values, unavailable); err != nil {
		return err
	}
	return loadBlobBatch(ctx, client, entries[half:], values, unavailable)
}

// isGRPCMessageSizeError reports a gRPC message size rejection, which carries no Dex error
// details. The receiver's limit yields RESOURCE_EXHAUSTED, and the server's send limit yields
// OUT_OF_RANGE.
func isGRPCMessageSizeError(err error) bool {
	rpcStatus, ok := status.FromError(err)
	if !ok || len(rpcStatus.Details()) > 0 {
		return false
	}
	return rpcStatus.Code() == codes.ResourceExhausted || rpcStatus.Code() == codes.OutOfRange
}

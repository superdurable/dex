// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The server rejects more than 1000 entries while history offload is on.
const maxLoadBlobsEntries = 1000

// loadBlobValues loads every entry through LoadBlobs. A batch whose response exceeds a gRPC
// message size limit is split in half, in order, until it fits or holds one entry.
// Each request holds at most 1000 entries.
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
	for start := 0; start < len(entries); start += maxLoadBlobsEntries {
		batch := entries[start:min(start+maxLoadBlobsEntries, len(entries))]
		if err := loadBlobBatch(ctx, client, batch, values, unavailable); err != nil {
			return nil, nil, err
		}
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
		unavailable[loadBlobEntryID(entries[0])] = fmt.Sprintf(
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

// Known grpc-go and tonic size diagnostics exclude unrelated transport failures using the same status codes.
var grpcMessageSizeDiagnostics = []string{"larger than max", "message length too large"}

// isGRPCMessageSizeError reports a gRPC message size rejection: RESOURCE_EXHAUSTED or
// OUT_OF_RANGE without Dex error details, whose message is a known size diagnostic. Other
// detail-free RESOURCE_EXHAUSTED errors, such as HTTP/2 flow control resets, do not match.
func isGRPCMessageSizeError(err error) bool {
	rpcStatus, ok := status.FromError(err)
	if !ok || len(rpcStatus.Details()) > 0 {
		return false
	}
	if rpcStatus.Code() != codes.ResourceExhausted && rpcStatus.Code() != codes.OutOfRange {
		return false
	}
	for _, diagnostic := range grpcMessageSizeDiagnostics {
		if strings.Contains(rpcStatus.Message(), diagnostic) {
			return true
		}
	}
	return false
}

func loadBlobEntryID(entry *dexpb.LoadBlobRequestEntry) string {
	switch kind := entry.GetBlobValue().GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		return kind.InternalBlobIdForStringValue
	case *dexpb.Value_InternalBlobIdForObjValue:
		return kind.InternalBlobIdForObjValue
	default:
		return ""
	}
}

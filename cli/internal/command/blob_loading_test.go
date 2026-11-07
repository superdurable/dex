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
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// sizeLimitedBlobClient answers LoadBlobs like a server whose responses must fit maxResponseBytes.
type sizeLimitedBlobClient struct {
	dexpb.FlowServiceClient
	blobs            map[string]*dexpb.Value
	maxResponseBytes int
	tooLargeCode     codes.Code
	failure          error
	batchSizes       []int
}

func (client *sizeLimitedBlobClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	client.batchSizes = append(client.batchSizes, len(request.GetEntries()))
	if client.failure != nil {
		return nil, client.failure
	}
	response := &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{}}
	for _, entry := range request.GetEntries() {
		blobID := loadBlobEntryID(entry)
		if value, ok := client.blobs[blobID]; ok {
			response.Values[blobID] = value
		}
	}
	if size := proto.Size(response); size > client.maxResponseBytes {
		return nil, status.Errorf(client.tooLargeCode, "message of %d bytes exceeds the limit", size)
	}
	return response, nil
}

func textBlob(length int) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: strings.Repeat("v", length)}}
}

func stringBlobReferences(ids ...string) map[string]blobReference {
	references := make(map[string]blobReference, len(ids))
	for _, id := range ids {
		reference := blobReference{id: id, kind: "string"}
		references[blobReferenceKey(reference)] = reference
	}
	return references
}

func TestHydrateBlobReferencesSplitsBatchesLargerThanTheMessageLimit(t *testing.T) {
	for _, tooLargeCode := range []codes.Code{codes.ResourceExhausted, codes.OutOfRange} {
		t.Run(tooLargeCode.String(), func(t *testing.T) {
			client := &sizeLimitedBlobClient{
				blobs: map[string]*dexpb.Value{
					"input":       textBlob(300),
					"attribute":   textBlob(600),
					"publication": textBlob(600),
					"oversized":   textBlob(2000),
				},
				maxResponseBytes: 1000,
				tooLargeCode:     tooLargeCode,
			}
			references := stringBlobReferences("input", "attribute", "publication", "oversized")
			replacements, warnings := hydrateBlobReferences(
				context.Background(), client, "flow-1", references,
			)
			for _, id := range []string{"input", "attribute", "publication"} {
				key := blobReferenceKey(blobReference{id: id, kind: "string"})
				if _, ok := replacements[key]; !ok {
					t.Errorf("value %s was not hydrated: %v", id, warnings)
				}
			}
			oversizedKey := blobReferenceKey(blobReference{id: "oversized", kind: "string"})
			if _, ok := replacements[oversizedKey]; ok {
				t.Error("the oversized value was hydrated")
			}
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0], "stored value oversized unavailable") ||
				!strings.Contains(warnings[0], "larger than the gRPC message limit") {
				t.Errorf("warnings = %v", warnings)
			}
			if client.batchSizes[0] != 4 {
				t.Errorf("first batch held %d values, want 4", client.batchSizes[0])
			}
			for _, size := range client.batchSizes[1:] {
				if size >= 4 {
					t.Errorf("a retried batch was not split: %v", client.batchSizes)
				}
			}
		})
	}
}

func TestHydrateBlobReferencesReportsErrorsOtherThanMessageSize(t *testing.T) {
	client := &sizeLimitedBlobClient{
		blobs:            map[string]*dexpb.Value{"input": textBlob(10)},
		maxResponseBytes: 1000,
		failure:          status.Error(codes.Unavailable, "server unavailable"),
	}
	_, warnings := hydrateBlobReferences(
		context.Background(), client, "flow-1", stringBlobReferences("input"),
	)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "server unavailable") {
		t.Errorf("warnings = %v", warnings)
	}
	if len(client.batchSizes) != 1 {
		t.Errorf("batches = %v, want one call without splitting", client.batchSizes)
	}
}

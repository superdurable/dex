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
	"reflect"
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
	tooLarge         func(size int) error
	failure          error
	batchSizes       []int
	batches          [][]string
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
	blobIDs := make([]string, 0, len(request.Entries))
	for _, entry := range request.Entries {
		blobIDs = append(blobIDs, loadBlobEntryID(entry))
	}
	client.batches = append(client.batches, blobIDs)
	response := &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{}}
	for _, entry := range request.GetEntries() {
		blobID := loadBlobEntryID(entry)
		if value, ok := client.blobs[blobID]; ok {
			response.Values[blobID] = value
		}
	}
	if size := proto.Size(response); size > client.maxResponseBytes {
		return nil, client.tooLarge(size)
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
	sizeErrors := map[string]func(size int) error{
		"grpc-go receive limit": func(size int) error {
			return status.Errorf(codes.ResourceExhausted, "grpc: received message larger than max (%d vs. 1000)", size)
		},
		"grpc-go send limit": func(size int) error {
			return status.Errorf(codes.ResourceExhausted, "trying to send message larger than max (%d vs. 1000)", size)
		},
		"tonic send limit": func(size int) error {
			return status.Errorf(codes.OutOfRange,
				"Error, encoded message length too large: found %d bytes, the limit is: 1000 bytes", size)
		},
	}
	for name, tooLarge := range sizeErrors {
		t.Run(name, func(t *testing.T) {
			client := &sizeLimitedBlobClient{
				blobs: map[string]*dexpb.Value{
					"input":       textBlob(300),
					"attribute":   textBlob(600),
					"publication": textBlob(600),
					"oversized":   textBlob(2000),
				},
				maxResponseBytes: 1000,
				tooLarge:         tooLarge,
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

func TestHydrateBlobReferencesReturnsErrorsOtherThanMessageSizeWithoutSplitting(t *testing.T) {
	dexDetailError, err := status.New(
		codes.ResourceExhausted,
		"grpc: received message larger than max (2000 vs. 1000)",
	).WithDetails(&dexpb.ServiceErrorResponse{Detail: "rate limited"})
	if err != nil {
		t.Fatalf("attach details: %v", err)
	}
	failures := map[string]error{
		"server unavailable": status.Error(codes.Unavailable, "server unavailable"),
		"HTTP/2 flow control reset": status.Error(codes.ResourceExhausted,
			"stream terminated by RST_STREAM with error code: FLOW_CONTROL_ERROR"),
		"Dex error with details": dexDetailError.Err(),
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			client := &sizeLimitedBlobClient{
				blobs:            map[string]*dexpb.Value{"input": textBlob(10), "other": textBlob(10)},
				maxResponseBytes: 1000,
				failure:          failure,
			}
			_, warnings := hydrateBlobReferences(
				context.Background(), client, "flow-1", stringBlobReferences("input", "other"),
			)
			wantWarning := "stored values unavailable: " + failure.Error()
			if len(warnings) != 1 || warnings[0] != wantWarning {
				t.Errorf("warnings = %v, want [%s]", warnings, wantWarning)
			}
			if len(client.batchSizes) != 1 {
				t.Errorf("batches = %v, want one call without splitting", client.batchSizes)
			}
		})
	}
}

func TestLoadBlobValuesCountBatchesPreserveSizeSplitting(t *testing.T) {
	for _, hasSizeFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(hasSizeFailure), func(t *testing.T) {
			entries := make([]*dexpb.LoadBlobRequestEntry, 2500)
			client := &sizeLimitedBlobClient{
				blobs:            make(map[string]*dexpb.Value),
				maxResponseBytes: 60000,
				tooLarge: func(size int) error {
					return status.Errorf(codes.ResourceExhausted, "message larger than max: %d", size)
				},
			}
			for index := range entries {
				blobID := fmt.Sprintf("blob-%d", index)
				entries[index] = &dexpb.LoadBlobRequestEntry{
					FlowId: "flow", BlobValue: &dexpb.Value{
						Kind: &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: blobID},
					},
				}
				payload := blobID
				if hasSizeFailure && index >= 1000 && index < 2000 {
					payload = strings.Repeat("v", 100)
				}
				client.blobs[blobID] = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: payload}}
			}
			values, unavailable, err := loadBlobValues(context.Background(), client, entries)
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != 2500 || len(unavailable) != 0 {
				t.Fatalf("values=%d unavailable=%v", len(values), unavailable)
			}
			for blobID, expected := range client.blobs {
				if !proto.Equal(expected, values[blobID]) {
					t.Fatalf("wrong value for %s", blobID)
				}
			}
			expectedRanges := [][2]int{{0, 1000}, {1000, 2000}, {2000, 2500}}
			if hasSizeFailure {
				expectedRanges = [][2]int{{0, 1000}, {1000, 2000}, {1000, 1500}, {1500, 2000}, {2000, 2500}}
			}
			expectedBatches := make([][]string, len(expectedRanges))
			for index, bounds := range expectedRanges {
				for offset := bounds[0]; offset < bounds[1]; offset++ {
					expectedBatches[index] = append(expectedBatches[index], fmt.Sprintf("blob-%d", offset))
				}
			}
			if !reflect.DeepEqual(expectedBatches, client.batches) {
				t.Fatalf("unexpected request order or batch sizes")
			}
		})
	}
}

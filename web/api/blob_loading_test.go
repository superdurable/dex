// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	mutex            sync.Mutex
	blobs            map[string]*dexpb.Value
	maxResponseBytes int
	tooLargeCode     codes.Code
	failure          error
	batches          [][]string
}

func (client *sizeLimitedBlobClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	blobIDs := make([]string, 0, len(request.GetEntries()))
	response := &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{}}
	for _, entry := range request.GetEntries() {
		blobID := v2BlobID(entry.GetBlobValue())
		blobIDs = append(blobIDs, blobID)
		if value, ok := client.blobs[blobID]; ok {
			response.Values[blobID] = value
		}
	}
	client.batches = append(client.batches, blobIDs)
	if client.failure != nil {
		return nil, client.failure
	}
	if size := proto.Size(response); size > client.maxResponseBytes {
		return nil, status.Errorf(client.tooLargeCode, "message of %d bytes exceeds the limit", size)
	}
	return response, nil
}

func textBlob(length int) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: strings.Repeat("v", length)}}
}

func postLoadBlobs(t *testing.T, client dexpb.FlowServiceClient, ids ...string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterHandlers(mux, client)
	references := make([]blobReference, 0, len(ids))
	for _, id := range ids {
		references = append(references, blobReference{ID: id, Kind: "string"})
	}
	body, err := json.Marshal(loadBlobsRequest{FlowID: "flow-1", Values: references})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/blobs/load", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func TestLoadBlobsSplitsBatchesLargerThanTheMessageLimit(t *testing.T) {
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
			recorder := postLoadBlobs(t, client, "input", "attribute", "publication", "oversized")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var result struct {
				Values      map[string]string `json:"values"`
				Unavailable map[string]string `json:"unavailable"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			for _, key := range []string{"string:input", "string:attribute", "string:publication"} {
				if _, ok := result.Values[key]; !ok {
					t.Errorf("value %s missing from %v", key, result.Values)
				}
			}
			if _, ok := result.Values["string:oversized"]; ok {
				t.Error("the oversized value was returned")
			}
			reason := result.Unavailable["string:oversized"]
			if !strings.Contains(reason, "larger than the gRPC message limit") {
				t.Errorf("unavailable reason = %q", reason)
			}
			if len(result.Unavailable) != 1 {
				t.Errorf("unavailable = %v, want only the oversized value", result.Unavailable)
			}
			client.mutex.Lock()
			defer client.mutex.Unlock()
			if got := client.batches[0]; len(got) != 4 {
				t.Errorf("first batch = %v, want every value", got)
			}
			for _, batch := range client.batches[1:] {
				if len(batch) >= 4 {
					t.Errorf("a retried batch was not split: %v", batch)
				}
			}
		})
	}
}

func TestLoadBlobsReportsErrorsOtherThanMessageSize(t *testing.T) {
	client := &sizeLimitedBlobClient{
		blobs:            map[string]*dexpb.Value{"input": textBlob(10)},
		maxResponseBytes: 1000,
		failure:          status.Error(codes.Unavailable, "server unavailable"),
	}
	recorder := postLoadBlobs(t, client, "input")
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(client.batches) != 1 {
		t.Errorf("batches = %v, want one call without splitting", client.batches)
	}
}

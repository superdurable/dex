// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// flowDefinitionListPageSize is the most Flow type names one ListFlowDefinitions page returns.
	flowDefinitionListPageSize = 1000
	// flowDefinitionReadBatchSize is how many Flow types one GetFlowDefinitions call asks for.
	flowDefinitionReadBatchSize = 16
	// flowDefinitionReadMaxBytes lets one response hold a definition at the Dex Web size limit.
	flowDefinitionReadMaxBytes = 4 * maxFlowDefinitionBytes
	// flowDefinitionListMaxPages stops a listing whose page tokens never end.
	flowDefinitionListMaxPages = 1000
)

// serverFlowDefinitionSource reads the definitions that Workers uploaded to the Dex Server.
// A server without the definition RPCs answers UNIMPLEMENTED, which means it stores no definitions.
type serverFlowDefinitionSource struct {
	client dexpb.FlowServiceClient
}

func newServerFlowDefinitionSource(client dexpb.FlowServiceClient) *serverFlowDefinitionSource {
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	return &serverFlowDefinitionSource{client: client}
}

// listFlowTypes returns every Flow type with an uploaded definition. On an error it returns the names read so far.
func (s *serverFlowDefinitionSource) listFlowTypes(ctx context.Context) ([]string, error) {
	flowTypes := make([]string, 0)
	pageToken := ""
	for page := 0; page < flowDefinitionListMaxPages; page++ {
		response, err := s.client.ListFlowDefinitions(ctx, &dexpb.ListFlowDefinitionsRequest{
			PageSize: flowDefinitionListPageSize, NextPageToken: pageToken,
		})
		if status.Code(err) == codes.Unimplemented {
			return nil, nil
		}
		if err != nil {
			return flowTypes, fmt.Errorf("list uploaded Flow definitions: %w", err)
		}
		flowTypes = append(flowTypes, response.GetFlowTypes()...)
		if response.GetNextPageToken() == "" || response.GetNextPageToken() == pageToken {
			return flowTypes, nil
		}
		pageToken = response.GetNextPageToken()
	}
	return flowTypes, fmt.Errorf("list uploaded Flow definitions: more than %d pages", flowDefinitionListMaxPages)
}

// readFlowTypes reads the uploaded documents of flowTypes; a Flow type without one is left out.
//
// When isolateErrors is true, a failed read becomes a document with readErr for each Flow type it covered,
// and the returned error is always nil. Otherwise the first failed read is returned. Either way, a definition
// too large to read becomes a document with readErr, so it never hides the others.
func (s *serverFlowDefinitionSource) readFlowTypes(
	ctx context.Context,
	flowTypes []string,
	isolateErrors bool,
) ([]flowDefinitionDocument, error) {
	documents := make([]flowDefinitionDocument, 0, len(flowTypes))
	for start := 0; start < len(flowTypes); start += flowDefinitionReadBatchSize {
		batch := flowTypes[start:min(start+flowDefinitionReadBatchSize, len(flowTypes))]
		read, err := s.readBatch(ctx, batch)
		if err != nil && !isolateErrors {
			return nil, err
		}
		if err != nil {
			for _, flowType := range batch {
				read = append(read, flowDefinitionDocument{origin: flowDefinitionOriginUploaded, flowType: flowType, readErr: err})
			}
		}
		documents = append(documents, read...)
	}
	return documents, nil
}

// readBatch splits a batch whose response would be too large, until a single Flow type is marked invalid.
func (s *serverFlowDefinitionSource) readBatch(ctx context.Context, flowTypes []string) ([]flowDefinitionDocument, error) {
	response, err := s.client.GetFlowDefinitions(ctx, &dexpb.GetFlowDefinitionsRequest{FlowTypes: flowTypes},
		grpc.MaxCallRecvMsgSize(flowDefinitionReadMaxBytes))
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		return nil, nil
	case codes.ResourceExhausted, codes.InvalidArgument:
		// A browser can name a Flow type the server rejects; that Flow type has no definition.
		if len(flowTypes) == 1 && status.Code(err) == codes.InvalidArgument {
			return nil, nil
		}
		if len(flowTypes) == 1 {
			return []flowDefinitionDocument{{
				origin: flowDefinitionOriginUploaded, flowType: flowTypes[0],
				readErr: fmt.Errorf("uploaded Flow definition %q is too large to read", flowTypes[0]),
			}}, nil
		}
		middle := len(flowTypes) / 2
		first, firstErr := s.readBatch(ctx, flowTypes[:middle])
		if firstErr != nil {
			return nil, firstErr
		}
		second, secondErr := s.readBatch(ctx, flowTypes[middle:])
		if secondErr != nil {
			return nil, secondErr
		}
		return append(first, second...), nil
	default:
		return nil, fmt.Errorf("read uploaded Flow definitions: %w", err)
	}
	requested := make(map[string]bool, len(flowTypes))
	for _, flowType := range flowTypes {
		requested[flowType] = true
	}
	documents := make([]flowDefinitionDocument, 0, len(response.GetDefinitions()))
	for _, stored := range response.GetDefinitions() {
		info := stored.GetInfo()
		// The server answers only requested Flow types; anything else would shadow another Flow type.
		if !requested[info.GetFlowType()] {
			continue
		}
		documents = append(documents, flowDefinitionDocument{
			origin:      flowDefinitionOriginUploaded,
			flowType:    info.GetFlowType(),
			data:        stored.GetContent(),
			generatedAt: optionalProtoTime(info.GetGeneratedAt().AsTime(), info.GetGeneratedAt() != nil),
			uploadedAt:  optionalProtoTime(info.GetUploadedAt().AsTime(), info.GetUploadedAt() != nil),
		})
	}
	return documents, nil
}

func optionalProtoTime(value time.Time, isSet bool) *time.Time {
	if !isSet {
		return nil
	}
	value = value.UTC()
	return &value
}

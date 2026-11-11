// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const flowDefinitionTestUploader = "integ-test/1.0"

func flowDefinitionDocument(flowType string, revision int) []byte {
	return []byte(fmt.Sprintf(
		`{"schemaVersion":"2.0","flow":{"name":%q},"steps":[{"name":"Start","revision":%d}]}`,
		flowType, revision,
	))
}

func flowDefinitionUpload(flowType string, revision int, generatedAt time.Time) *dexpb.FlowDefinitionUpload {
	return &dexpb.FlowDefinitionUpload{
		FlowType:    flowType,
		Content:     flowDefinitionDocument(flowType, revision),
		GeneratedAt: timestamppb.New(generatedAt),
	}
}

func uploadFlowDefinitions(
	t *testing.T,
	ctx context.Context,
	client dexpb.FlowServiceClient,
	isForced bool,
	uploads ...*dexpb.FlowDefinitionUpload,
) []*dexpb.FlowDefinitionUploadResult {
	t.Helper()
	response, err := client.UploadFlowDefinitions(ctx, &dexpb.UploadFlowDefinitionsRequest{
		Definitions: uploads,
		Uploader:    flowDefinitionTestUploader,
		Force:       isForced,
	})
	require.NoError(t, err)
	require.Len(t, response.GetResults(), len(uploads))
	return response.GetResults()
}

func getFlowDefinition(
	t *testing.T,
	ctx context.Context,
	client dexpb.FlowServiceClient,
	flowType string,
) *dexpb.FlowDefinition {
	t.Helper()
	response, err := client.GetFlowDefinitions(ctx, &dexpb.GetFlowDefinitionsRequest{FlowTypes: []string{flowType}})
	require.NoError(t, err)
	require.Len(t, response.GetDefinitions(), 1)
	return response.GetDefinitions()[0]
}

func listAllFlowDefinitionTypes(
	t *testing.T,
	ctx context.Context,
	client dexpb.FlowServiceClient,
	pageSize int32,
) []string {
	t.Helper()
	var flowTypes []string
	pageToken := ""
	for {
		response, err := client.ListFlowDefinitions(ctx, &dexpb.ListFlowDefinitionsRequest{
			PageSize:      pageSize,
			NextPageToken: pageToken,
		})
		require.NoError(t, err)
		require.LessOrEqual(t, len(response.GetFlowTypes()), int(pageSize))
		flowTypes = append(flowTypes, response.GetFlowTypes()...)
		if response.GetNextPageToken() == "" {
			sort.Strings(flowTypes)
			return flowTypes
		}
		pageToken = response.GetNextPageToken()
	}
}

func sha256Hex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func TestFlowDefinitionRoundTripAndReplaceRule(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	client := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	baseTime := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)

	content := []byte("\n  {\"schemaVersion\": \"2.1\", \"flow\": {\"name\": \"OrderFlow\"}, \"note\": \"é\"}\n")
	results := uploadFlowDefinitions(t, ctx, client, false, &dexpb.FlowDefinitionUpload{
		FlowType:    "OrderFlow",
		Content:     content,
		GeneratedAt: timestamppb.New(baseTime.Add(123 * time.Nanosecond)),
	})
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED, results[0].GetOutcome())
	require.Equal(t, sha256Hex(content), results[0].GetStoredDigest())
	require.True(t, results[0].GetStoredGeneratedAt().AsTime().Equal(baseTime), "the server keeps microseconds")

	stored := getFlowDefinition(t, ctx, client, "OrderFlow")
	require.Equal(t, content, stored.GetContent(), "content round-trips byte for byte")
	require.Equal(t, "OrderFlow", stored.GetInfo().GetFlowType())
	require.Equal(t, sha256Hex(content), stored.GetInfo().GetDigest())
	require.Equal(t, flowDefinitionTestUploader, stored.GetInfo().GetUploader())
	require.True(t, stored.GetInfo().GetGeneratedAt().AsTime().Equal(baseTime))
	firstUploadedAt := stored.GetInfo().GetUploadedAt().AsTime()
	require.WithinDuration(t, time.Now(), firstUploadedAt, time.Minute)

	// The same digest is UNCHANGED and writes nothing, so uploaded_at stays.
	results = uploadFlowDefinitions(t, ctx, client, false, &dexpb.FlowDefinitionUpload{
		FlowType:    "OrderFlow",
		Content:     content,
		GeneratedAt: timestamppb.New(baseTime.Add(time.Minute)),
	})
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED, results[0].GetOutcome())
	require.True(t, getFlowDefinition(t, ctx, client, "OrderFlow").GetInfo().GetUploadedAt().AsTime().Equal(firstUploadedAt))

	// Older and equal generation times keep the stored document and report it.
	for _, generatedAt := range []time.Time{baseTime.Add(-time.Minute), baseTime} {
		results = uploadFlowDefinitions(t, ctx, client, false, flowDefinitionUpload("OrderFlow", 2, generatedAt))
		require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING, results[0].GetOutcome())
		require.Equal(t, sha256Hex(content), results[0].GetStoredDigest())
		require.True(t, results[0].GetStoredGeneratedAt().AsTime().Equal(baseTime))
	}
	require.Equal(t, content, getFlowDefinition(t, ctx, client, "OrderFlow").GetContent())

	// A strictly newer generation time replaces it.
	newer := flowDefinitionUpload("OrderFlow", 3, baseTime.Add(time.Second))
	results = uploadFlowDefinitions(t, ctx, client, false, newer)
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED, results[0].GetOutcome())
	require.Equal(t, newer.GetContent(), getFlowDefinition(t, ctx, client, "OrderFlow").GetContent())

	// force rolls back to an older document; an identical forced upload is still UNCHANGED.
	rollback := flowDefinitionUpload("OrderFlow", 1, baseTime.Add(-time.Hour))
	results = uploadFlowDefinitions(t, ctx, client, true, rollback)
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED, results[0].GetOutcome())
	require.Equal(t, rollback.GetContent(), getFlowDefinition(t, ctx, client, "OrderFlow").GetContent())
	results = uploadFlowDefinitions(t, ctx, client, true, rollback)
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED, results[0].GetOutcome())

	// Get leaves out Flow types without a stored definition.
	response, err := client.GetFlowDefinitions(ctx, &dexpb.GetFlowDefinitionsRequest{FlowTypes: []string{"MissingFlow"}})
	require.NoError(t, err)
	require.Empty(t, response.GetDefinitions())
}

func TestFlowDefinitionConcurrentUploadsKeepTheNewest(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	client := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	baseTime := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)

	const uploaders = 8
	results := make([]*dexpb.FlowDefinitionUploadResult, uploaders)
	errs := make([]error, uploaders)
	var waitGroup sync.WaitGroup
	for revision := 0; revision < uploaders; revision++ {
		waitGroup.Add(1)
		go func(revision int) {
			defer waitGroup.Done()
			response, err := client.UploadFlowDefinitions(ctx, &dexpb.UploadFlowDefinitionsRequest{
				Definitions: []*dexpb.FlowDefinitionUpload{
					flowDefinitionUpload("RaceFlow", revision, baseTime.Add(time.Duration(revision)*time.Second)),
				},
				Uploader: flowDefinitionTestUploader,
			})
			errs[revision] = err
			if err == nil {
				results[revision] = response.GetResults()[0]
			}
		}(revision)
	}
	waitGroup.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	newestContent := flowDefinitionDocument("RaceFlow", uploaders-1)
	stored := getFlowDefinition(t, ctx, client, "RaceFlow")
	require.Equal(t, newestContent, stored.GetContent())
	newestGeneratedAt := baseTime.Add(time.Duration(uploaders-1) * time.Second)
	require.True(t, stored.GetInfo().GetGeneratedAt().AsTime().Equal(newestGeneratedAt))
	createdCount := 0
	for revision, result := range results {
		switch result.GetOutcome() {
		case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED:
			createdCount++
			require.Equal(t, sha256Hex(flowDefinitionDocument("RaceFlow", revision)), result.GetStoredDigest())
		case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED:
			require.Equal(t, sha256Hex(flowDefinitionDocument("RaceFlow", revision)), result.GetStoredDigest())
		case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING:
			// A kept upload saw a document at least as new as its own.
			require.False(t, result.GetStoredGeneratedAt().AsTime().Before(baseTime.Add(time.Duration(revision)*time.Second)))
		default:
			require.Failf(t, "unexpected outcome", "revision %d: %s", revision, result.GetOutcome())
		}
	}
	require.Equal(t, 1, createdCount, "exactly one upload creates the definition")
	require.NotEqual(t,
		dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING,
		results[uploaders-1].GetOutcome(),
		"the newest upload is stored",
	)
}

func TestFlowDefinitionValidationErrors(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{FlowDefinitionMaxBytes: 1024})
	client := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now()

	tooManyItems := make([]*dexpb.FlowDefinitionUpload, 257)
	for index := range tooManyItems {
		tooManyItems[index] = flowDefinitionUpload(fmt.Sprintf("Flow%d", index), 1, now)
	}
	oversized := flowDefinitionUpload("BigFlow", 1, now)
	oversized.Content = []byte(`{"schemaVersion":"2.0","flow":{"name":"BigFlow"},"pad":"` + strings.Repeat("x", 1024) + `"}`)
	missingTime := flowDefinitionUpload("NoTimeFlow", 1, now)
	missingTime.GeneratedAt = nil
	cases := map[string]*dexpb.UploadFlowDefinitionsRequest{
		"name mismatch": {Definitions: []*dexpb.FlowDefinitionUpload{{
			FlowType: "OrderFlow", Content: flowDefinitionDocument("OtherFlow", 1), GeneratedAt: timestamppb.New(now),
		}}},
		"too large": {Definitions: []*dexpb.FlowDefinitionUpload{oversized}},
		"invalid JSON": {Definitions: []*dexpb.FlowDefinitionUpload{{
			FlowType: "OrderFlow", Content: []byte(`{"schemaVersion":"2.0","flow":{"name":"OrderFlow"}`), GeneratedAt: timestamppb.New(now),
		}}},
		"not an object": {Definitions: []*dexpb.FlowDefinitionUpload{{
			FlowType: "OrderFlow", Content: []byte(`["OrderFlow"]`), GeneratedAt: timestamppb.New(now),
		}}},
		"schema version 1": {Definitions: []*dexpb.FlowDefinitionUpload{{
			FlowType: "OrderFlow", Content: []byte(`{"schemaVersion":"1.0","flow":{"name":"OrderFlow"}}`), GeneratedAt: timestamppb.New(now),
		}}},
		"too many items": {Definitions: tooManyItems},
		"future generated_at": {Definitions: []*dexpb.FlowDefinitionUpload{
			flowDefinitionUpload("FutureFlow", 1, now.Add(25*time.Hour)),
		}},
		"missing generated_at": {Definitions: []*dexpb.FlowDefinitionUpload{missingTime}},
		"duplicate type": {Definitions: []*dexpb.FlowDefinitionUpload{
			flowDefinitionUpload("DupFlow", 1, now), flowDefinitionUpload("DupFlow", 2, now),
		}},
		"empty type": {Definitions: []*dexpb.FlowDefinitionUpload{{
			FlowType: "", Content: flowDefinitionDocument("", 1), GeneratedAt: timestamppb.New(now),
		}}},
		"long uploader": {
			Definitions: []*dexpb.FlowDefinitionUpload{flowDefinitionUpload("UploaderFlow", 1, now)},
			Uploader:    strings.Repeat("u", 129),
		},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.UploadFlowDefinitions(ctx, request)
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
			require.Equal(t,
				dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_DEFINITION_INVALID,
				grpcServiceErrorResponse(t, err).GetSubStatus(),
			)
		})
	}

	// A rejected request stores nothing, not even its valid items.
	_, err := client.UploadFlowDefinitions(ctx, &dexpb.UploadFlowDefinitionsRequest{
		Definitions: []*dexpb.FlowDefinitionUpload{flowDefinitionUpload("ValidFlow", 1, now), oversized},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, listAllFlowDefinitionTypes(t, ctx, client, 100))

	for name, request := range map[string]*dexpb.GetFlowDefinitionsRequest{
		"no types":   {},
		"empty type": {FlowTypes: []string{""}},
	} {
		t.Run("get "+name, func(t *testing.T) {
			_, err := client.GetFlowDefinitions(ctx, request)
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
		})
	}
	_, err = client.ListFlowDefinitions(ctx, &dexpb.ListFlowDefinitionsRequest{PageSize: -1})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestFlowDefinitionListPagination(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	client := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now()

	flowTypes := []string{
		"alpha", "Beta", "gamma.v2", "delta-flow", "with space", "slash/name", "pct%2Fname", "é-flow", ".", "..",
	}
	uploads := make([]*dexpb.FlowDefinitionUpload, 0, len(flowTypes))
	for _, flowType := range flowTypes {
		uploads = append(uploads, flowDefinitionUpload(flowType, 1, now))
	}
	uploadFlowDefinitions(t, ctx, client, false, uploads...)

	expected := append([]string(nil), flowTypes...)
	sort.Strings(expected)
	for _, pageSize := range []int32{1, 2, 3, 1000} {
		require.Equal(t, expected, listAllFlowDefinitionTypes(t, ctx, client, pageSize), "page size %d", pageSize)
	}
	for _, flowType := range flowTypes {
		require.Equal(t, flowType, getFlowDefinition(t, ctx, client, flowType).GetInfo().GetFlowType())
	}
	response, err := client.ListFlowDefinitions(ctx, &dexpb.ListFlowDefinitionsRequest{})
	require.NoError(t, err)
	require.Len(t, response.GetFlowTypes(), len(flowTypes), "page size 0 returns everything that fits")
	require.Empty(t, response.GetNextPageToken())
}

func TestFlowDefinitionsPersistAcrossRestart(t *testing.T) {
	dataDirectory := t.TempDir()
	firstRuntime, firstProcess := startBinaryDexServiceInDirectory(t, serverZDTestConfig{}, dataDirectory)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	generatedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	upload := flowDefinitionUpload("DurableFlow", 1, generatedAt)
	uploadFlowDefinitions(t, ctx, firstRuntime.FlowClient, false, upload)
	firstProcess.stop(t)

	secondRuntime, _ := startBinaryDexServiceInDirectory(t, serverZDTestConfig{}, dataDirectory)
	require.Equal(t, []string{"DurableFlow"}, listAllFlowDefinitionTypes(t, ctx, secondRuntime.FlowClient, 10))
	stored := getFlowDefinition(t, ctx, secondRuntime.FlowClient, "DurableFlow")
	require.Equal(t, upload.GetContent(), stored.GetContent())
	require.True(t, stored.GetInfo().GetGeneratedAt().AsTime().Equal(generatedAt))
	results := uploadFlowDefinitions(t, ctx, secondRuntime.FlowClient, false, upload)
	require.Equal(t, dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED, results[0].GetOutcome())
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAttributeIndexSyncTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testAttributeIndexSync(t, service.BackendTypeTemporal)
}

func TestAttributeIndexSyncCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testAttributeIndexSync(t, service.BackendTypeCadence)
}

func testAttributeIndexSync(t *testing.T, backendType service.BackendType) {
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listed, err := runtime.UnifiedClient.ListAttributeIndexes(ctx)
	require.NoError(t, err)
	require.Equal(
		t,
		dexpb.IndexType_INDEX_TYPE_KEYWORD,
		listed[service.SearchAttributeDexWorkflowType],
	)
	require.Equal(
		t,
		dexpb.IndexType_INDEX_TYPE_KEYWORD,
		listed[service.SearchAttributeDexParentFlowID],
	)
	require.Equal(
		t,
		runtime.UnifiedClient.NormalizeAttributeIndexType(dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY),
		listed[service.SearchAttributeActiveStepTypes],
	)

	_, err = flowClient.SyncAttributeIndexes(ctx, &dexpb.SyncAttributeIndexRequest{})
	require.NoError(t, err)

	indexName := "DexIntegAttributeIndex_" + uuid.NewString()
	request := &dexpb.SyncAttributeIndexRequest{AttributeIndexes: map[string]dexpb.IndexType{
		indexName: dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
	}}
	_, err = flowClient.SyncAttributeIndexes(ctx, request)
	require.NoError(t, err)

	listed, err = runtime.UnifiedClient.ListAttributeIndexes(ctx)
	require.NoError(t, err)
	require.Equal(
		t,
		runtime.UnifiedClient.NormalizeAttributeIndexType(dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY),
		listed[indexName],
	)

	immediateCtx, cancelImmediate := context.WithTimeout(ctx, time.Second)
	_, err = flowClient.SyncAttributeIndexes(immediateCtx, request)
	cancelImmediate()
	require.NoError(t, err)

	_, err = flowClient.SyncAttributeIndexes(ctx, &dexpb.SyncAttributeIndexRequest{
		AttributeIndexes: map[string]dexpb.IndexType{
			indexName: dexpb.IndexType_INDEX_TYPE_TEXT,
		},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = flowClient.SyncAttributeIndexes(ctx, &dexpb.SyncAttributeIndexRequest{
		AttributeIndexes: map[string]dexpb.IndexType{
			"": dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	concurrentName := "DexIntegConcurrentAttributeIndex_" + uuid.NewString()
	concurrentRequest := &dexpb.SyncAttributeIndexRequest{AttributeIndexes: map[string]dexpb.IndexType{
		concurrentName: dexpb.IndexType_INDEX_TYPE_KEYWORD,
	}}
	var waitGroup sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, syncErr := flowClient.SyncAttributeIndexes(ctx, concurrentRequest)
			errors <- syncErr
		}()
	}
	waitGroup.Wait()
	close(errors)
	for syncErr := range errors {
		require.NoError(t, syncErr)
	}
}

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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAttributeIndexSyncTemporal(t *testing.T) {
	testAttributeIndexSync(t)
}

func testAttributeIndexSync(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Temporal search attributes back the Go Server indexes; server-zd keeps its own index catalog.

	_, err := flowClient.SyncAttributeIndexes(ctx, &dexpb.SyncAttributeIndexRequest{})
	require.NoError(t, err)

	indexName := "DexIntegAttributeIndex_" + uuid.NewString()
	request := &dexpb.SyncAttributeIndexRequest{AttributeIndexes: map[string]dexpb.IndexType{
		indexName: dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
	}}
	_, err = flowClient.SyncAttributeIndexes(ctx, request)
	require.NoError(t, err)

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

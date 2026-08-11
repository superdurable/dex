// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package indexsync

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type listResult struct {
	indexes map[string]dexpb.IndexType
	err     error
}

type scriptedClient struct {
	mu                    sync.Mutex
	listResults           []listResult
	addErr                error
	addErrors             []error
	normalizeKeywordArray bool
	listCalls             int
	addCalls              int
}

func TestSyncReturnsImmediatelyForExistingIndexes(t *testing.T) {
	client := &scriptedClient{listResults: []listResult{{indexes: map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	}}}}
	synchronizer := New(testConfig(time.Second), client)

	started := time.Now()
	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.NoError(t, err)
	require.Less(t, time.Since(started), initialPollInterval)
	require.Equal(t, 1, client.listCallCount())
	require.Equal(t, 0, client.addCallCount())
}

func TestSyncWaitsForNewIndexesToBecomeVisible(t *testing.T) {
	client := &scriptedClient{listResults: []listResult{
		{indexes: map[string]dexpb.IndexType{}},
		{indexes: map[string]dexpb.IndexType{"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD}},
	}}
	synchronizer := New(testConfig(time.Second), client)

	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.NoError(t, err)
	require.Equal(t, 2, client.listCallCount())
	require.Equal(t, 1, client.addCallCount())
}

func TestSyncAcceptsConcurrentRegistration(t *testing.T) {
	client := &scriptedClient{
		listResults: []listResult{
			{indexes: map[string]dexpb.IndexType{}},
			{indexes: map[string]dexpb.IndexType{"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD}},
		},
		addErr: status.Error(codes.AlreadyExists, "registered concurrently"),
	}
	synchronizer := New(testConfig(time.Second), client)

	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.NoError(t, err)
	require.Equal(t, 2, client.listCallCount())
}

func TestSyncRetriesTransientRegistration(t *testing.T) {
	client := &scriptedClient{
		listResults: []listResult{
			{indexes: map[string]dexpb.IndexType{}},
			{indexes: map[string]dexpb.IndexType{}},
			{indexes: map[string]dexpb.IndexType{}},
			{indexes: map[string]dexpb.IndexType{"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD}},
		},
		addErrors: []error{status.Error(codes.Unavailable, "not accepted"), nil},
	}
	synchronizer := New(testConfig(time.Second), client)

	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.NoError(t, err)
	require.Equal(t, 4, client.listCallCount())
	require.Equal(t, 2, client.addCallCount())
}

func TestSyncFailsImmediatelyForPermissionErrors(t *testing.T) {
	client := &scriptedClient{listResults: []listResult{{
		err: status.Error(codes.PermissionDenied, "denied"),
	}}}
	synchronizer := New(testConfig(time.Second), client)

	started := time.Now()
	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Less(t, time.Since(started), initialPollInterval)
	require.Equal(t, 1, client.listCallCount())
}

func TestSyncUsesEarlierCallerDeadline(t *testing.T) {
	client := &scriptedClient{listResults: []listResult{{
		err: status.Error(codes.Unavailable, "not ready"),
	}}}
	synchronizer := New(testConfig(time.Second), client)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := synchronizer.Sync(ctx, map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Less(t, time.Since(started), initialPollInterval)
	require.Equal(t, 1, client.listCallCount())
}

func TestSyncUsesConfiguredPropagationDeadline(t *testing.T) {
	client := &scriptedClient{listResults: []listResult{
		{indexes: map[string]dexpb.IndexType{}},
	}}
	synchronizer := New(testConfig(25*time.Millisecond), client)

	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Status": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	})

	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Equal(t, 1, client.addCallCount())
}

func TestSyncNormalizesBackendIndexTypes(t *testing.T) {
	client := &scriptedClient{
		listResults: []listResult{{indexes: map[string]dexpb.IndexType{
			"Tags": dexpb.IndexType_INDEX_TYPE_KEYWORD,
		}}},
		normalizeKeywordArray: true,
	}
	synchronizer := New(testConfig(time.Second), client)

	err := synchronizer.Sync(context.Background(), map[string]dexpb.IndexType{
		"Tags": dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
	})

	require.NoError(t, err)
	require.Equal(t, 0, client.addCallCount())
}

func (c *scriptedClient) ListAttributeIndexes(context.Context) (map[string]dexpb.IndexType, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resultIndex := c.listCalls
	if resultIndex >= len(c.listResults) {
		resultIndex = len(c.listResults) - 1
	}
	c.listCalls++
	result := c.listResults[resultIndex]
	return result.indexes, result.err
}

func (c *scriptedClient) AddAttributeIndexes(context.Context, map[string]dexpb.IndexType) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	callIndex := c.addCalls
	c.addCalls++
	if callIndex < len(c.addErrors) {
		return c.addErrors[callIndex]
	}
	return c.addErr
}

func (c *scriptedClient) NormalizeAttributeIndexType(indexType dexpb.IndexType) dexpb.IndexType {
	if c.normalizeKeywordArray && indexType == dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY {
		return dexpb.IndexType_INDEX_TYPE_KEYWORD
	}
	return indexType
}

func (c *scriptedClient) listCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listCalls
}

func (c *scriptedClient) addCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addCalls
}

func testConfig(timeout time.Duration) *config.Interpreter {
	return &config.Interpreter{AttributeIndexSyncTimeout: timeout}
}

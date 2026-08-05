// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package blobstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/log/loggerimpl"
	"go.temporal.io/sdk/client"
)

func TestLocalBlobStoreIntegration(t *testing.T) {
	root := t.TempDir()
	logger, err := loggerimpl.NewDevelopment()
	require.NoError(t, err)
	store := NewBlobStore(nil, "local-namespace", config.ExternalStorageConfig{
		Enabled: true,
		SupportedStorages: []config.BlobStorageConfig{{
			Status:         config.StorageStatusActive,
			StorageId:      "local",
			StorageType:    config.StorageTypeLocal,
			LocalDirectory: root,
		}},
	}, logger, client.MetricsNopHandler)
	ctx := context.Background()

	storeID, objectPath, err := store.WriteObject(ctx, "legacy-flow", "invocation", []byte("value"))
	require.NoError(t, err)
	require.Equal(t, "local", storeID)
	loaded, err := store.ReadObject(ctx, storeID, objectPath)
	require.NoError(t, err)
	require.Equal(t, []byte("value"), loaded)

	runStarted := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	flowID := "flow/with$difficult characters"
	runID := "run/with$difficult characters"
	stepExecutionID := "step/with$difficult characters"
	err = store.WriteStepEventInput(
		ctx,
		runStarted,
		flowID,
		runID,
		stepExecutionID,
		StepEventInputMethodExecute,
		[]byte("request"),
	)
	require.NoError(t, err)
	request, found, err := store.ReadStepEventInput(
		ctx,
		runStarted,
		flowID,
		runID,
		stepExecutionID,
		StepEventInputMethodExecute,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("request"), request)

	paths, err := store.ListWorkflowPaths(ctx, ListObjectPathsInput{StoreId: storeID})
	require.NoError(t, err)
	require.Len(t, paths.WorkflowPaths, 2)
	var runPath string
	for _, path := range paths.WorkflowPaths {
		parsed, parseErr := ParseWorkflowPath(path)
		require.NoError(t, parseErr)
		if parsed.RunID == runID {
			runPath = path
			require.Equal(t, flowID, parsed.FlowID)
		}
	}
	require.NotEmpty(t, runPath)
	require.NoError(t, store.DeleteWorkflowObjects(ctx, storeID, runPath))
	_, found, err = store.ReadStepEventInput(
		ctx,
		runStarted,
		flowID,
		runID,
		stepExecutionID,
		StepEventInputMethodExecute,
	)
	require.NoError(t, err)
	require.False(t, found)

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		require.NoError(t, walkErr)
		require.False(t, strings.HasPrefix(entry.Name(), ".dex-blob-"), path)
		return nil
	})
	require.NoError(t, err)
}

func TestLocalBlobStoreRejectsEscapingPaths(t *testing.T) {
	_, err := localObjectPath(t.TempDir(), "../../outside")
	require.Error(t, err)
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
)

func TestS3AttributeBlobCacheIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}
	cacheDirectory := t.TempDir()
	store := createTestBlobStoreWithCache(t, config.BlobCacheConfig{
		Directory: cacheDirectory,
		MaxBytes:  1 << 20,
	}).(*blobStoreImpl)
	ctx := context.Background()

	t.Run("write through serves data after source deletion", func(t *testing.T) {
		payload := []byte("write-through payload")
		flowID := uniqueCacheWorkflowID("write-through")
		storeID, path, err := store.WriteObject(
			ctx,
			flowID,
			"write-through",
			payload,
		)
		require.NoError(t, err)
		deleteS3ValueObject(t, ctx, store, flowID, path)
		loaded, err := store.ReadObject(ctx, storeID, flowID, path)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
	})

	t.Run("read miss fills cache", func(t *testing.T) {
		payload := []byte("read-fill payload")
		flowID := uniqueCacheWorkflowID("read-fill")
		referenceDate, err := formatBlobReferenceDate(time.Now())
		require.NoError(t, err)
		locator := referenceDate + "/0000000000"
		path, err := ValueObjectPath(flowID, locator)
		require.NoError(t, err)
		require.NoError(t, store.writeObject(ctx, store.activeStorage, path, payload))
		loaded, err := store.ReadObject(ctx, testStorageId, flowID, locator)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
		deleteS3Object(t, ctx, store, path)
		loaded, err = store.ReadObject(ctx, testStorageId, flowID, locator)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
	})

	t.Run("step event inputs bypass cache", func(t *testing.T) {
		runStarted := time.Now().UTC()
		flowID := uniqueCacheWorkflowID("step-input")
		runID := "run"
		stepExecutionID := "step"
		require.NoError(t, store.WriteStepEventInput(
			ctx,
			runStarted,
			flowID,
			runID,
			stepExecutionID,
			StepEventInputMethodExecute,
			[]byte("step input"),
		))
		path := StepEventInputPath(
			runStarted,
			flowID,
			runID,
			stepExecutionID,
			StepEventInputMethodExecute,
		)
		deleteS3Object(t, ctx, store, path)
		_, found, err := store.ReadStepEventInput(
			ctx,
			runStarted,
			flowID,
			runID,
			stepExecutionID,
			StepEventInputMethodExecute,
		)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("workflow deletion preserves cache", func(t *testing.T) {
		payload := []byte("retained cache payload")
		flowID := uniqueCacheWorkflowID("delete")
		storeID, path, err := store.WriteObject(
			ctx,
			flowID,
			"delete",
			payload,
		)
		require.NoError(t, err)
		workflowPath := strings.SplitN(mustValueObjectPath(t, flowID, path), "/", 2)[0]
		require.NoError(t, store.DeleteWorkflowObjects(ctx, storeID, workflowPath))
		loaded, err := store.ReadObject(ctx, storeID, flowID, path)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
	})

	t.Run("corruption invalidates and refills from source", func(t *testing.T) {
		payload := []byte("corruption payload")
		flowID := uniqueCacheWorkflowID("corrupt")
		storeID, path, err := store.WriteObject(
			ctx,
			flowID,
			"corrupt",
			payload,
		)
		require.NoError(t, err)
		physicalPath := mustValueObjectPath(t, flowID, path)
		cachePath := attributeCachePath(cacheDirectory, store.attributeCacheKey(storeID, physicalPath))
		file, err := os.OpenFile(cachePath, os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = file.WriteAt([]byte{0xff}, 24)
		require.NoError(t, err)
		require.NoError(t, file.Close())

		loaded, err := store.ReadObject(ctx, storeID, flowID, path)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
		deleteS3Object(t, ctx, store, physicalPath)
		loaded, err = store.ReadObject(ctx, storeID, flowID, path)
		require.NoError(t, err)
		require.Equal(t, payload, loaded)
	})
}

func TestS3AttributeBlobCacheOversizedBypass(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}
	store := createTestBlobStoreWithCache(t, config.BlobCacheConfig{
		Directory: t.TempDir(),
		MaxBytes:  64,
	}).(*blobStoreImpl)
	ctx := context.Background()
	payload := make([]byte, 128)
	flowID := uniqueCacheWorkflowID("oversized")
	storeID, path, err := store.WriteObject(
		ctx,
		flowID,
		"oversized",
		payload,
	)
	require.NoError(t, err)
	deleteS3ValueObject(t, ctx, store, flowID, path)
	_, err = store.ReadObject(ctx, storeID, flowID, path)
	require.Error(t, err)
}

func uniqueCacheWorkflowID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func deleteS3ValueObject(t *testing.T, ctx context.Context, store *blobStoreImpl, flowID string, locator string) {
	t.Helper()
	deleteS3Object(t, ctx, store, mustValueObjectPath(t, flowID, locator))
}

func mustValueObjectPath(t *testing.T, flowID string, locator string) string {
	t.Helper()
	path, err := ValueObjectPath(flowID, locator)
	require.NoError(t, err)
	return path
}

func deleteS3Object(t *testing.T, ctx context.Context, store *blobStoreImpl, path string) {
	t.Helper()
	_, err := store.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(store.activeStorage.S3Bucket),
		Key:    aws.String(store.pathPrefix + path),
	})
	require.NoError(t, err)
}

func attributeCachePath(cacheDirectory, cacheKey string) string {
	digest := sha256.Sum256([]byte(cacheKey))
	encoded := hex.EncodeToString(digest[:])
	return filepath.Join(cacheDirectory, "blobs", encoded[0:2], encoded[2:4], encoded+".blob")
}

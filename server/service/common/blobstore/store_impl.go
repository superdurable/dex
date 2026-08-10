// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/blobcache"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
	"go.temporal.io/sdk/client"
)

var errStoreNotFound = errors.New("store not found")

type blobStoreImpl struct {
	s3Client                    *s3.Client
	pathPrefix                  string // the Temporal namespace or Cadence domain + "/"
	activeStorage               config.BlobStoreConfigEntry
	supportedStore              map[string]config.BlobStoreConfigEntry // storeId as key
	logger                      log.Logger
	writeObjectErrorCounter     client.MetricsCounter
	readObjectErrorCounter      client.MetricsCounter
	writeObjectSuccessHistogram client.MetricsTimer
	readObjectSuccessHistogram  client.MetricsTimer
	blobCache                   *blobcache.Cache
	blobCacheHitCounter         client.MetricsCounter
	blobCacheMissCounter        client.MetricsCounter
	blobCacheWriteCounter       client.MetricsCounter
	blobCacheReadFillCounter    client.MetricsCounter
	blobCacheEvictionCounter    client.MetricsCounter
	blobCacheOversizedCounter   client.MetricsCounter
	blobCacheCorruptionCounter  client.MetricsCounter
	blobCacheErrorCounter       client.MetricsCounter
}

func NewBlobStore(
	s3Client *s3.Client,
	temporalOrCadenceNamespace string,
	storeConfig *config.BlobStoreConfig,
	logger log.Logger,
	metrics client.MetricsHandler,
) (BlobStore, error) {
	if storeConfig == nil {
		panic("NewBlobStore requires BlobStoreConfig")
	}
	if !storeConfig.Enabled {
		return nil, nil
	}
	if err := storeConfig.BlobCache.Validate(); err != nil {
		return nil, err
	}

	var activeStorage *config.BlobStoreConfigEntry
	supportedStores := map[string]config.BlobStoreConfigEntry{}
	hasS3Storage := false
	for _, storage := range storeConfig.SupportedStorages {
		if storage.Status == config.StorageStatusActive {
			if activeStorage != nil {
				return nil, errors.New("cannot have more than one active storage configured")
			}
			activeStorage = &storage
		}
		supportedStores[storage.StorageId] = storage
		switch storage.StorageType {
		case config.StorageTypeS3:
			hasS3Storage = true
		case config.StorageTypeLocal:
			if storage.LocalDirectory == "" {
				return nil, errors.New("local storage requires a directory")
			}
		default:
			return nil, fmt.Errorf("unsupported blob storage type %q", storage.StorageType)
		}
	}
	if activeStorage == nil {
		return nil, errors.New("no active storage found")
	}

	metricsHandler := metrics.WithTags(map[string]string{"prefix": temporalOrCadenceNamespace})
	writeObjectErrorCounter := metricsHandler.Counter("write_object_error")
	readObjectErrorCounter := metricsHandler.Counter("read_object_error")
	writeObjectSuccessHistogram := metricsHandler.Timer("write_object_success")
	readObjectSuccessHistogram := metricsHandler.Timer("read_object_success")
	var attributeCache *blobcache.Cache
	if storeConfig.BlobCache.Directory != "" && hasS3Storage {
		var err error
		attributeCache, err = blobcache.New(&storeConfig.BlobCache)
		if err != nil {
			return nil, fmt.Errorf("initialize Attribute blob cache: %w", err)
		}
	}

	return &blobStoreImpl{
		s3Client:                    s3Client,
		pathPrefix:                  temporalOrCadenceNamespace + "/",
		activeStorage:               *activeStorage,
		supportedStore:              supportedStores,
		logger:                      logger,
		writeObjectErrorCounter:     writeObjectErrorCounter,
		readObjectErrorCounter:      readObjectErrorCounter,
		writeObjectSuccessHistogram: writeObjectSuccessHistogram,
		readObjectSuccessHistogram:  readObjectSuccessHistogram,
		blobCache:                   attributeCache,
		blobCacheHitCounter:         metricsHandler.Counter("blob_cache_hit"),
		blobCacheMissCounter:        metricsHandler.Counter("blob_cache_miss"),
		blobCacheWriteCounter:       metricsHandler.Counter("blob_cache_write_through"),
		blobCacheReadFillCounter:    metricsHandler.Counter("blob_cache_read_fill"),
		blobCacheEvictionCounter:    metricsHandler.Counter("blob_cache_eviction"),
		blobCacheOversizedCounter:   metricsHandler.Counter("blob_cache_oversized_bypass"),
		blobCacheCorruptionCounter:  metricsHandler.Counter("blob_cache_corruption"),
		blobCacheErrorCounter:       metricsHandler.Counter("blob_cache_error"),
	}, nil
}

func (b *blobStoreImpl) WriteObject(
	ctx context.Context,
	workflowId string,
	invocationId string,
	data []byte,
) (storeId, path string, err error) {
	storeId = b.activeStorage.StorageId
	objectID, err := deterministicBlobUUID(invocationId, data)
	if err != nil {
		return "", "", err
	}
	yyyymmdd := time.Now().UTC().Format("20060102")
	// yyyymmdd$workflowId/uuid
	// Note: using $ here so that the listing can be much easier to implement for pagination
	path = fmt.Sprintf("%s$%s/%s", yyyymmdd, workflowId, objectID)

	err = b.writeObject(ctx, b.activeStorage, path, data)
	if err != nil {
		b.writeObjectErrorCounter.Inc(1)
		var re s3.ResponseError
		if errors.As(err, &re) {
			b.logger.Error("PutObject S3 API error",
				tag.Key("requestId"), tag.Value(re.ServiceRequestID()),
				tag.Key("hostId"), tag.Value(re.ServiceHostID()),
				tag.Key("bucket"), tag.Value(b.activeStorage.S3Bucket),
				tag.Key("workflowId"), tag.Value(workflowId),
				tag.Error(err))
			err = fmt.Errorf("failed to write object (requestId=%s, hostId=%s): %w",
				re.ServiceRequestID(), re.ServiceHostID(), err)
		} else {
			b.logger.Error("PutObject error",
				tag.Key("bucket"), tag.Value(b.activeStorage.S3Bucket),
				tag.Key("workflowId"), tag.Value(workflowId),
				tag.Error(err))
			err = fmt.Errorf("failed to write object: %w", err)
		}
		return
	}
	if b.activeStorage.StorageType == config.StorageTypeS3 {
		if err = b.cacheAttributeObject(storeId, path, data, b.blobCacheWriteCounter); err != nil {
			b.writeObjectErrorCounter.Inc(1)
			b.logger.Error("Attribute blob cache write failed",
				tag.Key("path"), tag.Value(path),
				tag.Key("storeId"), tag.Value(storeId),
				tag.Error(err))
			err = fmt.Errorf("failed to cache written object: %w", err)
			return
		}
	}
	b.writeObjectSuccessHistogram.Record(time.Duration(len(data)))
	return
}

func (b *blobStoreImpl) WriteStepEventInput(
	ctx context.Context,
	runStarted time.Time,
	flowID string,
	runID string,
	stepExecutionID string,
	method string,
	data []byte,
) error {
	path := StepEventInputPath(runStarted, flowID, runID, stepExecutionID, method)
	if err := b.writeObject(ctx, b.activeStorage, path, data); err != nil {
		b.writeObjectErrorCounter.Inc(1)
		return fmt.Errorf("write step event input: %w", err)
	}
	b.writeObjectSuccessHistogram.Record(time.Duration(len(data)))
	return nil
}

func (b *blobStoreImpl) ReadStepEventInput(
	ctx context.Context,
	runStarted time.Time,
	flowID string,
	runID string,
	stepExecutionID string,
	method string,
) ([]byte, bool, error) {
	path := StepEventInputPath(runStarted, flowID, runID, stepExecutionID, method)
	storeIDs := make([]string, 0, len(b.supportedStore))
	for storeID := range b.supportedStore {
		if storeID != b.activeStorage.StorageId {
			storeIDs = append(storeIDs, storeID)
		}
	}
	sort.Strings(storeIDs)
	storeIDs = append([]string{b.activeStorage.StorageId}, storeIDs...)
	for _, storeID := range storeIDs {
		storage := b.supportedStore[storeID]
		data, err := b.readObject(ctx, storage, path)
		if err == nil {
			b.readObjectSuccessHistogram.Record(time.Duration(len(data)))
			return data, true, nil
		}
		if IsObjectNotFound(err) {
			continue
		}
		b.readObjectErrorCounter.Inc(1)
		return nil, false, fmt.Errorf("read step event input: %w", err)
	}
	return nil, false, nil
}

func (b *blobStoreImpl) writeObject(
	ctx context.Context,
	storage config.BlobStoreConfigEntry,
	path string,
	data []byte,
) error {
	switch storage.StorageType {
	case config.StorageTypeS3:
		if b.s3Client == nil {
			return errors.New("S3 client is not configured")
		}
		return putObject(ctx, b.s3Client, storage.S3Bucket, b.pathPrefix+path, data)
	case config.StorageTypeLocal:
		return writeLocalObject(ctx, storage.LocalDirectory, b.pathPrefix+path, data)
	default:
		return fmt.Errorf("unsupported blob storage type %q", storage.StorageType)
	}
}

func deterministicBlobUUID(invocationId string, data []byte) (uuid.UUID, error) {
	hasher := sha256.New()
	components := [][]byte{
		[]byte("dex-blob-v1"),
		[]byte(invocationId),
		data,
	}
	for _, component := range components {
		if err := writeHashComponent(hasher, component); err != nil {
			return uuid.Nil, err
		}
	}

	digest := hasher.Sum(nil)
	var objectID uuid.UUID
	copy(objectID[:], digest[:16])
	objectID[6] = (objectID[6] & 0x0f) | 0x80
	objectID[8] = (objectID[8] & 0x3f) | 0x80
	return objectID, nil
}

func writeHashComponent(hasher hash.Hash, component []byte) error {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(component)))
	if err := writeHashBytes(hasher, length[:]); err != nil {
		return err
	}
	return writeHashBytes(hasher, component)
}

func writeHashBytes(hasher hash.Hash, data []byte) error {
	written, err := hasher.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (b *blobStoreImpl) ReadObject(ctx context.Context, storeId, path string) ([]byte, error) {
	storeConfig, ok := b.supportedStore[storeId]
	if !ok {
		b.readObjectErrorCounter.Inc(1)
		return nil, fmt.Errorf("%w for %s", errStoreNotFound, storeId)
	}
	if storeConfig.StorageType == config.StorageTypeS3 && b.blobCache != nil {
		data, found, err := b.readCachedAttributeObject(storeId, path)
		if err != nil {
			b.readObjectErrorCounter.Inc(1)
			b.logger.Error("Attribute blob cache read failed",
				tag.Key("path"), tag.Value(path),
				tag.Key("storeId"), tag.Value(storeId),
				tag.Error(err))
			return nil, fmt.Errorf("failed to read cached object: %w", err)
		}
		if found {
			b.readObjectSuccessHistogram.Record(time.Duration(len(data)))
			return data, nil
		}
	}
	data, err := b.readObject(ctx, storeConfig, path)
	if err != nil {
		b.readObjectErrorCounter.Inc(1)
		var re s3.ResponseError
		if errors.As(err, &re) {
			b.logger.Error("GetObject S3 API error",
				tag.Key("requestId"), tag.Value(re.ServiceRequestID()),
				tag.Key("hostId"), tag.Value(re.ServiceHostID()),
				tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
				tag.Key("path"), tag.Value(path),
				tag.Key("storeId"), tag.Value(storeId),
				tag.Error(err))
			return nil, fmt.Errorf("failed to read object (requestId=%s, hostId=%s): %w",
				re.ServiceRequestID(), re.ServiceHostID(), err)
		}
		b.logger.Error("GetObject error",
			tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
			tag.Key("path"), tag.Value(path),
			tag.Key("storeId"), tag.Value(storeId),
			tag.Error(err))
		return nil, fmt.Errorf("failed to read object: %w", err)
	}
	if storeConfig.StorageType == config.StorageTypeS3 {
		if err := b.cacheAttributeObject(storeId, path, data, b.blobCacheReadFillCounter); err != nil {
			b.readObjectErrorCounter.Inc(1)
			b.logger.Error("Attribute blob cache fill failed",
				tag.Key("path"), tag.Value(path),
				tag.Key("storeId"), tag.Value(storeId),
				tag.Error(err))
			return nil, fmt.Errorf("failed to fill object cache: %w", err)
		}
	}
	b.readObjectSuccessHistogram.Record(time.Duration(len(data)))
	return data, nil
}

func (b *blobStoreImpl) readCachedAttributeObject(storeID, path string) ([]byte, bool, error) {
	data, found, err := b.blobCache.Get(b.attributeCacheKey(storeID, path))
	if err != nil {
		b.recordBlobCacheError(err)
		return nil, false, err
	}
	if !found {
		b.blobCacheMissCounter.Inc(1)
		return nil, false, nil
	}
	b.blobCacheHitCounter.Inc(1)
	return data, true, nil
}

func (b *blobStoreImpl) cacheAttributeObject(
	storeID string,
	path string,
	data []byte,
	successCounter client.MetricsCounter,
) error {
	if b.blobCache == nil {
		return nil
	}
	result, err := b.blobCache.Put(b.attributeCacheKey(storeID, path), data)
	if result.Evicted > 0 {
		b.blobCacheEvictionCounter.Inc(int64(result.Evicted))
	}
	if err != nil {
		b.recordBlobCacheError(err)
		return err
	}
	if !result.Cached {
		b.blobCacheOversizedCounter.Inc(1)
		return nil
	}
	successCounter.Inc(1)
	return nil
}

func (b *blobStoreImpl) attributeCacheKey(storeID, path string) string {
	return fmt.Sprintf(
		"%d:%s%d:%s%d:%s",
		len(b.pathPrefix),
		b.pathPrefix,
		len(storeID),
		storeID,
		len(path),
		path,
	)
}

func (b *blobStoreImpl) recordBlobCacheError(err error) {
	b.blobCacheErrorCounter.Inc(1)
	if errors.Is(err, blobcache.ErrCorrupt) {
		b.blobCacheCorruptionCounter.Inc(1)
	}
}

func (b *blobStoreImpl) readObject(
	ctx context.Context,
	storage config.BlobStoreConfigEntry,
	path string,
) ([]byte, error) {
	switch storage.StorageType {
	case config.StorageTypeS3:
		if b.s3Client == nil {
			return nil, errors.New("S3 client is not configured")
		}
		return getObject(ctx, b.s3Client, storage.S3Bucket, b.pathPrefix+path)
	case config.StorageTypeLocal:
		return readLocalObject(ctx, storage.LocalDirectory, b.pathPrefix+path)
	default:
		return nil, fmt.Errorf("unsupported blob storage type %q", storage.StorageType)
	}
}

func (b *blobStoreImpl) Close() error {
	if b.blobCache == nil {
		return nil
	}
	return b.blobCache.Close()
}

// IsObjectNotFound reports whether a backend object is absent.
func IsObjectNotFound(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	return apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NotFound"
}

// IsObjectUnavailable reports whether persisted data cannot exist in this server configuration.
func IsObjectUnavailable(err error) bool {
	return errors.Is(err, errStoreNotFound) || IsObjectNotFound(err)
}

func putObject(ctx context.Context, client *s3.Client, bucketName string, key string, content []byte) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/json"),
	})
	return err
}

func getObject(ctx context.Context, client *s3.Client, bucketName, key string) ([]byte, error) {
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	data, readErr := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	return data, nil
}

func (b *blobStoreImpl) CountWorkflowObjectsForTesting(ctx context.Context, workflowId string) (int64, error) {
	// Create the prefix to match objects for this workflowId for today
	yyyymmdd := time.Now().UTC().Format("20060102")
	prefix := fmt.Sprintf("%s%s$%s/", b.pathPrefix, yyyymmdd, workflowId)
	if b.activeStorage.StorageType == config.StorageTypeLocal {
		return countLocalObjects(ctx, b.activeStorage.LocalDirectory, prefix)
	}
	if b.s3Client == nil {
		return 0, errors.New("S3 client is not configured")
	}

	// List objects with the prefix (limited to 1000 objects as documented)
	result, err := b.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.activeStorage.S3Bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return 0, err
	}

	return int64(len(result.Contents)), nil
}

func (b *blobStoreImpl) DeleteWorkflowObjects(ctx context.Context, storeId, workflowPath string) error {
	storeConfig, ok := b.supportedStore[storeId]
	if !ok {
		return fmt.Errorf("%w for %s", errStoreNotFound, storeId)
	}
	if storeConfig.StorageType == config.StorageTypeLocal {
		return deleteLocalWorkflowObjects(ctx, storeConfig.LocalDirectory, b.pathPrefix+workflowPath)
	}
	if b.s3Client == nil {
		return errors.New("S3 client is not configured")
	}

	// Construct the prefix for all objects of this workflow
	prefix := fmt.Sprintf("%s%s/", b.pathPrefix, workflowPath)

	// Paginate through all objects and delete them in batches
	var continuationToken *string
	var totalDeleted int
	for {
		listInput := &s3.ListObjectsV2Input{
			Bucket: aws.String(storeConfig.S3Bucket),
			Prefix: aws.String(prefix),
		}

		if continuationToken != nil {
			listInput.ContinuationToken = continuationToken
		}

		listResult, err := b.s3Client.ListObjectsV2(ctx, listInput)
		if err != nil {
			var re s3.ResponseError
			if errors.As(err, &re) {
				b.logger.Error("ListObjectsV2 S3 API error",
					tag.Key("requestId"), tag.Value(re.ServiceRequestID()),
					tag.Key("hostId"), tag.Value(re.ServiceHostID()),
					tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
					tag.Key("workflowPath"), tag.Value(workflowPath),
					tag.Error(err))
				return fmt.Errorf("failed to list objects for deletion (requestId=%s, hostId=%s): %w",
					re.ServiceRequestID(), re.ServiceHostID(), err)
			}
			return fmt.Errorf("failed to list objects for deletion: %w", err)
		}

		// If no objects found, we're done
		if len(listResult.Contents) == 0 {
			break
		}

		// Prepare objects for batch deletion
		var objectsToDelete []types.ObjectIdentifier
		for _, obj := range listResult.Contents {
			if obj.Key != nil {
				objectsToDelete = append(objectsToDelete, types.ObjectIdentifier{
					Key: obj.Key,
				})
			}
		}

		// Delete objects in batch
		if len(objectsToDelete) > 0 {
			deleteResult, err := b.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(storeConfig.S3Bucket),
				Delete: &types.Delete{
					Objects: objectsToDelete,
					Quiet:   aws.Bool(true), // Don't return successful deletions
				},
			})
			if err != nil {
				// Log S3-specific request identifiers for debugging with AWS Support
				var re s3.ResponseError
				if errors.As(err, &re) {
					b.logger.Error("DeleteObjects S3 API error",
						tag.Key("requestId"), tag.Value(re.ServiceRequestID()),
						tag.Key("hostId"), tag.Value(re.ServiceHostID()),
						tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
						tag.Key("workflowPath"), tag.Value(workflowPath),
						tag.Error(err))
					return fmt.Errorf("failed to delete objects (requestId=%s, hostId=%s): %w",
						re.ServiceRequestID(), re.ServiceHostID(), err)
				}
				return fmt.Errorf("failed to delete objects: %w", err)
			}

			// Check for any delete errors
			if len(deleteResult.Errors) > 0 {
				var errorMsgs []string
				for _, delErr := range deleteResult.Errors {
					if delErr.Key != nil && delErr.Code != nil && delErr.Message != nil {
						errorMsgs = append(errorMsgs, fmt.Sprintf("key=%s, code=%s, message=%s",
							*delErr.Key, *delErr.Code, *delErr.Message))
					}
				}
				resultMetadata := fmt.Sprintf("%+v", deleteResult.ResultMetadata)
				b.logger.Error("DeleteObjects failed",
					tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
					tag.Key("workflowPath"), tag.Value(workflowPath),
					tag.Key("resultMetadata"), tag.Value(resultMetadata))
				return fmt.Errorf("some objects failed to delete (resultMetadata=%s): %s", resultMetadata, strings.Join(errorMsgs, "; "))
			}

			totalDeleted += len(objectsToDelete)
		}

		// Check if there are more objects to process
		if listResult.IsTruncated == nil || !*listResult.IsTruncated {
			break
		}
		continuationToken = listResult.NextContinuationToken
	}

	b.logger.Info("DeleteWorkflowObjects completed",
		tag.Key("bucket"), tag.Value(storeConfig.S3Bucket),
		tag.Key("workflowPath"), tag.Value(workflowPath),
		tag.Key("totalDeleted"), tag.Value(totalDeleted))

	return nil
}

func (b *blobStoreImpl) ListWorkflowPaths(ctx context.Context, input ListObjectPathsInput) (*ListObjectPathsOutput, error) {
	storeConfig, ok := b.supportedStore[input.StoreId]
	if !ok {
		return nil, fmt.Errorf("%w for %s", errStoreNotFound, input.StoreId)
	}
	if storeConfig.StorageType == config.StorageTypeLocal {
		return listLocalWorkflowPaths(
			ctx,
			storeConfig.LocalDirectory,
			strings.TrimSuffix(b.pathPrefix, "/"),
			input.ContinuationToken,
		)
	}
	if b.s3Client == nil {
		return nil, errors.New("S3 client is not configured")
	}

	listInput := &s3.ListObjectsV2Input{
		Bucket:    aws.String(storeConfig.S3Bucket),
		Prefix:    aws.String(b.pathPrefix),
		Delimiter: aws.String("/"),
	}

	// Set continuation token if provided
	if input.ContinuationToken != nil {
		listInput.ContinuationToken = input.ContinuationToken
	}

	result, err := b.s3Client.ListObjectsV2(ctx, listInput)
	if err != nil {
		return nil, err
	}

	// Extract workflow paths from common prefixes
	workflowPaths := make([]string, 0, len(result.CommonPrefixes))
	for _, commonPrefix := range result.CommonPrefixes {
		if commonPrefix.Prefix != nil {
			// Remove the pathPrefix to get the workflow path (yyyymmdd$workflowId)
			prefixStr := *commonPrefix.Prefix
			if strings.HasPrefix(prefixStr, b.pathPrefix) {
				workflowPath := strings.TrimPrefix(prefixStr, b.pathPrefix)
				// Remove trailing "/" if present
				workflowPath = strings.TrimSuffix(workflowPath, "/")
				workflowPaths = append(workflowPaths, workflowPath)
			}
		}
	}

	return &ListObjectPathsOutput{
		ContinuationToken: result.NextContinuationToken,
		WorkflowPaths:     workflowPaths,
	}, nil
}

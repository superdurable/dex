// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package bootstrap

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/blobstore"
	"go.temporal.io/sdk/client"
)

func (r *Runtime) createBlobStore(
	ctx context.Context,
	namespace string,
	metrics client.MetricsHandler,
) (blobstore.BlobStore, error) {
	activeStorage, err := activeBlobStorage(r.cfg)
	if err != nil {
		return nil, err
	}
	var s3Client *s3.Client
	if activeStorage != nil && activeStorage.StorageType == config.StorageTypeS3 {
		s3Client = r.options.S3Clients[activeStorage.StorageId]
		if s3Client == nil {
			s3Client, err = NewS3ClientForStorage(ctx, activeStorage)
			if err != nil {
				return nil, err
			}
		}
		if err := createBucketIfNotExists(ctx, s3Client, activeStorage.S3Bucket); err != nil {
			return nil, err
		}
	}
	return blobstore.NewBlobStore(
		s3Client,
		namespace,
		&r.cfg.BlobStore,
		r.logger,
		metrics,
	)
}

func CreateS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	if cfg == nil {
		panic("S3 config must not be nil")
	}
	if !cfg.BlobStore.EffectiveEnabled() {
		return nil, nil
	}

	activeStorage, err := activeBlobStorage(cfg)
	if err != nil {
		return nil, err
	}
	if activeStorage == nil {
		return nil, fmt.Errorf("no active storage found")
	}
	if activeStorage.StorageType == config.StorageTypeLocal {
		if activeStorage.LocalDirectory == "" {
			return nil, fmt.Errorf("local blob storage directory is required")
		}
		return nil, nil
	}
	s3Client, err := NewS3ClientForStorage(ctx, activeStorage)
	if err != nil {
		return nil, err
	}
	if err := createBucketIfNotExists(ctx, s3Client, activeStorage.S3Bucket); err != nil {
		return nil, err
	}
	return s3Client, nil
}

func activeBlobStorage(cfg *config.Config) (*config.BlobStoreConfigEntry, error) {
	if !cfg.BlobStore.EffectiveEnabled() {
		return nil, nil
	}
	for index := range cfg.BlobStore.SupportedStorages {
		storage := &cfg.BlobStore.SupportedStorages[index]
		if storage.Status == config.StorageStatusActive {
			return storage, nil
		}
	}
	return nil, fmt.Errorf("no active storage found")
}

// FindS3Storage resolves one configured S3 blob storage without exposing credentials to Dex Web.
func FindS3Storage(cfg *config.Config, storageID string) (*config.BlobStoreConfigEntry, error) {
	if cfg == nil {
		panic("S3 config must not be nil")
	}
	for index := range cfg.BlobStore.SupportedStorages {
		storage := &cfg.BlobStore.SupportedStorages[index]
		if storage.StorageId != storageID {
			continue
		}
		if storage.StorageType != config.StorageTypeS3 {
			return nil, fmt.Errorf("blob storage %q is not S3", storageID)
		}
		return storage, nil
	}
	return nil, fmt.Errorf("S3 blob storage %q was not found", storageID)
}

// NewS3ClientForStorage constructs an S3 client from one existing blob storage entry.
func NewS3ClientForStorage(ctx context.Context, storage *config.BlobStoreConfigEntry) (*s3.Client, error) {
	if storage == nil {
		panic("S3 storage config must not be nil")
	}
	if storage.StorageType != config.StorageTypeS3 {
		return nil, fmt.Errorf("unsupported blob storage type %q", storage.StorageType)
	}

	// Create custom resolver for MinIO endpoint
	customResolver := aws.EndpointResolverWithOptionsFunc(func(
		service string,
		region string,
		options ...interface{},
	) (aws.Endpoint, error) {
		if service == s3.ServiceID {
			return aws.Endpoint{
				URL:               storage.S3Endpoint,
				HostnameImmutable: true,
				Source:            aws.EndpointSourceCustom,
			}, nil
		}
		return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
	})

	// Load AWS config with custom credentials and endpoint
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			storage.S3AccessKey,
			storage.S3SecretKey,
			"",
		)),
		awsconfig.WithRegion(storage.S3Region),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	// Create S3 client with path-style addressing (required for MinIO)
	s3Client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = true
	})
	return s3Client, nil
}

func createBucketIfNotExists(ctx context.Context, client *s3.Client, bucketName string) error {
	// Check if bucket exists
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		return nil
	}
	// Bucket doesn't exist, create it
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("create bucket %q: %w", bucketName, err)
	}
	return nil
}

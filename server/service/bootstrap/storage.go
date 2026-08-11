// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
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
	s3Client, err := CreateS3Client(ctx, r.cfg)
	if err != nil {
		return nil, err
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

	var activeStorage *config.BlobStoreConfigEntry
	for index := range cfg.BlobStore.SupportedStorages {
		storage := &cfg.BlobStore.SupportedStorages[index]
		if storage.Status == config.StorageStatusActive {
			activeStorage = storage
			break
		}
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
	if activeStorage.StorageType != config.StorageTypeS3 {
		return nil, fmt.Errorf("unsupported blob storage type %q", activeStorage.StorageType)
	}

	// Create custom resolver for MinIO endpoint
	customResolver := aws.EndpointResolverWithOptionsFunc(func(
		service string,
		region string,
		options ...interface{},
	) (aws.Endpoint, error) {
		if service == s3.ServiceID {
			return aws.Endpoint{
				URL:               activeStorage.S3Endpoint,
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
			activeStorage.S3AccessKey,
			activeStorage.S3SecretKey,
			"",
		)),
		awsconfig.WithRegion(activeStorage.S3Region),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	// Create S3 client with path-style addressing (required for MinIO)
	s3Client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = true
	})
	if err := createBucketIfNotExists(ctx, s3Client, activeStorage.S3Bucket); err != nil {
		return nil, err
	}
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

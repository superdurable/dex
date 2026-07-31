// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package runtime

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
		r.cfg.ExternalStorage,
		r.logger,
		metrics,
	), nil
}

func CreateS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	if cfg == nil {
		panic("S3 config must not be nil")
	}
	if !cfg.ExternalStorage.Enabled {
		return nil, nil
	}

	var activeStorage *config.BlobStorageConfig
	for index := range cfg.ExternalStorage.SupportedStorages {
		storage := &cfg.ExternalStorage.SupportedStorages[index]
		if storage.Status == config.StorageStatusActive {
			activeStorage = storage
			break
		}
	}
	if activeStorage == nil {
		return nil, fmt.Errorf("no active storage found")
	}
	if activeStorage.StorageType != "s3" {
		return nil, fmt.Errorf("only s3 is supported for external storage")
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

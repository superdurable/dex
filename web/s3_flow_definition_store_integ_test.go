// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3FlowDefinitionProviderMinIOIntegration(t *testing.T) {
	ctx := context.Background()
	bucket := fmt.Sprintf("dex-web-definitions-%d", time.Now().UnixNano())
	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", "")),
		awsconfig.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(string, string, ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: "http://localhost:9000", HostnameImmutable: true}, nil
			},
		)),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) { options.UsePathStyle = true })
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		objects, listErr := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if listErr == nil {
			for _, object := range objects.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: object.Key})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	objectStore, err := NewS3FlowDefinitionObjectStore(client, bucket)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "_superverse/dex-web/flow-definitions"
	provider, err := NewS3FlowDefinitionProvider(objectStore, prefix)
	if err != nil {
		t.Fatal(err)
	}

	firstRevision := publishS3DefinitionRelease(t, ctx, client, bucket, prefix, testReleaseID, "FirstFlow")
	first, err := provider.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := provider.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != first {
		t.Fatal("unchanged MinIO manifest did not reuse the validated snapshot")
	}
	secondRevision := publishS3DefinitionRelease(t, ctx, client, bucket, prefix, secondTestReleaseID, "SecondFlow")
	second, err := provider.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.DefinitionRevision != firstRevision || second.DefinitionRevision != secondRevision ||
		first.DefinitionRevision == second.DefinitionRevision {
		t.Fatalf("revisions = %q then %q", first.DefinitionRevision, second.DefinitionRevision)
	}
	if _, found := second.V2Definitions["SecondFlow"]; !found {
		t.Fatalf("second definitions = %+v", second.V2Definitions)
	}
}

func publishS3DefinitionRelease(
	t *testing.T,
	ctx context.Context,
	client *s3.Client,
	bucket string,
	prefix string,
	releaseID string,
	flowType string,
) string {
	t.Helper()
	graph := []byte(validFlowDefinitionV2(flowType, true))
	bundlePrefix := prefix + "/releases/" + releaseID + "/"
	digest := flowDefinitionDigest([]flowDefinitionFile{{path: "flow.json", data: graph}})
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(bundlePrefix + "flow.json"), Body: bytes.NewReader(graph),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: releaseID, BundlePrefix: bundlePrefix,
		BundleDigest: digest, DefinitionCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(prefix + "/active-manifest"), Body: bytes.NewReader(manifest),
	})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

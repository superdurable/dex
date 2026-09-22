// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type s3FlowDefinitionClient interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

type s3FlowDefinitionObjectStore struct {
	client s3FlowDefinitionClient
	bucket string
}

// NewS3FlowDefinitionObjectStore adapts an existing S3 client for read-only definition loading.
func NewS3FlowDefinitionObjectStore(client *s3.Client, bucket string) (FlowDefinitionObjectStore, error) {
	if client == nil {
		return nil, fmt.Errorf("S3 client is required")
	}
	if bucket == "" {
		return nil, fmt.Errorf("S3 bucket is required")
	}
	return &s3FlowDefinitionObjectStore{client: client, bucket: bucket}, nil
}

func (s *s3FlowDefinitionObjectStore) Get(
	ctx context.Context,
	key string,
	ifNoneMatch string,
) (FlowDefinitionObject, error) {
	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if ifNoneMatch != "" {
		input.IfNoneMatch = aws.String(ifNoneMatch)
	}
	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		var responseError *smithyhttp.ResponseError
		if errors.As(err, &responseError) && responseError.HTTPStatusCode() == http.StatusNotModified {
			return FlowDefinitionObject{NotModified: true}, nil
		}
		return FlowDefinitionObject{}, err
	}
	defer output.Body.Close()
	data, err := io.ReadAll(io.LimitReader(output.Body, maxFlowDefinitionBytes+1))
	if err != nil {
		return FlowDefinitionObject{}, err
	}
	return FlowDefinitionObject{Data: data, ETag: aws.ToString(output.ETag)}, nil
}

func (s *s3FlowDefinitionObjectStore) List(
	ctx context.Context,
	prefix string,
	continuationToken string,
) (FlowDefinitionObjectPage, error) {
	input := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)}
	if continuationToken != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}
	output, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return FlowDefinitionObjectPage{}, err
	}
	keys := make([]string, 0, len(output.Contents))
	for _, object := range output.Contents {
		keys = append(keys, aws.ToString(object.Key))
	}
	return FlowDefinitionObjectPage{
		Keys:              keys,
		ContinuationToken: aws.ToString(output.NextContinuationToken),
	}, nil
}

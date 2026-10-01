// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package web_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
)

type projectConfigurationAWSHTTPTest struct {
	baseURL, adminToken, projectID, prefix, bucket, kmsKey string
	client                                                 *http.Client
}

type projectConfigurationHTTPResult struct {
	status int
	body   []byte
	err    error
}

func TestProjectConfigurationAWSHTTPAndExactEnvironmentSnapshots(t *testing.T) {
	probe := &projectConfigurationAWSHTTPTest{
		baseURL: os.Getenv("DEX_PROJECT_CONFIG_TEST_URL"), adminToken: os.Getenv("DEX_PROJECT_CONFIG_TEST_ADMIN_TOKEN"),
		projectID: os.Getenv("DEX_PROJECT_CONFIG_TEST_PROJECT_ID"), prefix: os.Getenv("DEX_PROJECT_CONFIG_TEST_PREFIX"),
		bucket: os.Getenv("PROJECTCONFIG_TEST_BUCKET"), kmsKey: os.Getenv("PROJECTCONFIG_TEST_KMS_KEY_ARN"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
	parsedURL, err := url.Parse(probe.baseURL)
	require.NoError(t, err)
	require.True(t, parsedURL.Scheme == "http" || parsedURL.Scheme == "https", "an actual running Dex Web endpoint is required")
	_, err = uuid.Parse(probe.projectID)
	require.NoError(t, err, "the independently started Dex must use a fresh test-owned project UUID")
	require.Equal(t, "dex-web-integration/"+probe.projectID, probe.prefix)
	require.True(t, len(probe.adminToken) >= 32, "an explicit private admin token is required")
	require.NotEmpty(t, probe.bucket)
	require.NotEmpty(t, probe.kmsKey)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	awsConfig, err := config.LoadDefaultConfig(ctx)
	require.NoError(t, err)
	s3Client := s3.NewFromConfig(awsConfig)
	cleanup := &projectConfigurationAWSStorageCleanup{test: t, client: s3Client, bucket: probe.bucket, prefix: probe.prefix + "/"}
	t.Cleanup(cleanup.deleteOwnedVersions)
	objects, err := projectconfig.NewS3ObjectStore(ctx, &projectconfig.S3StoreConfig{Client: s3Client, Bucket: probe.bucket, Prefix: probe.prefix, KMSKeyID: probe.kmsKey})
	require.NoError(t, err)
	store, err := projectconfig.NewConfigurationStore(&projectconfig.ConfigurationStoreConfig{Objects: objects, Scope: projectconfig.Scope{ProjectID: probe.projectID, Kind: "live"}})
	require.NoError(t, err)
	manifest := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz","environment":[{"name":"APP_ENV","required":true,"enum":["testing","production"]},{"name":"SIGNING_SECRET","required":true,"secret":true,"minLength":32}]},"flowDefinitions":[{"sourcePath":"internal/process/basic_process_flow.go"}],"connectors":[]}`
	digestBytes := sha256.Sum256([]byte(manifest))
	manifestDigest := "sha256:" + hex.EncodeToString(digestBytes[:])
	sourceCommit := os.Getenv("DEX_PROJECT_CONFIG_TEST_SOURCE_COMMIT")
	require.Len(t, sourceCommit, 40)
	write := map[string]any{"expectedRevision": 0, "sourceCommit": sourceCommit, "manifestDigest": manifestDigest, "manifest": manifest}
	probe.expectStatus(t, probe.request(ctx, "PUT", "/api/v2/project-configuration/app-manifest", write, 0), 200)
	probe.expectStatus(t, probe.request(ctx, "PUT", "/api/v2/project-configuration/app-manifest", write, 0), 409)
	secret := uuid.NewString() + uuid.NewString()
	probe.expectStatus(t, probe.request(ctx, "PUT", "/api/v2/application-environment", map[string]any{"values": map[string]string{"APP_ENV": "testing"}, "secrets": map[string]string{"SIGNING_SECRET": secret}}, 0), 200)
	read := probe.request(ctx, "GET", "/api/v2/application-environment", nil, 1)
	probe.expectStatus(t, read, 200)
	require.False(t, bytes.Contains(read.body, []byte(secret)), "private values must not enter HTTP responses")
	var environment struct {
		ConfigurationRevision uint64 `json:"configurationRevision"`
		Fields                []struct {
			Name       string  `json:"name"`
			Value      *string `json:"value"`
			Configured bool    `json:"configured"`
		} `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(read.body, &environment))
	require.EqualValues(t, 1, environment.ConfigurationRevision)
	require.Len(t, environment.Fields, 2)
	for _, field := range environment.Fields {
		if field.Name == "SIGNING_SECRET" {
			require.True(t, field.Configured)
			require.Nil(t, field.Value)
		}
	}
	validation := probe.request(ctx, "POST", "/api/v2/project-configuration/validate", map[string]any{"appManifestRevision": 1, "manifestDigest": manifestDigest, "configurationRevision": 1}, 1)
	probe.expectStatus(t, validation, 200)
	var accepted struct {
		Status   string                    `json:"status"`
		Snapshot projectconfig.SnapshotRef `json:"snapshot"`
	}
	require.NoError(t, json.Unmarshal(validation.body, &accepted))
	require.Equal(t, "READY", accepted.Status)
	historical, err := store.ReadSnapshot(ctx, accepted.Snapshot)
	require.NoError(t, err)
	require.EqualValues(t, 1, historical.Revision)
	resolved, err := store.ResolveApplicationEnvironment(ctx, historical)
	require.NoError(t, err)
	firstSecret, found := resolved.Lookup("SIGNING_SECRET")
	require.True(t, found)
	require.True(t, firstSecret == secret, "the exact accepted secret version must load")
	results := make(chan projectConfigurationHTTPResult, 6)
	start := make(chan struct{})
	for index := 0; index < cap(results); index++ {
		go probe.writeConcurrentEnvironment(ctx, start, results)
	}
	close(start)
	winners := 0
	for index := 0; index < cap(results); index++ {
		result := <-results
		require.NoError(t, result.err)
		if result.status == 200 {
			winners++
		} else {
			require.Equal(t, 409, result.status)
		}
	}
	require.Equal(t, 1, winners)
	replacement := uuid.NewString() + uuid.NewString()
	probe.expectStatus(t, probe.request(ctx, "PUT", "/api/v2/application-environment", map[string]any{"secrets": map[string]string{"SIGNING_SECRET": replacement}}, 2), 200)
	historical, err = store.ReadSnapshot(ctx, accepted.Snapshot)
	require.NoError(t, err)
	resolved, err = store.ResolveApplicationEnvironment(ctx, historical)
	require.NoError(t, err)
	firstSecret, found = resolved.Lookup("SIGNING_SECRET")
	require.True(t, found)
	require.True(t, firstSecret == secret, "replacement must preserve prior accepted secret versions")
	current, _, err := store.ReadConfiguration(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3, current.Revision)
	probe.expectStatus(t, probe.request(ctx, "PUT", "/api/v2/application-environment", map[string]any{"values": map[string]string{"AWS_REGION": "us-west-2"}}, 3), 400)
	t.Logf("Real Dex HTTP + AWS S3/KMS: project=%s one concurrent CAS winner; accepted version=%s; prior private version preserved", probe.projectID, accepted.Snapshot.Version)
}

func (probe *projectConfigurationAWSHTTPTest) request(ctx context.Context, method, route string, body any, revision uint64) projectConfigurationHTTPResult {
	var contents []byte
	var err error
	if body != nil {
		contents, err = json.Marshal(body)
		if err != nil {
			return projectConfigurationHTTPResult{err: err}
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(probe.baseURL, "/")+route, bytes.NewReader(contents))
	if err != nil {
		return projectConfigurationHTTPResult{err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+probe.adminToken)
	request.Header.Set("X-Dex-Actor-ID", "aws-integration-operator")
	request.Header.Set("X-Forwarded-Prefix", "/dex/projects/"+probe.projectID+"/configuration/live")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "test.dexai.dev")
	request.Header.Set("X-Dex-Web-Embedded", "true")
	request.Header.Set("X-Dex-Web-CSRF-Token", probe.projectID)
	request.Header.Set("X-Dex-CSRF-Token", probe.projectID)
	request.Header.Set("Origin", "https://test.dexai.dev")
	request.Header.Set("X-Dex-App-Manifest-Revision", "1")
	request.Header.Set("X-Dex-Configuration-Revision", fmt.Sprint(revision))
	response, err := probe.client.Do(request)
	if err != nil {
		return projectConfigurationHTTPResult{err: err}
	}
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return projectConfigurationHTTPResult{err: readErr}
	}
	if closeErr != nil {
		return projectConfigurationHTTPResult{err: closeErr}
	}
	return projectConfigurationHTTPResult{status: response.StatusCode, body: contents}
}

func (probe *projectConfigurationAWSHTTPTest) expectStatus(t *testing.T, result projectConfigurationHTTPResult, status int) {
	t.Helper()
	require.NoError(t, result.err)
	var failure struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(result.body, &failure)
	require.Equal(t, status, result.status, "HTTP error code=%s; private response contents omitted", failure.Code)
}

func (probe *projectConfigurationAWSHTTPTest) writeConcurrentEnvironment(ctx context.Context, start <-chan struct{}, results chan<- projectConfigurationHTTPResult) {
	<-start
	results <- probe.request(ctx, "PUT", "/api/v2/application-environment", map[string]any{"values": map[string]string{"APP_ENV": "production"}}, 1)
}

type projectConfigurationAWSStorageCleanup struct {
	test           *testing.T
	client         *s3.Client
	bucket, prefix string
}

func (cleanup *projectConfigurationAWSStorageCleanup) deleteOwnedVersions() {
	t, client := cleanup.test, cleanup.client
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	prefix := cleanup.prefix
	paginator := s3.NewListObjectVersionsPaginator(client, &s3.ListObjectVersionsInput{Bucket: aws.String(cleanup.bucket), Prefix: aws.String(prefix)})
	deleted := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		require.NoError(t, err)
		owned := []types.ObjectIdentifier{}
		for _, version := range page.Versions {
			require.True(t, strings.HasPrefix(aws.ToString(version.Key), prefix))
			owned = append(owned, types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range page.DeleteMarkers {
			require.True(t, strings.HasPrefix(aws.ToString(marker.Key), prefix))
			owned = append(owned, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(owned) > 0 {
			result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(cleanup.bucket), Delete: &types.Delete{Objects: owned}})
			require.NoError(t, err)
			require.Empty(t, result.Errors)
			deleted += len(owned)
		}
	}
	remaining, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(cleanup.bucket), Prefix: aws.String(prefix)})
	require.NoError(t, err)
	require.Empty(t, remaining.Versions)
	require.Empty(t, remaining.DeleteMarkers)
	t.Logf("Removed %d exact owned S3 versions; absence confirmed under %s", deleted, prefix)
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVisualizeV2JSONContainsGeneratedAt(t *testing.T) {
	sourcePath := generatedAtTestFlowSource(t)
	before := time.Now().UTC().Truncate(time.Second)

	document := visualizeGeneratedAtDocument(t, sourcePath, "2.0", map[string]string{})

	after := time.Now().UTC()
	generatedAt, ok := document["generatedAt"].(string)
	require.True(t, ok, "generatedAt = %#v", document["generatedAt"])
	require.True(t, strings.HasSuffix(generatedAt, "Z"), generatedAt)
	parsed, err := time.Parse(time.RFC3339, generatedAt)
	require.NoError(t, err)
	require.Zero(t, parsed.Nanosecond())
	require.False(t, parsed.Before(before), "%s is before %s", parsed, before)
	require.False(t, parsed.After(after), "%s is after %s", parsed, after)
}

func TestVisualizeV2JSONHonorsSourceDateEpoch(t *testing.T) {
	sourcePath := generatedAtTestFlowSource(t)

	document := visualizeGeneratedAtDocument(t, sourcePath, "2.0", map[string]string{
		"SOURCE_DATE_EPOCH": "1762763400",
	})

	require.Equal(t, "2025-11-10T08:30:00Z", document["generatedAt"])
}

func TestVisualizeRejectsInvalidSourceDateEpoch(t *testing.T) {
	sourcePath := generatedAtTestFlowSource(t)
	for _, epochValue := range []string{"abc", "-1", "1762763400.5", "253402300800"} {
		t.Run(epochValue, func(t *testing.T) {
			var stdout bytes.Buffer
			app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
			app.lookupEnv = generatedAtTestEnvironment(map[string]string{"SOURCE_DATE_EPOCH": epochValue})
			err := app.Execute(context.Background(), []string{
				"visualize", sourcePath, "--schema-version", "2.0", "--json",
			})
			require.Error(t, err)
			require.Equal(t, 2, ExitCode(err))
			require.Contains(t, err.Error(), "SOURCE_DATE_EPOCH")
			require.Empty(t, stdout.String())
		})
	}
}

func TestVisualizeV1JSONOmitsGeneratedAt(t *testing.T) {
	sourcePath := generatedAtTestFlowSource(t)

	document := visualizeGeneratedAtDocument(t, sourcePath, "1.0", map[string]string{
		"SOURCE_DATE_EPOCH": "1762763400",
	})

	require.NotContains(t, document, "generatedAt")
}

func TestVisualizeV2RenderingAcceptsGeneratedAt(t *testing.T) {
	sourcePath := generatedAtTestFlowSource(t)
	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	app.lookupEnv = generatedAtTestEnvironment(map[string]string{"SOURCE_DATE_EPOCH": "1762763400"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.openBrowser = func(url string) error {
		baseURL := strings.TrimSuffix(url, "/v1/rendering")
		var catalog struct {
			Definitions []struct {
				SchemaVersion string `json:"schemaVersion"`
				FlowName      string `json:"flowName"`
			} `json:"definitions"`
		}
		getGeneratedAtTestJSON(t, baseURL+"/api/flow-definitions", &catalog)
		require.Len(t, catalog.Definitions, 1)
		require.Equal(t, "2.0", catalog.Definitions[0].SchemaVersion)
		var flowTypeDefinitions struct {
			Definitions []struct {
				Graph struct {
					GeneratedAt string `json:"generatedAt"`
				} `json:"graph"`
			} `json:"definitions"`
		}
		getGeneratedAtTestJSON(t, baseURL+"/api/flow-definitions/by-type?flowType="+neturl.QueryEscape(catalog.Definitions[0].FlowName), &flowTypeDefinitions)
		require.Len(t, flowTypeDefinitions.Definitions, 1)
		require.Equal(t, "2025-11-10T08:30:00Z", flowTypeDefinitions.Definitions[0].Graph.GeneratedAt)
		cancel()
		return nil
	}
	require.NoError(t, app.Execute(ctx, []string{"visualize", sourcePath, "--schema-version", "2.0"}))
}

func generatedAtTestFlowSource(t *testing.T) string {
	t.Helper()
	return filepath.Join(visualizerRepositoryRoot(t),
		"examples/python/dex_examples/patterns/cron/cron_schedule_flow.py")
}

func visualizeGeneratedAtDocument(
	t *testing.T,
	sourcePath string,
	schemaVersion string,
	environment map[string]string,
) map[string]any {
	t.Helper()
	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	app.lookupEnv = generatedAtTestEnvironment(environment)
	require.NoError(t, app.Execute(context.Background(), []string{
		"visualize", sourcePath, "--schema-version", schemaVersion, "--json",
	}))
	var document map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &document))
	return document
}

func generatedAtTestEnvironment(environment map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	}
}

func getGeneratedAtTestJSON(t *testing.T, url string, target any) {
	t.Helper()
	response, err := http.Get(url)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, json.NewDecoder(response.Body).Decode(target))
}

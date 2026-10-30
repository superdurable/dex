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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeScriptConnectorFactories(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			repositoryRoot := visualizerRepositoryRoot(t)
			t.Setenv("NODE_PATH", filepath.Join(repositoryRoot, "web", "node_modules"))
			directory := t.TempDir()
			require.NoError(t, os.CopyFS(directory, os.DirFS(filepath.Join(repositoryRoot, "cli", "internal", "flowviz", "testdata", language))))
			extension := ".py"
			if language == "typescript" {
				extension = ".ts"
				require.NoError(t, os.Rename(filepath.Join(directory, "node-packages"), filepath.Join(directory, "node_modules")))
			}
			var stdout, stderr bytes.Buffer
			app := NewApp(strings.NewReader(""), &stdout, &stderr)
			require.NoError(t, app.Execute(context.Background(), []string{"visualize", filepath.Join(directory, "workflow"+extension), "--language", language, "--schema-version", "2.0", "--json", "--lint", "app"}), "%s", stderr.String())
			var graph flowviz.Graph
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
			require.True(t, graph.Valid)
			require.Equal(t, language, graph.Source.Language)
			require.Equal(t, flowviz.SchemaVersionV2, graph.SchemaVersion)
			require.NotEmpty(t, graph.Groups)
			hasConnector := false
			for _, node := range graph.Nodes {
				if node.ID != "step:LookupRecord" {
					continue
				}
				hasConnector = true
				require.Equal(t, true, node.Metadata["connector"].(map[string]any)["configurationEnabled"])
			}
			require.True(t, hasConnector)
		})
	}
}

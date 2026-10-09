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
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeHandlesAppendOptionCycles(t *testing.T) {
	if os.Getenv("DEXCLI_TEST_APPEND_OPTIONS") != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, executable, "-test.run=^TestVisualizeHandlesAppendOptionCycles$", "-test.v")
		command.Env = append(os.Environ(), "DEXCLI_TEST_APPEND_OPTIONS=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	// Bound a fatal stack overflow to this subprocess and keep its failure output small.
	debug.SetMaxStack(64 << 20)
	source := stateLoadsFixture(t, "append-options/workflow.go")
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	validDirectory := t.TempDir()
	validSource := filepath.Join(validDirectory, "workflow.go")
	require.NoError(t, os.WriteFile(validSource, []byte(strings.ReplaceAll(string(data), "[]dex.AttributeDef{First}", "[]dex.AttributeDef{First, Missing}")), 0o644))
	module := "module appendoptioncontracts\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(visualizerRepositoryRoot(t), "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(validDirectory, "go.mod"), []byte(module), 0o644))
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
		for _, lint := range []string{"", flowviz.LintApplication} {
			t.Run(schemaVersion+"/lint="+lint, func(t *testing.T) {
				output := &bytes.Buffer{}
				app := NewApp(strings.NewReader(""), output, &bytes.Buffer{})
				arguments := []string{"visualize", source, "--json", "--schema-version", schemaVersion}
				if lint != "" {
					arguments = append(arguments, "--lint", lint)
				}
				err := app.Execute(context.Background(), arguments)
				require.Error(t, err)
				require.Equal(t, 1, ExitCode(err))
				var graph flowviz.Graph
				require.NoError(t, json.Unmarshal(output.Bytes(), &graph))
				require.False(t, graph.Valid)
				require.Equal(t, []string{"attribute_map_enumeration_not_loaded"}, sortedDiagnosticCodes(graph.Diagnostics), "%+v", graph.Diagnostics)
				require.Contains(t, graph.Diagnostics[0].Message, `"missing"`)
				arguments[1] = validSource
				output.Reset()
				require.NoError(t, app.Execute(context.Background(), arguments))
				require.NoError(t, json.Unmarshal(output.Bytes(), &graph))
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
				require.Empty(t, graph.Diagnostics)
			})
		}
	}
}

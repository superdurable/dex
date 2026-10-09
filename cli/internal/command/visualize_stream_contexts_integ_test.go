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

func TestVisualizeRejectsStreamOperationsOutsideSteps(t *testing.T) {
	source := stateLoadsFixture(t, "stream-contexts/workflow.go")
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
		for _, lint := range []string{"", flowviz.LintApplication} {
			t.Run(schemaVersion+"/lint="+lint, func(t *testing.T) {
				graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{
					SchemaVersion: schemaVersion, Lint: lint,
				})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Len(t, graph.Diagnostics, 9, "%+v", graph.Diagnostics)
				for _, diagnostic := range graph.Diagnostics {
					require.Equal(t, "step_progress_outside_step", diagnostic.Code)
					require.Equal(t, "error", diagnostic.Severity)
					require.Contains(t, diagnostic.Message, `Stream "activity"`)
					require.Contains(t, diagnostic.Message, "only available in Step WaitFor and Execute")
				}

				helper := diagnosticWithCode(t, graph, "step_progress_outside_step", "RPC SendActivity")
				require.Contains(t, helper.Message, "Write in writeStreamActivity at activity.go:")
				require.Equal(t, sourceLine(t, source, "func (flow *ActivityFlow) SendActivity(")+1, helper.Span.StartLine)
				require.Contains(t, helper.Message, "publish a Channel message or return NextSteps")

				constructor := diagnosticWithCode(t, graph, "step_progress_outside_step", "RPC CreateActivityWriter")
				require.Contains(t, constructor.Message, "(NewBufferedTextStream)")
				require.Equal(t, sourceLine(t, source, "func (*ActivityFlow) CreateActivityWriter(")+1, constructor.Span.StartLine)

				require.NotEmpty(t, diagnosticWithCode(t, graph, "step_progress_outside_step", "RPC SendLoopActivity"))
				require.NotEmpty(t, diagnosticWithCode(t, graph, "step_progress_outside_step", "RPC SendBufferedActivity"))
				require.NotEmpty(t, diagnosticWithCode(t, graph, "step_progress_outside_step", "HandleTimeout"))
			})
		}
	}
}

func TestVisualizeWritesPartialJSONForInvalidStreamContext(t *testing.T) {
	outputPrefix := filepath.Join(t.TempDir(), "invalid-stream-context")
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{
		"visualize", stateLoadsFixture(t, "stream-contexts/workflow.go"),
		"--schema-version", "2.0", "--json", "--out", outputPrefix,
	})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	data, readErr := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, readErr)
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(data, &graph))
	require.False(t, graph.Valid)
	require.Contains(t, diagnosticCodes(graph.Diagnostics), "step_progress_outside_step")
}

func TestVisualizeAcceptsStepStreamOperations(t *testing.T) {
	source := stateLoadsFixture(t, "stream-contexts-valid/workflow.go")
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
		graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{
			SchemaVersion: schemaVersion, Lint: flowviz.LintApplication,
		})
		require.NoError(t, err)
		require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		require.Empty(t, graph.Diagnostics)
		for _, phase := range []string{"wait_for", "execute"} {
			hasStreamWrite := false
			for _, edge := range graph.Edges {
				if edge.Kind == "resource_write" && edge.To == "resource:stream:Activity" && edge.Metadata["phase"] == phase {
					hasStreamWrite = true
				}
			}
			require.True(t, hasStreamWrite, "missing %s Stream write", phase)
		}
	}
}

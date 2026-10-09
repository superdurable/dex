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

func TestVisualizeRejectsMismatchedStartInputs(t *testing.T) {
	source := stateLoadsFixture(t, "start-inputs/workflow.go")
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
		for _, lint := range []string{"", flowviz.LintApplication} {
			t.Run(schemaVersion+"/lint="+lint, func(t *testing.T) {
				graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{
					SchemaVersion: schemaVersion, Lint: lint,
				})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Len(t, graph.Diagnostics, 6, "%+v", graph.Diagnostics)
				for _, diagnostic := range graph.Diagnostics {
					require.Equal(t, "start_flow_input_type_mismatch", diagnostic.Code)
					require.Equal(t, "error", diagnostic.Severity)
					require.Contains(t, diagnostic.Message, "before sending the start request")
					require.Contains(t, diagnostic.Message, "declare dex.None and pass nil")
				}
				nilInput := diagnosticWithCode(t, graph, "start_flow_input_type_mismatch", "passes nil")
				require.Contains(t, nilInput.Message, "BeginInputStep, whose input is startinputs.StartInput")
				pointer := diagnosticWithCode(t, graph, "start_flow_input_type_mismatch", "passes *startinputs.StartInput")
				require.Equal(t, sourceLine(t, source, `"pointer-input"`), pointer.Span.StartLine)
				pointerTarget := diagnosticWithCode(t, graph, "start_flow_input_type_mismatch", "whose input is *startinputs.StartInput")
				require.Equal(t, sourceLine(t, source, `"pointer-struct"`), pointerTarget.Span.StartLine)
				require.NotEmpty(t, diagnosticWithCode(t, graph, "start_flow_input_type_mismatch", "NoInputStep"))
				helper := diagnosticWithCode(t, graph, "start_flow_input_type_mismatch", "startMissingInput at targets.go:")
				require.Equal(t, sourceLine(t, source, "startMissingInput(context.Background()"), helper.Span.StartLine)
			})
		}
	}
}

func TestVisualizeAcceptsMatchingAndUnknownStartInputs(t *testing.T) {
	fixtureSource := stateLoadsFixture(t, "start-inputs/workflow.go")
	directory := t.TempDir()
	for _, name := range []string{"workflow.go", "targets.go"} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(fixtureSource), name))
		require.NoError(t, err)
		source := string(data)
		for _, replacement := range [][2]string{
			{`"nil-input", nil`, `"nil-input", StartInput{}`},
			{`"string-input", "wrong type"`, `"string-input", StartInput{}`},
			{`"pointer-input", &StartInput{}`, `"pointer-input", StartInput{}`},
			{`"none-value", struct{}{}`, `"none-value", dex.None(nil)`},
			{`"pointer-struct", StartInput{}`, `"pointer-struct", &StartInput{}`},
			{`"helper-start", nil`, `"helper-start", StartInput{}`},
		} {
			source = strings.ReplaceAll(source, replacement[0], replacement[1])
		}
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(source), 0o644))
	}
	module := "module startinputcontracts\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(visualizerRepositoryRoot(t), "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
		graph, err := flowviz.Analyze(context.Background(), filepath.Join(directory, "workflow.go"), flowviz.AnalyzeOptions{
			SchemaVersion: schemaVersion, Lint: flowviz.LintApplication,
		})
		require.NoError(t, err)
		require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		require.Empty(t, graph.Diagnostics)
	}
}

func TestVisualizeWritesPartialJSONForMismatchedStartInput(t *testing.T) {
	outputPrefix := filepath.Join(t.TempDir(), "invalid-start-input")
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{
		"visualize", stateLoadsFixture(t, "start-inputs/workflow.go"), "--json", "--out", outputPrefix,
	})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	data, readErr := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, readErr)
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(data, &graph))
	require.False(t, graph.Valid)
	require.Contains(t, diagnosticCodes(graph.Diagnostics), "start_flow_input_type_mismatch")
}

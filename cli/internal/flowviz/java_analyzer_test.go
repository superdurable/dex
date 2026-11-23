// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavaAnalyzerGolden(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	source := filepath.Join("testdata", "java", "workflow.java")
	graph, err := Analyze(t.Context(), source, AnalyzeOptions{JavaPath: javaPath})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	graph.Source.Path = "workflow.java"
	data, err := MarshalJSON(graph)
	require.NoError(t, err)
	golden := filepath.Join("testdata", "java", "workflow.golden.json")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(golden, data, 0o644))
	}
	expected, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(data))
}

func TestJavaAnalyzerDiagnostics(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	tests := []struct {
		name     string
		file     string
		flow     string
		state    string
		expected []string
	}{
		{name: "ambiguous", file: "diagnostics.java", expected: []string{"multiple_flows"}},
		{name: "missing", file: "diagnostics.java", flow: "ExternalFlow", expected: []string{"flow_not_found"}},
		{name: "dynamic", file: "diagnostics.java", flow: "BrokenFlow", state: "bool", expected: []string{
			"dynamic_step_target", "hidden_dex_decision", "hidden_dex_wait", "start_input_unresolved", "v2_start_input",
		}},
		{name: "external", file: "diagnostics.java", flow: "RegisteredFlow", state: "string", expected: []string{"step_type_unresolved"}},
		{name: "syntax", file: "parse.java", expected: []string{"java_parse_failed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := Analyze(t.Context(), filepath.Join("testdata", "java", "shapes", test.file),
				AnalyzeOptions{Language: "java", JavaPath: javaPath, FlowType: test.flow})
			require.NoError(t, err)
			var codes []string
			for _, diagnostic := range graph.Diagnostics {
				codes = append(codes, diagnostic.Code)
			}
			require.ElementsMatch(t, test.expected, codes, "%+v", graph.Diagnostics)
			var states []string
			for _, node := range graph.Nodes {
				if node.ID == "resource:attribute:state" {
					states = append(states, node.Resource.ValueType)
				}
			}
			if test.state == "" {
				require.Empty(t, states)
			} else {
				require.Equal(t, []string{test.state}, states)
			}
		})
	}
}

func javaAnalyzerTestExecutable(t *testing.T) string {
	t.Helper()
	path := os.Getenv("DEX_JAVA")
	if path == "" {
		path = "java"
	}
	executable, err := exec.LookPath(path)
	if err != nil {
		t.Skip("Java visualization tests require JDK 17+ java")
	}
	return executable
}

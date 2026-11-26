// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavaAnalyzerGolden(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	source := filepath.Join("testdata", "java", "workflow.java")
	graph, err := Analyze(t.Context(), source, AnalyzeOptions{JavaPath: javaPath})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.NotContains(t, diagnosticCodes(graph), "unused_resource")
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
			"dynamic_step_target", "step_type_unresolved", "hidden_dex_wait", "start_input_unresolved", "start_input_unresolved",
		}},
		{name: "external", file: "diagnostics.java", flow: "RegisteredFlow", state: "string", expected: []string{"step_type_unresolved"}},
		{name: "progress outside a step", file: "diagnostics.java", flow: "ProgressFlow", expected: []string{
			"dynamic_type_name", "dynamic_type_name", "step_progress_outside_step", "step_progress_outside_step", "step_progress_outside_step",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := Analyze(t.Context(), filepath.Join("testdata", "java", "shapes", test.file),
				AnalyzeOptions{Language: "java", JavaPath: javaPath, FlowType: test.flow})
			require.NoError(t, err)
			require.ElementsMatch(t, test.expected, diagnosticCodes(graph), "%+v", graph.Diagnostics)
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
	t.Run("syntax", func(t *testing.T) {
		graph, err := analyzeJava(t.Context(), "broken.java", []byte("class Broken { public\n}"), javaPath, "")
		require.NoError(t, err)
		require.Equal(t, []string{"java_parse_failed"}, diagnosticCodes(graph))
	})
}

func TestJavaLockedMethodExternalCall(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	tests := []struct {
		flow string
		// messages lists every locked_method_external_call message fragment, in report order.
		messages []string
	}{
		{flow: "InjectedServiceFlow", messages: []string{
			"Execute of Send holds lock status, which RPC stop also takes, and calls mailer.send; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step.",
		}},
		{flow: "MovedCallFlow"},
		{flow: "StepOnlyLockFlow"},
		{flow: "PureFlow"},
		{flow: "SiblingMethodFlow", messages: []string{"and calls new Ticket(input, List.of()).render;"}},
		{flow: "HttpHelperFlow", messages: []string{"and calls notifyCustomer (calls HttpRequest.newBuilder); Dex fails"}},
		{flow: "ClientFlow", messages: []string{"Execute of StopOther holds lock status, which RPC stop also takes, and calls client.stopFlow;"}},
		{flow: "WaitForFlow", messages: []string{"WaitFor of Pause holds lock status, which RPC stop also takes, and calls Thread.sleep (and 1 more);"}},
		{flow: "MapInstanceFlow", messages: []string{"Execute of Computed holds lock orders, which RPC expedite also takes, and calls mailer.send;"}},
	}
	for _, test := range tests {
		t.Run(test.flow, func(t *testing.T) {
			graph, err := Analyze(t.Context(), filepath.Join("testdata", "java", "shapes", "locked_methods.java"),
				AnalyzeOptions{Language: "java", JavaPath: javaPath, FlowType: test.flow})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.NotEmpty(t, graph.methodExternalCalls)
			var messages []string
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "locked_method_external_call" {
					require.Equal(t, "warning", diagnostic.Severity)
					require.NotNil(t, diagnostic.Span)
					messages = append(messages, diagnostic.Message)
				}
			}
			require.Len(t, messages, len(test.messages), "%+v", graph.Diagnostics)
			for index, fragment := range test.messages {
				require.Contains(t, messages[index], fragment)
			}
		})
	}
}

func TestJavaAnalyzerUncaughtFailure(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	script := filepath.Join(t.TempDir(), "FlowvizJavaAnalyzer.java")
	require.NoError(t, os.WriteFile(script, javaAnalyzerSource, 0o600))
	command := exec.CommandContext(t.Context(), javaPath, script)
	command.Stdin = strings.NewReader("!")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	require.NoError(t, err, "%s", stderr.String())
	require.Empty(t, stderr.String())
	var result scriptAnalysis
	require.NoError(t, json.Unmarshal(output, &result))
	require.False(t, result.Graph.Valid)
	require.Equal(t, []string{"java_analyzer_failed"}, diagnosticCodes(&result.Graph))
}

func TestJavaAnalyzerProcessFailure(t *testing.T) {
	longDetail := strings.Repeat("é", 130)
	version17 := "printf 'openjdk version \"17.0.19\"\\n' >&2"
	tests := []struct {
		name    string
		version string
		analyze string
		want    string
	}{
		{"Java 11", "printf 'openjdk version \"11.0.29\"\\n' >&2", "", "Java analysis requires a JDK 17 or later: found Java 11"},
		{"Java 15", "printf 'java version \"15\"\\n' >&2", "", "Java analysis requires a JDK 17 or later: found Java 15"},
		{"Java 8", "printf 'java version \"1.8.0_451\"\\n' >&2", "", "Java analysis requires a JDK 17 or later: found Java 8"},
		{"launcher", "printf 'ignored stdout\\n'; printf 'Error: Could not find or load main class\\nstack trace\\n' >&2; exit 1", "",
			"Java analysis requires a JDK 17 or later: Error: Could not find or load main class"},
		{"unsupported class", version17, "printf 'UnsupportedClassVersionError: incompatible class\\nstack trace\\n' >&2",
			"Java analysis requires a JDK 17 or later: UnsupportedClassVersionError: incompatible class"},
		{"other failure", version17, "printf '" + longDetail + "\\nstack trace\\n' >&2",
			"Java analysis failed: " + strings.Repeat("é", 120) + "…"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			launcher := filepath.Join(t.TempDir(), "java")
			script := "#!/bin/sh\nif [ \"$1\" = '-version' ]; then\n" + test.version + "\nexit 0\nfi\n" +
				test.analyze + "\nexit 1\n"
			require.NoError(t, os.WriteFile(launcher, []byte(script), 0o700))
			graph, err := analyzeJava(t.Context(), "workflow.java", nil, launcher, "")
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Equal(t, []string{"java_analyzer_failed"}, diagnosticCodes(graph))
			require.Equal(t, test.want, graph.Diagnostics[0].Message)
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

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeDefaultsToJSONNextToPythonSource(t *testing.T) {
	temporaryDirectory := t.TempDir()
	sourcePath := filepath.Join(temporaryDirectory, "order_flow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o644))

	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, app.Execute(context.Background(), []string{"visualize", sourcePath}))

	jsonPath := filepath.Join(temporaryDirectory, "order_flow.flow.json")
	require.FileExists(t, jsonPath)
	require.NoFileExists(t, filepath.Join(temporaryDirectory, "order_flow.flow.svg"))
	require.Contains(t, stdout.String(), jsonPath)
}

func TestVisualizeCanWriteJSONToStdout(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "flow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o644))

	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, app.Execute(context.Background(), []string{"visualize", sourcePath, "--out", "-"}))

	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
	require.True(t, graph.Valid)
	require.Equal(t, "PythonFlow", graph.Flow.Name)
}

func TestVisualizeRejectsRemovedFormatFlag(t *testing.T) {
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{"visualize", "flow.py", "--format", "svg"})
	require.Error(t, err)
	require.Equal(t, 2, ExitCode(err))
}

func TestVisualizeGoAndPythonRecoveryPolicies(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name   string
		source string
	}{
		{name: "go", source: filepath.Join(repositoryRoot, "examples/go/patterns/recovery/workflow.go")},
		{name: "python", source: filepath.Join(repositoryRoot, "examples/python/dex_examples/patterns/recovery/failure_recovery_flow.py")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outputPrefix := filepath.Join(t.TempDir(), "recovery")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			app := NewApp(strings.NewReader(""), &stdout, &stderr)
			executeErr := app.Execute(context.Background(), []string{"visualize", test.source, "--out", outputPrefix})
			if executeErr != nil {
				partial, readErr := os.ReadFile(outputPrefix + ".json")
				if readErr == nil {
					t.Log(string(partial))
				}
			}
			require.NoError(t, executeErr, stderr.String())

			data, err := os.ReadFile(outputPrefix + ".json")
			require.NoError(t, err)
			var graph flowviz.Graph
			require.NoError(t, json.Unmarshal(data, &graph))
			require.True(t, graph.Valid, graph.Diagnostics)
			failureEdges := edgesOfKind(graph.Edges, "failure_transition")
			require.Len(t, failureEdges, 2)
			for _, edge := range failureEdges {
				require.Equal(t, "Execute failure", edge.Label)
				require.NotEmpty(t, edge.Metadata["skipWaitFor"])
			}
		})
	}
}

func TestVisualizeDynamicPythonTargetWritesPartialArtifacts(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "dynamic.py")
	source := strings.Replace(minimalPythonFlow, "return graceful_complete()", "return go_to(target(), None)", 1)
	source = strings.Replace(source, "StepList, graceful_complete", "StepList, go_to, graceful_complete", 1)
	require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
	outputPrefix := filepath.Join(t.TempDir(), "dynamic")

	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{"visualize", sourcePath, "--out", outputPrefix})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	require.FileExists(t, outputPrefix+".json")
	data, readErr := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, readErr)
	require.Contains(t, string(data), `"kind": "unknown"`)
}

func TestVisualizeRepresentativeGoAndPythonFlows(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name        string
		source      string
		expectKinds []string
	}{
		{name: "go drain", source: "examples/go/patterns/drain-channels/internal-drain/workflow.go", expectKinds: []string{"wait_condition", "resource_publish"}},
		{name: "python drain", source: "examples/python/dex_examples/patterns/drain-channels/internal/drain_internal_channels_flow.py", expectKinds: []string{"wait_condition", "resource_publish"}},
		{name: "go branches", source: "examples/go/primitives/step-decision/workflow.go", expectKinds: []string{"decision", "cancel"}},
		{name: "python branches", source: "examples/python/dex_examples/primitives/step_decision/step_decision_flow.py", expectKinds: []string{"decision", "cancel"}},
		{name: "go RPC", source: "examples/go/primitives/rpc/workflow.go", expectKinds: []string{"rpc", "resource_publish", "wait_condition"}},
		{name: "python RPC", source: "examples/python/dex_examples/primitives/rpc/rpc_flow.py", expectKinds: []string{"rpc", "resource_publish", "wait_condition"}},
		{name: "go stream", source: "examples/go/primitives/stream/workflow.go", expectKinds: []string{"stream", "resource_write"}},
		{name: "python stream", source: "examples/python/dex_examples/primitives/stream/stream_flow.py", expectKinds: []string{"stream", "resource_write"}},
		{name: "go timeout", source: "examples/go/patterns/timeout/workflow.go", expectKinds: []string{"timeout_handler", "timer"}},
		{name: "python timeout", source: "examples/python/dex_examples/patterns/timeout/flow_graceful_timeout.py", expectKinds: []string{"timeout_handler", "timer"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join(repositoryRoot, test.source)
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			for _, kind := range test.expectKinds {
				require.True(t, graphHasKind(graph, kind), "missing %s in nodes or edges", kind)
			}
			if strings.Contains(test.name, "RPC") {
				require.True(t, graphHasEdgeFromPrefix(graph, "rpc:", "transition"), "RPC next Step transition is missing")
			}
		})
	}
}

func TestVisualizeScansEveryExampleFlowSource(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	expectedBlocking := map[string][]string{
		"examples/go/patterns/entity-store/workflow.go":                                                   {"dynamic_step_registration"},
		"examples/go/patterns/parallel-subflows/flows.go":                                                 {"multiple_flows"},
		"examples/go/primitives/options-override/workflow.go":                                             {"hidden_dex_wait"},
		"examples/go/primitives/proceed-on-wait-failure/workflow.go":                                      {"hidden_dex_wait"},
		"examples/go/primitives/subflow/workflow.go":                                                      {"multiple_flows"},
		"examples/go/products/subscription/workflow.go":                                                   {"hidden_dex_decision", "hidden_dex_wait"},
		"examples/python/dex_examples/patterns/entity-store/user_profile_flow.py":                         {"dynamic_step_registration"},
		"examples/python/dex_examples/patterns/parallel-subflows/flows.py":                                {"multiple_flows"},
		"examples/python/dex_examples/primitives/options_override/options_override_flow.py":               {"hidden_dex_wait"},
		"examples/python/dex_examples/primitives/proceed_on_wait_failure/proceed_on_wait_failure_flow.py": {"hidden_dex_wait"},
		"examples/python/dex_examples/primitives/subflow/subflow_flow.py":                                 {"multiple_flows"},
		"examples/python/dex_examples/primitives/timer/timer_flow.py":                                     {"dynamic_step_target"},
		"examples/python/sync-python/sync_examples/patterns/parent_child/parent_child.py":                 {"multiple_flows"},
	}

	sources := exampleFlowSources(t, repositoryRoot)
	require.NotEmpty(t, sources)
	for _, source := range sources {
		relative, err := filepath.Rel(repositoryRoot, source)
		require.NoError(t, err)
		relative = filepath.ToSlash(relative)
		t.Run(relative, func(t *testing.T) {
			graph, analyzeErr := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
			require.NoError(t, analyzeErr)
			require.Equal(t, "1.0", graph.SchemaVersion)
			first, marshalErr := json.Marshal(graph)
			require.NoError(t, marshalErr)
			second, marshalErr := json.Marshal(graph)
			require.NoError(t, marshalErr)
			require.Equal(t, first, second)

			expected := expectedBlocking[relative]
			if len(expected) == 0 {
				require.True(t, graph.Valid, graph.Diagnostics)
				return
			}
			require.False(t, graph.Valid)
			for _, code := range expected {
				require.Contains(t, diagnosticCodes(graph.Diagnostics), code)
			}
		})
	}
}

func TestVisualizeProducesStructuredStepInternals(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name   string
		source string
	}{
		{name: "go", source: "examples/go/primitives/channel/workflow.go"},
		{name: "python", source: "examples/python/dex_examples/primitives/channel/channel_flow.py"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, test.source), flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)

			waits := nodesOfKind(graph.Nodes, "wait")
			require.Len(t, waits, 1)
			require.NotNil(t, waits[0].Wait)
			require.Equal(t, "anyOf", waits[0].Wait.Type)
			if test.name == "python" {
				require.Equal(t, "step:ChannelWaitStep", waits[0].ParentID)
			} else {
				require.Equal(t, "step:channelWaitStep", waits[0].ParentID)
			}
			require.Equal(t, []string{"channel", "timer"}, waitConditionKinds(waits[0].Wait.Conditions))
			require.Contains(t, waits[0].Wait.Conditions[0].Label, ".for 1")

			for _, legacyKind := range []string{"start", "timer", "terminal"} {
				require.Empty(t, nodesOfKind(graph.Nodes, legacyKind))
			}
			for _, edge := range edgesOfKind(graph.Edges, "wait_condition") {
				require.True(t, strings.HasPrefix(edge.From, "resource:channel:"))
				require.True(t, strings.HasPrefix(edge.To, "wait:"))
			}
		})
	}
}

func TestVisualizeGroupsConditionalWaitReturns(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizconditionalwait\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	goSource := filepath.Join(directory, "conditional_wait.go")
	require.NoError(t, os.WriteFile(goSource, []byte(goConditionalWaitFlow), 0o644))
	pythonSource := filepath.Join(directory, "conditional_wait.py")
	require.NoError(t, os.WriteFile(pythonSource, []byte(pythonConditionalWaitFlow), 0o644))

	tests := []struct {
		name           string
		source         string
		conditionKinds []string
	}{
		{name: "go", source: goSource, conditionKinds: []string{"channel", "timer"}},
		{name: "python returns", source: pythonSource, conditionKinds: []string{"channel", "timer"}},
		{name: "python conditional resource", source: filepath.Join(repositoryRoot, "sdk-python/tests/integ/conditional_complete_flow.py"), conditionKinds: []string{"channel", "channel"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), test.source, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			require.Len(t, nodesOfKind(graph.Nodes, "wait_dispatch"), 1)

			waits := nodesOfKind(graph.Nodes, "wait")
			require.Len(t, waits, 2)
			actualKinds := make([]string, 0, len(waits))
			for _, wait := range waits {
				require.NotEmpty(t, wait.Condition)
				require.NotNil(t, wait.Wait)
				require.Len(t, wait.Wait.Conditions, 1)
				actualKinds = append(actualKinds, wait.Wait.Conditions[0].Kind)
			}
			require.ElementsMatch(t, test.conditionKinds, actualKinds)
			if test.name != "python conditional resource" {
				require.Contains(t, waitConditionLabels(waits), "2 seconds timer")
			}
		})
	}
}

func TestVisualizeFormatsChannelBoundsAndDynamicTimers(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizwaitbounds\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	sources := map[string]string{
		"go":     goWaitBoundsFlow,
		"python": pythonWaitBoundsFlow,
	}
	for language, source := range sources {
		t.Run(language, func(t *testing.T) {
			extension := map[string]string{"go": "go", "python": "py"}[language]
			sourcePath := filepath.Join(directory, "bounds."+extension)
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)

			labels := strings.Join(waitConditionLabels(nodesOfKind(graph.Nodes, "wait")), "\n")
			for _, fragment := range []string{"Messages.for 2", "Messages.at least 3", "Messages.at most 4", "Messages.for 2…5", "Buckets["} {
				require.Contains(t, labels, fragment)
			}
			if language == "go" {
				require.Contains(t, labels, "time.Duration(input) * time.Second timer")
			} else {
				require.Contains(t, labels, "timedelta(seconds=input) timer")
			}
			var hasMap bool
			for _, resource := range graph.Nodes {
				if resource.Kind == "channel" && resource.Resource != nil && resource.Resource.Map {
					hasMap = true
				}
			}
			require.True(t, hasMap)
		})
	}
}

func TestVisualizeIncludesResourceTypesAndDecisionDetails(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name   string
		source string
		typeOf string
	}{
		{name: "go", source: "examples/go/primitives/attribute/workflow.go", typeOf: "string"},
		{name: "python", source: "examples/python/dex_examples/primitives/attribute/attribute_flow.py", typeOf: "str"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, test.source), flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)

			attributes := nodesOfKind(graph.Nodes, "attribute")
			require.NotEmpty(t, attributes)
			for _, attribute := range attributes {
				require.NotNil(t, attribute.Resource)
				require.Equal(t, test.typeOf, attribute.Resource.ValueType)
			}
			decisions := nodesOfKind(graph.Nodes, "decision")
			require.NotEmpty(t, decisions)
			require.True(t, hasDecisionType(decisions, "gracefulComplete"))
		})
	}
}

func TestVisualizeConditionalCompletionCarriesFallbackAndChannels(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name   string
		source string
	}{
		{name: "go", source: "examples/go/patterns/drain-channels/external-publishing/workflow.go"},
		{name: "python", source: "examples/python/dex_examples/patterns/drain-channels/external_publishing/draining_channel_flow.py"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, test.source), flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			var conditional *flowviz.Node
			for index := range graph.Nodes {
				if graph.Nodes[index].Decision != nil && graph.Nodes[index].Decision.Type == "forceCompleteIfChannelsEmpty" {
					conditional = &graph.Nodes[index]
					break
				}
			}
			require.NotNil(t, conditional)
			require.NotEmpty(t, conditional.Decision.CheckedChannels)
			require.NotEmpty(t, edgesFrom(graph.Edges, conditional.ID, "transition"))
		})
	}
}

func TestVisualizeDynamicFanOutUsesStaticTargetAndDynamicMultiplicity(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	sources := []string{
		"examples/go/patterns/parallel/dynamic.go",
		"examples/python/dex_examples/patterns/parallel/dynamic_parallel_steps_flow.py",
	}
	for _, source := range sources {
		graph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, source), flowviz.AnalyzeOptions{})
		require.NoError(t, err)
		require.True(t, graph.Valid, graph.Diagnostics)
		require.Contains(t, edgeMultiplicities(graph.Edges), "×N")
	}
}

func TestVisualizeStepStreamProgress(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizprogress\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	goSource := filepath.Join(directory, "progress.go")
	require.NoError(t, os.WriteFile(goSource, []byte(goStepProgressFlow), 0o644))

	tests := []struct {
		name   string
		source string
	}{
		{name: "go", source: goSource},
		{name: "python sync generator", source: filepath.Join(repositoryRoot, "sdk-python/tests/integ/step_streaming_flow.py")},
		{name: "python async", source: filepath.Join(repositoryRoot, "sdk-python/tests/integ/async_stream_flow.py")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), test.source, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			require.False(t, graphHasKind(graph, "heartbeat"))

			streamWrites := edgesOfKind(graph.Edges, "resource_write")
			require.NotEmpty(t, streamWrites)
			for _, edge := range streamWrites {
				if !strings.HasPrefix(edge.To, "resource:stream:") {
					continue
				}
				require.Equal(t, true, edge.Metadata["bestEffort"])
				require.Equal(t, true, edge.Metadata["repeatable"])
				require.Equal(t, "progress", edge.Metadata["role"])
			}
		})
	}
}

func TestVisualizeRejectsStepProgressFromRPC(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizinvalidprogress\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	sources := map[string]string{
		"go":     goRPCProgressFlow,
		"python": pythonRPCProgressFlow,
	}
	for language, source := range sources {
		t.Run(language, func(t *testing.T) {
			sourcePath := filepath.Join(directory, "invalid."+map[string]string{"go": "go", "python": "py"}[language])
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))

			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph.Diagnostics), "step_progress_outside_step")
		})
	}
}

func TestVisualizeUnsupportedSourceProducesBlockingDiagnostics(t *testing.T) {
	dynamicResourceSource := strings.Replace(minimalPythonFlow, "StepList, graceful_complete", "StepList, Attribute, graceful_complete", 1)
	dynamicResourceSource = strings.Replace(dynamicResourceSource, "class PythonFlow(Flow[None]):", "class PythonFlow(Flow[None]):\n    status = Attribute(dynamic_name(), str)", 1)
	escapedMovementsSource := strings.Replace(minimalPythonFlow, "StepList, graceful_complete", "StepList, go_to_many, graceful_complete", 1)
	escapedMovementsSource = strings.Replace(escapedMovementsSource, "return graceful_complete()", "return go_to_many(*self.movements)", 1)
	tests := []struct {
		name   string
		source string
		code   string
	}{
		{
			name: "multiple flows",
			source: minimalPythonFlow + `
class SecondFlow(Flow[None]):
    def get_steps(self):
        return StepList.start_step(Finish())
`,
			code: "multiple_flows",
		},
		{
			name:   "wildcard import",
			source: strings.Replace(minimalPythonFlow, "from dex import Flow, Step, StepDecision, StepList, graceful_complete", "from dex import *", 1),
			code:   "wildcard_import",
		},
		{
			name:   "hidden decision helper",
			source: strings.Replace(minimalPythonFlow, "return graceful_complete()", "return choose_next()", 1),
			code:   "hidden_dex_decision",
		},
		{name: "dynamic resource name", source: dynamicResourceSource, code: "dynamic_resource_name"},
		{name: "escaped movements", source: escapedMovementsSource, code: "dynamic_step_target"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "invalid.py")
			require.NoError(t, os.WriteFile(sourcePath, []byte(test.source), 0o644))
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph.Diagnostics), test.code)
		})
	}
}

func TestVisualizeMissingPythonWritesDiagnosticArtifact(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "flow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o644))
	outputPrefix := filepath.Join(t.TempDir(), "missing-python")
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	err := app.Execute(context.Background(), []string{"visualize", sourcePath, "--python", filepath.Join(t.TempDir(), "python-does-not-exist"), "--out", outputPrefix})
	require.Error(t, err)
	data, readErr := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, readErr)
	require.Contains(t, string(data), "python_analyzer_failed")
}

func TestVisualizeGoRequiresVisibleDecisionsAndTypeCheckedPackage(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name    string
		execute string
		code    string
	}{
		{name: "hidden helper", execute: "return chooseNext(), nil", code: "hidden_dex_decision"},
		{name: "type error", execute: "_ = missingIdentifier\n\treturn dex.GracefulComplete(nil), nil", code: "go_type_check_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			goModule := "module flowviztest\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
			source := strings.Replace(minimalGoFlow, "EXECUTE_BODY", test.execute, 1)
			sourcePath := filepath.Join(directory, "flow.go")
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))

			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph.Diagnostics), test.code)
		})
	}
}

func TestVisualizeTreatsSubFlowAsAnExternalFoldedNode(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizsubflow\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "child.go"), []byte(goSubFlowChild), 0o644))
	goSource := filepath.Join(directory, "parent.go")
	require.NoError(t, os.WriteFile(goSource, []byte(goSubFlowParent), 0o644))
	pythonSource := filepath.Join(directory, "parent.py")
	require.NoError(t, os.WriteFile(pythonSource, []byte(pythonSubFlowParent), 0o644))

	for _, source := range []string{goSource, pythonSource} {
		graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
		require.NoError(t, err)
		require.True(t, graph.Valid, graph.Diagnostics)
		require.True(t, graphHasKind(graph, "subflow"))
		for _, node := range graph.Nodes {
			if node.Kind == "subflow" {
				require.True(t, node.External)
			}
		}
	}
}

func edgesOfKind(edges []flowviz.Edge, kind string) []flowviz.Edge {
	matched := make([]flowviz.Edge, 0)
	for _, edge := range edges {
		if edge.Kind == kind {
			matched = append(matched, edge)
		}
	}
	return matched
}

func edgesFrom(edges []flowviz.Edge, source string, kind string) []flowviz.Edge {
	matched := make([]flowviz.Edge, 0)
	for _, edge := range edges {
		if edge.From == source && edge.Kind == kind {
			matched = append(matched, edge)
		}
	}
	return matched
}

func nodesOfKind(nodes []flowviz.Node, kind string) []flowviz.Node {
	matched := make([]flowviz.Node, 0)
	for _, node := range nodes {
		if node.Kind == kind {
			matched = append(matched, node)
		}
	}
	return matched
}

func waitConditionKinds(conditions []flowviz.WaitCondition) []string {
	kinds := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		kinds = append(kinds, condition.Kind)
	}
	return kinds
}

func waitConditionLabels(waits []flowviz.Node) []string {
	labels := make([]string, 0)
	for _, wait := range waits {
		if wait.Wait == nil {
			continue
		}
		for _, condition := range wait.Wait.Conditions {
			labels = append(labels, condition.Label)
		}
	}
	return labels
}

func hasDecisionType(nodes []flowviz.Node, decisionType string) bool {
	for _, node := range nodes {
		if node.Decision != nil && node.Decision.Type == decisionType {
			return true
		}
	}
	return false
}

func graphHasKind(graph *flowviz.Graph, kind string) bool {
	for _, node := range graph.Nodes {
		if node.Kind == kind {
			return true
		}
		if node.Wait != nil {
			for _, condition := range node.Wait.Conditions {
				if condition.Kind == kind {
					return true
				}
			}
		}
	}
	for _, edge := range graph.Edges {
		if edge.Kind == kind {
			return true
		}
	}
	return false
}

func edgeMultiplicities(edges []flowviz.Edge) []string {
	values := make([]string, 0, len(edges))
	for _, edge := range edges {
		if edge.Multiplicity != "" {
			values = append(values, edge.Multiplicity)
		}
	}
	return values
}

func graphHasEdgeFromPrefix(graph *flowviz.Graph, prefix string, kind string) bool {
	for _, edge := range graph.Edges {
		if edge.Kind != kind {
			continue
		}
		if strings.HasPrefix(edge.From, prefix) {
			return true
		}
		for _, node := range graph.Nodes {
			if node.ID == edge.From && strings.HasPrefix(node.ParentID, prefix) {
				return true
			}
		}
	}
	return false
}

func diagnosticCodes(diagnostics []flowviz.Diagnostic) []string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func visualizerRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
}

func exampleFlowSources(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	type sourceRoot struct {
		directory string
		extension string
		marker    string
	}
	roots := []sourceRoot{
		{directory: filepath.Join(repositoryRoot, "examples/go"), extension: ".go", marker: "GetSteps("},
		{directory: filepath.Join(repositoryRoot, "examples/python"), extension: ".py", marker: "(Flow"},
	}
	result := make([]string, 0)
	for _, root := range roots {
		err := filepath.WalkDir(root.directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != root.extension || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(contents), root.marker) {
				result = append(result, path)
			}
			return nil
		})
		require.NoError(t, err)
	}
	return result
}

const minimalPythonFlow = `from dex import Flow, Step, StepDecision, StepList, graceful_complete

class Finish(Step[None]):
    def execute(self, context, input) -> StepDecision:
        return graceful_complete()

class PythonFlow(Flow[None]):
    def __init__(self):
        self.finish = Finish()

    def get_steps(self):
        return StepList.start_step(self.finish)
`

const minimalGoFlow = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

type ExampleFlow struct{ dex.FlowDefaults }

func (*ExampleFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(startStep{})}
}

func (*ExampleFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type startStep struct{ dex.StepDefaultsNoWaitFor[dex.None] }

func (startStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	EXECUTE_BODY
}

func chooseNext() *dex.StepDecision {
	return dex.GracefulComplete(nil)
}
`

const goConditionalWaitFlow = `package flow

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var Signal = dex.DefineChannel[dex.None]("Signal")

type ConditionalWaitFlow struct{ dex.FlowDefaults }

func (*ConditionalWaitFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(conditionalWaitStep{})}
}

func (*ConditionalWaitFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Signal}}
}

type conditionalWaitStep struct{ dex.StepDefaults }

func (conditionalWaitStep) WaitFor(_ dex.Context, useSignal bool) (*dex.Wait, error) {
	if useSignal {
		return dex.Until(Signal.ForOne()), nil
	}
	return dex.Until(dex.Timer(2 * time.Second)), nil
}

func (conditionalWaitStep) Execute(_ dex.Context, _ bool) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
`

const pythonConditionalWaitFlow = `from datetime import timedelta

from dex import Channel, Flow, PersistenceSchema, Step, StepDecision, StepList, Timer, Wait, graceful_complete

class ConditionalWaitStep(Step[bool]):
    def __init__(self, signal):
        self.signal = signal

    def wait_for(self, context, input):
        if input:
            return Wait.until(self.signal.for_one())
        return Wait.until(Timer.by_duration(timedelta(seconds=2)))

    def execute(self, context, input) -> StepDecision:
        return graceful_complete(input)

class ConditionalWaitFlow(Flow[bool]):
    def __init__(self):
        self.signal = Channel("Signal", bool)
        self.start = ConditionalWaitStep(self.signal)

    def get_steps(self):
        return StepList.start_step(self.start)

    def get_persistence_schema(self):
        return PersistenceSchema.of(self.signal)
`

const goWaitBoundsFlow = `package flow

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var Messages = dex.DefineChannel[string]("Messages")
var Buckets = dex.DefineChannelMap[string]("Buckets")

type BoundsFlow struct{ dex.FlowDefaults }

func (*BoundsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(boundsStep{})}
}

func (*BoundsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Messages, Buckets}}
}

type boundsStep struct{ dex.StepDefaults }

func (boundsStep) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.AnyOf(
		Messages.ForN(2),
		Messages.AtLeast(3),
		Messages.AtMost(4),
		Messages.AtLeastAtMost(2, 5),
		Buckets.ForN("priority", input),
		dex.Timer(time.Duration(input) * time.Second),
	), nil
}

func (boundsStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}
`

const pythonWaitBoundsFlow = `from datetime import timedelta

from dex import Channel, ChannelMap, Flow, PersistenceSchema, Step, StepDecision, StepList, Timer, Wait, graceful_complete

messages = Channel("Messages", str)
buckets = ChannelMap("Buckets", str)

class BoundsStep(Step[int]):
    def wait_for(self, context, input):
        return Wait.any_of(
            messages.for_n(2),
            messages.at_least(3),
            messages.at_most(4),
            messages.for_range(at_least=2, at_most=5),
            buckets.for_n("priority", input),
            Timer.by_duration(timedelta(seconds=input)),
        )

    def execute(self, context, input) -> StepDecision:
        return graceful_complete(input)

class BoundsFlow(Flow[int]):
    def __init__(self):
        self.start = BoundsStep()

    def get_steps(self):
        return StepList.start_step(self.start)

    def get_persistence_schema(self):
        return PersistenceSchema.of(messages, buckets)
`

const goStepProgressFlow = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

var Progress = dex.DefineStream[string]("Progress", 1024)

type ProgressFlow struct{ dex.FlowDefaults }

func (*ProgressFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(progressStep{})}
}

func (*ProgressFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{Progress}}
}

type progressStep struct{ dex.StepDefaultsNoWaitFor[dex.None] }

func (progressStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	var checkpoint string
	if _, err := ctx.GetLastHeartbeatValue(&checkpoint); err != nil {
		return nil, err
	}
	if err := ctx.RecordHeartbeat("checkpoint"); err != nil {
		return nil, err
	}
	if err := Progress.Write(ctx, "working"); err != nil {
		return nil, err
	}
	if err := ctx.RecordHeartbeat(nil); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(checkpoint), nil
}
`

const goRPCProgressFlow = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

var Progress = dex.DefineStream[string]("Progress", 1024)

type ProgressFlow struct{ dex.FlowDefaults }

func (*ProgressFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(finishStep{})}
}

func (*ProgressFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{Progress}}
}

func (*ProgressFlow) Report(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Progress.Write(ctx, "invalid"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type finishStep struct{ dex.StepDefaultsNoWaitFor[dex.None] }

func (finishStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
`

const pythonRPCProgressFlow = `from dex import Context, Flow, PersistenceSchema, RPCResult, Step, StepDecision, StepList, Stream, graceful_complete, rpc

class Finish(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete()

class ProgressFlow(Flow[None]):
    progress = Stream("Progress", str, 1024)

    def __init__(self):
        self.finish = Finish()

    def get_steps(self):
        return StepList.start_step(self.finish)

    def get_persistence_schema(self):
        return PersistenceSchema.of(self.progress)

    @rpc()
    async def report(self, context: Context, input: None) -> RPCResult[None]:
        self.progress.write(context, "invalid")
        return RPCResult(None)
`

const goSubFlowChild = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

type ChildFlow struct{ dex.FlowDefaults }
func (*ChildFlow) GetSteps() []dex.StepDef { return []dex.StepDef{dex.DefineStartStep(childStep{})} }
func (*ChildFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }
type childStep struct{ dex.StepDefaultsNoWaitFor[int] }
func (childStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) { return dex.GracefulComplete(input), nil }
`

const goSubFlowParent = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

type ParentFlow struct { dex.FlowDefaults; child *ChildFlow }
func (*ParentFlow) GetSteps() []dex.StepDef { return []dex.StepDef{dex.DefineStartStep(parentStep{})} }
func (*ParentFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }
type parentStep struct { dex.StepDefaults; child *ChildFlow }
func (step parentStep) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.child, input, dex.SubFlowOptions{})), nil
}
func (parentStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}
`

const pythonSubFlowParent = `from dex import Flow, Step, StepDecision, StepList, SubFlow, Wait, graceful_complete

class ParentStep(Step[int]):
    def __init__(self, target):
        self.target = target
    def wait_for(self, context, input):
        return Wait.until(SubFlow.run(self.target, input))
    def execute(self, context, input) -> StepDecision:
        return graceful_complete(input)

class ParentFlow(Flow[int]):
    def __init__(self, target):
        self.start = ParentStep(target)
    def get_steps(self):
        return StepList.start_step(self.start)
`

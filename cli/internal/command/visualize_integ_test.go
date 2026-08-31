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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeDefaultsToJSONAndSVGNextToPythonSource(t *testing.T) {
	temporaryDirectory := t.TempDir()
	sourcePath := filepath.Join(temporaryDirectory, "order_flow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o644))

	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, app.Execute(context.Background(), []string{"visualize", sourcePath}))

	jsonPath := filepath.Join(temporaryDirectory, "order_flow.flow.json")
	svgPath := filepath.Join(temporaryDirectory, "order_flow.flow.svg")
	require.FileExists(t, jsonPath)
	require.FileExists(t, svgPath)
	require.Contains(t, stdout.String(), jsonPath)
	require.Contains(t, stdout.String(), svgPath)
	svg, err := os.ReadFile(svgPath)
	require.NoError(t, err)
	require.Contains(t, string(svg), `viewBox="0 0`)
	require.Contains(t, string(svg), `width="100%"`)
	require.Contains(t, string(svg), ">Execute</text>")
}

func TestVisualizeSingleFormatCanWriteStdout(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "flow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o644))

	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, app.Execute(context.Background(), []string{"visualize", sourcePath, "--format", "json", "--out", "-"}))

	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
	require.True(t, graph.Valid)
	require.Equal(t, "PythonFlow", graph.Flow.Name)
}

func TestVisualizeRejectsBothFormatsOnStdout(t *testing.T) {
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{"visualize", "flow.py", "--out", "-"})
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
			svg, readErr := os.ReadFile(outputPrefix + ".svg")
			require.NoError(t, readErr)
			require.Contains(t, string(svg), "failure-edge")
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
	require.FileExists(t, outputPrefix+".svg")
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

func TestVisualizeSVGIsDeterministicAndEscapesLabels(t *testing.T) {
	graph := flowviz.NewGraph("python", "flow<&>.py")
	graph.Flow.Name = "Flow <unsafe>"
	graph.AddNode(flowviz.Node{ID: "step:a", Kind: "step", Name: "A & B", Start: true})
	graph.AddNode(flowviz.Node{ID: "step:b", Kind: "step", Name: "B"})
	graph.AddEdge(flowviz.Edge{Kind: "transition", From: "step:a", To: "step:b", Condition: `value < 3 && value > 0`})
	graph.AddEdge(flowviz.Edge{Kind: "transition", From: "step:b", To: "step:a", Label: "cycle"})
	graph.Normalize()

	first, err := flowviz.RenderSVG(graph)
	require.NoError(t, err)
	second, err := flowviz.RenderSVG(graph)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Contains(t, string(first), "Flow &lt;unsafe&gt;")
	require.Contains(t, string(first), "A &amp; B")
	require.NotContains(t, string(first), "A & B")
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

func graphHasKind(graph *flowviz.Graph, kind string) bool {
	for _, node := range graph.Nodes {
		if node.Kind == kind {
			return true
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
		if strings.HasPrefix(edge.From, prefix) && edge.Kind == kind {
			return true
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

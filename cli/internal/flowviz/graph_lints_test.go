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
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const goGraphLintSource = `package graphlints

import "github.com/superdurable/dex/sdk-go/dex"

var notes = dex.DefineChannel[string]("Notes")

type ReviewFlow struct{ dex.FlowDefaults }

func (*ReviewFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(Collect{}), dex.DefineStep(Review{})}
}

func (flow *ReviewFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.AddNote, nil)}
}

func (*ReviewFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{notes}}
}

func (*ReviewFlow) AddNote(ctx dex.Context, note string) (*dex.RPCResult[dex.None], error) {
	if err := notes.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type Collect struct{ dex.StepDefaultsNoWaitFor[string] }

func (Collect) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GoTo(Review{}, ""), nil
}

type Review struct{ dex.StepDefaults }

func (Review) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return dex.AnyOf(REVIEW_CONDITION), nil
}

func (Review) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return REVIEW_DECISION, nil
}

func reviewAgain() *dex.StepDecision {
	return dex.GoTo(Collect{}, "")
}
`

const pythonGraphLintSource = `from dex import Channel, Flow, PersistenceSchema, Step, StepDecision, StepList, Timer, Wait, dead_end, force_fail, go_to, graceful_complete, rpc


class Collect(Step[str]):
    def execute(self, context, input: str) -> StepDecision:
        return go_to(Review, "")


class Review(Step[str]):
    def __init__(self, notes: Channel[str]) -> None:
        self.notes = notes

    def wait_for(self, context, input: str) -> Wait:
        return Wait.any_of(REVIEW_CONDITION)

    def execute(self, context, input: str) -> StepDecision:
        return REVIEW_DECISION


def review_again() -> StepDecision:
    return go_to(Collect, "")


class ReviewFlow(Flow[str]):
    notes = Channel("Notes", str)

    def __init__(self) -> None:
        self.collect = Collect()
        self.review = Review(self.notes)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.collect).other_steps(self.review)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.notes)

    @rpc
    def add_note(self, context, note: str) -> None:
        self.notes.publish(context, note)
`

type graphLintCase struct {
	name      string
	condition string
	decision  string
	expected  []string
	// blocking is the error code that must suppress no_completion_path.
	blocking string
}

func TestGraphLintsGo(t *testing.T) {
	cases := []graphLintCase{
		{name: "consumed and completing", condition: "notes.ForOne()", decision: "dex.GracefulComplete(nil)"},
		{name: "fail closes the Flow", condition: "notes.ForOne()", decision: `dex.ForceFail("rejected")`},
		{name: "unconsumed Channel", condition: "dex.Timer(0)", decision: "dex.GracefulComplete(nil)", expected: []string{"channel_without_consumer"}},
		{name: "loop without completion", condition: "notes.ForOne()", decision: `dex.GoTo(Collect{}, "")`, expected: []string{"no_completion_path"}},
		{name: "dead end does not close", condition: "notes.ForOne()", decision: "dex.DeadEnd()", expected: []string{"no_completion_path"}},
		{name: "hidden decision may close", condition: "notes.ForOne()", decision: "reviewAgain()", blocking: "hidden_dex_decision"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := strings.NewReplacer("REVIEW_CONDITION", testCase.condition, "REVIEW_DECISION", testCase.decision).Replace(goGraphLintSource)
			graph := analyzeGoGraphLintSource(t, source)
			requireGraphLints(t, graph, testCase)
		})
	}
}

func TestGraphLintsPython(t *testing.T) {
	cases := []graphLintCase{
		{name: "consumed and completing", condition: "self.notes.for_one()", decision: "graceful_complete()"},
		{name: "fail closes the Flow", condition: "self.notes.for_one()", decision: `force_fail("rejected")`},
		{name: "unconsumed Channel", condition: "Timer.by_duration(1)", decision: "graceful_complete()", expected: []string{"channel_without_consumer"}},
		{name: "loop without completion", condition: "self.notes.for_one()", decision: `go_to(Collect, "")`, expected: []string{"no_completion_path"}},
		{name: "dead end does not close", condition: "self.notes.for_one()", decision: "dead_end()", expected: []string{"no_completion_path"}},
		{name: "hidden decision may close", condition: "self.notes.for_one()", decision: "review_again()", blocking: "hidden_dex_decision"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := strings.NewReplacer("REVIEW_CONDITION", testCase.condition, "REVIEW_DECISION", testCase.decision).Replace(pythonGraphLintSource)
			sourcePath := filepath.Join(t.TempDir(), "workflow.py")
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReviewFlow"})
			require.NoError(t, err)
			requireGraphLints(t, graph, testCase)
		})
	}
}

// The RPC reads PrioritizedMessages only through a helper that takes the Channel as a parameter.
func TestGraphLintsJavaHelperChannelRead(t *testing.T) {
	javaPath := javaAnalyzerTestExecutable(t)
	source := filepath.Join("..", "..", "..", "examples", "java", "src", "main", "java", "io", "superdurable", "dex", "primitives", "channel", "ChannelFlow.java")
	graph, err := Analyze(t.Context(), source, AnalyzeOptions{JavaPath: javaPath})
	require.NoError(t, err)
	require.NotContains(t, diagnosticCodes(graph), "channel_without_consumer", "%+v", graph.Diagnostics)
	found := false
	for _, edge := range graph.Edges {
		if edge.Kind == "resource_read" && edge.From == "resource:channel:PrioritizedMessages" && edge.To == "rpc:getPrioritizedMessages" {
			found = true
		}
	}
	require.True(t, found, "%+v", graph.Edges)
}

func TestGraphLintsSkipFlowsWithUnknownNodes(t *testing.T) {
	graph := NewGraph("go", "workflow.go")
	graph.Flow = Flow{Name: "LoopFlow", StartStepID: "step:Loop"}
	graph.AddNode(Node{ID: "step:Loop", Kind: "step", Name: "Loop", Start: true})
	graph.AddNode(Node{ID: "decision:step:Loop:1:1", Kind: "decision", Name: "goTo", ParentID: "step:Loop", Decision: &DecisionDetails{Type: "goTo"}})
	graph.AddNode(Node{ID: "unknown:step:dynamic", Kind: "unknown", Name: "Dynamic Step"})
	graph.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "unknown:step:dynamic"})
	graph.Normalize()
	require.NotContains(t, diagnosticCodes(graph), "no_completion_path")
}

func requireGraphLints(t *testing.T, graph *Graph, testCase graphLintCase) {
	t.Helper()
	codes := diagnosticCodes(graph)
	if testCase.blocking != "" {
		require.Contains(t, codes, testCase.blocking)
	}
	var lints []string
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "channel_without_consumer" || diagnostic.Code == "no_completion_path" {
			require.Equal(t, "warning", diagnostic.Severity)
			lints = append(lints, diagnostic.Code)
		}
	}
	require.ElementsMatch(t, testCase.expected, lints, "%+v", graph.Diagnostics)
}

func analyzeGoGraphLintSource(t *testing.T, source string) *Graph {
	t.Helper()
	directory := t.TempDir()
	sdkDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk-go"))
	require.NoError(t, err)
	module := "module graphlints\n\ngo 1.26.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.ToSlash(sdkDirectory) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	sourcePath := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, FlowType: "ReviewFlow"})
	require.NoError(t, err)
	require.NotContains(t, diagnosticCodes(graph), "go_type_check_failed", "%+v", graph.Diagnostics)
	return graph
}

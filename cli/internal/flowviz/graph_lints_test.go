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

var status = dex.DefineAttribute[string]("status")

type ReviewFlow struct{ dex.FlowDefaults }

func (*ReviewFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(Collect{}), dex.DefineStep(Review{}), dex.DefineStep(Finish{})}
}

func (flow *ReviewFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.FinishReview, nil),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*ReviewFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{status}}
}

func (*ReviewFlow) FinishReview(_ dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{NextSteps: []dex.StepMovement{dex.MovementOf(Finish{}, "")}}, nil
}

// dex:field attribute-key:status value-type:string editable:false description:"Status"
func (*ReviewFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := status.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"status": value}}, nil
}

// dex:field attribute-key:status value-type:string editable:false description:"Status"
func (*ReviewFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	value, err := status.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"status": value}}, nil
}

TIMEOUT_HANDLER

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Record that a review started."
type Collect struct{ dex.StepDefaultsNoWaitFor[string] }

func (Collect) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	if err := status.Set(ctx, "collecting"); err != nil {
		return nil, err
	}
	return dex.GoTo(Review{}, ""), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Review the request."
type Review struct{ dex.StepDefaultsNoWaitFor[string] }

func (Review) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	REVIEW_BODY
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Finish the review."
type Finish struct{ dex.StepDefaultsNoWaitFor[string] }

func (Finish) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return FINISH_DECISION, nil
}

func closeReview() *dex.StepDecision {
	return dex.ForceComplete(nil)
}
`

const pythonGraphLintSource = `from dex import Flow, RPCResult, Step, StepDecision, StepList, StepMovement, dead_end, force_complete, force_fail, go_to, graceful_complete, rpc


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Record that a review started."
class Collect(Step[str]):
    def execute(self, context, input: str) -> StepDecision:
        return go_to(Review, "")


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Review the request."
class Review(Step[str]):
    def execute(self, context, input: str) -> StepDecision:
        REVIEW_BODY


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Finish the review."
class Finish(Step[str]):
    def execute(self, context, input: str) -> StepDecision:
        return FINISH_DECISION


def close_review() -> StepDecision:
    return force_complete()


class ReviewFlow(Flow[str]):
    def __init__(self) -> None:
        self.collect = Collect()
        self.review = Review()
        self.finish = Finish()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.collect).other_steps(self.review, self.finish)

    @rpc
    def finish_review(self, context) -> RPCResult[None]:
        return RPCResult(None, next_steps=(StepMovement.of(Finish, ""),))
TIMEOUT_HANDLER
`

type graphLintCase struct {
	name     string
	review   string
	finish   string
	timeout  string
	expected bool
	// requiredCode is a diagnostic that must be present, such as the error that suppresses the lint.
	requiredCode string
}

func TestNoCompletionPathGo(t *testing.T) {
	cases := []graphLintCase{
		{name: "graceful completion", review: "return dex.GracefulComplete(nil), nil"},
		{name: "fail closes the Flow", review: `return dex.ForceFail("rejected"), nil`},
		{name: "loop without completion", review: `return dex.GoTo(Collect{}, ""), nil`, expected: true},
		{name: "dead end does not close", review: "return dex.DeadEnd(), nil", expected: true},
		{name: "RPC movement reaches a closing Step", review: `return dex.GoTo(Collect{}, ""), nil`, finish: "dex.GracefulComplete(nil)"},
		{name: "timeout handler fails the Flow", review: `return dex.GoTo(Collect{}, ""), nil`,
			timeout: `func (*ReviewFlow) HandleTimeout(_ dex.Context) (*dex.StepDecision, error) { return dex.ForceFail("timed out"), nil }`},
		{name: "hidden decision may close", review: "return closeReview(), nil", requiredCode: "hidden_dex_decision"},
		{name: "mixed return hides a closing branch", review: "if input == \"close\" {\n\t\treturn closeReview(), nil\n\t}\n\treturn dex.GoTo(Collect{}, \"\"), nil"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			graph := analyzeGoGraphLintSource(t, graphLintSource(goGraphLintSource, testCase, "dex.DeadEnd()"))
			if testCase.requiredCode == "" {
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			}
			requireNoCompletionPath(t, graph, testCase)
		})
	}
}

func TestNoCompletionPathPython(t *testing.T) {
	cases := []graphLintCase{
		{name: "graceful completion", review: "return graceful_complete()"},
		{name: "fail closes the Flow", review: `return force_fail("rejected")`},
		{name: "loop without completion", review: `return go_to(Collect, "")`, expected: true},
		{name: "dead end does not close", review: "return dead_end()", expected: true},
		{name: "RPC movement reaches a closing Step", review: `return go_to(Collect, "")`, finish: "graceful_complete()"},
		{name: "timeout handler fails the Flow", review: `return go_to(Collect, "")`,
			timeout: "\n    def handle_timeout(self, context) -> StepDecision:\n        return force_fail(\"timed out\")\n"},
		{name: "hidden decision may close", review: "return close_review()", requiredCode: "hidden_dex_decision"},
		{name: "mixed return hides a closing branch", review: "if input == \"close\":\n            return close_review()\n        return go_to(Collect, \"\")"},
		{name: "conditional expression hides a closing branch", review: `return close_review() if input == "close" else go_to(Collect, "")`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "workflow.py")
			require.NoError(t, os.WriteFile(sourcePath, []byte(graphLintSource(pythonGraphLintSource, testCase, "dead_end()")), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReviewFlow"})
			require.NoError(t, err)
			requireNoCompletionPath(t, graph, testCase)
		})
	}
}

func TestNoCompletionPathSkipsFlowsWithUnknownNodes(t *testing.T) {
	graph := NewGraph("go", "workflow.go")
	graph.decisionAnalysisComplete = true
	graph.Flow = Flow{Name: "LoopFlow", StartStepID: "step:Loop"}
	graph.AddNode(Node{ID: "step:Loop", Kind: "step", Name: "Loop", Start: true})
	graph.AddNode(Node{ID: "decision:step:Loop:1:1", Kind: "decision", Name: "goTo", ParentID: "step:Loop", Decision: &DecisionDetails{Type: "goTo"}})
	graph.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "step:Loop"})
	graph.Normalize()
	require.Contains(t, diagnosticCodes(graph), "no_completion_path")

	dynamic := NewGraph("go", "workflow.go")
	dynamic.decisionAnalysisComplete = true
	dynamic.Flow = graph.Flow
	dynamic.AddNode(Node{ID: "step:Loop", Kind: "step", Name: "Loop", Start: true})
	dynamic.AddNode(Node{ID: "decision:step:Loop:1:1", Kind: "decision", Name: "goTo", ParentID: "step:Loop", Decision: &DecisionDetails{Type: "goTo"}})
	dynamic.AddNode(Node{ID: "unknown:step:dynamic", Kind: "unknown", Name: "Dynamic Step"})
	dynamic.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "unknown:step:dynamic"})
	dynamic.Normalize()
	require.NotContains(t, diagnosticCodes(dynamic), "no_completion_path")
}

// Analyzers that do not check every decision return never report the lint.
func TestNoCompletionPathRequiresCompleteDecisionAnalysis(t *testing.T) {
	graph := NewGraph("rust", "workflow.rs")
	graph.Flow = Flow{Name: "LoopFlow", StartStepID: "step:Loop"}
	graph.AddNode(Node{ID: "step:Loop", Kind: "step", Name: "Loop", Start: true})
	graph.AddNode(Node{ID: "decision:step:Loop:1:1", Kind: "decision", Name: "goTo", ParentID: "step:Loop", Decision: &DecisionDetails{Type: "goTo"}})
	graph.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "step:Loop"})
	graph.Normalize()
	require.NotContains(t, diagnosticCodes(graph), "no_completion_path")
}

func graphLintSource(template string, testCase graphLintCase, defaultFinish string) string {
	finish := testCase.finish
	if finish == "" {
		finish = defaultFinish
	}
	return strings.NewReplacer(
		"REVIEW_BODY", testCase.review,
		"FINISH_DECISION", finish,
		"TIMEOUT_HANDLER", testCase.timeout,
	).Replace(template)
}

func requireNoCompletionPath(t *testing.T, graph *Graph, testCase graphLintCase) {
	t.Helper()
	codes := diagnosticCodes(graph)
	if testCase.requiredCode != "" {
		require.Contains(t, codes, testCase.requiredCode)
	}
	var lints []Diagnostic
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "no_completion_path" {
			lints = append(lints, diagnostic)
		}
	}
	if !testCase.expected {
		require.Empty(t, lints, "%+v", graph.Diagnostics)
		return
	}
	require.Len(t, lints, 1, "%+v", graph.Diagnostics)
	require.Equal(t, "warning", lints[0].Severity)
	require.True(t, graph.Valid || testCase.requiredCode != "")
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

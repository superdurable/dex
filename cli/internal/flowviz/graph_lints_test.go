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

type ReviewFlow struct {
	dex.FlowDefaults
	FLOW_EMBED
}

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
	return &dex.RPCResult[dex.None]{NextSteps: RPC_NEXT_STEPS}, nil
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

EXTRA_DECLARATIONS

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

func (Review) Execute(_ dex.Context, input string) REVIEW_SIGNATURE {
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

func finishMovements() []dex.StepMovement {
	return []dex.StepMovement{dex.MovementOf(Finish{}, "")}
}
`

const pythonGraphLintSource = `from dex import Context, Flow, RPCResult, Step, StepDecision, StepList, StepMovement, dead_end, force_complete, force_fail, go_to, go_to_many, graceful_complete, rpc


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Record that a review started."
class Collect(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to(Review, "")


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Review the request."
class Review(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        REVIEW_BODY
REVIEW_EXTRA


# dex:group group-id:review group-label:"Review"
# dex:explanation text:"Finish the review."
class Finish(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return FINISH_DECISION


def close_review() -> StepDecision:
    return force_complete()


def finish_movements():
    return (StepMovement.of(Finish, ""),)


class ReviewFlow(Flow[str]):
    def __init__(self) -> None:
        self.collect = Collect()
        self.review = Review()
        self.finish = Finish()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.collect).other_steps(self.review, self.finish)

    @rpc
    def finish_review(self, context: Context) -> RPCResult[None]:
        return RPC_RESULT
FLOW_EXTRA
`

type graphLintCase struct {
	name     string
	review   string
	finish   string
	expected bool
	// requiredCode is a diagnostic that must be present, such as the error that suppresses the lint.
	requiredCode string
	// requiredEdge is an edge kind that the graph must draw.
	requiredEdge string
	// Go-only slots.
	signature, flowEmbed, rpcNextSteps, declarations string
	// Python-only slots.
	reviewExtra, rpcResult, flowExtra string
}

const goLoop = `return dex.GoTo(Collect{}, ""), nil`

func TestNoCompletionPathGo(t *testing.T) {
	cases := []graphLintCase{
		{name: "graceful completion", review: "return dex.GracefulComplete(nil), nil"},
		{name: "fail closes the Flow", review: `return dex.ForceFail("rejected"), nil`},
		{name: "loop without completion", review: goLoop, expected: true},
		{name: "dead end does not close", review: "return dex.DeadEnd(), nil", expected: true},
		{name: "RPC movement reaches a closing Step", review: goLoop, finish: "dex.GracefulComplete(nil)"},
		{name: "timeout handler fails the Flow", review: goLoop,
			declarations: `func (*ReviewFlow) HandleTimeout(_ dex.Context) (*dex.StepDecision, error) { return dex.ForceFail("timed out"), nil }`},
		{name: "failure recovery reaches a closing Step", review: goLoop, finish: "dex.GracefulComplete(nil)", requiredEdge: "failure_transition",
			declarations: "func (Review) GetStepOptions() *dex.StepOptions {\n\treturn &dex.StepOptions{ExecuteFailure: dex.ProceedToOnExecuteFailure(Finish{}, nil)}\n}"},
		{name: "hidden decision may close", review: "return closeReview(), nil", requiredCode: "hidden_dex_decision"},
		{name: "mixed return hides a closing branch", review: "if input == \"close\" {\n\t\treturn closeReview(), nil\n\t}\n\t" + goLoop},
		{name: "bare return of named results", signature: "(decision *dex.StepDecision, err error)",
			review: "decision = dex.GoTo(Collect{}, \"\")\n\tif input == \"close\" {\n\t\tdecision = dex.ForceComplete(nil)\n\t}\n\treturn"},
		{name: "defer rewrites a named result", signature: "(decision *dex.StepDecision, err error)",
			review: "defer func() {\n\t\tif input == \"close\" {\n\t\t\tdecision = dex.ForceComplete(nil)\n\t\t}\n\t}()\n\t" + goLoop},
		{name: "shadowed nil holds a decision",
			review: "var noError error\n\tif input == \"close\" {\n\t\tnil := dex.ForceComplete(\"done\")\n\t\treturn nil, noError\n\t}\n\t" + goLoop},
		{name: "promoted timeout handler", review: goLoop, flowEmbed: "ReviewTimeout",
			declarations: "type ReviewTimeout struct{}\n\nfunc (ReviewTimeout) HandleTimeout(_ dex.Context) (*dex.StepDecision, error) {\n\treturn dex.ForceComplete(nil), nil\n}"},
		{name: "SDK name shadowed by a local value", review: "dex := ReviewCloser{}\n\treturn dex.GoTo(Collect{}, \"\"), nil",
			declarations: "type ReviewCloser struct{}\n\nfunc (ReviewCloser) GoTo(_ Collect, _ string) *dex.StepDecision {\n\treturn dex.ForceComplete(nil)\n}"},
		{name: "RPC movements from a helper", review: goLoop, finish: "dex.GracefulComplete(nil)", rpcNextSteps: "finishMovements()"},
		{name: "fan-out movement from a helper", review: "return dex.GoToMany(dex.MovementOf(Collect{}, \"\"), finishMovements()[0]), nil", finish: "dex.GracefulComplete(nil)"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			graph := analyzeGoGraphLintSource(t, goGraphLintFixture(testCase))
			if testCase.requiredCode == "" {
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			}
			requireNoCompletionPath(t, graph, testCase)
		})
	}
}

func TestNoCompletionPathPython(t *testing.T) {
	const loop = `return go_to(Collect, "")`
	cases := []graphLintCase{
		{name: "graceful completion", review: "return graceful_complete()"},
		{name: "fail closes the Flow", review: `return force_fail("rejected")`},
		{name: "loop without completion", review: loop, expected: true},
		{name: "dead end does not close", review: "return dead_end()", expected: true},
		{name: "RPC movement reaches a closing Step", review: loop, finish: "graceful_complete()"},
		{name: "timeout handler fails the Flow", review: loop,
			flowExtra: "\n    def handle_timeout(self, context: Context) -> StepDecision:\n        return force_fail(\"timed out\")\n"},
		{name: "hidden decision may close", review: "return close_review()", requiredCode: "hidden_dex_decision"},
		{name: "mixed return hides a closing branch", review: "if input == \"close\":\n            return close_review()\n        " + loop},
		{name: "conditional expression hides a closing branch", review: `return close_review() if input == "close" else go_to(Collect, "")`},
		{name: "timeout handler bound by assignment", review: loop,
			flowExtra: "\n    def on_timeout(self, context: Context) -> StepDecision:\n        return force_complete()\n\n    handle_timeout = on_timeout\n"},
		{name: "execute defined twice", review: loop,
			reviewExtra: "\n    def execute(self, context: Context, input: str) -> StepDecision:\n        return force_complete()\n"},
		{name: "RPC movements from a helper", review: loop, finish: "graceful_complete()", rpcResult: "RPCResult(None, next_steps=finish_movements())"},
		{name: "fan-out movement from a helper", review: `return go_to_many(StepMovement.of(Collect, ""), *finish_movements())`, finish: "graceful_complete()", requiredCode: "dynamic_step_target"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "workflow.py")
			require.NoError(t, os.WriteFile(sourcePath, []byte(pythonGraphLintFixture(testCase)), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReviewFlow"})
			require.NoError(t, err)
			if testCase.requiredCode == "" {
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			}
			requireNoCompletionPath(t, graph, testCase)
		})
	}
}

func TestNoCompletionPathSkipsFlowsWithUnknownNodes(t *testing.T) {
	graph := loopGraph(true)
	graph.Normalize()
	require.Contains(t, diagnosticCodes(graph), "no_completion_path")

	dynamic := loopGraph(true)
	dynamic.AddNode(Node{ID: "unknown:step:dynamic", Kind: "unknown", Name: "Dynamic Step"})
	dynamic.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "unknown:step:dynamic"})
	dynamic.Normalize()
	require.NotContains(t, diagnosticCodes(dynamic), "no_completion_path")
}

// Analyzers that do not check every decision return never report the lint.
func TestNoCompletionPathRequiresCompleteDecisionAnalysis(t *testing.T) {
	graph := loopGraph(false)
	graph.Normalize()
	require.NotContains(t, diagnosticCodes(graph), "no_completion_path")
}

func loopGraph(decisionAnalysisComplete bool) *Graph {
	graph := NewGraph("go", "workflow.go")
	graph.decisionAnalysisComplete = decisionAnalysisComplete
	graph.Flow = Flow{Name: "LoopFlow", StartStepID: "step:Loop"}
	graph.AddNode(Node{ID: "step:Loop", Kind: "step", Name: "Loop", Start: true})
	graph.AddNode(Node{ID: "decision:step:Loop:1:1", Kind: "decision", Name: "goTo", ParentID: "step:Loop", Decision: &DecisionDetails{Type: "goTo"}})
	graph.AddEdge(Edge{Kind: "transition", From: "decision:step:Loop:1:1", To: "step:Loop"})
	return graph
}

func goGraphLintFixture(testCase graphLintCase) string {
	return strings.NewReplacer(
		"REVIEW_SIGNATURE", valueOr(testCase.signature, "(*dex.StepDecision, error)"),
		"REVIEW_BODY", testCase.review,
		"FINISH_DECISION", valueOr(testCase.finish, "dex.DeadEnd()"),
		"FLOW_EMBED", testCase.flowEmbed,
		"RPC_NEXT_STEPS", valueOr(testCase.rpcNextSteps, `[]dex.StepMovement{dex.MovementOf(Finish{}, "")}`),
		"EXTRA_DECLARATIONS", testCase.declarations,
	).Replace(goGraphLintSource)
}

func pythonGraphLintFixture(testCase graphLintCase) string {
	return strings.NewReplacer(
		"REVIEW_BODY", testCase.review,
		"REVIEW_EXTRA", testCase.reviewExtra,
		"FINISH_DECISION", valueOr(testCase.finish, "dead_end()"),
		"RPC_RESULT", valueOr(testCase.rpcResult, `RPCResult(None, next_steps=(StepMovement.of(Finish, ""),))`),
		"FLOW_EXTRA", testCase.flowExtra,
	).Replace(pythonGraphLintSource)
}

func requireNoCompletionPath(t *testing.T, graph *Graph, testCase graphLintCase) {
	t.Helper()
	codes := diagnosticCodes(graph)
	if testCase.requiredCode != "" {
		require.Contains(t, codes, testCase.requiredCode)
	}
	if testCase.requiredEdge != "" {
		found := false
		for _, edge := range graph.Edges {
			found = found || edge.Kind == testCase.requiredEdge
		}
		require.True(t, found, "%+v", graph.Edges)
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

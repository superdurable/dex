// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriptConnectorDefinitionGolden(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
			graph.Source.Path = filepath.Base(sourcePath)
			actual, err := MarshalJSON(graph)
			require.NoError(t, err)
			goldenPath := filepath.Join("testdata", language, "workflow.golden.json")
			if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
				require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
			}
			expected, err := os.ReadFile(goldenPath)
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func TestScriptActionsGolden(t *testing.T) {
	for language, file := range map[string]string{"python": "actions.py", "typescript": "actions.ts"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, language)), file)
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			graph.Source.Path = file
			actual, err := MarshalJSON(graph)
			require.NoError(t, err)
			goldenPath := filepath.Join("testdata", language, "actions.golden.json")
			if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
				require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
			}
			expected, err := os.ReadFile(goldenPath)
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func TestTypeScriptJoinGolden(t *testing.T) {
	sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "join.ts")
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
	graph.Source.Path = "join.ts"
	actual, err := MarshalJSON(graph)
	require.NoError(t, err)
	goldenPath := filepath.Join("testdata", "typescript", "join.golden.json")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
	}
	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual))
}

func TestTypeScriptJoinUndeterminedBuilderListIsDynamic(t *testing.T) {
	singlePush := `pairBuilders.push((round: JoinRound) => StepMovement.of(EscalateReview, {reviewer: "backup", round}));`
	eachPush := "reviewers.forEach((reviewer) => eachBuilders.push("
	cases := map[string]struct{ original, replacement string }{
		"conditional push":            {singlePush, "if (reviewers.length > 5) " + singlePush},
		"list passed along":           {singlePush, "reorderBuilders(pairBuilders);\n      " + singlePush},
		"push in another callback":    {singlePush, "scheduleLater(() => " + strings.TrimSuffix(singlePush, ";") + ");"},
		"push in a non-array forEach": {eachPush, "reviewerDirectory.forEach((reviewer) => eachBuilders.push("},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "join.ts")
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := strings.Replace(string(source), testCase.original, testCase.replacement, 1)
			require.NotEqual(t, string(source), modified)
			modified += "\nfunction reorderBuilders(builders: unknown[]): void {\n  builders.reverse();\n}\n"
			modified += "\nfunction scheduleLater(callback: () => unknown): void {\n  void callback;\n}\n"
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Equal(t, []string{"dynamic_step_target"}, diagnosticCodes(graph))
		})
	}
}

func TestTypeScriptWaitsRPCsAndTimeoutGolden(t *testing.T) {
	sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "waits.ts")
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
	graph.Source.Path = "waits.ts"
	actual, err := MarshalJSON(graph)
	require.NoError(t, err)
	goldenPath := filepath.Join("testdata", "typescript", "waits.golden.json")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
	}
	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual))
}

func TestTypeScriptUnsupportedWaitsAndRPCsAreBlocking(t *testing.T) {
	helpers := "\nfunction approvalWait(): Wait {\n  return Wait.until(approvals.forOne());\n}\n" +
		"\nfunction atMostFour() {\n  return approvals.atMost(4);\n}\n" +
		"\nfunction plannedSteps() {\n  return [StepMovement.of(FailingWait, \"planned\")];\n}\n"
	cases := map[string]struct {
		original    string
		replacement string
		codes       []string
	}{
		"Wait from a helper": {
			`return Wait.anyOf(approvals.forOne("approval"), Timer.byDuration(2_000, "deadline"));`,
			"return approvalWait();",
			[]string{"hidden_dex_wait"},
		},
		"spread condition":      {"approvals.atMost(4),", "...[approvals.atMost(4)],", []string{"dynamic_wait_condition"}},
		"condition from helper": {"approvals.atMost(4),", "atMostFour(),", []string{"dynamic_wait_condition"}},
		"opaque RPC result": {
			"return { output: status.get(context) };",
			"return Promise.resolve({ output: status.get(context) });",
			// describe returns approve's results, so it reports the same return.
			[]string{"hidden_dex_decision", "hidden_dex_decision"},
		},
		"nextSteps from helper": {
			"nextSteps: [StepMovement.of(FailingWait, decision)]",
			"nextSteps: plannedSteps()",
			[]string{"dynamic_step_target"},
		},
		"spread RPC result": {
			"return { output: status.get(context) };",
			"const base = { output: status.get(context) };\n    return { ...base };",
			[]string{"dynamic_step_target"},
		},
		"Stream write in an RPC": {
			"approvals.publish(context, decision);",
			"progress.write(context, decision);",
			// approve writes directly, and describe reaches the write through approve.
			[]string{"step_progress_outside_step", "step_progress_outside_step"},
		},
		"hidden timeout decision": {
			`return forceFail("approval timed out");`,
			"return this.timeoutDecision();\n  }\n\n  private timeoutDecision(): StepDecision {\n    return forceFail(\"approval timed out\");",
			[]string{"hidden_dex_decision"},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "waits.ts")
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := strings.Replace(string(source), testCase.original, testCase.replacement, 1)
			require.NotEqual(t, string(source), modified)
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified+helpers), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			errorCodes := make([]string, 0)
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Severity == "error" {
					errorCodes = append(errorCodes, diagnostic.Code)
				}
			}
			require.Equal(t, testCase.codes, errorCodes, "%+v", graph.Diagnostics)
		})
	}
}

// Each case once produced a valid graph that was wrong; it must now be either correct or blocked.
func TestTypeScriptWaitAndRPCShapesAreCorrectOrBlocked(t *testing.T) {
	const skipReturn = "      return { output: decision, nextSteps: [StepMovement.of(FailingWait, decision)], cancelingSteps: [AwaitApproval] };"
	const approveBranches = "    if (decision === \"skip\") {\n" + skipReturn + "\n    }\n    return { output: status.get(context) };"
	const signalWait = `return Wait.anyOf(approvals.forOne("approval"), Timer.byDuration(2_000, "deadline"));`
	const publish = "approvals.publish(context, decision);"
	emptyReturn := func(statement string) string {
		return "    if (decision === \"skip\") " + statement + "\n    return { output: decision, nextSteps: [StepMovement.of(FailingWait, decision)] };"
	}
	type replacement struct{ original, replacement string }
	cases := map[string]struct {
		replacements []replacement
		codes        []string
		check        func(t *testing.T, graph *Graph)
	}{
		"local nextSteps with a helper element": {
			replacements: []replacement{{skipReturn, "      const nextSteps = [plannedMovement(decision)];\n      return { output: decision, nextSteps };"}},
			codes:        []string{"dynamic_step_target"},
		},
		"local nextSteps emptied after construction": {
			replacements: []replacement{{skipReturn, "      const nextSteps = [StepMovement.of(FailingWait, decision)];\n      nextSteps.length = 0;\n      return { output: decision, nextSteps };"}},
			codes:        []string{"dynamic_step_target"},
		},
		"local nextSteps with a push": {
			replacements: []replacement{{skipReturn, "      const nextSteps = [StepMovement.of(FailingWait, decision)];\n      nextSteps.push(StepMovement.of(ReviewBounds, 1));\n      return { output: decision, nextSteps };"}},
			codes:        []string{"dynamic_step_target"},
		},
		"single-use const nextSteps": {
			replacements: []replacement{{skipReturn, "      const nextSteps = [StepMovement.of(FailingWait, decision)];\n      return { output: decision, nextSteps };"}},
			check: func(t *testing.T, graph *Graph) {
				require.Equal(t, []string{"step:FailingWait"}, rpcTransitionTargets(graph, "rpc:approve", `decision === "skip"`))
			},
		},
		"one helper called on every path": {
			replacements: []replacement{{approveBranches, "    if (decision === \"skip\") return this.finish(context, decision);\n    return this.finish(context, decision);\n  }\n\n  private finish(_context: Context, decision: string): RPCResult<string> {\n    return { output: decision, nextSteps: [StepMovement.of(FailingWait, decision)] };"}},
			check: func(t *testing.T, graph *Graph) {
				require.Equal(t, []string{"step:FailingWait"}, rpcTransitionTargets(graph, "rpc:approve", `decision === "skip"`))
				require.Equal(t, []string{"step:FailingWait"}, rpcTransitionTargets(graph, "rpc:approve", `!(decision === "skip")`))
			},
		},
		"empty return":           {replacements: []replacement{{approveBranches, emptyReturn("return;")}}, check: checkGuardedRPCResult},
		"return undefined":       {replacements: []replacement{{approveBranches, emptyReturn("return undefined;")}}, check: checkGuardedRPCResult},
		"return void expression": {replacements: []replacement{{approveBranches, emptyReturn("return void 0;")}}, check: checkGuardedRPCResult},
		"local shadows a Channel in a Wait": {
			replacements: []replacement{{signalWait, "const approvals = getApprovalChannel();\n      return Wait.until(approvals.forOne(\"priority\"));"}},
			codes:        []string{"dynamic_wait_condition"},
		},
		"local shadows a Channel in a publish": {
			replacements: []replacement{{publish, "const approvals = getApprovalChannel();\n    approvals.publish(context, decision);"}},
			codes:        []string{"dynamic_resource_access"},
		},
		"one Wait returned twice": {
			replacements: []replacement{
				{"  waitFor(context: Context, input: ApprovalInput): Wait {\n", "  waitFor(context: Context, input: ApprovalInput): Wait {\n    const waiting = Wait.until(approvals.forOne());\n"},
				{signalWait, "if (input.minutes > 1) return waiting;\n      return waiting;"},
			},
			check: func(t *testing.T, graph *Graph) {
				waits := 0
				for _, node := range graph.Nodes {
					if node.Kind == "wait" && node.ParentID == "step:AwaitApproval" {
						waits++
					}
				}
				require.Equal(t, 5, waits)
			},
		},
		"Stream write with a literal key in an RPC": {
			replacements: []replacement{{publish, `progress["write"](context, decision);`}},
			codes:        []string{"step_progress_outside_step"},
		},
		"publish with a literal key": {
			replacements: []replacement{{publish, `approvals["publish"](context, decision);`}},
			check: func(t *testing.T, graph *Graph) {
				require.True(t, hasScriptEdge(graph, "resource_publish", "rpc:approve", "resource:channel:approvals"))
			},
		},
		"read with a literal key is the only access": {
			replacements: []replacement{
				{"    return { output: status.get(context) };", `    return { output: status["get"](context) };`},
				{`    status.set(context, "timed out");`, ""},
				{"    status.set(context, approvals.results(context).join(\",\"));", ""},
			},
			check: func(t *testing.T, graph *Graph) {
				require.True(t, hasScriptEdge(graph, "resource_read", "resource:attribute:status", "rpc:approve"))
			},
		},
		"publish with a computed key": {
			replacements: []replacement{{publish, "const method = \"publish\";\n    approvals[method](context, decision);"}},
			codes:        []string{"dynamic_resource_access"},
		},
	}
	helpers := "\nfunction plannedMovement(input: string) {\n  return StepMovement.of(FailingWait, input);\n}\n" +
		"\nfunction getApprovalChannel() {\n  return buckets;\n}\n"
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "waits.ts")
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := string(source)
			for _, change := range testCase.replacements {
				require.Contains(t, modified, change.original)
				modified = strings.Replace(modified, change.original, change.replacement, 1)
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified+helpers), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
			require.NoError(t, err)
			errorCodes := make(map[string]bool)
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Severity == "error" {
					errorCodes[diagnostic.Code] = true
				}
			}
			expectedCodes := make(map[string]bool)
			for _, code := range testCase.codes {
				expectedCodes[code] = true
			}
			require.Equal(t, expectedCodes, errorCodes, "%+v", graph.Diagnostics)
			require.Equal(t, len(testCase.codes) == 0, graph.Valid)
			for _, diagnostic := range graph.Diagnostics {
				require.NotEqual(t, "unused_resource", diagnostic.Code, diagnostic.Message)
			}
			nodeIDs := make(map[string]bool)
			for _, node := range graph.Nodes {
				require.False(t, nodeIDs[node.ID], "duplicate node %s", node.ID)
				nodeIDs[node.ID] = true
			}
			edgeKeys := make(map[string]bool)
			for _, edge := range graph.Edges {
				key := edge.Kind + " " + edge.From + " " + edge.To + " " + edge.Label
				require.False(t, edgeKeys[key], "duplicate edge %s", key)
				edgeKeys[key] = true
			}
			if testCase.check != nil {
				testCase.check(t, graph)
			}
		})
	}
}

// The review sources in testdata/typescript/shapes either render the correct graph or are blocked.
// shapes.golden.txt records each graph's decisions, Waits, and edges; it was checked by hand.
func TestTypeScriptReviewShapes(t *testing.T) {
	expectedErrorCodes := map[string][]string{
		"baseline": nil, "both": nil, "computedLiteral": nil, "contextWeakMap": nil, "hard": nil,
		"helperNestedBinding": nil, "helperParameter": nil, "innerReturn": nil, "nestedHelper": nil,
		"r03-void-helper": nil, "r10-weakmap": nil, "repeatHelper": nil, "reusedWait": nil,
		"rpcHelperVoidPath": nil, "streamReboundWriter": nil, "sync": nil, "thisField": nil,
		"unreachableAfterEmpty": nil, "unrelatedMap": nil, "waitTernary": nil,
		"aliasMutation": {"dynamic_step_target"}, "aliasPush": {"dynamic_step_target"},
		"arrayDirectAlias": {"dynamic_step_target"}, "conditionalPush": {"dynamic_step_target"},
		"directAliasMutation": {"dynamic_step_target"}, "directAliasPush": {"dynamic_step_target"},
		"originalAliasMutation": {"dynamic_step_target"}, "passed": {"dynamic_step_target"},
		"r01-alias": {"dynamic_step_target"}, "r02-spread": {"dynamic_step_target"},
		"spread": {"dynamic_step_target"}, "spreadConditional": {"dynamic_step_target"},
		"spreadEmpty": {"dynamic_step_target"}, "spreadPassed": {"dynamic_step_target"},
		"spreadPush": {"dynamic_step_target"}, "typedAlias": {"dynamic_step_target"},
		"unconditionalPush": {"dynamic_step_target"},
		"blockShadow":       {"dynamic_resource_access"}, "computedLocal": {"dynamic_resource_access"},
		"computedModule": {"dynamic_resource_access"}, "contextAlias": {"dynamic_resource_access"},
		"contextTypeAlias": {"dynamic_resource_access"}, "fieldReassigned": {"dynamic_resource_access"},
		"helperAlias": {"dynamic_resource_access"}, "helperChangedToBuckets": {"dynamic_resource_access"},
		"helperInstance": {"dynamic_resource_access"}, "helperReassign": {"dynamic_resource_access"},
		"helperRecursiveResources": {"dynamic_resource_access"}, "localAlias": {"dynamic_resource_access"},
		"localInstanceComputed": {"dynamic_resource_access"}, "parameterShadow": {"dynamic_resource_access"},
		"r04-this-chain": {"dynamic_resource_access"}, "r05-reassigned-helper": {"dynamic_resource_access"},
		"r06-resource-recursion": {"dynamic_resource_access"}, "r07-context-alias": {"dynamic_resource_access"},
		"r08-unresolved-computed": {"dynamic_resource_access"}, "r09-writer-computed": {"dynamic_resource_access"},
		"streamComputedWriter": {"dynamic_resource_access"}, "thisChainWrong": {"dynamic_resource_access"},
		"moduleReassigned": {"dynamic_resource_access", "dynamic_wait_condition"},
		"moduleReusedWait": {"hidden_dex_wait"},
		"r11-result-list":  {"hidden_dex_decision"},
		"reusedResult":     {"hidden_dex_decision"},
		"recursiveHelper":  {"dynamic_resource_access", "hidden_dex_decision"},
		"destructured":     {"dynamic_class", "dynamic_resource_access", "dynamic_step_target", "unknown_step_target"},
		"thisComputed":     {"dynamic_class", "dynamic_resource_access", "dynamic_step_target", "unknown_step_target"},
	}
	directory := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "shapes")
	sources, err := filepath.Glob(filepath.Join(directory, "*.ts"))
	require.NoError(t, err)
	require.Len(t, sources, len(expectedErrorCodes))
	var summary strings.Builder
	for _, sourcePath := range sources {
		name := strings.TrimSuffix(filepath.Base(sourcePath), ".ts")
		expected, isListed := expectedErrorCodes[name]
		require.True(t, isListed, name)
		graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
		require.NoError(t, err)
		errorCodes := make([]string, 0)
		for _, diagnostic := range graph.Diagnostics {
			if diagnostic.Severity == "error" && !slices.Contains(errorCodes, diagnostic.Code) {
				errorCodes = append(errorCodes, diagnostic.Code)
			}
		}
		if expected == nil {
			expected = []string{}
		}
		require.Equal(t, expected, errorCodes, "%s: %+v", name, graph.Diagnostics)
		require.Equal(t, len(expected) == 0, graph.Valid, name)
		fmt.Fprintf(&summary, "== %s valid=%t\n", name, graph.Valid)
		if !graph.Valid {
			continue
		}
		for _, node := range graph.Nodes {
			if node.Kind == "decision" || node.Kind == "wait" {
				fmt.Fprintf(&summary, "node %s condition=%q\n", node.ID, node.Condition)
			}
		}
		for _, edge := range graph.Edges {
			fmt.Fprintf(&summary, "edge %s %s -> %s %s\n", edge.Kind, edge.From, edge.To, edge.Label)
		}
		for _, diagnostic := range graph.Diagnostics {
			fmt.Fprintf(&summary, "%s %s %s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Message)
		}
	}
	goldenPath := filepath.Join("testdata", "typescript", "shapes.golden.txt")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, []byte(summary.String()), 0o644))
	}
	golden, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(golden), summary.String())
}

func checkGuardedRPCResult(t *testing.T, graph *Graph) {
	t.Helper()
	conditions := make([]string, 0)
	for _, node := range graph.Nodes {
		if node.Kind == "decision" && node.ParentID == "rpc:approve" {
			conditions = append(conditions, node.Condition)
		}
	}
	require.Equal(t, []string{`!(decision === "skip")`}, conditions)
	require.Equal(t, []string{"step:FailingWait"}, rpcTransitionTargets(graph, "rpc:approve", `!(decision === "skip")`))
}

// rpcTransitionTargets returns the Steps that the RPC decisions with condition schedule.
func rpcTransitionTargets(graph *Graph, rpcID string, condition string) []string {
	decisions := make(map[string]bool)
	for _, node := range graph.Nodes {
		if node.Kind == "decision" && node.ParentID == rpcID && node.Condition == condition {
			decisions[node.ID] = true
		}
	}
	targets := make([]string, 0)
	for _, edge := range graph.Edges {
		if edge.Kind == "transition" && decisions[edge.From] {
			targets = append(targets, edge.To)
		}
	}
	return targets
}

func hasScriptEdge(graph *Graph, kind string, from string, to string) bool {
	for _, edge := range graph.Edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func TestTypeScriptRPCCancelsRegisteredInstance(t *testing.T) {
	sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "waits.ts")
	source, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	modified := strings.Replace(string(source), "cancelingSteps: [AwaitApproval]", "cancelingSteps: [this.approval]", 1)
	require.NotEqual(t, string(source), modified)
	require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	cancellations := make([]Cancellation, 0)
	for _, node := range graph.Nodes {
		if node.ID == "decision:rpc:approve:137:14" {
			cancellations = node.Decision.Cancellations
		}
	}
	require.Equal(t, []Cancellation{{StepID: "step:AwaitApproval", Scope: "all"}}, cancellations)
}

func TestTypeScriptMultipleFlowsMarkEachFlow(t *testing.T) {
	sourcePath := filepath.Join(filepath.Dir(scriptFixture(t, "typescript")), "waits.ts")
	source, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	secondFlow := "\nexport class ReviewFlow implements Flow<number> {\n  getSteps() {\n    return StepList.startStep(new ReviewBounds());\n  }\n}\n"
	require.NoError(t, os.WriteFile(sourcePath, append(source, secondFlow...), 0o644))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Equal(t, []string{"multiple_flows"}, diagnosticCodes(graph))
	flowNodes := make([]string, 0)
	for _, node := range graph.Nodes {
		if node.Kind == "unknown" {
			require.NotNil(t, node.Span)
			flowNodes = append(flowNodes, node.ID)
		}
	}
	require.Equal(t, []string{"unknown:flow:ApprovalFlow", "unknown:flow:ReviewFlow"}, flowNodes)
}

func TestScriptStepAnnotationDiagnostics(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, replacement := range []string{"", "dex:explanation text:\"\"", "dex:explanation text:\"First.\"\nCOMMENT dex:explanation text:\"Second.\"", "dex:explanation text:\"First.\" extra:true"} {
			t.Run(language+"/"+replacement, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				prefix := "#"
				if language == "typescript" {
					prefix = "//"
				}
				replacement = strings.ReplaceAll(replacement, "COMMENT", prefix)
				modified := strings.Replace(string(source), "dex:explanation text:\"Complete the Flow after the lookup.\"", replacement, 1)
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "v2_step_explanation")
			})
		}
	}
}

func TestScriptDynamicConnectionDisablesConfiguration(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(string(source), "\"primary\"", "read_connection()", 1)), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Contains(t, diagnosticCodes(graph), "connector_connection_name_required")
			for _, node := range graph.Nodes {
				if node.ID == "step:LookupRecord" {
					require.Equal(t, false, node.Metadata["connector"].(map[string]any)["configurationEnabled"])
				}
			}
		})
	}
}

func TestScriptMalformedManifestIsBlocking(t *testing.T) {
	replacements := map[string][2]string{
		"releaseTag":         {`"connectors/catalog/v1.2.3"`, `"../catalog/latest"`},
		"unit required flag": {`"required": true,`, ""},
		"JSON pointer":       {`"/query"`, `"/query~2"`},
	}
	messages := map[string]string{
		"releaseTag":         `releaseTag "../catalog/latest"`,
		"unit required flag": "configurationUI units require required and bindings",
		"JSON pointer":       `invalid JSON pointer "/query~2"`,
	}
	for _, language := range []string{"python", "typescript"} {
		for name, replacement := range replacements {
			t.Run(language+"/"+name, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				manifestPath := filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json")
				manifest, err := os.ReadFile(manifestPath)
				require.NoError(t, err)
				modified := strings.Replace(string(manifest), replacement[0], replacement[1], 1)
				require.NotEqual(t, string(manifest), modified)
				require.NoError(t, os.WriteFile(manifestPath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "connector_manifest")
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest" {
						require.Contains(t, diagnostic.Message, messages[name])
					}
				}
			})
		}
	}
}

// A package published before agent tools has no operation input metadata and must keep analyzing.
func TestScriptManifestWithoutToolMetadataAnalyzes(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			manifest := readScriptManifest(t, sourcePath, language)
			for _, factory := range manifest["factories"].([]any) {
				for _, name := range []string{"inputType", "inputSchema", "description", "agent"} {
					require.NotContains(t, factory, name)
				}
			}
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func TestScriptManifestToolMetadataLeavesGraphUnchanged(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			before, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
			require.NoError(t, err)
			require.True(t, before.Valid, "%+v", before.Diagnostics)
			manifest := readScriptManifest(t, sourcePath, language)
			factory := manifest["factories"].([]any)[0].(map[string]any)
			for name, value := range validScriptToolMetadata() {
				factory[name] = value
			}
			writeScriptManifest(t, sourcePath, language, manifest)
			after, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
			require.NoError(t, err)
			require.True(t, after.Valid, "%+v", after.Diagnostics)
			require.Empty(t, after.Diagnostics)
			beforeJSON, err := MarshalJSON(before)
			require.NoError(t, err)
			afterJSON, err := MarshalJSON(after)
			require.NoError(t, err)
			require.JSONEq(t, string(beforeJSON), string(afterJSON))
		})
	}
}

func TestScriptManifestInvalidToolMetadataIsBlocking(t *testing.T) {
	for name, testCase := range map[string]struct {
		factoryIndex int
		change       func(factory map[string]any)
		message      string
	}{
		"schema without type":      {0, func(factory map[string]any) { factory["inputSchema"] = map[string]any{} }, "inputSchema must be a JSON Schema object"},
		"schema is not an object":  {0, func(factory map[string]any) { factory["inputSchema"] = map[string]any{"type": "string"} }, "inputSchema must be a JSON Schema object"},
		"schema is null":           {0, func(factory map[string]any) { factory["inputSchema"] = nil }, "inputSchema must be a JSON Schema object"},
		"type without schema":      {0, func(factory map[string]any) { delete(factory, "inputSchema") }, "inputType and inputSchema must appear together"},
		"schema without type name": {0, func(factory map[string]any) { delete(factory, "inputType") }, "inputType and inputSchema must appear together"},
		"agent without schema": {0, func(factory map[string]any) {
			delete(factory, "inputType")
			delete(factory, "inputSchema")
		}, "agent requires inputType and inputSchema"},
		"agent without exposed": {0, func(factory map[string]any) { factory["agent"] = map[string]any{"approval": "recommended"} }, "agent requires exposed"},
		"unknown approval":      {0, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "approval": "always"} }, `agent.approval "always"`},
		"approval without exposed": {0, func(factory map[string]any) {
			factory["agent"] = map[string]any{"exposed": false, "approval": "recommended"}
		}, "agent.approval requires agent.exposed"},
		"Trigger factory":        {1, func(map[string]any) {}, "Trigger factory must not declare"},
		"blank type":             {0, func(factory map[string]any) { factory["inputType"] = "" }, "inputType must be a non-empty string"},
		"null type":              {0, func(factory map[string]any) { factory["inputType"] = nil }, "inputType must be a non-empty string"},
		"blank description":      {0, func(factory map[string]any) { factory["description"] = "" }, "description must be a non-empty string"},
		"null description":       {0, func(factory map[string]any) { factory["description"] = nil }, "description must be a non-empty string"},
		"null agent":             {0, func(factory map[string]any) { factory["agent"] = nil }, "agent must be an object"},
		"blank approval":         {0, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "approval": ""} }, `agent.approval "" must be recommended`},
		"null approval":          {0, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "approval": nil} }, "agent.approval null must be recommended"},
		"unknown retry":          {2, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "retry": "always"} }, `agent.retry "always" must be safe`},
		"blank retry":            {2, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "retry": ""} }, `agent.retry "" must be safe`},
		"null retry":             {2, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "retry": nil} }, "agent.retry null must be safe"},
		"retry without exposed":  {2, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": false, "retry": "safe"} }, "agent.retry requires agent.exposed"},
		"retry on a query":       {0, func(factory map[string]any) { factory["agent"] = map[string]any{"exposed": true, "retry": "safe"} }, "agent.retry is only for mutations"},
		"blank field on Trigger": {1, func(factory map[string]any) { clearToolMetadata(factory); factory["description"] = "" }, "Trigger factory must not declare"},
		"null agent on Trigger":  {1, func(factory map[string]any) { clearToolMetadata(factory); factory["agent"] = nil }, "Trigger factory must not declare"},
	} {
		for _, language := range []string{"python", "typescript"} {
			t.Run(language+"/"+name, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				manifest := readScriptManifest(t, sourcePath, language)
				factories := manifest["factories"].([]any)
				if testCase.factoryIndex == 2 {
					factories = append(factories, scriptMutationFactory(factories[0].(map[string]any)))
					manifest["factories"] = factories
				}
				factory := factories[testCase.factoryIndex].(map[string]any)
				for metadataName, value := range validScriptToolMetadata() {
					factory[metadataName] = value
				}
				testCase.change(factory)
				writeScriptManifest(t, sourcePath, language, manifest)
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "connector_manifest")
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest" {
						require.Contains(t, diagnostic.Message, testCase.message)
					}
				}
			})
		}
	}
}

func TestScriptManifestAcceptsRetrySafeOnMutation(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			manifest := readScriptManifest(t, sourcePath, language)
			factories := manifest["factories"].([]any)
			mutation := scriptMutationFactory(factories[0].(map[string]any))
			for name, value := range validScriptToolMetadata() {
				mutation[name] = value
			}
			mutation["agent"] = map[string]any{"exposed": true, "retry": "safe"}
			manifest["factories"] = append(factories, mutation)
			writeScriptManifest(t, sourcePath, language, manifest)
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func TestScriptManifestIgnoresUnknownFieldOnlyInsideAgent(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language+"/inside agent", func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			manifest := readScriptManifest(t, sourcePath, language)
			factory := manifest["factories"].([]any)[0].(map[string]any)
			for name, value := range validScriptToolMetadata() {
				factory[name] = value
			}
			factory["agent"].(map[string]any)["futureField"] = map[string]any{"nested": true}
			writeScriptManifest(t, sourcePath, language, manifest)
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		})
		for name, change := range map[string]func(manifest map[string]any){
			"manifest": func(manifest map[string]any) { manifest["futureField"] = true },
			"package":  func(manifest map[string]any) { manifest["package"].(map[string]any)["futureField"] = true },
			"factory": func(manifest map[string]any) {
				manifest["factories"].([]any)[0].(map[string]any)["futureField"] = true
			},
			"branch": func(manifest map[string]any) {
				factory := manifest["factories"].([]any)[0].(map[string]any)
				factory["branches"].([]any)[0].(map[string]any)["futureField"] = true
			},
		} {
			t.Run(language+"/"+name, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				manifest := readScriptManifest(t, sourcePath, language)
				change(manifest)
				writeScriptManifest(t, sourcePath, language, manifest)
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "connector_manifest")
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest" {
						require.Contains(t, diagnostic.Message, `unknown field "futureField"`)
					}
				}
			})
		}
	}
}

// The schema file is the published contract; the decoder alone would accept a field it omits.
func TestConnectorManifestSchemaDeclaresToolMetadata(t *testing.T) {
	contents, err := os.ReadFile("dex-connector.schema.json")
	require.NoError(t, err)
	var document struct {
		Definitions map[string]struct {
			Properties           map[string]any `json:"properties"`
			AdditionalProperties any            `json:"additionalProperties"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(contents, &document))
	for name := range validScriptToolMetadata() {
		require.Contains(t, document.Definitions["factory"].Properties, name)
	}
	require.Contains(t, document.Definitions["agent"].Properties, "exposed")
	require.Contains(t, document.Definitions["agent"].Properties, "approval")
	branches := document.Definitions["agent"].Properties["branches"].(map[string]any)
	require.Equal(t, map[string]any{"enum": []any{"rejected", "unknown"}}, branches["additionalProperties"])
	require.Equal(t, false, document.Definitions["agent"].AdditionalProperties, "authors still get an error for a typo")
	require.Equal(t, map[string]any{"enum": []any{"safe"}, "description": document.Definitions["agent"].Properties["retry"].(map[string]any)["description"]}, document.Definitions["agent"].Properties["retry"])
}

func scriptMutationFactory(query map[string]any) map[string]any {
	mutation := make(map[string]any, len(query))
	for name, value := range query {
		mutation[name] = value
	}
	mutation["name"] = "create_write_for_tool_metadata"
	mutation["kind"] = "mutation"
	return mutation
}

func clearToolMetadata(factory map[string]any) {
	for name := range validScriptToolMetadata() {
		delete(factory, name)
	}
}

func validScriptToolMetadata() map[string]any {
	return map[string]any{
		"inputType": "LookupInput",
		"inputSchema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"recordId"},
			"properties":           map[string]any{"recordId": map[string]any{"type": "string"}},
		},
		"description": "Look up one record by ID.",
		"agent":       map[string]any{"exposed": true, "approval": "recommended"},
	}
}

func readScriptManifest(t *testing.T, sourcePath string, language string) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json"))
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(contents, &manifest))
	return manifest
}

func writeScriptManifest(t *testing.T, sourcePath string, language string, manifest map[string]any) {
	t.Helper()
	contents, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json"), contents, 0o644))
}

func TestScriptConnectorTargetsMustBeStatic(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			original, replacement := "found=self.finish", "found=choose_step()"
			if language == "typescript" {
				original, replacement = "found: this.finish", "found: chooseStep()"
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(string(source), original, replacement, 1)), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph), "connector_factory_target")
		})
	}
}

func TestPythonConnectorResolutionUsesSelectedVirtualEnvironment(t *testing.T) {
	sourcePath := scriptFixture(t, "python")
	virtualEnvironment := filepath.Join(t.TempDir(), "venv")
	output, err := exec.Command("python3", "-m", "venv", "--without-pip", virtualEnvironment).CombinedOutput()
	require.NoError(t, err, "%s", output)
	interpreter := filepath.Join(virtualEnvironment, "bin", "python")
	output, err = exec.Command(interpreter, "-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").CombinedOutput()
	require.NoError(t, err, "%s", output)
	packageDirectory := filepath.Join(strings.TrimSpace(string(output)), "catalog_connector")
	require.NoError(t, os.Rename(filepath.Join(filepath.Dir(sourcePath), "catalog_connector"), packageDirectory))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, PythonPath: interpreter})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
}

func TestPythonAnalyzerDoesNotRunSitePackagesStartupCode(t *testing.T) {
	sourcePath := scriptFixture(t, "python")
	virtualEnvironment := filepath.Join(t.TempDir(), "venv")
	output, err := exec.Command("python3", "-m", "venv", "--without-pip", virtualEnvironment).CombinedOutput()
	require.NoError(t, err, "%s", output)
	interpreter := filepath.Join(virtualEnvironment, "bin", "python")
	output, err = exec.Command(interpreter, "-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").CombinedOutput()
	require.NoError(t, err, "%s", output)
	purelib := strings.TrimSpace(string(output))
	editableDirectory := t.TempDir()
	require.NoError(t, os.Rename(filepath.Join(filepath.Dir(sourcePath), "catalog_connector"), filepath.Join(editableDirectory, "catalog_connector")))
	probeDirectory := t.TempDir()
	pathProbe := filepath.Join(probeDirectory, "pth-executed")
	customizeProbe := filepath.Join(probeDirectory, "sitecustomize-executed")
	pathConfiguration := fmt.Sprintf("import os; open(%q, 'w').close()\n%s\n", pathProbe, editableDirectory)
	require.NoError(t, os.WriteFile(filepath.Join(purelib, "zz_probe.pth"), []byte(pathConfiguration), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(purelib, "sitecustomize.py"), []byte(fmt.Sprintf("open(%q, 'w').close()\n", customizeProbe)), 0o644))

	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, PythonPath: interpreter})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
	require.NoFileExists(t, pathProbe)
	require.NoFileExists(t, customizeProbe)

	output, err = exec.Command(interpreter, "-I", "-c", "pass").CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.FileExists(t, pathProbe, "the .pth probe must run under a normal interpreter start")
}

func TestScriptMissingConnectorManifestNamesThePackage(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, removal := range []string{"manifest", "package"} {
			t.Run(language+"/"+removal, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				packagePath := scriptFixturePackage(sourcePath, language)
				expected := filepath.Join(packagePath, "dex-connector.json")
				if removal == "package" {
					require.NoError(t, os.RemoveAll(packagePath))
					expected = "@dex-test/catalog is not installed"
					if language == "python" {
						expected = "catalog_connector cannot be found"
					}
				} else {
					require.NoError(t, os.Remove(filepath.Join(packagePath, "dex-connector.json")))
				}
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				codes := diagnosticCodes(graph)
				require.NotContains(t, codes, language+"_analyzer_failed")
				require.Contains(t, codes, "dynamic_step_target")
				missing := 0
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest_missing" {
						missing++
						require.Contains(t, diagnostic.Message, expected)
					}
				}
				require.Equal(t, 2, missing, "%+v", graph.Diagnostics)
			})
		}
	}
}

func TestScriptWrongTypedManifestIsBlocking(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			manifestPath := filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json")
			var manifest map[string]any
			contents, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(contents, &manifest))
			manifest["factories"].([]any)[0].(map[string]any)["fields"] = []any{"stepType"}
			contents, err = json.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, contents, 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			codes := diagnosticCodes(graph)
			require.NotContains(t, codes, language+"_analyzer_failed")
			require.Contains(t, codes, "connector_manifest")
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "connector_manifest" {
					require.Contains(t, diagnostic.Message, "factories[0].fields must be an object of strings")
				}
			}
		})
	}
}

func TestScriptDuplicateStepTypesAreBlocking(t *testing.T) {
	type duplicateCase struct {
		name         string
		replacements map[string][2]string
	}
	cases := []duplicateCase{
		{name: "connector factories", replacements: map[string][2]string{
			"python":     {"        self.lookup = lookup_record(", "        self.second_lookup = lookup_record(step_type=\"LookupRecord\", connection_name=CONNECTION_NAME, annotations={\"groupId\": \"lookup\", \"groupLabel\": \"Lookup\", \"explanation\": \"Look up again.\"}, found=self.finish, unavailable=self.finish)\n        self.lookup = lookup_record("},
			"typescript": {"  private readonly lookup = lookupRecord({", "  private readonly secondLookup = lookupRecord({stepType: \"LookupRecord\", connectionName, annotations: {groupId: \"lookup\", groupLabel: \"Lookup\", explanation: \"Look up again.\"}, found: this.finish, unavailable: this.finish});\n  private readonly lookup = lookupRecord({"},
		}},
		{name: "connector and local Step", replacements: map[string][2]string{
			"python":     {`step_type="LookupRecord"`, `step_type="FinishStep"`},
			"typescript": {`stepType: "LookupRecord"`, `stepType: "FinishStep"`},
		}},
	}
	for _, testCase := range cases {
		for _, language := range []string{"python", "typescript"} {
			t.Run(testCase.name+"/"+language, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				modified := strings.Replace(string(source), testCase.replacements[language][0], testCase.replacements[language][1], 1)
				if testCase.name == "connector factories" {
					registration := map[string][2]string{
						"python":     {"other_steps(self.finish)", "other_steps(self.finish, self.second_lookup)"},
						"typescript": {"otherSteps(this.finish)", "otherSteps(this.finish, this.secondLookup)"},
					}[language]
					modified = strings.Replace(modified, registration[0], registration[1], 1)
				}
				require.NotEqual(t, string(source), modified)
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "duplicate_step_type")
			})
		}
	}
}

func TestTypeScriptEveryHiddenDecisionIsBlocking(t *testing.T) {
	cases := map[string]struct {
		execute       string
		hiddenCount   int
		hasRecognized bool
	}{
		"helper in one branch":   {"if (_input) return forceComplete();\n    return chooseDecision();", 1, true},
		"variable in one branch": {"const decision = forceComplete();\n    if (_input) return forceComplete();\n    return decision;", 1, true},
		"conditional arm":        {"return _input ? forceComplete() : chooseDecision();", 1, true},
		"every return hidden":    {"if (_input) return chooseDecision();\n    return chooseDecision();", 2, false},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sourcePath := scriptFixture(t, "typescript")
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := strings.Replace(string(source), "return forceComplete();", testCase.execute, 1)
			modified += "\nfunction chooseDecision(): StepDecision {\n  return forceComplete();\n}\n"
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			for _, schemaVersion := range []string{SchemaVersionV1, SchemaVersionV2} {
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: schemaVersion})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				hiddenCount := 0
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "hidden_dex_decision" {
						require.Equal(t, "error", diagnostic.Severity)
						hiddenCount++
					}
				}
				require.Equal(t, testCase.hiddenCount, hiddenCount, "%+v", graph.Diagnostics)
				hasRecognized := false
				for _, node := range graph.Nodes {
					if node.Kind == "decision" && node.ParentID == "step:FinishStep" && node.Decision.Type == "forceComplete" {
						hasRecognized = true
					}
				}
				require.Equal(t, testCase.hasRecognized, hasRecognized)
			}
		})
	}
}

func TestTypeScriptAnalyzerResolvesCompilerFromSourceProject(t *testing.T) {
	sourcePath := scriptFixture(t, "typescript")
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Setenv("NODE_PATH", "")
	t.Chdir(filepath.Join(root, "web"))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Len(t, graph.Diagnostics, 1)
	require.Equal(t, "typescript_analyzer_failed", graph.Diagnostics[0].Code)
	require.Contains(t, graph.Diagnostics[0].Message, "typescript package installed in the source project")
	require.Contains(t, graph.Diagnostics[0].Message, "NODE_PATH")
}

func TestTypeScriptAnalyzerLoadsCompilerFromNodePathOutsideSourceProject(t *testing.T) {
	sourcePath := scriptFixture(t, "typescript")
	require.NoDirExists(t, filepath.Join(filepath.Dir(sourcePath), "node_modules", "typescript"))
	t.Chdir(t.TempDir())
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
}

func TestScriptConnectorNamespaceImports(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := string(source)
			if language == "python" {
				modified = strings.ReplaceAll(modified, "from catalog_connector import create_lookup as lookup_record, create_trigger", "import catalog_connector as catalog")
				modified = strings.ReplaceAll(modified, "lookup_record(", "catalog.create_lookup(")
				modified = strings.ReplaceAll(modified, "create_trigger(", "catalog.create_trigger(")
			} else {
				modified = strings.ReplaceAll(modified, `import { createLookup as lookupRecord, createTrigger } from "@dex-test/catalog";`, `import * as catalog from "@dex-test/catalog";`)
				modified = strings.ReplaceAll(modified, "lookupRecord(", "catalog.createLookup(")
				modified = strings.ReplaceAll(modified, "createTrigger(", "catalog.createTrigger(")
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
		})
	}
}

func scriptFixture(t *testing.T, language string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Setenv("NODE_PATH", filepath.Join(root, "web", "node_modules"))
	directory := t.TempDir()
	require.NoError(t, os.CopyFS(directory, os.DirFS(filepath.Join("testdata", language))))
	if language == "typescript" {
		require.NoError(t, os.Rename(filepath.Join(directory, "node-packages"), filepath.Join(directory, "node_modules")))
		return filepath.Join(directory, "workflow.ts")
	}
	return filepath.Join(directory, "workflow.py")
}

func scriptFixturePackage(sourcePath string, language string) string {
	if language == "typescript" {
		return filepath.Join(filepath.Dir(sourcePath), "node_modules", "@dex-test", "catalog")
	}
	return filepath.Join(filepath.Dir(sourcePath), "catalog_connector")
}

func diagnosticCodes(graph *Graph) []string {
	codes := make([]string, 0, len(graph.Diagnostics))
	for _, diagnostic := range graph.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

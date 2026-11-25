// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonUnpacksSubFlowRunComprehensions(t *testing.T) {
	const source = `from dex import ConditionCombination, Flow, Step, StepList, SubFlow, Timer, Wait, graceful_complete
from child_flows import ChildFlow, child_flow

class LocalChildFlow(Flow[str]):
    def get_steps(self):
        return StepList.empty()

local_child_flow = LocalChildFlow()

# dex:group group-id:child-flows group-label:"Child Flows"
# dex:explanation text:"Wait for the child Flows."
class RunChildren(Step[list[str]]):
    def __init__(self, child: ChildFlow):
        self.child = child

    def wait_for(self, context, requests: list[str]):
        WAIT_BODY

    def execute(self, context, requests: list[str]):
        return graceful_complete()

class ParentFlow(Flow[list[str]]):
    def __init__(self, child: ChildFlow):
        self.step = RunChildren(child)

    def get_steps(self):
        return StepList.start_step(self.step)
`
	const comprehension = "[SubFlow.run(child_flow, request) for request in requests]"
	testCases := map[string]struct {
		body  string
		label string
	}{
		"bound list":                  {"runs = " + comprehension + "\nreturn Wait.all_of(*runs)", "child_flow"},
		"annotated list with timer":   {"runs: list = " + comprehension + "\nreturn Wait.any_of(*runs, Timer.by_duration(1000))", "child_flow"},
		"inline list":                 {"return Wait.all_of(*" + comprehension + ")", "child_flow"},
		"inline generator":            {"return Wait.all_of(*(SubFlow.run(child_flow, request) for request in requests))", "child_flow"},
		"field with injected Flow":    {"runs = [SubFlow.run(self.child, request) for request in requests]\nreturn Wait.all_of(*runs)", "self.child"},
		"inline generator with field": {"return Wait.all_of(*(SubFlow.run(self.child, request) for request in requests))", "self.child"},
		"stable child alias":          {"child = child_flow\nruns = [SubFlow.run(child, request) for request in requests]\nreturn Wait.all_of(*runs)", "child"},
		"local child Flow instance":   {"return Wait.all_of(*(SubFlow.run(local_child_flow, request) for request in requests))", "local_child_flow"},
		"reassigned list":             {body: "runs = " + comprehension + "\nruns = []\nreturn Wait.all_of(*runs)"},
		"mutated list":                {body: "runs = " + comprehension + "\nruns.append(Timer.by_duration(1000))\nreturn Wait.all_of(*runs)"},
		"list passed to a helper":     {body: "runs = " + comprehension + "\nupdate(runs)\nreturn Wait.all_of(*runs)"},
		"list aliased":                {body: "runs = " + comprehension + "\nalias = runs\nreturn Wait.all_of(*alias)"},
		"list read before binding":    {body: "return Wait.all_of(*runs)\nruns = " + comprehension},
		"conditional binding":         {body: "if requests:\n    runs = " + comprehension + "\nreturn Wait.all_of(*runs)"},
		"helper returning conditions": {body: "return Wait.any_of(*build_runs(requests), Timer.by_duration(1000))"},
		"element built by helper":     {body: "runs = [build_run(request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"timer comprehension":         {body: "return Wait.all_of(*[Timer.by_duration(request) for request in requests])"},
		"filtered comprehension":      {body: "return Wait.all_of(*[SubFlow.run(child_flow, request) for request in requests if request])"},
		"empty list":                  {body: "runs = []\nreturn Wait.all_of(*runs)"},
		"computed child":              {body: "runs = [SubFlow.run(select_child(request), request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"computed child alias":        {body: "child = select_child(requests)\nruns = [SubFlow.run(child, request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"reassigned child":            {body: "child = child_flow\nchild = select_child(requests)\nreturn Wait.all_of(*[SubFlow.run(child, request) for request in requests])"},
		"reassigned child field":      {body: "self.child = select_child(requests)\nreturn Wait.all_of(*[SubFlow.run(self.child, request) for request in requests])"},
		"child is the item":           {body: "return Wait.any_of(*(SubFlow.run(child_flow, '') for child_flow in requests))"},
		"child is an item property":   {body: "return Wait.any_of(*(SubFlow.run(request.child, request) for request in requests))"},
		"unpacked run arguments":      {body: "return Wait.all_of(*(SubFlow.run(*(child_flow, request)) for request in requests))"},
		"unpacked collection copy":    {body: "runs = " + comprehension + "\nreturn Wait.all_of(*runs[:])"},
		"unpacked nested condition":   {body: "return Wait.any_combination_of(ConditionCombination.of(*" + comprehension + "))"},
		"unpacked Wait.until":         {body: "return Wait.until(*" + comprehension + ")"},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "workflow.py")
			body := strings.ReplaceAll(testCase.body, "\n", "\n        ")
			require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(source, "WAIT_BODY", body, 1)), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ParentFlow"})
			require.NoError(t, err)
			var waitNode *Node
			for index := range graph.Nodes {
				if graph.Nodes[index].Kind == "wait" {
					require.Nil(t, waitNode)
					waitNode = &graph.Nodes[index]
				}
			}
			require.NotNil(t, waitNode)
			require.NotEmpty(t, waitNode.Wait.Conditions)
			condition := waitNode.Wait.Conditions[0]
			if testCase.label == "" {
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "dynamic_wait_condition")
				require.Equal(t, "unknown", condition.Kind)
				for _, edge := range graph.Edges {
					require.NotEqual(t, "subflow", edge.Kind)
				}
			} else {
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
				require.Equal(t, "subflow", condition.Kind)
				require.Equal(t, testCase.label, condition.Label)
				require.NotEmpty(t, condition.SubFlowID)
				require.True(t, hasScriptEdge(graph, "subflow", waitNode.ID, condition.SubFlowID))
				for _, edge := range graph.Edges {
					if edge.Kind == "subflow" {
						require.Equal(t, "start", edge.Label)
					}
				}
			}
			if strings.Contains(testCase.body, ", Timer.by_duration") {
				require.Len(t, waitNode.Wait.Conditions, 2)
				require.Equal(t, "timer", waitNode.Wait.Conditions[1].Kind)
			} else {
				require.Len(t, waitNode.Wait.Conditions, 1)
			}
		})
	}
}

func TestPythonAnalyzerScriptIsPassedAsPrivateFile(t *testing.T) {
	command, removeScript, err := newPythonAnalyzerCommand(t.Context(), "python3")
	require.NoError(t, err)
	t.Cleanup(removeScript)
	for _, argument := range command.Args {
		require.Less(t, len(argument), 100*1024)
	}
	require.Len(t, command.Args, 4)
	require.Equal(t, []string{"python3", "-I", "-S"}, command.Args[:3])
	scriptPath := command.Args[3]
	directory := filepath.Dir(scriptPath)
	directoryInfo, err := os.Stat(directory)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), directoryInfo.Mode().Perm())
	scriptInfo, err := os.Stat(scriptPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), scriptInfo.Mode().Perm())
	removeScript()
	require.NoDirExists(t, directory)
}

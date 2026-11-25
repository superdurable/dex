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

const pythonWaitUnpackingFlow = `from dex import Channel, ConditionCombination, Flow, Step, StepList, SubFlow, Timer, Wait, graceful_complete
from child_flows import ChildFlow, child_flow
import child_flows as child_flows_module
from dex.wait import Wait as DexWait
from typing import Any

REQUEST_QUEUE = Channel("requests", str)

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

func TestPythonSubFlowWaitArguments(t *testing.T) {
	const comprehension = "[SubFlow.run(child_flow, request) for request in requests]"
	testCases := map[string]struct {
		body  string
		label string
	}{
		"bound list":                   {"runs = " + comprehension + "\nreturn Wait.all_of(*runs)", "child_flow"},
		"bound condition":              {"run = SubFlow.run(child_flow, requests[0])\nreturn Wait.until(run)", "child_flow"},
		"bound keyword condition":      {"run = SubFlow.run(child_flow, requests[0])\nreturn Wait.until(condition=run)", "child_flow"},
		"annotated list with timer":    {"runs: list = " + comprehension + "\nreturn Wait.any_of(*runs, Timer.by_duration(1000))", "child_flow"},
		"inline list":                  {"return Wait.all_of(*" + comprehension + ")", "child_flow"},
		"inline generator":             {"return Wait.all_of(*(SubFlow.run(child_flow, request) for request in requests))", "child_flow"},
		"filtered comprehension":       {"return Wait.all_of(*[SubFlow.run(child_flow, request) for request in requests if request])", "child_flow"},
		"multiple for clauses":         {"return Wait.all_of(*[SubFlow.run(child_flow, request) for group in requests for request in group])", "child_flow"},
		"keyword child":                {"return Wait.all_of(*[SubFlow.run(flow=child_flow, input=request) for request in requests])", "SubFlow"},
		"imported module member":       {"return Wait.all_of(*[SubFlow.run(child_flows_module.child_flow, request) for request in requests])", "child_flows_module.child_flow"},
		"nested module member":         {"return Wait.all_of(*[SubFlow.run(child_flows_module.flows.child_flow, request) for request in requests])", "child_flows_module.flows.child_flow"},
		"field with injected Flow":     {"runs = [SubFlow.run(self.child, request) for request in requests]\nreturn Wait.all_of(*runs)", "self.child"},
		"inline generator with field":  {"return Wait.all_of(*(SubFlow.run(self.child, request) for request in requests))", "self.child"},
		"stable child alias":           {"child = child_flow\nruns = [SubFlow.run(child, request) for request in requests]\nreturn Wait.all_of(*runs)", "child"},
		"local child Flow instance":    {"return Wait.all_of(*(SubFlow.run(local_child_flow, request) for request in requests))", "local_child_flow"},
		"reassigned list":              {body: "runs = " + comprehension + "\nruns = []\nreturn Wait.all_of(*runs)"},
		"augmented list assignment":    {body: "runs = " + comprehension + "\nruns += []\nreturn Wait.all_of(*runs)"},
		"mutated list":                 {body: "runs = " + comprehension + "\nruns.append(Timer.by_duration(1000))\nreturn Wait.all_of(*runs)"},
		"list unpacked twice":          {body: "runs = " + comprehension + "\nreturn Wait.all_of(*runs, *runs)"},
		"list from parameter":          {body: "return Wait.all_of(*requests)"},
		"plain SubFlow list":           {body: "runs = " + comprehension + "\nreturn Wait.until(runs)"},
		"condition from field":         {body: "return Wait.until(self.run)"},
		"condition from indexed list":  {body: "runs = " + comprehension + "\nreturn Wait.until(runs[0])"},
		"condition from helper":        {body: "return Wait.until(build_run(requests[0]))"},
		"condition from method":        {body: "return Wait.until(condition=self.build_run(requests[0]))"},
		"combination field condition":  {body: "return Wait.any_combination_of(ConditionCombination.of(self.run))"},
		"combination indexed list":     {body: "runs = " + comprehension + "\nreturn Wait.any_combination_of(ConditionCombination.of(runs[0]))"},
		"combination helper condition": {body: "return Wait.any_combination_of(ConditionCombination.of(build_run(requests[0])))"},
		"unknown keyword unpacking":    {body: "keywords = build_keywords(requests)\nreturn Wait.all_of(**keywords)"},
		"SubFlow keyword unpacking":    {body: "return Wait.all_of(**{'condition': SubFlow.run(child_flow, requests[0])})"},
		"opaque star in inline list":   {body: "return Wait.all_of([*requests])"},
		"list from field":              {body: "return Wait.all_of(*self.conditions)"},
		"bound generator":              {body: "runs = (SubFlow.run(child_flow, request) for request in requests)\nreturn Wait.all_of(*runs)"},
		"list read in nested function": {body: "runs = " + comprehension + "\ndef read_runs():\n    return runs\nreturn Wait.all_of(*runs)"},
		"list passed to a helper":      {body: "runs = " + comprehension + "\nupdate(runs)\nreturn Wait.all_of(*runs)"},
		"list aliased":                 {body: "runs = " + comprehension + "\nalias = runs\nreturn Wait.all_of(*alias)"},
		"list read before binding":     {body: "return Wait.all_of(*runs)\nruns = " + comprehension},
		"conditional binding":          {body: "if requests:\n    runs = " + comprehension + "\nreturn Wait.all_of(*runs)"},
		"helper returning conditions":  {body: "return Wait.any_of(*build_runs(requests), Timer.by_duration(1000))"},
		"element built by helper":      {body: "runs = [build_run(request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"element from unknown channel": {body: "runs = [self.unknown.for_one()]\nreturn Wait.all_of(*runs)"},
		"list extended by helper":      {body: "runs = [Timer.by_duration(1000)]\nruns.extend(build_runs(requests))\nreturn Wait.all_of(*runs)"},
		"list mutated by helper":       {body: "runs = [Timer.by_duration(1000)]\nupdate(runs)\nreturn Wait.all_of(*runs)"},
		"mixed list":                   {body: "runs = [Timer.by_duration(1000)]\nruns.append(SubFlow.run(child_flow, requests[0]))\nreturn Wait.all_of(*runs)"},
		"timer comprehension":          {"return Wait.all_of(*[Timer.by_duration(request) for request in requests])", "request timer"},
		"nested timer generator":       {"return Wait.any_combination_of(ConditionCombination.of(*(Timer.by_duration(request) for request in requests if request)))", "request timer"},
		"computed child":               {body: "runs = [SubFlow.run(select_child(request), request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"computed child alias":         {body: "child = select_child(requests)\nruns = [SubFlow.run(child, request) for request in requests]\nreturn Wait.all_of(*runs)"},
		"reassigned child":             {body: "child = child_flow\nchild = select_child(requests)\nreturn Wait.all_of(*[SubFlow.run(child, request) for request in requests])"},
		"reassigned child field":       {body: "self.child = select_child(requests)\nreturn Wait.all_of(*[SubFlow.run(self.child, request) for request in requests])"},
		"child is the item":            {body: "return Wait.any_of(*(SubFlow.run(child_flow, '') for child_flow in requests))"},
		"child bound by second for":    {body: "return Wait.all_of(*[SubFlow.run(child_flow, request) for request in requests for child_flow in request])"},
		"child unpacked by for":        {body: "return Wait.all_of(*[SubFlow.run(child_flow, request) for child_flow, request in requests])"},
		"child is an item property":    {body: "return Wait.any_of(*(SubFlow.run(request.child, request) for request in requests))"},
		"typing import":                {body: "return Wait.all_of(*[SubFlow.run(Any, request) for request in requests])"},
		"dex submodule import":         {body: "return Wait.all_of(*[SubFlow.run(DexWait, request) for request in requests])"},
		"bare module import":           {body: "return Wait.all_of(*[SubFlow.run(child_flows_module, request) for request in requests])"},
		"assignment expression":        {body: "return Wait.all_of(*[SubFlow.run(child_flow, request) for request in requests if (allowed := request)])"},
		"unpacked run arguments":       {body: "return Wait.all_of(*(SubFlow.run(*(child_flow, request)) for request in requests))"},
		"unpacked collection copy":     {body: "runs = " + comprehension + "\nreturn Wait.all_of(*runs[:])"},
		"unpacked nested condition":    {body: "return Wait.any_combination_of(ConditionCombination.of(*" + comprehension + "))"},
		"unpacked Wait.until":          {body: "return Wait.until(*" + comprehension + ")"},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph, waitNode := analyzePythonWaitUnpacking(t, testCase.body)
			require.NotEmpty(t, waitNode.Wait.Conditions)
			condition := waitNode.Wait.Conditions[0]
			switch {
			case testCase.label == "":
				require.False(t, graph.Valid)
				require.Contains(t, dynamicWaitConditionSeverities(graph), "error")
				require.Equal(t, "unknown", condition.Kind)
				for _, edge := range graph.Edges {
					require.NotEqual(t, "subflow", edge.Kind)
				}
			case testCase.label == "request timer":
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
				require.Empty(t, dynamicWaitConditionSeverities(graph))
				require.Equal(t, "timer", condition.Kind)
				require.Equal(t, testCase.label, condition.Label)
				require.Equal(t, "request", condition.Expression)
			default:
				require.True(t, graph.Valid, "%+v", graph.Diagnostics)
				require.Empty(t, dynamicWaitConditionSeverities(graph))
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
			switch {
			case strings.Contains(testCase.body, ", Timer.by_duration"):
				require.Len(t, waitNode.Wait.Conditions, 2)
				require.Equal(t, "timer", waitNode.Wait.Conditions[1].Kind)
			case name == "list unpacked twice":
				require.Len(t, waitNode.Wait.Conditions, 2)
				require.Equal(t, "unknown", waitNode.Wait.Conditions[1].Kind)
			default:
				require.Len(t, waitNode.Wait.Conditions, 1)
			}
		})
	}
}

func TestPythonWarnsOnUnpackedNonSubFlowConditions(t *testing.T) {
	testCases := map[string]struct {
		body           string
		conditionKinds []string
	}{
		"bound channel comprehension with insert": {
			body: "conditions = [REQUEST_QUEUE.for_one() for request in requests]\nif requests:\n    conditions.insert(0, REQUEST_QUEUE.for_one())\nreturn Wait.any_of(*conditions)",
		},
		"bound timers with append and extend": {
			body: "conditions = []\nconditions.append(Timer.by_duration(1000))\nif len(conditions) < len(requests):\n    conditions.extend(Timer.by_duration(request) for request in requests)\nreturn Wait.all_of(*conditions)",
		},
		"bound timers with augmented assignment": {
			body: "conditions = []\nif requests:\n    conditions += [Timer.by_duration(1000)]\nreturn Wait.all_of(*conditions)",
		},
		"empty bound list":                  {body: "conditions = []\nreturn Wait.all_of(*conditions)"},
		"bound condition without unpacking": {body: "condition = Timer.by_duration(1000)\nreturn Wait.until(condition)"},
		"bound list without unpacking":      {body: "conditions = [Timer.by_duration(1000)]\nreturn Wait.until(conditions)"},
		"known keyword unpacking":           {body: "keywords = {'condition': Timer.by_duration(1000)}\nreturn Wait.all_of(**keywords)"},
		"known element in inline list": {
			body:           "return Wait.all_of([*[Timer.by_duration(1000)]])",
			conditionKinds: []string{"timer"},
		},
		"inline channel comprehension": {
			body:           "return Wait.any_of(*[REQUEST_QUEUE.for_one() for request in requests], Timer.by_duration(1000))",
			conditionKinds: []string{"timer", "channel"},
		},
		"inline timer list": {
			body:           "return Wait.all_of(*[Timer.by_duration(1000)])",
			conditionKinds: []string{"timer"},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph, waitNode := analyzePythonWaitUnpacking(t, testCase.body)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Equal(t, []string{"warning"}, dynamicWaitConditionSeverities(graph))
			var conditionKinds []string
			for _, condition := range waitNode.Wait.Conditions {
				conditionKinds = append(conditionKinds, condition.Kind)
			}
			require.Equal(t, testCase.conditionKinds, conditionKinds)
		})
	}
}

func analyzePythonWaitUnpacking(t *testing.T, waitBody string) (*Graph, *Node) {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "workflow.py")
	body := strings.ReplaceAll(waitBody, "\n", "\n        ")
	require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(pythonWaitUnpackingFlow, "WAIT_BODY", body, 1)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ParentFlow"})
	require.NoError(t, err)
	var waitNode *Node
	for index := range graph.Nodes {
		if graph.Nodes[index].Kind == "wait" {
			require.Nil(t, waitNode)
			waitNode = &graph.Nodes[index]
		}
	}
	require.NotNil(t, waitNode, "%+v", graph.Diagnostics)
	return graph, waitNode
}

func dynamicWaitConditionSeverities(graph *Graph) []string {
	var severities []string
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "dynamic_wait_condition" {
			severities = append(severities, diagnostic.Severity)
		}
	}
	return severities
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

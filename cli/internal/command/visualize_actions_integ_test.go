// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

// The same Flow in Go, Python, and TypeScript must give Dex Web the same Actions and RPC locks.
func TestVisualizeActionsMatchAcrossLanguages(t *testing.T) {
	t.Setenv("NODE_PATH", filepath.Join(visualizerRepositoryRoot(t), "web", "node_modules"))
	var expectedActions string
	var expectedLocks []string
	for _, file := range []string{"workflow.go", "workflow.py", "workflow.ts"} {
		t.Run(file, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), stateLoadsFixture(t, "action-parity/"+file), flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			actions, err := json.MarshalIndent(graph.V2.Actions, "", "  ")
			require.NoError(t, err)
			locks := rpcLockEdgesByRPCName(graph)
			if expectedActions == "" {
				expectedActions, expectedLocks = string(actions), locks
				require.Contains(t, expectedActions, `"rpcName": "reject_refund"`)
				require.Equal(t, []string{"reject_refund>case-status"}, locks)
				return
			}
			require.JSONEq(t, expectedActions, string(actions))
			require.Equal(t, expectedLocks, locks)
		})
	}
}

func rpcLockEdgesByRPCName(graph *flowviz.Graph) []string {
	names := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		names[node.ID] = node.Name
	}
	locks := make([]string, 0)
	for _, edge := range graph.Edges {
		if edge.Kind != "resource_lock" {
			continue
		}
		rpcName, _ := edge.Metadata["rpcName"].(string)
		locks = append(locks, rpcName+">"+names[edge.To])
	}
	sort.Strings(locks)
	return locks
}

// An Action field that cannot be read statically blocks the graph, so Dex Web never loads a partial Action.
func TestVisualizeReportsUnreadablePythonAndTypeScriptActions(t *testing.T) {
	t.Setenv("NODE_PATH", filepath.Join(visualizerRepositoryRoot(t), "web", "node_modules"))
	cases := []struct {
		name     string
		file     string
		replace  [2]string
		code     string
		fragment string
	}{
		{"python dynamic label", "workflow.py", [2]string{`"Reject refund",`, `"Reject " + "refund",`}, "v2_action", "RPC reject_refund Action label is not static"},
		{"python non-equal match", "workflow.py", [2]string{`AttributeMatch.equal_to(1)`, `AttributeMatch.greater_than(1)`}, "v2_action", "Actions support only equality"},
		{"python dynamic locks", "workflow.py", [2]string{`lock_attributes=(CASE_STATUS.lock(),),`, `lock_attributes=locks(),`}, "rpc_lock_unresolved", "lock_attributes of RPC reject_refund"},
		{"python missing input directive", "workflow.py", [2]string{`    # dex:input field-name:note value-type:string source:user required:false description:"Note"` + "\n", ""}, "v2_action_input", "every field of Action RPC reject_refund input RejectRefundInput"},
		{"python required mismatch", "workflow.py", [2]string{`field-name:note value-type:string source:user required:false`, `field-name:note value-type:string source:user required:true`}, "v2_directive", `input field "note" required`},
		{"python mistyped condition value", "workflow.py", [2]string{`AttributeMatch.equal_to(1)`, `AttributeMatch.equal_to("1")`}, "v2_action", "matching compile-time int64"},
		{"typescript dynamic label", "workflow.ts", [2]string{`label: "Reject refund",`, "label: `Reject ${\"refund\"}`,"}, "v2_action", "RPC reject_refund Action"},
		{"typescript non-equal match", "workflow.ts", [2]string{`AttributeMatch.equalTo(1n)`, `AttributeMatch.greaterThan(1n)`}, "v2_action", "RPC escalate_case Action"},
		{"typescript dynamic locks", "workflow.ts", [2]string{`lockAttributes: [caseStatus.lock()],`, `lockAttributes: locks(),`}, "rpc_lock_unresolved", "RPC reject_refund"},
		{"typescript missing input directive", "workflow.ts", [2]string{`  // dex:input field-name:note value-type:string source:user required:false description:"Note"` + "\n", ""}, "v2_action_input", "every field of Action RPC reject_refund"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source, err := os.ReadFile(stateLoadsFixture(t, "action-parity/"+testCase.file))
			require.NoError(t, err)
			require.Contains(t, string(source), testCase.replace[0])
			path := filepath.Join(t.TempDir(), testCase.file)
			require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(source), testCase.replace[0], testCase.replace[1], 1)), 0o600))
			graph, err := flowviz.Analyze(context.Background(), path, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			diagnostic := diagnosticWithCode(t, graph, testCase.code, testCase.fragment)
			require.Equal(t, "error", diagnostic.Severity)
		})
	}
}

// Python and TypeScript Actions may omit the condition; the graph then has no condition, as Dex Web expects.
func TestVisualizeKeepsConditionLessScriptActions(t *testing.T) {
	t.Setenv("NODE_PATH", filepath.Join(visualizerRepositoryRoot(t), "web", "node_modules"))
	replacements := map[string][2]string{
		"workflow.py": {`condition=when_attribute_matches(PRIORITY, AttributeMatch.equal_to(1), AttributeMatch.equal_to(2)),`, ``},
		"workflow.ts": {`condition: whenAttributeMatches(priority, AttributeMatch.equalTo(1n), AttributeMatch.equalTo(2n)),`, ``},
	}
	for file, replace := range replacements {
		t.Run(file, func(t *testing.T) {
			source, err := os.ReadFile(stateLoadsFixture(t, "action-parity/"+file))
			require.NoError(t, err)
			require.Contains(t, string(source), replace[0])
			path := filepath.Join(t.TempDir(), file)
			require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(source), replace[0], replace[1], 1)), 0o600))
			graph, err := flowviz.Analyze(context.Background(), path, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			encoded, err := flowviz.MarshalJSON(graph)
			require.NoError(t, err)
			var decoded struct {
				V2 struct {
					Actions []map[string]any `json:"actions"`
				} `json:"v2"`
			}
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			require.Len(t, decoded.V2.Actions, 2)
			require.Equal(t, "escalate_case", decoded.V2.Actions[1]["rpcName"])
			require.NotContains(t, decoded.V2.Actions[1], "condition")
			require.Contains(t, decoded.V2.Actions[0], "condition")
		})
	}
}

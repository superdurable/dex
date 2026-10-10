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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeSubFlowWaitsGolden(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	source := "examples/go/primitives/subflow/selection_flow.go"
	graph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, source), flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, graph.Diagnostics)
	graph.Source.Path = source
	actual, err := flowviz.MarshalJSON(graph)
	require.NoError(t, err)
	goldenPath := filepath.Join(repositoryRoot, "cli/internal/command/testdata/subflow-selection.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
	}
	golden, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(actual))

	waits := nodesOfKind(graph.Nodes, "wait")
	require.Len(t, waits, 3)
	for _, wait := range waits {
		switch wait.ParentID {
		case "step:awaitChildAlone":
			require.Equal(t, "until", wait.Wait.Type)
			require.Equal(t, []string{"subflow"}, waitConditionKinds(wait.Wait.Conditions))
		case "step:awaitChildOrMessage":
			require.Equal(t, "anyOf", wait.Wait.Type)
			require.Equal(t, []string{"subflow", "channel"}, waitConditionKinds(wait.Wait.Conditions))
		case "step:awaitChildrenTogether":
			require.Equal(t, "allOf", wait.Wait.Type)
			require.Equal(t, []string{"subflow", "subflow"}, waitConditionKinds(wait.Wait.Conditions))
		default:
			t.Fatalf("unexpected wait owner: %s", wait.ParentID)
		}
		for index, condition := range wait.Wait.Conditions {
			require.NotNil(t, condition.Index)
			require.Equal(t, index, *condition.Index)
		}
	}
	children := nodesOfKind(graph.Nodes, "subflow")
	require.Len(t, children, 4)
	childGraph, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, "examples/go/primitives/subflow/child_flow.go"), flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, childGraph.Valid, childGraph.Diagnostics)
	for _, child := range children {
		require.Equal(t, childGraph.Flow.Name, child.Name)
		require.Equal(t, childGraph.Flow.Name, child.Metadata["flowType"])
		require.Equal(t, "child_flow.go", child.Metadata["sourcePath"])
		incomingEdges := 0
		for _, edge := range edgesOfKind(graph.Edges, "subflow") {
			if edge.To == child.ID {
				incomingEdges++
			}
		}
		require.Equal(t, 1, incomingEdges)
	}
	version2, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, source), flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
	require.NoError(t, err)
	require.True(t, version2.Valid, version2.Diagnostics)
	require.Equal(t, waits, nodesOfKind(version2.Nodes, "wait"))
	require.Equal(t, children, nodesOfKind(version2.Nodes, "subflow"))

}

func TestVisualizeSubFlowHelpersDoNotInventWaits(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(repositoryRoot, "examples/go/primitives/subflow/selection_flow.go"))
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "recursive", body: "return childCompletionCondition(child, input)"},
		{name: "dynamic", body: "if input > 0 { return dex.SubFlow(child, input) }; return SelectionMessages.ForOne()"},
		{name: "discarded", body: "dex.SubFlow(child, input); return SelectionMessages.ForOne()"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			module := "module subflowhelpers\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v1.5.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
			child, err := os.ReadFile(filepath.Join(repositoryRoot, "examples/go/primitives/subflow/child_flow.go"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "child_flow.go"), child, 0o644))
			modified := strings.Replace(string(source), "return dex.SubFlow(child, input)", test.body, 1)
			sourcePath := filepath.Join(directory, "selection_flow.go")
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			require.Len(t, nodesOfKind(graph.Nodes, "subflow"), 1)
			for _, wait := range nodesOfKind(graph.Nodes, "wait") {
				if wait.ParentID == "step:awaitChildrenTogether" {
					expectedKind := "unknown"
					if test.name == "discarded" {
						expectedKind = "channel"
					}
					require.Equal(t, []string{expectedKind, expectedKind}, waitConditionKinds(wait.Wait.Conditions))
				}
			}
		})
	}
}

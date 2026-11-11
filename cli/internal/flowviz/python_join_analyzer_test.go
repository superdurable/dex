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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonJoinDefinitionGolden(t *testing.T) {
	directory := t.TempDir()
	source, err := os.ReadFile(filepath.Join("testdata", "python", "join.py"))
	require.NoError(t, err)
	sourcePath := filepath.Join(directory, "join.py")
	require.NoError(t, os.WriteFile(sourcePath, source, 0o644))

	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	requirePythonJoinEdge(t, graph, "transition", "step:RequestQuotes", "step:QuoteFromSupplierA")
	requirePythonJoinEdge(t, graph, "transition", "step:RequestQuotes", "step:QuoteFromSupplierB")
	requirePythonJoinEdge(t, graph, "transition", "step:RequestQuotes", "step:AcceptQuotes")
	requirePythonJoinEdge(t, graph, "transition", "rpc:request_reviews", "step:ReviewDocument")
	requirePythonJoinEdge(t, graph, "transition", "rpc:request_reviews", "step:CollectReviews")
	requirePythonJoinEdge(t, graph, "resource_publish", "step:QuoteFromSupplierA", "resource:channel:QUOTE_RECEIVED")
	requirePythonJoinEdge(t, graph, "resource_publish", "step:QuoteFromSupplierB", "resource:channel:QUOTE_RECEIVED")
	requirePythonJoinEdge(t, graph, "resource_read", "resource:channel:QUOTE_RECEIVED", "step:AcceptQuotes")
	requirePythonJoinEdge(t, graph, "wait_condition", "resource:channel:QUOTE_RECEIVED", "wait:AcceptQuotes")
	requirePythonJoinEdge(t, graph, "wait_condition", "resource:channel:QUOTE_RECEIVED", "wait:RequestQuotes")
	requirePythonJoinEdge(t, graph, "wait_condition", "resource:channel:QUOTE_RECEIVED", "wait:CollectReviews")

	graph.Source.Path = "join.py"
	actual, err := MarshalJSON(graph)
	require.NoError(t, err)
	goldenPath := filepath.Join("testdata", "python", "join.golden.json")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
	}
	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual))
}

// requirePythonJoinEdge matches an edge whose endpoints equal or start with the given node IDs.
func requirePythonJoinEdge(t *testing.T, graph *Graph, kind string, from string, to string) {
	t.Helper()
	for _, edge := range graph.Edges {
		if edge.Kind != kind || !pythonJoinNodeMatches(graph, edge.From, from) || !pythonJoinNodeMatches(graph, edge.To, to) {
			continue
		}
		return
	}
	require.Failf(t, "missing edge", "%s %s -> %s in %+v", kind, from, to, graph.Edges)
}

// pythonJoinNodeMatches accepts the node itself, an ID prefix, or a child node of it.
func pythonJoinNodeMatches(graph *Graph, nodeID string, expected string) bool {
	if nodeID == expected || len(nodeID) > len(expected) && nodeID[:len(expected)+1] == expected+":" {
		return true
	}
	for _, node := range graph.Nodes {
		if node.ID == nodeID && node.ParentID == expected {
			return true
		}
	}
	return false
}

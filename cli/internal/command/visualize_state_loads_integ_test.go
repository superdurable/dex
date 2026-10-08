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
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeFollowsHelpersAndLoopBodies(t *testing.T) {
	source := stateLoadsFixture(t, "loaded/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	helperWrite := graphEdge(t, graph, "resource_write", "step:deliverMessages", "resource:attribute:ReleaseIDs", "Set")
	require.Equal(t, "helper", helperWrite.Metadata["via"])
	require.Equal(t, "recordRelease", helperWrite.Metadata["function"])
	require.Equal(t, sourceLine(t, source, "recordRelease(ctx, ReleaseIDs, recipient)"), helperWrite.Span.StartLine)
	require.True(t, hasEdge(graph.Edges, "resource_read", "resource:channel:Replies", "step:deliverMessages"))
	require.True(t, hasEdge(graph.Edges, "resource_read", "resource:channel:Retries", "step:drainRetries"))

	pollDecisions := make([]string, 0)
	for _, node := range nodesOfKind(graph.Nodes, "decision") {
		if node.ParentID == "step:pollRetries" {
			pollDecisions = append(pollDecisions, node.Name)
		}
	}
	sort.Strings(pollDecisions)
	require.Equal(t, []string{"goTo", "gracefulComplete"}, pollDecisions)
}

func stateLoadsFixture(t *testing.T, relativePath string) string {
	t.Helper()
	return filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/visualization-state-loads", relativePath)
}

func sourceLine(t *testing.T, path string, fragment string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for index, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, fragment) {
			return index + 1
		}
	}
	require.Failf(t, "source fragment not found", "%q in %s", fragment, path)
	return 0
}

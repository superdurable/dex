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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeScansEveryTypeScriptExampleFlowSource(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	t.Setenv("NODE_PATH", filepath.Join(repositoryRoot, "web", "node_modules"))
	sources := typeScriptExampleFlowSources(t, repositoryRoot)
	require.NotEmpty(t, sources)
	// These Execute methods return a decision computed by a helper method.
	hiddenDecisionSources := map[string]bool{
		"examples/typescript/src/patterns/cron/cron-schedule-flow.ts": true,
	}
	for _, source := range sources {
		relative, err := filepath.Rel(repositoryRoot, source)
		require.NoError(t, err)
		t.Run(filepath.ToSlash(relative), func(t *testing.T) {
			contents, err := os.ReadFile(source)
			require.NoError(t, err)
			graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			require.Equal(t, "1.0", graph.SchemaVersion)
			again, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
			require.NoError(t, err)
			first, err := json.Marshal(graph)
			require.NoError(t, err)
			second, err := json.Marshal(again)
			require.NoError(t, err)
			require.Equal(t, string(first), string(second))
			hasUnsupportedFeature := false
			hasHiddenDecision := false
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Severity != "error" {
					continue
				}
				switch diagnostic.Code {
				case "typescript_wait_for_unsupported":
					require.Contains(t, string(contents), "waitFor(")
				case "multiple_flows":
					require.Greater(t, strings.Count(string(contents), "getSteps("), 1)
				case "hidden_dex_decision":
					require.True(t, hiddenDecisionSources[filepath.ToSlash(relative)], diagnostic.Message)
					hasHiddenDecision = true
				default:
					require.Failf(t, "unexpected TypeScript diagnostic", "%s: %s", diagnostic.Code, diagnostic.Message)
				}
				hasUnsupportedFeature = true
			}
			require.Equal(t, hiddenDecisionSources[filepath.ToSlash(relative)], hasHiddenDecision)
			require.Equal(t, !hasUnsupportedFeature, graph.Valid, graph.Diagnostics)
		})
	}
}

func TestVisualizeTypeScriptExampleDecisions(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	t.Setenv("NODE_PATH", filepath.Join(repositoryRoot, "web", "node_modules"))

	retry, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, "examples/typescript/src/primitives/step/retry-flow.ts"), flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, retry.Valid, retry.Diagnostics)
	require.Equal(t, "RetryFlow", retry.Flow.Name)
	require.Equal(t, "step:RetryExecute", retry.Flow.StartStepID)
	require.Equal(t, []string{"gracefulComplete"}, decisionTypes(retry, "step:RetryExecute"))

	transfer, err := flowviz.Analyze(context.Background(), filepath.Join(repositoryRoot, "examples/typescript/src/products/money-transfer/money-transfer-flow.ts"), flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, transfer.Valid, transfer.Diagnostics)
	require.Equal(t, []string{"forceFail", "goTo"}, decisionTypes(transfer, "step:CheckBalance"))
	require.True(t, hasDecisionEdge(transfer, "step:CheckBalance", "transition", "step:CreateDebitMemo"))
	require.Equal(t, []string{"gracefulComplete"}, decisionTypes(transfer, "step:Credit"))
	for _, step := range []string{"step:CreateDebitMemo", "step:Debit", "step:CreateCreditMemo", "step:Credit"} {
		require.True(t, hasEdge(transfer.Edges, "failure_transition", step, "step:Compensate"), step)
	}
	for _, diagnostic := range transfer.Diagnostics {
		require.NotEqual(t, "unreachable_step", diagnostic.Code, diagnostic.Message)
	}
}

func decisionTypes(graph *flowviz.Graph, stepID string) []string {
	types := make([]string, 0)
	for _, node := range graph.Nodes {
		if node.Kind == "decision" && node.ParentID == stepID {
			types = append(types, node.Decision.Type)
		}
	}
	return types
}

func hasDecisionEdge(graph *flowviz.Graph, stepID string, kind string, target string) bool {
	for _, node := range graph.Nodes {
		if node.Kind != "decision" || node.ParentID != stepID {
			continue
		}
		if hasEdge(graph.Edges, kind, node.ID, target) {
			return true
		}
	}
	return false
}

func typeScriptExampleFlowSources(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	sources := make([]string, 0)
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "examples/typescript/src"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".ts" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents), "getSteps(") {
			sources = append(sources, path)
		}
		return nil
	})
	require.NoError(t, err)
	return sources
}

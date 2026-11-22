// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/agents/go/core/agentstest"
)

// flowGraph is the part of a dexcli visualize Flow Definition Graph that this test reads.
type flowGraph struct {
	Edges []struct {
		Kind string `json:"kind"`
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"edges"`
	Diagnostics []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"diagnostics"`
}

// dexcli visualize, the Flow audit tool, draws every move to a tool Step and finds every Step reachable.
func TestTheSessionFlowGraphShowsEveryToolStepTransition(t *testing.T) {
	t.Parallel()
	dexcli, err := agentstest.ResolveDexcliPath()
	require.NoError(t, err)
	source, err := filepath.Abs(filepath.Join("..", "session_flow.go"))
	require.NoError(t, err)
	outputPrefix := filepath.Join(t.TempDir(), "session-flow")
	command := exec.CommandContext(context.Background(), dexcli, "visualize", source, "--json", "--out", outputPrefix)
	// The analyser reports diagnostics that do not apply to a library Flow, so it exits non-zero.
	output, _ := command.CombinedOutput()
	text, err := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, err, string(output))
	var graph flowGraph
	require.NoError(t, json.Unmarshal(text, &graph))

	decisionPosition := regexp.MustCompile(`:\d+:\d+$`)
	transitions := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.Kind == "transition" {
			transitions[decisionPosition.ReplaceAllString(edge.From, "")+" -> "+edge.To] = true
		}
	}
	for _, expected := range []string{
		"decision:step:RunModel -> step:AwaitToolApproval",
		"decision:step:RunModel -> step:CallTool",
		"decision:step:RunModel -> step:CallSingleAttemptTool",
		"decision:step:RunModel -> step:ResumeTurn",
		"decision:step:RunModel -> step:RunModel",
		"decision:step:AwaitToolApproval -> step:CallTool",
		"decision:step:AwaitToolApproval -> step:CallSingleAttemptTool",
	} {
		require.True(t, transitions[expected], "missing transition %s", expected)
	}
	for _, diagnostic := range graph.Diagnostics {
		require.NotEqual(t, "unreachable_step", diagnostic.Code, diagnostic.Message)
	}
}

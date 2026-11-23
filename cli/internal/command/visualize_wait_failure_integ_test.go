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
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

// A Step whose WaitFor only fails gets a wait without conditions, which Dex Web must accept.
func TestVisualizeWaitFailureGraphLoadsInDexWeb(t *testing.T) {
	source := stateLoadsFixture(t, "waitfailure/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, graph.Diagnostics)
	encoded, err := flowviz.MarshalJSON(graph)
	require.NoError(t, err)
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, encoded))
	require.Contains(t, compact.String(), `"wait":{"type":"failure","conditions":[]}`)
	require.NotContains(t, string(encoded), "null")
	// NewFlowRenderingServer validates the graph as Dex Web does and fails on a rejected one.
	_, err = web.NewFlowRenderingServer(&web.Config{BindAddress: "127.0.0.1"}, encoded, assets.Files)
	require.NoError(t, err, string(encoded))
}

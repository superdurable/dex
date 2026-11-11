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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

// A Step whose WaitFor only fails gets a wait without conditions, which Dex Web must accept.
func TestVisualizeWaitFailureGraphLoadsInDexWeb(t *testing.T) {
	source := stateLoadsFixture(t, "waitfailure/workflow.go")
	// Schema 1.0 suffices: Dex Web checks graph elements the same way for both versions.
	for _, schemaVersion := range []string{flowviz.SchemaVersionV1} {
		t.Run(schemaVersion, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{SchemaVersion: schemaVersion})
			require.NoError(t, err)
			encoded, err := json.Marshal(graph)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"wait":{"type":"failure","conditions":[]}`)
			require.NotContains(t, string(encoded), "null")
			// NewFlowRenderingServer validates the graph as Dex Web does and fails on a rejected one.
			_, err = web.NewFlowRenderingServer(&web.Config{BindAddress: "127.0.0.1"}, encoded, assets.Files)
			require.NoError(t, err, strings.TrimSpace(string(encoded)))
		})
	}
}

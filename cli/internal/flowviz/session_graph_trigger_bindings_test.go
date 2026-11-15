// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Dex Agents session graph commands write the same Trigger bindings that this analyzer
// derives from code. The suite file pins both outputs to one set of entries.
const sessionGraphTriggerBindingsSource = `import { StepList, forceComplete, type Context, type Flow, type Step, type StepDecision } from "@superdurable/dex";
import { defineReplyCreatedTriggerBinding, defineThreadCreatedTriggerBinding } from "@dex-test/messages";

// dex:group group-id:session group-label:"Session"
// dex:explanation text:"Complete the Flow."
class FinishStep implements Step<unknown> {
  public getStepType(): string {
    return "FinishStep";
  }

  public execute(_context: Context, _input: unknown): StepDecision {
    return forceComplete();
  }
}

export class MessagesFlow implements Flow<unknown> {
  private readonly finish = new FinishStep();

  public getFlowType(): string {
    return "MessagesFlow";
  }

  public getSteps() {
    return StepList.startStep(this.finish);
  }

  public getConnectorTriggerBindings() {
    return [
      defineReplyCreatedTriggerBinding({connectionName: "workspace", bindingName: "session-replies"}),
      defineThreadCreatedTriggerBinding({
        connectionName: "workspace",
        bindingName: "session-threads",
        configurationUI: {units: [{id: "keyword", unitId: "textInput", label: "Keyword", bindings: [{port: "text", jsonPointer: "/threadTriggerMatcher/messageContains"}]}]},
      }),
    ];
  }
}
`

func TestSessionGraphTriggerBindingsMatchTypeScriptAnalysis(t *testing.T) {
	suitePath := filepath.Join("..", "..", "..", "agents", "conformance", "session_graph_trigger_bindings.json")
	suiteContents, err := os.ReadFile(suitePath)
	require.NoError(t, err)
	var suite struct {
		PackageDirectory string `json:"package_directory"`
		Cases            []struct {
			Binding json.RawMessage `json:"binding"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(suiteContents, &suite))

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Setenv("NODE_PATH", filepath.Join(root, "web", "node_modules"))
	directory := t.TempDir()
	packageDirectory := filepath.Join(directory, "node_modules", "@dex-test", "messages")
	require.NoError(t, os.MkdirAll(filepath.Dir(packageDirectory), 0o755))
	require.NoError(t, os.CopyFS(packageDirectory, os.DirFS(filepath.Join(filepath.Dir(suitePath), suite.PackageDirectory))))
	sourcePath := filepath.Join(directory, "messages.ts")
	require.NoError(t, os.WriteFile(sourcePath, []byte(sessionGraphTriggerBindingsSource), 0o644))

	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.NotNil(t, graph.V2)
	require.Len(t, graph.V2.ConnectorTriggerBindings, len(suite.Cases))
	for index, binding := range graph.V2.ConnectorTriggerBindings {
		var analyzed bytes.Buffer
		encoder := json.NewEncoder(&analyzed)
		encoder.SetEscapeHTML(false)
		require.NoError(t, encoder.Encode(binding))
		var expected bytes.Buffer
		require.NoError(t, json.Compact(&expected, suite.Cases[index].Binding))
		require.Equal(t, expected.String(), string(bytes.TrimSuffix(analyzed.Bytes(), []byte("\n"))), "case %d", index)
	}
}

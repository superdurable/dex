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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeSelectsRegisteredFlowType(t *testing.T) {
	temporaryDirectory := t.TempDir()
	pythonPath := filepath.Join(temporaryDirectory, "workflow.py")
	require.NoError(t, os.WriteFile(pythonPath, []byte(pythonFlowSelectionSource), 0o600))
	typeScriptPath := filepath.Join(temporaryDirectory, "workflow.ts")
	require.NoError(t, os.WriteFile(typeScriptPath, []byte(typeScriptFlowSelectionSource), 0o600))
	t.Setenv("NODE_PATH", filepath.Join(visualizerRepositoryRoot(t), "web", "node_modules"))

	for _, source := range []struct {
		language   string
		path       string
		startSteps []string
	}{
		{"go", stateLoadsFixture(t, "flow-selection/workflow.go"), []string{"step:firstStep", "step:secondStep"}},
		{"python", pythonPath, []string{"step:FirstStep", "step:SecondStep"}},
		{"typescript", typeScriptPath, []string{"step:FirstStep", "step:SecondStep"}},
	} {
		t.Run(source.language, func(t *testing.T) {
			for index, flowType := range []string{"OrdinaryFlow", "RegisteredFlow"} {
				graph, err := runVisualizeFlowSelection(t, source.path, "--flow", flowType)
				require.NoError(t, err, graph.Diagnostics)
				require.Equal(t, "2.0", graph.SchemaVersion)
				require.True(t, graph.Valid, graph.Diagnostics)
				require.Equal(t, flowType, graph.Flow.Name)
				require.Equal(t, source.startSteps[index], graph.Flow.StartStepID)
				require.Len(t, nodesOfKind(graph.Nodes, "step"), 1)
			}

			graph, err := runVisualizeFlowSelection(t, source.path)
			require.Error(t, err)
			require.False(t, graph.Valid)
			require.Len(t, graph.Diagnostics, 1)
			require.Equal(t, "multiple_flows", graph.Diagnostics[0].Code)
			require.Contains(t, graph.Diagnostics[0].Message, "OrdinaryFlow, RegisteredFlow")

			graph, err = runVisualizeFlowSelection(t, source.path, "--flow", "RenamedFlow")
			require.Error(t, err)
			require.False(t, graph.Valid)
			require.Len(t, graph.Diagnostics, 1)
			require.Equal(t, "flow_not_found", graph.Diagnostics[0].Code)
			require.Contains(t, graph.Diagnostics[0].Message, "OrdinaryFlow, RegisteredFlow")
		})
	}
}

func TestVisualizeRejectsUnknownTypeForSingleFlow(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "workflow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(minimalPythonFlow), 0o600))
	graph, err := runVisualizeFlowSelection(t, sourcePath, "--flow", "PythonFlow")
	require.NoError(t, err, graph.Diagnostics)
	require.Equal(t, "PythonFlow", graph.Flow.Name)

	graph, err = runVisualizeFlowSelection(t, sourcePath, "--flow", "MissingFlow")
	require.Error(t, err)
	require.False(t, graph.Valid)
	require.Len(t, graph.Diagnostics, 1)
	require.Equal(t, "flow_not_found", graph.Diagnostics[0].Code)
	require.Contains(t, graph.Diagnostics[0].Message, "available Flow types: PythonFlow")
}

func runVisualizeFlowSelection(t *testing.T, sourcePath string, flags ...string) (flowviz.Graph, error) {
	t.Helper()
	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	err := app.Execute(context.Background(), append([]string{"visualize", sourcePath, "--json"}, flags...))
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph), "%v", err)
	return graph, err
}

const pythonFlowSelectionSource = `from dex import Flow, Step, StepList, graceful_complete

# dex:group group-id:"first" group-label:"First"
# dex:explanation text:"Completes the ordinary Flow."
class FirstStep(Step[None]):
    def execute(self, context, input):
        return graceful_complete()

# dex:group group-id:"second" group-label:"Second"
# dex:explanation text:"Completes the renamed Flow."
class SecondStep(Step[None]):
    def execute(self, context, input):
        return graceful_complete()

class OrdinaryFlow(Flow[None]):
    def get_steps(self):
        return StepList.start_step(FirstStep())

class RenamedFlow(Flow[None]):
    def get_flow_type(self):
        return "RegisteredFlow"

    def get_steps(self):
        return StepList.start_step(SecondStep())
`

const typeScriptFlowSelectionSource = `import { StepList, gracefulComplete, voidCodec } from "@superdurable/dex";

// dex:group group-id:"first" group-label:"First"
// dex:explanation text:"Completes the ordinary Flow."
class FirstStep {
  readonly inputCodec = voidCodec;
  execute() { return gracefulComplete(); }
}

// dex:group group-id:"second" group-label:"Second"
// dex:explanation text:"Completes the renamed Flow."
class SecondStep {
  readonly inputCodec = voidCodec;
  execute() { return gracefulComplete(); }
}

class OrdinaryFlow {
  getSteps() { return StepList.startStep(new FirstStep()); }
}

class RenamedFlow {
  private readonly registeredType = "RegisteredFlow";
  getFlowType() { return this.registeredType; }
  getSteps() { return StepList.startStep(new SecondStep()); }
}
`

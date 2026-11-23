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
				require.NotNil(t, graph.V2.Start)
				require.Equal(t, strings.TrimPrefix(source.startSteps[index], "step:"), graph.V2.Start.StepType)
				require.Equal(t, "null", graph.V2.Start.Input.Kind)
				require.Len(t, nodesOfKind(graph.Nodes, "step"), 1)
				prefix := []string{"first", "second"}[index]
				require.Len(t, nodesOfKind(graph.Nodes, "attribute"), 2)
				require.Equal(t, []string{prefix + "Indexed", prefix + "State"}, flowResourceNames(graph))
				require.Len(t, graph.Diagnostics, 1)
				require.Equal(t, "unused_resource", graph.Diagnostics[0].Code)
				require.Contains(t, graph.Diagnostics[0].Message, prefix+"Indexed")
				require.Len(t, graph.V2.IndexedAttributes, 1)
				require.Equal(t, prefix+"Indexed", graph.V2.IndexedAttributes[0].AttributeKey)
				if index == 1 {
					require.Len(t, edgesOfKind(graph.Edges, "resource_lock"), 1)
				}
			}

			graph, err := runVisualizeFlowSelection(t, source.path)
			require.Error(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, graph.Diagnostics, flowviz.Diagnostic{
				Severity: "error",
				Code:     "multiple_flows",
				Message:  "source defines multiple Flows; use --flow with one of: OrdinaryFlow, RegisteredFlow",
			})

			graph, err = runVisualizeFlowSelection(t, source.path, "--flow", "RenamedFlow")
			require.Error(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, graph.Diagnostics, flowviz.Diagnostic{
				Severity: "error",
				Code:     "flow_not_found",
				Message:  "Flow type \"RenamedFlow\" was not found; available Flow types: OrdinaryFlow, RegisteredFlow",
			})
		})
	}
}

func TestVisualizeSelectsCancellationFlowResources(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	t.Setenv("NODE_PATH", filepath.Join(repositoryRoot, "web", "node_modules"))
	for _, source := range []string{
		"examples/go/patterns/workflow-patterns/cancellation.go",
		"examples/python/dex_examples/patterns/workflow_patterns/cancellation.py",
		"examples/typescript/src/patterns/workflow-patterns/cancellation.ts",
	} {
		t.Run(source, func(t *testing.T) {
			graph, err := runVisualizeFlowSelection(t, filepath.Join(repositoryRoot, source), "--flow", "CancelTaskFlow")
			require.Equal(t, 1, ExitCode(err))
			require.Equal(t, "CancelTaskFlow", graph.Flow.Name)
			require.Equal(t, []string{"courierArrived"}, flowResourceNames(graph))
			for _, diagnostic := range graph.Diagnostics {
				require.NotEqual(t, "unused_resource", diagnostic.Code, diagnostic.Message)
			}
		})
	}
}

func flowResourceNames(graph flowviz.Graph) []string {
	var names []string
	for _, node := range graph.Nodes {
		if node.Resource != nil {
			names = append(names, node.Name)
		}
	}
	return names
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

const pythonFlowSelectionSource = `from dex import Attribute, AttributeIndex, Flow, IndexType, PersistenceSchema, Step, StepList, StepOptions, graceful_complete

FIRST_STATE = Attribute("firstState", str)
SECOND_STATE = Attribute("secondState", str)

# dex:indexed-attribute attribute-key:firstIndexed index-key:firstIndexed index-type:keyword value-type:string description:"First search value."
FIRST_INDEXED = Attribute("firstIndexed", str, index=AttributeIndex(IndexType.KEYWORD))

# dex:indexed-attribute attribute-key:secondIndexed index-key:secondIndexed index-type:keyword value-type:string description:"Second search value."
SECOND_INDEXED = Attribute("secondIndexed", str, index=AttributeIndex(IndexType.KEYWORD))

# dex:group group-id:"first" group-label:"First"
# dex:explanation text:"Completes the ordinary Flow."
class FirstStep(Step[None]):
    def execute(self, context, input: None):
        FIRST_STATE.get(context)
        return graceful_complete()

# dex:group group-id:"second" group-label:"Second"
# dex:explanation text:"Completes the renamed Flow."
class SecondStep(Step[None]):
    def execute(self, context, input: None):
        return graceful_complete()

    def get_step_options(self):
        return StepOptions(execute_lock_attributes=[SECOND_STATE.lock()])

class OrdinaryFlow(Flow[None]):
    def get_steps(self):
        return StepList.start_step(FirstStep())

    def get_persistence_schema(self):
        return PersistenceSchema.of(FIRST_INDEXED)

class RenamedFlow(Flow[None]):
    def get_flow_type(self):
        return "RegisteredFlow"

    def get_steps(self):
        return StepList.start_step(SecondStep())

    def get_persistence_schema(self):
        attributes = (SECOND_INDEXED,)
        return PersistenceSchema(attributes=attributes)

def unowned_name():
    return "unowned"

UNOWNED = Attribute(unowned_name(), str, index=AttributeIndex(IndexType.KEYWORD))
`

const typeScriptFlowSelectionSource = `import { Attribute, IndexType, StepList, gracefulComplete, stringCodec, voidCodec, type Context } from "@superdurable/dex";

const firstState = new Attribute("firstState", stringCodec);
const secondState = new Attribute("secondState", stringCodec);

// dex:indexed-attribute attribute-key:firstIndexed index-key:firstIndexed index-type:keyword value-type:string description:"First search value."
const firstIndexed = new Attribute("firstIndexed", stringCodec, { type: IndexType.KEYWORD });

// dex:indexed-attribute attribute-key:secondIndexed index-key:secondIndexed index-type:keyword value-type:string description:"Second search value."
const secondIndexed = new Attribute("secondIndexed", stringCodec, { type: IndexType.KEYWORD });

// dex:group group-id:"first" group-label:"First"
// dex:explanation text:"Completes the ordinary Flow."
class FirstStep {
  readonly inputCodec = voidCodec;
  execute(context: Context, _input: void) { firstState.get(context); return gracefulComplete(); }
}

// dex:group group-id:"second" group-label:"Second"
// dex:explanation text:"Completes the renamed Flow."
class SecondStep {
  readonly inputCodec = voidCodec;
  execute(_context: Context, _input: void) { return gracefulComplete(); }
  getStepOptions() { return { executeLockAttributes: [secondState.lock()] }; }
}

class OrdinaryFlow {
  getSteps() { return StepList.startStep(new FirstStep()); }
  getPersistenceSchema() { return { attributes: [firstIndexed] }; }
}

class RenamedFlow {
  private readonly registeredType = "RegisteredFlow";
  getFlowType() { return this.registeredType; }
  getSteps() { return StepList.startStep(new SecondStep()); }
  getPersistenceSchema() {
    const attributes = [secondIndexed];
    return { attributes };
  }
}

function unownedName() { return "unowned"; }
const unowned = new Attribute(unownedName(), stringCodec, { type: IndexType.KEYWORD });
`

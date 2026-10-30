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

func TestVisualizeScriptStepInstancesGolden(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := stepInstancesFixture(t, language)
			var stdout, stderr bytes.Buffer
			app := NewApp(strings.NewReader(""), &stdout, &stderr)
			require.NoError(t, app.Execute(context.Background(), []string{"visualize", sourcePath, "--schema-version", "2.0", "--json", "--lint", "app"}), stderr.String())
			var graph flowviz.Graph
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
			require.True(t, graph.Valid, graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
			require.Equal(t, "step:First", graph.Flow.StartStepID)
			for _, stepType := range []string{"First", "Second"} {
				require.True(t, hasDecisionEdge(&graph, "step:"+stepType, "transition", "step:First"))
				require.True(t, hasDecisionEdge(&graph, "step:"+stepType, "transition", "step:Second"))
				require.True(t, hasDecisionEdge(&graph, "step:"+stepType, "cancel", "step:First"))
				require.True(t, hasDecisionEdge(&graph, "step:"+stepType, "cancel", "step:Second"))
			}
			require.True(t, hasEdge(graph.Edges, "failure_transition", "step:First", "step:Second"))
			require.True(t, hasEdge(graph.Edges, "failure_transition", "step:Second", "step:First"))
			graph.Source.Path = filepath.Base(sourcePath)
			actual, err := flowviz.MarshalJSON(&graph)
			require.NoError(t, err)
			goldenPath := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/flowviz/testdata", language, "step_instances.golden.json")
			if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
				require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
			}
			expected, err := os.ReadFile(goldenPath)
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func TestVisualizeScriptStepInstancesRejectDynamicTargets(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, scenario := range []string{"dynamic type", "dynamic reference", "unregistered reference", "duplicate type", "hidden decision", "ambiguous class", "mutable target", "dynamic field", "reference registration"} {
			t.Run(language+"/"+scenario, func(t *testing.T) {
				sourcePath := stepInstancesFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				replacements := map[string][2]string{
					"dynamic type":           {`"First", StepRef("Second")`, `read_step_type(), StepRef("Second")`},
					"dynamic reference":      {`go_to(StepRef("Second"), step_input)`, `go_to(StepRef(read_step_type()), step_input)`},
					"unregistered reference": {`go_to(StepRef("Second"), step_input)`, `go_to(StepRef("Missing"), step_input)`},
					"duplicate type":         {`"Second", FIRST`, `"First", FIRST`},
					"hidden decision":        {`return go_to(destination, step_input)`, `return choose_decision(destination)`},
					"ambiguous class":        {`go_to(StepRef("Second"), step_input)`, `go_to(RoutedStep, step_input)`},
					"mutable target":         {"destination = self.target", "destination = self.target\n        destination = read_target()"},
					"dynamic field":          {"self.step_type = step_type", "if read_flag():\n            self.step_type = step_type"},
					"reference registration": {"start_step(first)", `start_step(StepRef("First"))`},
				}
				if language == "typescript" {
					replacements = map[string][2]string{
						"dynamic type":           {`"First", new StepRef("Second")`, `readStepType(), new StepRef("Second")`},
						"dynamic reference":      {`goTo(new StepRef("Second"), input)`, `goTo(new StepRef(readStepType()), input)`},
						"unregistered reference": {`goTo(new StepRef("Second"), input)`, `goTo(new StepRef("Missing"), input)`},
						"duplicate type":         {`"Second", first`, `"First", first`},
						"hidden decision":        {`return goTo(destination, input)`, `return chooseDecision(destination)`},
						"ambiguous class":        {`goTo(new StepRef("Second"), input)`, `goTo(RoutedStep, input)`},
						"mutable target":         {"const destination = this.target;", "let destination = this.target; destination = readTarget();"},
						"dynamic field":          {"return this.stepType;", "return readFlag() ? this.stepType : readStepType();"},
						"reference registration": {"startStep(start)", `startStep(new StepRef("First"))`},
					}
				}
				replacement := replacements[scenario]
				modified := strings.Replace(string(source), replacement[0], replacement[1], 1)
				if language == "python" && scenario == "hidden decision" {
					modified = strings.ReplaceAll(modified, "return go_to(", "return choose_decision(")
					modified = strings.ReplaceAll(modified, "return go_to_many(", "return choose_decision(")
					modified = strings.ReplaceAll(modified, "return graceful_complete()", "return choose_decision()")
				}
				require.NotEqual(t, string(source), modified)
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				expectedCode := map[string]string{"dynamic type": "dynamic_type_name", "dynamic reference": "unknown_step_target", "unregistered reference": "unknown_step_target", "duplicate type": "duplicate_step_type", "hidden decision": "hidden_dex_decision", "ambiguous class": "unknown_step_target", "mutable target": "unknown_step_target", "dynamic field": "dynamic_type_name", "reference registration": "dynamic_step_target"}[scenario]
				require.Contains(t, diagnosticCodes(graph.Diagnostics), expectedCode, graph.Diagnostics)
			})
		}
	}
}

func TestVisualizeScriptStepInstanceStaticNames(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, scenario := range []string{"literal return", "class attribute", "flow constructor", "constructor arguments", "single class target"} {
			t.Run(language+"/"+scenario, func(t *testing.T) {
				sourcePath := stepInstancesFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				modified := string(source)
				replacements := map[string][2]string{
					"literal return":        {"return self.step_type", `return "First"`},
					"class attribute":       {"return self.step_type", `return RoutedStep.STEP_TYPE`},
					"flow constructor":      {`    second = RoutedStep[str]("Second", FIRST)`, "    def __init__(self):\n        self.second = RoutedStep[str](\"Second\", FIRST)"},
					"constructor arguments": {`RoutedStep[int]("First", StepRef("Second"))`, `RoutedStep[int](step_type="First", target=StepRef("Second"))`},
					"single class target":   {`go_to(StepRef("Second"), step_input)`, `go_to(RoutedStep, step_input)`},
				}
				if language == "typescript" {
					replacements = map[string][2]string{
						"literal return":        {"return this.stepType", `return "First"`},
						"class attribute":       {"return this.stepType", `return RoutedStep.stepTypeName`},
						"flow constructor":      {`  private readonly second = new RoutedStep<string>("Second", first);`, "  private readonly second: RoutedStep<string>;\n  constructor() { this.second = new RoutedStep<string>(\"Second\", first); }"},
						"constructor arguments": {"constructor(private readonly stepType: string, private readonly target: StepRef | RoutedStep<unknown>) {}", "private readonly stepType: string;\n  private readonly target: StepRef | RoutedStep<unknown>;\n  constructor(stepType: string, target: StepRef | RoutedStep<unknown>) { this.stepType = stepType; this.target = target; }"},
						"single class target":   {`goTo(new StepRef("Second"), input)`, `goTo(RoutedStep, input)`},
					}
				}
				replacement := replacements[scenario]
				modified = strings.Replace(modified, replacement[0], replacement[1], 1)
				require.NotEqual(t, string(source), modified)
				if scenario == "literal return" || scenario == "class attribute" || scenario == "single class target" {
					modified = strings.ReplaceAll(modified, `"Second"`, `"First"`)
					modified = strings.ReplaceAll(modified, ".other_steps(self.second)", "")
					modified = strings.ReplaceAll(modified, ".otherSteps(this.second)", "")
				}
				if scenario == "class attribute" {
					if language == "python" {
						modified = strings.Replace(modified, "    def get_step_type(self):", "    STEP_TYPE = \"First\"\n\n    def get_step_type(self):", 1)
					} else {
						modified = strings.Replace(modified, "  getStepType(): string {", "  static readonly stepTypeName = \"First\";\n\n  getStepType(): string {", 1)
					}
				}
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
					graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{SchemaVersion: schemaVersion})
					require.NoError(t, err)
					require.True(t, graph.Valid, graph.Diagnostics)
					require.Equal(t, "step:First", graph.Flow.StartStepID)
				}
			})
		}
	}
}

func TestVisualizeScriptTwoConnectorInstances(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := stepInstancesFixture(t, language)
			extension := filepath.Ext(sourcePath)
			sourcePath = filepath.Join(filepath.Dir(sourcePath), "workflow"+extension)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			declarationStart, declarationEnd := "        self.lookup =", "\n\n    def get_steps"
			originalName, newName := "self.lookup", "self.second_lookup"
			registration, expandedRegistration := "other_steps(self.finish)", "other_steps(self.finish, self.second_lookup)"
			if language == "typescript" {
				declarationStart, declarationEnd = "  private readonly lookup =", "\n\n  public getFlowType"
				originalName, newName = "readonly lookup", "readonly secondLookup"
				registration, expandedRegistration = "otherSteps(this.finish)", "otherSteps(this.finish, this.secondLookup)"
			}
			start := strings.Index(string(source), declarationStart)
			end := strings.Index(string(source), declarationEnd)
			require.Greater(t, end, start)
			second := strings.ReplaceAll(string(source[start:end]), originalName, newName)
			second = strings.ReplaceAll(second, "LookupRecord", "LookupOtherRecord")
			modified := string(source[:end]) + "\n" + second + string(source[end:])
			modified = strings.Replace(modified, registration, expandedRegistration, 1)
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, graph.Diagnostics)
			connectorTypes := []string{}
			for _, node := range graph.Nodes {
				if node.Kind == "step" && node.Metadata["connector"] != nil {
					connectorTypes = append(connectorTypes, node.Name)
				}
			}
			require.ElementsMatch(t, []string{"LookupRecord", "LookupOtherRecord"}, connectorTypes)
		})
	}
}

func stepInstancesFixture(t *testing.T, language string) string {
	t.Helper()
	repositoryRoot := visualizerRepositoryRoot(t)
	t.Setenv("NODE_PATH", filepath.Join(repositoryRoot, "web", "node_modules"))
	directory := t.TempDir()
	require.NoError(t, os.CopyFS(directory, os.DirFS(filepath.Join(repositoryRoot, "cli/internal/flowviz/testdata", language))))
	extension := ".py"
	if language == "typescript" {
		extension = ".ts"
		require.NoError(t, os.Rename(filepath.Join(directory, "node-packages"), filepath.Join(directory, "node_modules")))
	}
	return filepath.Join(directory, "step_instances"+extension)
}

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
			require.True(t, hasEdge(graph.Edges, "failure_transition", "step:First", "step:Recovery"))
			require.True(t, hasEdge(graph.Edges, "failure_transition", "step:Second", "step:Recovery"))
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
		for _, scenario := range []string{"dynamic type", "dynamic reference", "unregistered reference", "duplicate type", "hidden decision", "ambiguous class", "mutable target", "dynamic field", "reference registration", "inline instance", "unregistered instance"} {
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
					"inline instance":        {`go_to(StepRef("Second"), step_input)`, `go_to(RoutedStep[str]("Second", FIRST, RECOVERY), step_input)`},
					"unregistered instance":  {"destination = self.target\n", "destination = RoutedStep[str](\"Second\", FIRST, RECOVERY)\n"},
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
						"inline instance":        {`goTo(new StepRef("Second"), input)`, `goTo(new RoutedStep<string>("Second", first, recovery), input)`},
						"unregistered instance":  {"const destination = this.target;", `const destination = new RoutedStep<string>("Second", first, recovery);`},
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
				expectedCode := map[string]string{"dynamic type": "dynamic_type_name", "dynamic reference": "unknown_step_target", "unregistered reference": "unknown_step_target", "duplicate type": "duplicate_step_type", "hidden decision": "hidden_dex_decision", "ambiguous class": "unknown_step_target", "mutable target": "unknown_step_target", "dynamic field": "dynamic_type_name", "reference registration": "dynamic_step_target", "inline instance": "unknown_step_target", "unregistered instance": "unknown_step_target"}[scenario]
				require.Contains(t, diagnosticCodes(graph.Diagnostics), expectedCode, graph.Diagnostics)
				if scenario == "inline instance" || scenario == "unregistered instance" {
					require.True(t, hasDiagnosticMessage(graph.Diagnostics, "Step instance is not the registered instance"), graph.Diagnostics)
				}
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
					"flow constructor":      {`    second = RoutedStep[str]("Second", FIRST, StepRef("Recovery"))`, "    def __init__(self) -> None:\n        self.second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))"},
					"constructor arguments": {`RoutedStep[str]("First", StepRef("Second"), RECOVERY)`, `RoutedStep[str](step_type="First", target=StepRef("Second"), recovery=RECOVERY)`},
					"single class target":   {`go_to(StepRef("Second"), step_input)`, `go_to(RoutedStep, step_input)`},
				}
				if language == "typescript" {
					replacements = map[string][2]string{
						"literal return":        {"return this.stepType", `return "First"`},
						"class attribute":       {"return this.stepType", `return RoutedStep.stepTypeName`},
						"flow constructor":      {`  private readonly second = new RoutedStep<string>("Second", first, new StepRef("Recovery"));`, "  private readonly second: RoutedStep<string>;\n  constructor() { this.second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\")); }"},
						"constructor arguments": {"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "private readonly stepType: string;\n  private readonly target: StepTarget<InputT>;\n  private readonly recovery: StepTarget<InputT>;\n  constructor(stepType: string, target: StepTarget<InputT>, recovery: StepTarget<InputT>) { this.stepType = stepType; this.target = target; this.recovery = recovery; }"},
						"single class target":   {`goTo(new StepRef("Second"), input)`, `goTo(RoutedStep, input)`},
					}
				}
				replacement := replacements[scenario]
				modified = strings.Replace(modified, replacement[0], replacement[1], 1)
				require.NotEqual(t, string(source), modified)
				if scenario == "literal return" || scenario == "class attribute" || scenario == "single class target" {
					modified = strings.ReplaceAll(modified, `"Second"`, `"First"`)
					modified = strings.ReplaceAll(modified, ".other_steps(self.second, RECOVERY)", ".other_steps(RECOVERY)")
					modified = strings.ReplaceAll(modified, ".otherSteps(this.second, recovery)", ".otherSteps(recovery)")
				}
				if scenario == "class attribute" {
					if language == "python" {
						modified = strings.Replace(modified, "    def get_step_type(self) -> str:", "    STEP_TYPE = \"First\"\n\n    def get_step_type(self) -> str:", 1)
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

// Each probe changes one binding the analyzer must prove; invalid probes would route differently at runtime.
func TestVisualizeScriptStepInstancesFailClosed(t *testing.T) {
	type probe struct {
		name    string
		isValid bool
		code    string
		edits   [][2]string
	}
	probes := map[string][]probe{
		"python": {
			{name: "unregistered instance same type", isValid: false, code: "unknown_step_target", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nOTHER = RoutedStep[str](\"Second\", StepRef(\"First\"), RECOVERY)"}, {"return go_to(destination, step_input)", "return go_to(OTHER, step_input)"}}},
			{name: "inline instance target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"go_to(StepRef(\"Second\"), step_input)", "go_to(RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\")), step_input)"}}},
			{name: "flow init local shadows module binding", isValid: true, code: "", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n\n    def __init__(self) -> None:\n        FIRST = RoutedStep[str](\"First\", StepRef(\"First\"), RECOVERY)\n        self.unused = FIRST\n"}}},
			{name: "module tuple rebinding", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"\n\nclass StepInstancesFlow", "\nFIRST, _UNUSED = RoutedStep[str](\"Third\", StepRef(\"Second\"), RECOVERY), 0\n\n\nclass StepInstancesFlow"}}},
			{name: "local tuple rebinding", isValid: false, code: "unknown_step_target", edits: [][2]string{{"destination = self.target\n", "destination = self.target\n        destination, _ = StepRef(\"Missing\"), 0\n"}}},
			{name: "local for rebinding", isValid: false, code: "unknown_step_target", edits: [][2]string{{"destination = self.target\n", "destination = self.target\n        for destination in [StepRef(\"Missing\")]:\n            pass\n"}}},
			{name: "local walrus rebinding", isValid: false, code: "unknown_step_target", edits: [][2]string{{"destination = self.target\n", "destination = self.target\n        if (destination := StepRef(\"Missing\")):\n            pass\n"}}},
			{name: "local with rebinding", isValid: false, code: "unknown_step_target", edits: [][2]string{{"destination = self.target\n", "destination = self.target\n        with open(\"x\") as destination:\n            pass\n"}}},
			{name: "constructor parameter rebinding", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"self.step_type = step_type\n", "step_type = step_type + \"X\"\n        self.step_type = step_type\n"}}},
			{name: "constructor parameter literal rebinding", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"self.step_type = step_type\n", "step_type = \"Renamed\"\n        self.step_type = step_type\n"}}},
			{name: "field reassigned in another method", isValid: false, code: "unknown_step_target", edits: [][2]string{{"    def get_step_type(self) -> str:", "    def retarget(self) -> None:\n        self.target = StepRef(\"Missing\")\n\n    def get_step_type(self) -> str:"}, {"destination = self.target\n", "self.retarget()\n        destination = self.target\n"}}},
			{name: "field set through setattr", isValid: false, code: "dynamic_python", edits: [][2]string{{"self.target = target\n", "self.target = target\n        setattr(self, \"step_type\", \"Other\")\n"}}},
			{name: "field mutated from module", isValid: false, code: "unknown_step_target", edits: [][2]string{{"\n\nclass StepInstancesFlow", "\nFIRST.target = StepRef(\"Missing\")\n\n\nclass StepInstancesFlow"}}},
			{name: "subclass inherits get_step_type", isValid: false, code: "step_inheritance_unsupported", edits: [][2]string{{"FIRST = RoutedStep[str](", "class SpecialStep(RoutedStep[str]):\n    pass\n\n\nFIRST = SpecialStep("}}},
			{name: "fields from __dict__ update", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"    def __init__(self, step_type: str, target: StepTarget[InputT], recovery: StepTarget[InputT]) -> None:\n        self.step_type = step_type\n        self.target = target\n        self.recovery = recovery\n", "    step_type = \"Default\"\n    target = None\n    recovery = None\n\n    def __init__(self, **kwargs) -> None:\n        self.__dict__.update(kwargs)\n"}, {"RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)", "RoutedStep[str](step_type=\"First\", target=StepRef(\"Second\"), recovery=RECOVERY)"}, {"RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))", "RoutedStep[str](step_type=\"Second\", target=FIRST, recovery=StepRef(\"Recovery\"))"}}},
			{name: "StepRef alias import", isValid: true, code: "", edits: [][2]string{{"StepRef, StepTarget", "StepRef, StepRef as Ref, StepTarget"}, {"go_to(StepRef(\"Second\"), step_input)", "go_to(Ref(\"Second\"), step_input)"}}},
			{name: "StepRef keyword", isValid: true, code: "", edits: [][2]string{{"go_to(StepRef(\"Second\"), step_input)", "go_to(StepRef(step_type=\"Second\"), step_input)"}}},
			{name: "local class shadows StepRef", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"InputT = TypeVar(\"InputT\")", "InputT = TypeVar(\"InputT\")\n\n\nclass StepRef:\n    def __init__(self, name):\n        self.step_type = name + \"X\""}}},
			{name: "ambiguous class failure target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"StepOptions().on_execute_failure_proceed_to(self.recovery)", "StepOptions().on_execute_failure_proceed_to(RoutedStep)"}}},
			{name: "ambiguous class cancel target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"with_canceling_steps(self.target, StepRef(\"First\"))", "with_canceling_steps(RoutedStep, StepRef(\"First\"))"}}},
			{name: "flow field reassigned in get_steps", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"        first = FIRST\n", "        first = FIRST\n        self.second = RoutedStep[str](\"Third\", FIRST, RECOVERY)\n"}}},
			{name: "flow field set in another flow method", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"    def get_steps(self) -> StepList[str]:", "    def configure(self) -> None:\n        self.second = RoutedStep[str](\"Third\", FIRST, RECOVERY)\n\n    def get_steps(self) -> StepList[str]:\n        self.configure()"}}},
			{name: "module global rebound in function", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"\n\nclass StepInstancesFlow", "\n\ndef rebind() -> None:\n    global FIRST\n    FIRST = RoutedStep[str](\"Third\", StepRef(\"Second\"), RECOVERY)\n\n\nrebind()\n\n\nclass StepInstancesFlow"}}},
			{name: "nested function parameter shadows", isValid: true, code: "", edits: [][2]string{{"destination = self.target\n", "destination = self.target\n\n        def pick(destination):\n            return go_to(destination, step_input)\n"}}},
			{name: "conditional get_step_type literal", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"return self.step_type\n", "if self.target is None:\n            return \"Zero\"\n        return self.step_type\n"}}},
			{name: "dataclass fields", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"class RoutedStep(Step[InputT], Generic[InputT]):\n    def __init__(self, step_type: str, target: StepTarget[InputT], recovery: StepTarget[InputT]) -> None:\n        self.step_type = step_type\n        self.target = target\n        self.recovery = recovery\n", "@dataclass\nclass RoutedStep(Step[InputT], Generic[InputT]):\n    step_type: str = \"Default\"\n    target: object = None\n    recovery: object = None\n"}, {"from typing import", "from dataclasses import dataclass\nfrom typing import"}}},
			{name: "class default without initializer", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"    def __init__(self, step_type: str, target: StepTarget[InputT], recovery: StepTarget[InputT]) -> None:\n        self.step_type = step_type\n        self.target = target\n        self.recovery = recovery\n", "    step_type = \"Default\"\n    target = None\n    recovery = None\n"}}},
			{name: "Step field reads Flow field", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def get_step_type(self) -> str:\n        return self.label"}, {"class StepInstancesFlow(Flow[str]):\n", "class StepInstancesFlow(Flow[str]):\n    label = \"First\"\n"}}},
			{name: "module binding used before definition", isValid: false, code: "unknown_step_target", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", ""}, {"        return StepList.start_step(first).other_steps(self.second, RECOVERY)\n", "        return StepList.start_step(first).other_steps(self.second, RECOVERY)\n\n\nFIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n"}}},
			{name: "module binding defined after Flow", isValid: true, code: "", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", ""}, {"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    def __init__(self) -> None:\n        self.second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n"}, {"        return StepList.start_step(first).other_steps(self.second, RECOVERY)\n", "        return StepList.start_step(first).other_steps(self.second, RECOVERY)\n\n\nFIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n"}}},
			{name: "keyword constructor arguments", isValid: true, code: "", edits: [][2]string{{"RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)", "RoutedStep[str](step_type=\"First\", target=StepRef(\"Second\"), recovery=RECOVERY)"}}},
			{name: "default constructor argument", isValid: true, code: "", edits: [][2]string{{"recovery: StepTarget[InputT]) -> None:", "recovery: StepTarget[InputT] = StepRef(\"Recovery\")) -> None:"}, {"RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)", "RoutedStep[str](\"First\", StepRef(\"Second\"))"}}},
			{name: "class registered instead of instance", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"        first = FIRST\n", "        first = RoutedStep\n"}}},
			{name: "flow field and class field both bound", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n\n    def __init__(self) -> None:\n        self.second = RoutedStep[str](\"Third\", FIRST, RECOVERY)\n"}}},
			{name: "generic alias class target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"go_to(StepRef(\"Second\"), step_input)", "go_to(RoutedStep[str], step_input)"}}},
			{name: "flow field read before assignment", isValid: false, code: "unknown_step_target", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    def __init__(self) -> None:\n        self.second = RoutedStep[str](\"Second\", self.first, StepRef(\"Recovery\"))\n        self.first = FIRST\n"}}},
			{name: "flow field read after assignment", isValid: true, code: "", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    def __init__(self) -> None:\n        self.first = FIRST\n        self.second = RoutedStep[str](\"Second\", self.first, StepRef(\"Recovery\"))\n"}}},
			{name: "mixin base", isValid: false, code: "step_inheritance_unsupported", edits: [][2]string{{"class RoutedStep(Step[InputT], Generic[InputT]):", "class Routing:\n    pass\n\n\nclass RoutedStep(Step[InputT], Routing, Generic[InputT]):"}}},
			{name: "Dex module alias shadowed locally", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar\nimport dex\n"}, {"            return go_to(StepRef(\"Second\"), step_input)", "            dex = type(\"TargetNamespace\", (), {\"StepRef\": lambda step_type: FIRST})\n            return go_to(dex.StepRef(\"Second\"), step_input)"}}},
			{name: "Dex module alias rebound to self", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar\nimport dex\n"}, {"            return go_to(StepRef(\"Second\"), step_input)", "            dex = self\n            return go_to(dex.StepRef(\"Second\"), step_input)"}}},
			{name: "Dex module alias StepRef", isValid: true, code: "", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar\nimport dex\n"}, {"go_to(StepRef(\"Second\"), step_input)", "go_to(dex.StepRef(\"Second\"), step_input)"}}},
			{name: "Dex module attribute assigned", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar\nimport dex\n"}, {"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\ndex.StepRef = lambda step_type: FIRST\n"}}},
			{name: "Dex class attribute assigned", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nStepRef.__new__ = lambda cls, step_type: FIRST\n"}}},
			{name: "wildcard import after Dex import", isValid: false, code: "wildcard_import", edits: [][2]string{{"go_to, go_to_many, graceful_complete\n", "go_to, go_to_many, graceful_complete\nfrom routing_helpers import *\n"}}},
			{name: "module binding written through globals", isValid: false, code: "dynamic_python", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nglobals()[\"FIRST\"] = RECOVERY\n"}}},
			{name: "setattr through builtins", isValid: false, code: "dynamic_python", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar\nimport builtins\n"}, {"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nbuiltins.setattr(FIRST, \"target\", StepRef(\"Missing\"))\n"}}},
			{name: "Step __getattribute__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def __getattribute__(self, name):\n        if name == \"target\":\n            return StepRef(\"First\")\n        return super().__getattribute__(name)\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "Step __getattr__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def __getattr__(self, name):\n        return StepRef(\"First\")\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "Step __class_getitem__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def __class_getitem__(cls, item):\n        return RecoveryStep\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "Step descriptor __get__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def __get__(self, owner, owner_type=None):\n        return RECOVERY\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "Step __new__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    def __new__(cls, *args):\n        return RECOVERY\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "Flow __getattribute__", isValid: false, code: "dynamic_class", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n\n    def __getattribute__(self, name):\n        return object.__getattribute__(self, name)\n"}}},
			{name: "Step method reassigned on instance", isValid: false, code: "dynamic_class", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nFIRST.get_step_type = lambda: \"Other\"\n"}}},
			{name: "Step method reassigned in constructor", isValid: false, code: "dynamic_class", edits: [][2]string{{"        self.recovery = recovery\n", "        self.recovery = recovery\n        self.get_step_type = lambda: \"Other\"\n"}}},
			{name: "Step class reassigned", isValid: false, code: "dynamic_class", edits: [][2]string{{"FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\n", "FIRST = RoutedStep[str](\"First\", StepRef(\"Second\"), RECOVERY)\nFIRST.__class__ = RecoveryStep\n"}}},
			{name: "flow constructor rebinds self", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    def __init__(self) -> None:\n        self = RECOVERY\n        self.second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n"}}},
			{name: "flow constructor passes self to helper", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n\n    def __init__(self) -> None:\n        configure_flow(self)\n"}}},
			{name: "decorated flow", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"class StepInstancesFlow(Flow[str]):", "@replace_flow_fields\nclass StepInstancesFlow(Flow[str]):"}}},
			{name: "Step self escapes to helper", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"        destination = self.target\n", "        record_step(self)\n        destination = self.target\n"}}},
			{name: "flow self passed to local class", isValid: true, code: "", edits: [][2]string{{"\n\nclass StepInstancesFlow", "\n\nclass FlowHolder:\n    def __init__(self, flow: object) -> None:\n        self.flow = flow\n\n\nclass StepInstancesFlow"}, {"    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n", "    second = RoutedStep[str](\"Second\", FIRST, StepRef(\"Recovery\"))\n\n    def __init__(self) -> None:\n        self.holder = FlowHolder(self)\n"}}},
			{name: "decorated Step without field reads", isValid: false, code: "dynamic_class", edits: [][2]string{{"class RecoveryStep(Step[str]):", "@swap_step\nclass RecoveryStep(Step[str]):"}}},
			{name: "decorated Step method", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return \"Recovery\"", "    @swap_method\n    def get_step_type(self) -> str:\n        return \"Recovery\""}}},
			{name: "final Step class", isValid: true, code: "", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from typing import Generic, TypeVar, final\n"}, {"class RecoveryStep(Step[str]):", "@final\nclass RecoveryStep(Step[str]):"}}},
			{name: "dataclass Step class without field reads", isValid: true, code: "", edits: [][2]string{{"from typing import Generic, TypeVar\n", "from dataclasses import dataclass\nfrom typing import Generic, TypeVar\n"}, {"class RecoveryStep(Step[str]):", "@dataclass\nclass RecoveryStep(Step[str]):"}}},
			{name: "unused property", isValid: true, code: "", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def description(self):\n        return \"routing Step\"\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "property read in execute", isValid: true, code: "", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def description(self):\n        return \"routing Step\"\n\n    def get_step_type(self) -> str:\n        return self.step_type"}, {"destination = self.target\n", "description = self.description\n        destination = self.target\n"}}},
			{name: "property shadows routing field", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def target(self):\n        return StepRef(\"First\")\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "property replaces entry method", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "property setter", isValid: false, code: "dynamic_class", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def description(self):\n        return \"routing Step\"\n\n    @description.setter\n    def description(self, value):\n        pass\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "shadowed property decorator", isValid: false, code: "dynamic_class", edits: [][2]string{{"InputT = TypeVar(\"InputT\")\n", "InputT = TypeVar(\"InputT\")\n\n\ndef property(method):\n    return method\n"}, {"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def description(self):\n        return \"routing Step\"\n\n    def get_step_type(self) -> str:\n        return self.step_type"}}},
			{name: "property used as Step target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"    def get_step_type(self) -> str:\n        return self.step_type", "    @property\n    def pick(self):\n        return StepRef(\"First\")\n\n    def get_step_type(self) -> str:\n        return self.step_type"}, {"return go_to(destination, step_input)", "return go_to(self.pick, step_input)"}}},
		},
		"typescript": {
			{name: "unregistered instance same type", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nconst other = new RoutedStep<string>(\"Second\", new StepRef(\"First\"), recovery);"}, {"return goTo(destination, input);", "return goTo(other, input);"}}},
			{name: "inline instance target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"goTo(new StepRef(\"Second\"), input)", "goTo(new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\")), input)"}}},
			{name: "flow constructor local shadows module binding", isValid: true, code: "", edits: [][2]string{{"  getFlowType(): string {", "  constructor() { const first = new RoutedStep<string>(\"First\", new StepRef(\"First\"), recovery); void first; }\n\n  getFlowType(): string {"}}},
			{name: "module let reassigned", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "let first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nfirst = new RoutedStep<string>(\"First\", new StepRef(\"First\"), recovery);"}}},
			{name: "local destructuring reassigned", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const destination = this.target;", "let destination: unknown = this.target; [destination] = [new StepRef(\"Missing\")];"}}},
			{name: "local for-of reassigned", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const destination = this.target;", "let destination: unknown = this.target; for (destination of [new StepRef(\"Missing\")]) {}"}}},
			{name: "nested block shadow", isValid: false, code: "unknown_step_target", edits: [][2]string{{"if (input === \"instance\") return goTo(destination, input);", "if (input === \"instance\") { const destination = new StepRef(\"Missing\"); return goTo(destination, input); }"}}},
			{name: "constructor parameter reassigned", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "private readonly stepType: string;\n  private readonly target: StepTarget<InputT>;\n  private readonly recovery: StepTarget<InputT>;\n  constructor(stepType: string, target: StepTarget<InputT>, recovery: StepTarget<InputT>) { this.recovery = recovery; stepType = stepType + \"X\"; this.stepType = stepType; this.target = target; }"}}},
			{name: "field cast-assigned in another method", isValid: false, code: "unknown_step_target", edits: [][2]string{{"  getStepType(): string {", "  retarget() { (this as any).target = new StepRef(\"Missing\"); }\n\n  getStepType(): string {"}, {"const destination = this.target;", "this.retarget(); const destination = this.target;"}}},
			{name: "field assigned in another method", isValid: false, code: "unknown_step_target", edits: [][2]string{{"private readonly target: StepTarget<InputT>", "private target: StepTarget<InputT>"}, {"  getStepType(): string {", "  retarget() { this.target = new StepRef(\"Missing\"); }\n\n  getStepType(): string {"}, {"const destination = this.target;", "this.retarget(); const destination = this.target;"}}},
			{name: "field initializers with Object.assign", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "stepType = \"Default\";\n  target: any = undefined;\n  recovery: any = undefined;\n  constructor(options: {stepType: string; target: unknown}) { Object.assign(this, options); }"}, {"new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery)", "new RoutedStep<string>({stepType: \"First\", target: new StepRef(\"Second\")})"}, {"new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"))", "new RoutedStep<string>({stepType: \"Second\", target: first})"}}},
			{name: "Object.assign with one instance", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "stepType = \"Default\";\n  target: any = undefined;\n  recovery: any = undefined;\n  constructor(options: {stepType: string; target?: unknown}) { Object.assign(this, options); }"}, {"new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery)", "new RoutedStep<string>({stepType: \"First\"})"}, {"    if (input === \"reference\") return goTo(new StepRef(\"Second\"), input);\n    if (input === \"movement\") return goToMany(StepMovement.of(this.target, input), StepMovement.of(new StepRef(\"First\"), input));\n    return withCancelingSiblingSteps(withCancelingSteps(gracefulComplete(), this.target, new StepRef(\"First\")), new StepRef(\"Second\"));", "    return gracefulComplete();"}, {"return StepList.startStep(start).otherSteps(this.second, recovery);", "return StepList.startStep(start);"}, {"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));\n", ""}, {"    const destination = this.target;\n    if (input === \"instance\") return goTo(destination, input);\n", ""}, {"  getStepOptions() {\n    return {executeFailure: ExecuteFailure.proceedTo(this.recovery)};\n  }\n\n", ""}}},
			{name: "subclass inherits", isValid: false, code: "step_inheritance_unsupported", edits: [][2]string{{"const first = new RoutedStep<string>(", "class SpecialStep extends RoutedStep<string> {}\nconst first = new SpecialStep("}}},
			{name: "ambiguous class target", isValid: false, code: "unknown_step_target", edits: [][2]string{{"goTo(new StepRef(\"Second\"), input)", "goTo(RoutedStep as any, input)"}}},
			{name: "StepRef called without new", isValid: false, code: "unknown_step_target", edits: [][2]string{{"goTo(new StepRef(\"Second\"), input)", "goTo((StepRef as any)(\"Second\"), input)"}}},
			{name: "spread constructor arguments", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery)", "new RoutedStep<string>(...([\"First\", new StepRef(\"Second\")] as const))"}}},
			{name: "getStepType arrow property", isValid: true, code: "", edits: [][2]string{{"  getStepType(): string {\n    return this.stepType;\n  }", "  getStepType = (): string => this.stepType;"}}},
			{name: "flow field reassigned in getSteps", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"    const start = first;", "    const start = first;\n    (this as any).second = new RoutedStep<string>(\"Third\", first, recovery);"}}},
			{name: "flow field and constructor store", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));\n\n  constructor() { this.second = new RoutedStep<string>(\"Third\", first, recovery); }"}}},
			{name: "step field mutated from module", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n(first as any).target = new StepRef(\"Missing\");"}}},
			{name: "class registered instead of instance", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"const start = first;", "const start = RoutedStep as any;"}}},
			{name: "Step field reads Flow field", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"    return this.stepType;", "    return (this as any).label;"}, {"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly label = \"First\";\n  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));"}}},
			{name: "module binding defined after Flow", isValid: true, code: "", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n", ""}, {"    return {};\n  }\n}\n", "    return {};\n  }\n}\n\nconst first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n"}}},
			{name: "module binding used before definition", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n", "const second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));\nconst first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n"}, {"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly second = second;"}}},
			{name: "default constructor argument", isValid: true, code: "", edits: [][2]string{{"private readonly recovery: StepTarget<InputT>) {}", "private readonly recovery: StepTarget<InputT> = new StepRef(\"Recovery\")) {}"}, {"new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery)", "new RoutedStep<string>(\"First\", new StepRef(\"Second\"))"}}},
			{name: "explicit constructor assignments", isValid: true, code: "", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "private readonly stepType: string;\n  private readonly target: StepTarget<InputT>;\n  private readonly recovery: StepTarget<InputT>;\n  constructor(stepType: string, target: StepTarget<InputT>, recovery: StepTarget<InputT>) { this.recovery = recovery; this.stepType = stepType; this.target = target; }"}}},
			{name: "Dex import shadowed locally", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"    const destination = this.target;", "    const goTo = (target: unknown, value: unknown) => gracefulComplete();\n    const destination = this.target;"}}},
			{name: "constructor helper call", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>) { configure(this); }"}, {"const first = new RoutedStep<string>(", "function configure(step: object): void { void step; }\nconst first = new RoutedStep<string>("}}},
			{name: "flow constructor field", isValid: true, code: "", edits: [][2]string{{"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly second: RoutedStep<string>;\n  constructor() { this.second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\")); }"}}},
			{name: "static class field", isValid: true, code: "", edits: [][2]string{{"    return this.stepType;", "    return RoutedStep.firstType;"}, {"  getStepType(): string {", "  static readonly firstType = \"First\";\n\n  getStepType(): string {"}, {"\"Second\"", "\"First\""}, {"\"Second\"", "\"First\""}, {"\"Second\"", "\"First\""}, {"\"Second\"", "\"First\""}, {".otherSteps(this.second, recovery)", ".otherSteps(recovery)"}}},
			{name: "flow field read before declaration", isValid: false, code: "unknown_step_target", edits: [][2]string{{"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly second = new RoutedStep<string>(\"Second\", this.first, new StepRef(\"Recovery\"));\n  private readonly first = first;"}}},
			{name: "flow field read after declaration", isValid: true, code: "", edits: [][2]string{{"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly first = first;\n  private readonly second = new RoutedStep<string>(\"Second\", this.first, new StepRef(\"Recovery\"));"}}},
			{name: "computed key write through this alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const targetOwner = this as any; const targetKey = \"target\"; targetOwner[targetKey] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed key write with unknown key", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n(first as any)[readKey()] = new StepRef(\"First\");"}}},
			{name: "computed literal key through const", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nconst targetKey = \"target\";\n(first as any)[targetKey] = new StepRef(\"Missing\");"}}},
			{name: "computed write on fresh object", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const record: Record<string, unknown> = {}; record[String(input)] = input; const destination = this.target;"}}},
			{name: "computed write on split array", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const words = String(input).split(\",\"); for (let index = 0; index < words.length; index++) words[index] = words[index].trim(); const destination = this.target;"}}},
			{name: "computed write in array helper", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const words = String(input).split(\",\"); trimWords(words); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function trimWords(words: string[]): void { for (let index = 0; index < words.length; index++) words[index] = words[index].trim(); }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write on owned array field", isValid: true, code: "", edits: [][2]string{{"class RoutedStep<InputT> implements Step<InputT> {\n", "class RoutedStep<InputT> implements Step<InputT> {\n  private readonly words: string[] = [];\n"}, {"const destination = this.target;", "this.words[String(input).length] = String(input); const destination = this.target;"}}},
			{name: "computed unknown key through Step alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const owner = first as any; owner[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write in helper given a Step", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "poke(first, String(input)); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function poke(target: any, key: string): void { target[key] = new StepRef(\"First\"); }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write in helper also given a Step", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words = String(input).split(\",\"); trimWords(words); trimWords(first as any); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function trimWords(words: string[]): void { for (let index = 0; index < words.length; index++) words[index] = words[index].trim(); }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write in exported helper", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words = String(input).split(\",\"); trimWords(words); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "export function trimWords(words: string[]): void { for (let index = 0; index < words.length; index++) words[index] = words[index].trim(); }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write on reassigned array field", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"class RoutedStep<InputT> implements Step<InputT> {\n", "class RoutedStep<InputT> implements Step<InputT> {\n  private words: any = [];\n"}, {"const destination = this.target;", "(this as any).words = first; this.words[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write on call result", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "pick()[String(input)] = new StepRef(\"First\"); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function pick(): any { return first; }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write on method result of object literal", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const holder = {split(): any { return first; }}; holder.split()[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write after split is replaced", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "(String.prototype as any).split = () => first; String(input).split(\",\")[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write after String is reassigned", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "(String as any) = (value: unknown) => ({split: () => first}); String(input).split(\",\")[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write after globalThis String is replaced", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "(globalThis as any).String = (value: unknown) => ({split: () => first}); String(input).split(\",\")[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write on array method result", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words: any = []; words[String(input)] = () => first; words.map((word: unknown) => word)[String(input)] = new StepRef(\"First\"); const destination = this.target;"}}},
			{name: "computed write in helper with this parameter", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "poke(first, String(input)); const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function poke(this: void, target: any, key: string): void { target[key] = new StepRef(\"First\"); }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write on mapped array", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const words = String(input).split(\",\").map(word => word.trim()); for (let index = 0; index < words.length; index++) words[index] = words[index].toUpperCase(); const destination = this.target;"}}},
			{name: "computed write on typed row of split rows", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const rows = String(input).split(\";\").map(line => line.split(\",\")); for (const row of rows) for (let index = 0; index < row.length; index++) row[index] = row[index].trim(); const destination = this.target;"}}},
			{name: "computed write through Object constructor alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const targetOwner = new Object(first) as any; const targetKey = String(input) === \"instance\" ? \"target\" : \"unrelated\"; targetOwner[targetKey] = new StepRef(\"Recovery\"); const destination = this.target;"}}},
			{name: "computed string write through Object constructor alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const targetOwner = new Object(first) as any; targetOwner[String(input)] = \"Recovery\"; const destination = this.target;"}}},
			{name: "Object constructor alias cast to record", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const targetOwner = new Object(first) as Record<string, string>; targetOwner[String(input)] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write through double cast", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words = first as unknown as string[]; words[String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write on any result typed as array", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words: string[] = pick(); words[String(input) as any] = \"Recovery\"; const destination = this.target;"}, {"const recovery = new RecoveryStep();", "function pick(): any { return first; }\n\nconst recovery = new RecoveryStep();"}}},
			{name: "computed write on array field written through any", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"class RoutedStep<InputT> implements Step<InputT> {\n", "class RoutedStep<InputT> implements Step<InputT> {\n  private readonly words: string[] = [];\n"}, {"const destination = this.target;", "(this as any).words = first; this.words[String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write after array map is replaced", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "Array.prototype.map = function (): any { return first; }; const words = String(input).split(\",\").map(word => word.trim()); words[String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write that installs a method", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const words: string[] = String(input).split(\",\"); words[String(input)] = (() => first) as any; const mapped = words.map(word => word); mapped[String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write through widened alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const holder = {words: [] as string[]}; const wide: {words: unknown} = holder; wide.words = first; holder.words[String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write through widened array alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "const outer: string[][] = [[]]; const wide: unknown[] = outer; wide[0] = first; outer[0][String(input) as any] = \"Recovery\"; const destination = this.target;"}}},
			{name: "computed write in callback parameter", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "[String(input).split(\",\")].forEach(row => { row[String(input) as any] = \"Recovery\"; }); const destination = this.target;"}}},
			{name: "Reflect.set on Object constructor alias", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nReflect.set(new Object(first), \"target\", new StepRef(\"Missing\"));"}}},
			{name: "Object.assign on Object constructor alias", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nObject.assign(new Object(first), {target: new StepRef(\"Missing\")});"}}},
			{name: "Reflect.set literal key on instance", isValid: false, code: "unknown_step_target", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nReflect.set(first, \"target\", new StepRef(\"Missing\"));"}}},
			{name: "this escapes to helper", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"const destination = this.target;", "recordStep(this); const destination = this.target;"}}},
			{name: "this passed to local class constructor", isValid: true, code: "", edits: [][2]string{{"export class StepInstancesFlow", "class FlowHolder {\n  constructor(readonly flow: unknown) {}\n}\n\nexport class StepInstancesFlow"}, {"  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));", "  private readonly holder = new FlowHolder(this);\n  private readonly second = new RoutedStep<string>(\"Second\", first, new StepRef(\"Recovery\"));"}}},
			{name: "Dex namespace import shadowed locally", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"from \"@superdurable/dex\";\n", "from \"@superdurable/dex\";\nimport * as dex from \"@superdurable/dex\";\n"}, {"if (input === \"reference\") return goTo(new StepRef(\"Second\"), input);", "if (input === \"reference\") { const dex = {StepRef: class { constructor(_stepType: string) { return first as any; } }}; return goTo(new dex.StepRef(\"Second\") as any, input); }"}}},
			{name: "Dex namespace StepRef", isValid: true, code: "", edits: [][2]string{{"from \"@superdurable/dex\";\n", "from \"@superdurable/dex\";\nimport * as dex from \"@superdurable/dex\";\n"}, {"goTo(new StepRef(\"Second\"), input)", "goTo(new dex.StepRef(\"Second\"), input)"}}},
			{name: "Dex namespace member assigned", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"from \"@superdurable/dex\";\n", "from \"@superdurable/dex\";\nimport * as dex from \"@superdurable/dex\";\n"}, {"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n(dex as any).StepRef = class {};"}}},
			{name: "Dex class prototype defined", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\nObject.defineProperty(StepRef.prototype, \"stepType\", {get: () => \"First\"});"}}},
			{name: "function expression shadows Dex import", isValid: false, code: "dex_alias_rebinding", edits: [][2]string{{"const destination = this.target;", "const route = function goTo(target: unknown) { return target; }; void route; const destination = this.target;"}}},
			{name: "eval in Flow source", isValid: false, code: "dynamic_typescript", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\neval(\"0\");"}}},
			{name: "accessor intercepts field", isValid: false, code: "dynamic_type_name", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "constructor(stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) { this.stepType = stepType; }\n\n  get stepType(): string {\n    return \"Other\";\n  }\n\n  set stepType(_value: string) {}"}}},
			{name: "method reassigned on instance", isValid: false, code: "dynamic_class", edits: [][2]string{{"const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);", "const first = new RoutedStep<string>(\"First\", new StepRef(\"Second\"), recovery);\n(first as any).getStepType = () => \"Other\";"}}},
			{name: "method reassigned in constructor", isValid: false, code: "dynamic_class", edits: [][2]string{{"constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}", "constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) { this.getStepType = () => \"Other\"; }"}}},
			{name: "flow constructor returns another object", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"  getFlowType(): string {", "  constructor() { return {} as any; }\n\n  getFlowType(): string {"}}},
			{name: "flow this escapes to helper", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"  getFlowType(): string {", "  constructor() { configureFlow(this); }\n\n  getFlowType(): string {"}}},
			{name: "decorated flow", isValid: false, code: "dynamic_step_target", edits: [][2]string{{"export class StepInstancesFlow", "@sealed\nexport class StepInstancesFlow"}}},
			{name: "object literal method returns its own this", isValid: true, code: "", edits: [][2]string{{"const destination = this.target;", "const chain = {next() { return this; }}; void chain; const destination = this.target;"}}},
			{name: "decorated Step without field reads", isValid: false, code: "dynamic_class", edits: [][2]string{{"class RecoveryStep implements Step<string> {", "@sealStep\nclass RecoveryStep implements Step<string> {"}}},
			{name: "decorated Step method", isValid: false, code: "dynamic_class", edits: [][2]string{{"  getStepType(): string {\n    return \"Recovery\";", "  @swapMethod\n  getStepType(): string {\n    return \"Recovery\";"}}},
		},
	}
	for _, language := range []string{"python", "typescript"} {
		for _, probe := range probes[language] {
			t.Run(language+"/"+probe.name, func(t *testing.T) {
				sourcePath := stepInstancesFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				modified := string(source)
				for _, edit := range probe.edits {
					require.Contains(t, modified, edit[0])
					modified = strings.Replace(modified, edit[0], edit[1], 1)
				}
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
				require.NoError(t, err)
				require.Equal(t, probe.isValid, graph.Valid, graph.Diagnostics)
				if !probe.isValid {
					require.Contains(t, diagnosticCodes(graph.Diagnostics), probe.code, graph.Diagnostics)
					return
				}
				require.Equal(t, "step:First", graph.Flow.StartStepID)
				if hasNode(graph.Nodes, "step:Second") {
					require.True(t, hasEdge(graph.Edges, "failure_transition", "step:First", "step:Recovery"))
					require.True(t, hasEdge(graph.Edges, "failure_transition", "step:Second", "step:Recovery"))
				}
			})
		}
	}
}

func TestVisualizeScriptConnectorStepTargets(t *testing.T) {
	tests := []struct {
		name     string
		language string
		isValid  bool
		code     string
		edits    [][2]string
	}{
		{name: "python StepRef to factory Step", language: "python", isValid: true},
		{name: "python factory Step field", language: "python", isValid: true, edits: [][2]string{
			{"class PrepareSearch(dex.Step[str]):\n", "class PrepareSearch(dex.Step[str]):\n    def __init__(self, search: dex.Step[str]) -> None:\n        self.search = search\n\n"},
			{`dex.go_to(dex.StepRef("SearchCompanyArticles"), prepared)`, "dex.go_to(self.search, prepared)"},
			{"self.prepare = PrepareSearch()", "self.prepare = PrepareSearch(self.search_company_articles)"},
		}},
		{name: "python factory field reassigned", language: "python", code: "dynamic_step_target", edits: [][2]string{
			{"    def get_steps(self)", "    def reset_search(self) -> None:\n        self.search_company_articles = None\n\n    def get_steps(self)"},
		}},
		{name: "python factory field read before assignment", language: "python", code: "unknown_step_target", edits: [][2]string{
			{"class PrepareSearch(dex.Step[str]):\n", "class PrepareSearch(dex.Step[str]):\n    def __init__(self, search: dex.Step[str]) -> None:\n        self.search = search\n\n"},
			{`dex.go_to(dex.StepRef("SearchCompanyArticles"), prepared)`, "dex.go_to(self.search, prepared)"},
			{"        self.prepare = PrepareSearch()\n", ""},
			{"        self.finish = FinishSearch()\n", "        self.finish = FinishSearch()\n        self.prepare = PrepareSearch(self.search_company_articles)\n"},
		}},
		{name: "typescript factory Flow field through Step field", language: "typescript", isValid: true},
		{name: "typescript factory Flow field through Flow", language: "typescript", isValid: true, edits: [][2]string{
			{"constructor(private readonly searchCompanyArticles: Step<string>) {}", "constructor(private readonly flow: CompanyArticlesFlow) {}"},
			{"goTo(this.searchCompanyArticles, prepared)", "goTo(this.flow.searchCompanyArticles, prepared)"},
			{"new PrepareSearch(this.searchCompanyArticles)", "new PrepareSearch(this)"},
		}},
		{name: "typescript factory Flow field reassigned", language: "typescript", code: "dynamic_step_target", edits: [][2]string{
			{"  getFlowType(): string {", "  resetSearch(): void {\n    (this as any).searchCompanyArticles = undefined;\n  }\n\n  getFlowType(): string {"},
		}},
		{name: "typescript factory Flow field read before assignment", language: "typescript", code: "unknown_step_target", edits: [][2]string{
			{"    this.prepare = new PrepareSearch(this.searchCompanyArticles);\n", ""},
			{"  constructor() {\n", "  constructor() {\n    this.prepare = new PrepareSearch(this.searchCompanyArticles);\n"},
		}},
		{name: "typescript factory Flow field read by property initializer", language: "typescript", code: "unknown_step_target", edits: [][2]string{
			{"  private readonly prepare: PrepareSearch;\n", "  private readonly prepare = new PrepareSearch(this.searchCompanyArticles);\n"},
			{"    this.prepare = new PrepareSearch(this.searchCompanyArticles);\n", ""},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := stepInstancesFixture(t, test.language)
			source := pythonCompanyArticlesFlow
			sourcePath = filepath.Join(filepath.Dir(sourcePath), "company_articles.py")
			if test.language == "typescript" {
				source = typeScriptCompanyArticlesFlow
				sourcePath = strings.TrimSuffix(sourcePath, ".py") + ".ts"
			}
			for _, edit := range test.edits {
				require.Contains(t, source, edit[0])
				source = strings.Replace(source, edit[0], edit[1], 1)
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2})
			require.NoError(t, err)
			require.Equal(t, test.isValid, graph.Valid, graph.Diagnostics)
			if !test.isValid {
				require.Contains(t, diagnosticCodes(graph.Diagnostics), test.code, graph.Diagnostics)
				return
			}
			require.Equal(t, "step:PrepareSearch", graph.Flow.StartStepID)
			require.True(t, hasDecisionEdge(graph, "step:PrepareSearch", "transition", "step:SearchCompanyArticles"))
			var connector *flowviz.Node
			for index := range graph.Nodes {
				if graph.Nodes[index].ID == "step:SearchCompanyArticles" {
					connector = &graph.Nodes[index]
				}
			}
			require.NotNil(t, connector)
			require.NotNil(t, connector.Metadata["connector"])
		})
	}
}

const pythonCompanyArticlesFlow = `import dex
from catalog_connector import create_lookup

ARTICLES = dex.Attribute("CompanyArticles", dict)


# dex:group group-id:articles group-label:"Articles"
# dex:explanation text:"Prepare the company article search."
class PrepareSearch(dex.Step[str]):
    def execute(self, context: dex.Context, step_input: str) -> dex.StepDecision:
        prepared = step_input.strip()
        return dex.go_to(dex.StepRef("SearchCompanyArticles"), prepared)


# dex:group group-id:articles group-label:"Articles"
# dex:explanation text:"Complete the Flow after the search."
class FinishSearch(dex.Step[dict]):
    def execute(self, context: dex.Context, step_input: dict) -> dex.StepDecision:
        return dex.force_complete()


class CompanyArticlesFlow(dex.Flow[str]):
    def __init__(self) -> None:
        self.finish = FinishSearch()
        self.search_company_articles = create_lookup(
            step_type="SearchCompanyArticles",
            connection_name="primary",
            annotations={"groupId": "articles", "groupLabel": "Articles", "explanation": "Search company articles."},
            execute_failure=self.finish,
            found=self.finish,
            unavailable=self.finish,
            result_attribute=ARTICLES,
        )
        self.prepare = PrepareSearch()

    def get_steps(self) -> dex.StepList[str]:
        return dex.StepList.start_step(self.prepare).other_steps(self.search_company_articles, self.finish)

    def get_persistence_schema(self) -> dex.PersistenceSchema:
        return dex.PersistenceSchema.of(ARTICLES)
`

const typeScriptCompanyArticlesFlow = `import {StepList, forceComplete, goTo, type Context, type Flow, type PersistenceSchema, type Step, type StepDecision} from "@superdurable/dex";
import {createLookup} from "@dex-test/catalog";

// dex:group group-id:articles group-label:"Articles"
// dex:explanation text:"Complete the Flow after the search."
class FinishSearch implements Step<unknown> {
  getStepType(): string {
    return "FinishSearch";
  }

  execute(_context: Context, _input: unknown): StepDecision {
    return forceComplete();
  }
}

// dex:group group-id:articles group-label:"Articles"
// dex:explanation text:"Prepare the company article search."
class PrepareSearch implements Step<string> {
  constructor(private readonly searchCompanyArticles: Step<string>) {}

  getStepType(): string {
    return "PrepareSearch";
  }

  execute(_context: Context, input: string): StepDecision {
    const prepared = input.trim();
    return goTo(this.searchCompanyArticles, prepared);
  }
}

export class CompanyArticlesFlow implements Flow<string> {
  private readonly finish = new FinishSearch();
  readonly searchCompanyArticles: Step<string>;
  private readonly prepare: PrepareSearch;

  constructor() {
    this.searchCompanyArticles = createLookup({
      stepType: "SearchCompanyArticles",
      connectionName: "primary",
      annotations: {groupId: "articles", groupLabel: "Articles", explanation: "Search company articles."},
      executeFailure: this.finish,
      found: this.finish,
      unavailable: this.finish,
    });
    this.prepare = new PrepareSearch(this.searchCompanyArticles);
  }

  getFlowType(): string {
    return "CompanyArticlesFlow";
  }

  getSteps() {
    return StepList.startStep(this.prepare).otherSteps(this.searchCompanyArticles, this.finish);
  }

  getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}
`

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

func hasDiagnosticMessage(diagnostics []flowviz.Diagnostic, message string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, message) {
			return true
		}
	}
	return false
}

func hasNode(nodes []flowviz.Node, nodeID string) bool {
	for _, node := range nodes {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

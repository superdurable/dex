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

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFlowDefinitionsEnablesOnlyValidV2Definitions(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "legacy.json", validFlowDefinitionV1("RefundFlow"))
	writeFlowDefinitionTestFile(t, directory, "refund.json", validFlowDefinitionV2("RefundFlow", true))
	writeFlowDefinitionTestFile(t, directory, "invalid.json", validFlowDefinitionV2("InvalidFlow", false))

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	definitions := handler.V2Definitions()
	if len(definitions) != 1 {
		t.Fatalf("v2 definitions = %+v", definitions)
	}
	if _, found := definitions["RefundFlow"]; !found {
		t.Fatalf("RefundFlow is not enabled: %+v", definitions)
	}
}

func TestLoadFlowDefinitionsRejectsDuplicateValidV2FlowTypes(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "first.json", validFlowDefinitionV2("RefundFlow", true))
	writeFlowDefinitionTestFile(t, directory, "second.json", validFlowDefinitionV2("RefundFlow", true))

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), "multiple valid Flow Definition Graph 2.0") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestLoadFlowDefinitionsRejectsMalformedV2Contract(t *testing.T) {
	directory := t.TempDir()
	malformed := strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"rpcName":"GetDexDisplay"`, `"rpcName":"WrongDisplay"`, 1)
	writeFlowDefinitionTestFile(t, directory, "malformed.json", malformed)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), "fixed RPC names") {
		t.Fatalf("malformed error = %v", err)
	}
}

// A Flow Definition Graph can reach the server from anywhere, so UI slot rules are enforced here
// too rather than trusted to whatever produced the file.
func TestLoadFlowDefinitionsRejectsUnknownV2UISlot(t *testing.T) {
	directory := t.TempDir()
	unknown := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"description":"Note"`,
		`"description":"Note","uiSlot":"headline"`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "ui-slot.json", unknown)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `claims unknown UI slot "headline"`) {
		t.Fatalf("unknown UI slot error = %v", err)
	}
}

func TestLoadFlowDefinitionsRejectsRepeatedUniqueV2UISlot(t *testing.T) {
	directory := t.TempDir()
	repeated := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note"}`,
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note","uiSlot":"title"},`+
			`{"attributeKey":"case-note","valueType":"string","editable":true,"description":"Other","uiSlot":"title"}`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "ui-slot.json", repeated)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `UI slot "title" is claimed by both`) {
		t.Fatalf("repeated UI slot error = %v", err)
	}
}

func TestLoadFlowDefinitionsAcceptsRepeatedReasonUISlot(t *testing.T) {
	directory := t.TempDir()
	// `reason` is the one UI slot several fields may fill.
	twoReasons := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note"}`,
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note","uiSlot":"reason"},`+
			`{"attributeKey":"case-note","valueType":"string","editable":true,"description":"Other","uiSlot":"reason"}`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "ui-slot.json", twoReasons)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatalf("two reasons should load: %v", err)
	}
	if handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestLoadFlowDefinitionsRejectsInvalidV2ActionPermission(t *testing.T) {
	directory := t.TempDir()
	withPermission := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"actions":[]`,
		`"actions":[{"rpcName":"ApproveRefund","label":"Approve","requiredPermission":"Manager",`+
			`"condition":{"attributeKey":"case-status","operator":"in","values":["open"]},`+
			`"input":{"kind":"none","fields":[]}}]`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "permission.json", withPermission)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `invalid required permission "Manager"`) {
		t.Fatalf("invalid permission error = %v", err)
	}
}

func TestLoadFlowDefinitionsPreservesInt64ConditionValues(t *testing.T) {
	directory := t.TempDir()
	definition := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"actions":[]`,
		`"actions":[{"rpcName":"RetryRefund","label":"Retry","requiredPermission":"refund.retry","condition":{"attributeKey":"attempts","operator":"in","values":[9223372036854775807]},"input":{"kind":"none"}}]`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "refund.json", definition)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	value := handler.V2Definitions()["RefundFlow"].Actions[0].Condition.Values[0]
	if number, ok := value.(json.Number); !ok || number.String() != "9223372036854775807" {
		t.Fatalf("condition value = %#v", value)
	}
}

func TestLoadFlowDefinitionsAcceptsRecursiveStartInput(t *testing.T) {
	directory := t.TempDir()
	definition := withV2Start(validFlowDefinitionV2("RefundFlow", true),
		`{"stepType":"StartRefund","input":{"kind":"object","fields":[`+
			`{"name":"amount","required":true,"schema":{"kind":"integer","minimum":"0","maximum":"9223372036854775807"}},`+
			`{"name":"labels","required":false,"schema":{"kind":"map","nullable":true,"values":{"kind":"array","items":{"kind":"string"}}}}]}}`)
	writeFlowDefinitionTestFile(t, directory, "start.json", definition)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	if handler.V2Definitions()["RefundFlow"].Start.StepType != "StartRefund" {
		t.Fatalf("Start definition = %+v", handler.V2Definitions()["RefundFlow"].Start)
	}
}

func TestLoadFlowDefinitionsRejectsInvalidStartInput(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		input  string
		needle string
	}{
		{name: "unknown kind", input: `{"kind":"record"}`, needle: "unknown kind"},
		{name: "repeated field", input: `{"kind":"object","fields":[` +
			`{"name":"value","required":true,"schema":{"kind":"string"}},` +
			`{"name":"value","required":false,"schema":{"kind":"boolean"}}]}`, needle: "repeated field"},
		{name: "invalid range", input: `{"kind":"integer","minimum":"10","maximum":"1"}`, needle: "invalid range"},
		{name: "invalid enum", input: `{"kind":"integer","minimum":"0","maximum":"9",` +
			`"enumValues":[{"name":"Ten","value":"10"}]}`, needle: "outside its range"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			definition := withV2Start(validFlowDefinitionV2("RefundFlow", true),
				`{"stepType":"StartRefund","input":`+testCase.input+`}`)
			writeFlowDefinitionTestFile(t, directory, "start.json", definition)
			_, err := loadFlowDefinitions(directory)
			if err == nil || !strings.Contains(err.Error(), testCase.needle) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLoadFlowDefinitionsRejectsStartInputBeyondMaximumDepth(t *testing.T) {
	input := `{"kind":"string"}`
	for depth := 0; depth < 34; depth++ {
		input = `{"kind":"array","items":` + input + `}`
	}
	directory := t.TempDir()
	definition := withV2Start(validFlowDefinitionV2("RefundFlow", true),
		`{"stepType":"StartRefund","input":`+input+`}`)
	writeFlowDefinitionTestFile(t, directory, "start.json", definition)
	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), "nesting exceeds 32 levels") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadFlowDefinitionsAcceptsConnectorTriggerBindings(t *testing.T) {
	directory := t.TempDir()
	definition := withV2ConnectorTriggerBindings(validFlowDefinitionV2("ApprovalFlow", true),
		`[{"connectorId":"slack","triggerName":"threadReplyCreated",`+
			`"connectionName":"slack-workspace","bindingName":"approval-reply",`+
			`"modulePath":"github.com/superdurable/dex-connectors-library/connectors/slack",`+
			`"moduleVersion":"v0.1.0","configurationEnabled":true}]`)
	writeFlowDefinitionTestFile(t, directory, "approval.json", definition)
	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	bindings := handler.v2Definitions["ApprovalFlow"].ConnectorTriggerBindings
	if len(bindings) != 1 || bindings[0].BindingName != "approval-reply" || !bindings[0].ConfigurationEnabled {
		t.Fatalf("Connector Trigger bindings = %+v", bindings)
	}
}

func writeFlowDefinitionTestFile(t *testing.T, directory string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validFlowDefinitionV1(flowType string) string {
	return `{"schemaVersion":"1.0","valid":true,"source":{"language":"go","path":"flow.go"},` +
		`"flow":{"name":"` + flowType + `"},"nodes":[],"edges":[],"diagnostics":[]}`
}

func validFlowDefinitionV2(flowType string, valid bool) string {
	validJSON := "false"
	if valid {
		validJSON = "true"
	}
	return `{"schemaVersion":"2.0","valid":` + validJSON + `,"source":{"language":"go","path":"flow.go"},` +
		`"flow":{"name":"` + flowType + `"},"nodes":[],"edges":[],"diagnostics":[],` +
		`"groups":[{"id":"control","label":"Control","stepIds":["step:control"]}],` +
		`"v2":{"indexedAttributes":[{"attributeKey":"case-status","indexKey":"case-status",` +
		`"indexType":"keyword","valueType":"string","description":"Status"}],` +
		`"summary":{"rpcName":"GetDexSummary","fields":[{"attributeKey":"charge-reference",` +
		`"valueType":"string","editable":false,"description":"Charge"}]},` +
		`"display":{"rpcName":"GetDexDisplay","fields":[{"attributeKey":"operator-note",` +
		`"valueType":"string","editable":true,"description":"Note"}]},"actions":[]}}`
}

func withV2Start(definition string, start string) string {
	return strings.Replace(definition, `"actions":[]`, `"actions":[],"start":`+start, 1)
}

func withV2ConnectorTriggerBindings(definition string, bindings string) string {
	return strings.Replace(definition, `"actions":[]`, `"actions":[],"connectorTriggerBindings":`+bindings, 1)
}

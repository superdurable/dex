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

// A Flow Definition Graph can reach the server from anywhere, so the slot rules are enforced here
// too rather than trusted to whatever produced the file.
func TestLoadFlowDefinitionsRejectsUnknownV2Slot(t *testing.T) {
	directory := t.TempDir()
	unknown := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"description":"Note"`,
		`"description":"Note","slot":"headline"`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "slot.json", unknown)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `claims unknown slot "headline"`) {
		t.Fatalf("unknown slot error = %v", err)
	}
}

func TestLoadFlowDefinitionsRejectsRepeatedUniqueV2Slot(t *testing.T) {
	directory := t.TempDir()
	repeated := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note"}`,
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note","slot":"title"},`+
			`{"attributeKey":"case-note","valueType":"string","editable":true,"description":"Other","slot":"title"}`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "slot.json", repeated)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `slot "title" is claimed by both`) {
		t.Fatalf("repeated slot error = %v", err)
	}
}

func TestLoadFlowDefinitionsAcceptsRepeatedReasonSlot(t *testing.T) {
	directory := t.TempDir()
	// `reason` is the one slot several fields may fill: a recommendation can rest on more than one.
	twoReasons := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note"}`,
		`{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note","slot":"reason"},`+
			`{"attributeKey":"case-note","valueType":"string","editable":true,"description":"Other","slot":"reason"}`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "slot.json", twoReasons)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatalf("two reasons should load: %v", err)
	}
	if handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestLoadFlowDefinitionsRejectsInvalidV2ActionRole(t *testing.T) {
	directory := t.TempDir()
	withRole := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"actions":[]`,
		`"actions":[{"rpcName":"ApproveRefund","label":"Approve","role":"Manager",`+
			`"condition":{"attributeKey":"case-status","operator":"in","values":["open"]},`+
			`"input":{"kind":"none","fields":[]}}]`,
		1,
	)
	writeFlowDefinitionTestFile(t, directory, "role.json", withRole)

	_, err := loadFlowDefinitions(directory)
	if err == nil || !strings.Contains(err.Error(), `invalid role "Manager"`) {
		t.Fatalf("invalid role error = %v", err)
	}
}

func TestLoadFlowDefinitionsPreservesInt64ConditionValues(t *testing.T) {
	directory := t.TempDir()
	definition := strings.Replace(
		validFlowDefinitionV2("RefundFlow", true),
		`"actions":[]`,
		`"actions":[{"rpcName":"RetryRefund","label":"Retry","condition":{"attributeKey":"attempts","operator":"in","values":[9223372036854775807]},"input":{"kind":"none"}}]`,
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

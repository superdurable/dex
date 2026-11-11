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
	if !strings.Contains(err.Error(), "first.json") || !strings.Contains(err.Error(), "second.json") {
		t.Fatalf("duplicate error does not name both files: %v", err)
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

func TestLoadFlowDefinitionsAcceptsV2ActionWithoutCondition(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "refund.json", withV2Action("RefundFlow", ``))

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	if action := handler.V2Definitions()["RefundFlow"].Actions[0]; action.Condition != nil {
		t.Fatalf("Action condition = %+v", action.Condition)
	}
}

func TestLoadFlowDefinitionsRejectsMalformedV2ActionCondition(t *testing.T) {
	tests := map[string]string{
		"empty object":   `"condition":{},`,
		"no Attribute":   `"condition":{"attributeKey":"","operator":"in","values":["open"]},`,
		"other operator": `"condition":{"attributeKey":"case-status","operator":"eq","values":["open"]},`,
		"no values":      `"condition":{"attributeKey":"case-status","operator":"in","values":[]},`,
		"object value":   `"condition":{"attributeKey":"case-status","operator":"in","values":[{}]},`,
		"misspelled":     `"conditions":{"attributeKey":"case-status","operator":"in","values":["open"]},`,
	}
	for name, condition := range tests {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeFlowDefinitionTestFile(t, directory, "refund.json", withV2Action("RefundFlow", condition))
			if _, err := loadFlowDefinitions(directory); err == nil {
				t.Fatal("malformed condition was accepted")
			}
		})
	}
}

func TestLoadFlowDefinitionsPreservesV2ActionQRCapture(t *testing.T) {
	directory := t.TempDir()
	definition := withV2ActionInput(validFlowDefinitionV2("RefundFlow", true),
		`{"fieldName":"code","valueType":"string","source":"user","capture":"qr-code","required":true,"description":"Code"}`)
	writeFlowDefinitionTestFile(t, directory, "capture.json", definition)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	field := handler.V2Definitions()["RefundFlow"].Actions[0].Input.Fields[0]
	if field.Capture != "qr-code" {
		t.Fatalf("Action input capture = %q", field.Capture)
	}
}

func TestLoadFlowDefinitionsRejectsInvalidV2ActionCapture(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		field string
	}{
		{name: "unknown capture", field: `{"fieldName":"code","valueType":"string","source":"user","capture":"barcode","required":true,"description":"Code"}`},
		{name: "numeric capture", field: `{"fieldName":"code","valueType":"int64","source":"user","capture":"qr-code","required":true,"description":"Code"}`},
		{name: "Attribute capture", field: `{"fieldName":"code","valueType":"string","source":"attribute","attributeKey":"case-status","capture":"qr-code","required":true,"description":"Code"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			definition := withV2ActionInput(validFlowDefinitionV2("RefundFlow", true), testCase.field)
			writeFlowDefinitionTestFile(t, directory, "capture.json", definition)

			_, err := loadFlowDefinitions(directory)
			if err == nil || !strings.Contains(err.Error(), `input field "code" has invalid capture`) {
				t.Fatalf("invalid capture error = %v", err)
			}
		})
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

func withV2Action(flowType string, condition string) string {
	return strings.Replace(
		validFlowDefinitionV2(flowType, true),
		`"actions":[]`,
		`"actions":[{"rpcName":"ConfirmCase","label":"Confirm","requiredPermission":"refund.manage",`+
			condition+`"input":{"kind":"none"}}]`,
		1,
	)
}

func withV2Start(definition string, start string) string {
	var declared struct {
		StepType string `json:"stepType"`
	}
	if err := json.Unmarshal([]byte(start), &declared); err != nil {
		panic(err)
	}
	nodes, err := json.Marshal([]map[string]string{{"id": "step:" + declared.StepType, "kind": "step", "name": declared.StepType, "phase": "wait_for+execute"}})
	if err != nil {
		panic(err)
	}
	definition = strings.Replace(definition, `"nodes":[]`, `"nodes":`+string(nodes), 1)
	return strings.Replace(definition, `"actions":[]`, `"actions":[],"start":`+start, 1)
}

func withV2ConnectorTriggerBindings(definition string, bindings string) string {
	return strings.Replace(definition, `"actions":[]`, `"actions":[],"connectorTriggerBindings":`+bindings, 1)
}

func withV2ActionInput(definition string, field string) string {
	action := `{"rpcName":"SubmitCode","label":"Submit","requiredPermission":"code.submit",` +
		`"condition":{"attributeKey":"case-status","operator":"in","values":["open"]},` +
		`"input":{"kind":"object","fields":[` + field + `]}}`
	return strings.Replace(definition, `"actions":[]`, `"actions":[`+action+`]`, 1)
}

// The agents conformance suite ships this graph; each agents core fills in the two placeholders.
func TestLoadFlowDefinitionsAcceptsTheAgentSessionGraph(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "agents", "conformance", "session_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	graph := strings.NewReplacer(`"<flow-type>"`, `"SupportAgent"`, `"<action-permission>"`, `"agent.operate"`).
		Replace(string(template))
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "support-agent.json", graph)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := handler.V2Definitions()["SupportAgent"]
	if !found || len(definition.Actions) != 4 {
		t.Fatalf("v2 definitions = %+v", handler.V2Definitions())
	}
	for _, action := range definition.Actions {
		if action.Condition != nil || action.RequiredPermission != "agent.operate" {
			t.Fatalf("Action = %+v", action)
		}
	}
}

func TestLoadFlowDefinitionsReadsRPCLocksFromLockEdges(t *testing.T) {
	directory := t.TempDir()
	graph := withV2Graph(withV2Action("RefundFlow", ""), `[
		{"id":"rpc:confirmCaseMethod","kind":"rpc","name":"confirmCaseMethod"},
		{"id":"rpc:resolve_interrupt","kind":"rpc","name":"resolve_interrupt"},
		{"id":"step:control","kind":"step","name":"Control"},
		{"id":"resource:attribute:status","kind":"attribute","name":"case-status","resource":{"valueType":"string"}},
		{"id":"resource:attribute:gate","kind":"attribute","name":"gate-key","resource":{"valueType":"string"}},
		{"id":"resource:attribute:pending","kind":"attribute","name":"PendingInterrupts","resource":{"valueType":"list"}}
	]`, `[
		{"id":"edge:0001","kind":"resource_lock","from":"rpc:confirmCaseMethod","to":"resource:attribute:status","metadata":{"phase":"rpc","rpcName":"ConfirmCase"}},
		{"id":"edge:0002","kind":"resource_lock","from":"rpc:confirmCaseMethod","to":"resource:attribute:gate","metadata":{"phase":"rpc","rpcName":"ConfirmCase"}},
		{"id":"edge:0003","kind":"resource_lock","from":"rpc:confirmCaseMethod","to":"resource:attribute:status","metadata":{"phase":"rpc","rpcName":"ConfirmCase"}},
		{"id":"edge:0004","kind":"resource_lock","from":"rpc:resolve_interrupt","to":"resource:attribute:pending","label":"Lock"},
		{"id":"edge:0005","kind":"resource_lock","from":"step:control","to":"resource:attribute:gate","metadata":{"phase":"execute"}},
		{"id":"edge:0006","kind":"resource_write","from":"rpc:resolve_interrupt","to":"resource:attribute:status"}
	]`)
	writeFlowDefinitionTestFile(t, directory, "refund.json", graph)

	handler, err := loadFlowDefinitions(directory)
	if err != nil {
		t.Fatal(err)
	}
	locks := handler.V2Definitions()["RefundFlow"].RPCLocks
	if got := strings.Join(locks["ConfirmCase"].AttributeKeys, ","); got != "case-status,gate-key" || locks["ConfirmCase"].UnresolvedReason != "" {
		t.Fatalf("ConfirmCase locks = %+v", locks["ConfirmCase"])
	}
	if got := strings.Join(locks["resolve_interrupt"].AttributeKeys, ","); got != "PendingInterrupts" {
		t.Fatalf("resolve_interrupt locks = %+v", locks["resolve_interrupt"])
	}
	if _, found := locks["confirmCaseMethod"]; found {
		t.Fatalf("locks are keyed by method name instead of RPC name: %+v", locks)
	}
	if len(locks) != 2 {
		t.Fatalf("Step locks or non-lock edges leaked into RPC locks: %+v", locks)
	}
}

func TestLoadFlowDefinitionsMarksUnresolvableRPCLocks(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "AttributeMap instance", target: "resource:attribute:progress", want: `locks an instance of AttributeMap "progress"`},
		{name: "missing node", target: "resource:attribute:missing", want: `"resource:attribute:missing", which is not an Attribute`},
		{name: "Channel", target: "resource:channel:events", want: `"resource:channel:events", which is not an Attribute`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			graph := withV2Graph(withV2Action("RefundFlow", ""), `[
				{"id":"rpc:ConfirmCase","kind":"rpc","name":"ConfirmCase"},
				{"id":"resource:attribute:status","kind":"attribute","name":"case-status","resource":{"valueType":"string"}},
				{"id":"resource:attribute:progress","kind":"attribute","name":"progress","resource":{"valueType":"string","map":true}},
				{"id":"resource:channel:events","kind":"channel","name":"events","resource":{"valueType":"string"}}
			]`, `[
				{"id":"edge:0001","kind":"resource_lock","from":"rpc:ConfirmCase","to":"resource:attribute:status"},
				{"id":"edge:0002","kind":"resource_lock","from":"rpc:ConfirmCase","to":"`+test.target+`"}
			]`)
			writeFlowDefinitionTestFile(t, directory, "refund.json", graph)

			handler, err := loadFlowDefinitions(directory)
			if err != nil {
				t.Fatal(err)
			}
			locks := handler.V2Definitions()["RefundFlow"].RPCLocks["ConfirmCase"]
			if !strings.Contains(locks.UnresolvedReason, test.want) {
				t.Fatalf("unresolved reason = %q, want %q", locks.UnresolvedReason, test.want)
			}
		})
	}
}

func TestLoadFlowDefinitionsReadsSessionGraphRPCLocks(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "agents", "conformance", "session_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	graph := strings.NewReplacer(`"<flow-type>"`, `"SupportAgent"`, `"<action-permission>"`, `"agent.operate"`).
		Replace(string(template))
	definition, err := flowDefinitionFromGraph("support-agent.json", []byte(graph))
	if err != nil {
		t.Fatal(err)
	}
	locks := definition.V2.RPCLocks
	for rpcName, want := range map[string]string{
		"resolve_interrupt": "PendingInterrupts",
		"answer_question":   "PendingInterrupts",
		"cancel_turn":       "PendingInterrupts,TurnCancellationRequest",
	} {
		if got := strings.Join(locks[rpcName].AttributeKeys, ","); got != want || locks[rpcName].UnresolvedReason != "" {
			t.Fatalf("%s locks = %+v, want %s", rpcName, locks[rpcName], want)
		}
	}
}

func TestLoadFlowDefinitionsMarksOnlyTheSessionGraphAsAnAgentSession(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "agents", "conformance", "session_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	graph := strings.NewReplacer(`"<flow-type>"`, `"SupportAgent"`, `"<action-permission>"`, `"agent.operate"`).
		Replace(string(template))
	definition, err := flowDefinitionFromGraph("support-agent.json", []byte(graph))
	if err != nil {
		t.Fatal(err)
	}
	session := definition.V2.AgentSession
	if session == nil || !session.StartStepSkipWaitFor ||
		!session.RPCNames["send_user_message"] || !session.RPCNames["update_session_settings"] {
		t.Fatalf("agent session = %+v", session)
	}

	// Another Start Step means another Flow, even with every session Attribute and Action name.
	renamed := strings.ReplaceAll(graph, `"StartAgentSession"`, `"StartImpostor"`)
	renamed = strings.ReplaceAll(renamed, `step:StartAgentSession`, `step:StartImpostor`)
	impostor, err := flowDefinitionFromGraph("impostor.json", []byte(renamed))
	if err != nil {
		t.Fatal(err)
	}
	if impostor.V2.AgentSession != nil || len(impostor.V2.Actions) != 4 {
		t.Fatalf("impostor agent session = %+v, actions = %d", impostor.V2.AgentSession, len(impostor.V2.Actions))
	}

	// A repeated AgentState does not stand in for the missing PendingInterrupts.
	duplicated := strings.ReplaceAll(graph, `"name": "PendingInterrupts"`, `"name": "AgentState"`)
	if duplicated == graph {
		t.Fatal("the session graph names no PendingInterrupts node")
	}
	malformed, err := flowDefinitionFromGraph("duplicated.json", []byte(duplicated))
	if err != nil {
		t.Fatal(err)
	}
	if malformed.V2.AgentSession != nil {
		t.Fatalf("duplicated AgentState agent session = %+v", malformed.V2.AgentSession)
	}
}

func withV2Graph(definition string, nodes string, edges string) string {
	definition = strings.Replace(definition, `"nodes":[]`, `"nodes":`+nodes, 1)
	return strings.Replace(definition, `"edges":[]`, `"edges":`+edges, 1)
}

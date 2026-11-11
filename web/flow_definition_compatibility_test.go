// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const waitStepNode = `{"id":"step:a","kind":"step","name":"A","phase":"wait_for+execute"}`

func buildTestDefinition(t *testing.T, graph string) flowDefinition {
	t.Helper()
	definitions := buildFlowDefinitions([]flowDefinitionDocument{{origin: flowDefinitionOriginUploaded, flowType: "WaitFlow", data: []byte(graph)}})
	return definitions[0]
}

func withNodes(nodes string) string {
	return strings.Replace(validFlowDefinitionV2("WaitFlow", true), `"nodes":[]`, `"nodes":[`+nodes+`]`, 1)
}

// dexcli visualize 1.44 and older writes "conditions": null for a WaitFor that only fails.
func TestFlowDefinitionsAcceptTheNullWaitConditionsOfReleasedAnalyzers(t *testing.T) {
	definition := buildTestDefinition(t, withNodes(waitStepNode+
		`,{"id":"wait:a","kind":"wait","name":"failure","parentId":"step:a","phase":"wait_for","wait":{"type":"failure","conditions":null}}`))
	if definition.Error != "" {
		t.Fatalf("released analyzer output was rejected: %s", definition.Error)
	}
}

func TestFlowDefinitionsRejectWaitsTheCanvasCannotRead(t *testing.T) {
	for name, wait := range map[string]string{
		"empty wait":         `{}`,
		"wait without type":  `{"conditions":[]}`,
		"empty condition":    `{"type":"allOf","conditions":[{}]}`,
		"condition no label": `{"type":"allOf","conditions":[{"kind":"channel"}]}`,
		"numeric label":      `{"type":"allOf","conditions":[{"kind":"channel","label":3}]}`,
		"null condition":     `{"type":"allOf","conditions":[null]}`,
		"string conditions":  `{"type":"allOf","conditions":"all"}`,
	} {
		t.Run(name, func(t *testing.T) {
			definition := buildTestDefinition(t, withNodes(waitStepNode+
				`,{"id":"wait:a","kind":"wait","name":"allOf","parentId":"step:a","phase":"wait_for","wait":`+wait+`}`))
			if !strings.Contains(definition.Error, "is malformed") {
				t.Fatalf("error = %q", definition.Error)
			}
		})
	}
}

func TestFlowDefinitionsCheckTheMetadataTheServerDecodes(t *testing.T) {
	for name, metadata := range map[string]string{
		"numeric connector":       `{"connectorFactory":true,"connector":7}`,
		"connector field type":    `{"connectorFactory":true,"connector":{"connectorId":7}}`,
		"connector UI type":       `{"connectorFactory":true,"connector":{"configurationUI":{"units":"none"}}}`,
		"string connectorFactory": `{"connectorFactory":"yes"}`,
		"numeric flowType":        `{"flowType":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			definition := buildTestDefinition(t, withNodes(`{"id":"step:a","kind":"step","name":"A","metadata":`+metadata+`}`))
			if !strings.Contains(definition.Error, "is malformed") {
				t.Fatalf("error = %q", definition.Error)
			}
		})
	}
	edge := buildTestDefinition(t, withNodes(`{"id":"rpc:r","kind":"rpc","name":"R"}`))
	if edge.Error != "" {
		t.Fatalf("plain graph rejected: %s", edge.Error)
	}
	badEdge := strings.Replace(withNodes(`{"id":"rpc:r","kind":"rpc","name":"R"}`), `"edges":[]`,
		`"edges":[{"id":"e","kind":"resource_lock","from":"rpc:r","to":"rpc:r","metadata":{"rpcName":5}}]`, 1)
	if definition := buildTestDefinition(t, badEdge); !strings.Contains(definition.Error, "is malformed") {
		t.Fatalf("edge metadata error = %q", definition.Error)
	}
}

// Connector setup reads each definition on its own, so one it cannot decode hides no other connection.
func TestConnectorCatalogSkipsADefinitionItCannotDecode(t *testing.T) {
	good := `{"flowName":"GoodFlow","graph":{"nodes":[{"id":"step:send","kind":"step","name":"Send","metadata":{"connectorFactory":true,` +
		`"connector":{"connectorId":"gmail","operationId":"send","connectionName":"sender","modulePath":"github.com/superdurable/dex-connectors-library/connectors/google/gmail",` +
		`"moduleVersion":"v1.0.0","configurationEnabled":true,"configurationUI":{"units":[]}}}}]}}`
	bad := `{"flowName":"BadFlow","graph":{"nodes":[{"id":"step:x","kind":"step","name":"X","metadata":{"connectorFactory":true,"connector":7}}]}}`
	views, err := connectorViewsFromCatalog([]byte(`{"definitions":[`+bad+`,`+good+`]}`), nil, time.Now(), nil)
	if err != nil || len(views) != 1 || views[0].ConnectorID != "gmail" {
		t.Fatalf("views = %+v, err = %v", views, err)
	}
}

func TestFlowDefinitionsBoundStartInputItemsAtEveryDepth(t *testing.T) {
	inner := make([]string, 0, 550)
	for index := 0; index < 550; index++ {
		inner = append(inner, fmt.Sprintf(`{"name":"f%d","required":false,"schema":{"kind":"string"}}`, index))
	}
	object := `{"kind":"object","fields":[` + strings.Join(inner, ",") + `]}`
	start := `{"stepType":"Begin","input":{"kind":"object","fields":[{"name":"a","required":true,"schema":` + object +
		`},{"name":"b","required":true,"schema":` + object + `}]}}`
	definition := buildTestDefinition(t, withV2Start(validFlowDefinitionV2("WaitFlow", true), start))
	if !strings.Contains(definition.Error, "start input has 1102 fields and enum values; the limit is 1024") {
		t.Fatalf("error = %q", definition.Error)
	}
}

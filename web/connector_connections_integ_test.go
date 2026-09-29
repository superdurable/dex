// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/api"
)

const connectorLLMReleaseMetadata = `{"connectorId":"llm","modulePath":"github.com/superdurable/dex-connectors-library/connectors/llm",` +
	`"version":"v0.2.0","tag":"connectors/llm/v0.2.0","sourceSha":"source-sha",` +
	`"manifestSha256":"0000000000000000000000000000000000000000000000000000000000000000",` +
	`"manifest":{"apiVersion":"connectors.dex.dev/v1alpha1","kind":"Connector",` +
	`"metadata":{"name":"llm","displayName":"LLM","description":"Several model providers"},` +
	`"spec":{"provider":"llm","configuration":{"fields":[{"name":"model","type":"string","description":"Default model.",` +
	`"required":false,"studioUnit":{"unit":"modelPicker","port":"model"}}]},` +
	`"auth":{"selection":"multiple","methodLabel":"Provider","methods":[` +
	`{"id":"openai","displayName":"OpenAI","description":"OpenAI key.","type":"apiKey","connectionKind":"apiKey",` +
	`"fields":[{"name":"openai_api_key","type":"secretString","description":"","required":true}]},` +
	`{"id":"anthropic","displayName":"Claude","description":"Claude key.","type":"apiKey","connectionKind":"apiKey",` +
	`"fields":[{"name":"anthropic_api_key","type":"secretString","description":"","required":true}],` +
	`"configuration":{"fields":[{"name":"anthropicWorkspaceId","type":"string","description":"Claude workspace.","required":false}]}}]}}}}`

type connectorIntegConnections struct {
	CSRFToken          string           `json:"csrfToken"`
	DefinitionRevision string           `json:"definitionRevision"`
	Connections        []map[string]any `json:"connections"`
}

func TestDexWebServesAndSavesConnectionsWithSeveralAuthMethods(t *testing.T) {
	releaseDirectory := t.TempDir()
	metadataDigest := sha256.Sum256([]byte(connectorLLMReleaseMetadata))
	writeConnectorIntegFile(t, releaseDirectory, "connector-release.json", connectorLLMReleaseMetadata)
	writeConnectorIntegFile(t, releaseDirectory, "connector-release.json.sha256", fmt.Sprintf("%x  connector-release.json\n", metadataDigest))
	definitionDirectory := t.TempDir()
	writeConnectorIntegFile(t, definitionDirectory, "llm.json", `{"schemaVersion":"1.0","valid":true,"source":{"language":"go","path":"flow.go"},`+
		`"flow":{"name":"llm.AnswerFlow"},"nodes":[{"id":"step:Answer","name":"Answer","kind":"step","metadata":{"connectorFactory":true,`+
		`"connector":{"connectorId":"llm","operationId":"generateText","operationKind":"mutation","connectionName":"default",`+
		`"modulePath":"github.com/superdurable/dex-connectors-library/connectors/llm","moduleVersion":"v0.2.0","configurationEnabled":true}}}],`+
		`"edges":[],"diagnostics":[]}`)
	harness := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{
		BindAddress: "127.0.0.1", Port: dexweb.DefaultPort,
		FlowRenderingDirectory: definitionDirectory, ConnectorSetupEnabled: true,
		ConnectorConfigDirectory:  t.TempDir(),
		ConnectorReleaseOverrides: map[string]string{"llm": releaseDirectory},
	})

	catalog := listConnectorIntegConnections(t, harness)
	if len(catalog.Connections) != 1 || catalog.Connections[0]["status"] != "Missing" {
		t.Fatalf("connections = %+v", catalog.Connections)
	}
	session := sendConnectorIntegRequest(t, harness, catalog, http.MethodPost, "/api/v2/connector-ui-sessions", `{"connectorId":"llm","connectionName":"default"}`)
	var sessionBody struct {
		Manifest struct {
			Spec struct {
				Configuration struct {
					Fields []map[string]any `json:"fields"`
				} `json:"configuration"`
				Auth struct {
					Selection   string           `json:"selection"`
					MethodLabel string           `json:"methodLabel"`
					Methods     []map[string]any `json:"methods"`
				} `json:"auth"`
			} `json:"spec"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(session), &sessionBody); err != nil {
		t.Fatal(err)
	}
	spec := sessionBody.Manifest.Spec
	if spec.Auth.Selection != "multiple" || spec.Auth.MethodLabel != "Provider" || len(spec.Auth.Methods) != 2 ||
		!reflect.DeepEqual(spec.Auth.Methods[1]["configuration"], map[string]any{"fields": []any{map[string]any{
			"name": "anthropicWorkspaceId", "type": "string", "description": "Claude workspace.", "required": false,
		}}}) ||
		!reflect.DeepEqual(spec.Configuration.Fields[0]["studioUnit"], map[string]any{"unit": "modelPicker", "port": "model"}) {
		t.Fatalf("session manifest = %s", session)
	}

	const modulePrefix = `{"modulePath":"github.com/superdurable/dex-connectors-library/connectors/llm","moduleVersion":"v0.2.0","provider":"llm",`
	sendConnectorIntegRequest(t, harness, catalog, http.MethodPut, "/api/v2/connector-connections/llm/default", modulePrefix+
		`"authMethodIds":["anthropic","openai"],"configuration":{"anthropicWorkspaceId":"ws_1"},`+
		`"credentials":{"anthropic_api_key":"anthropic-secret","openai_api_key":"openai-secret"}}`)
	sendConnectorIntegRequest(t, harness, catalog, http.MethodPut, "/api/v2/connector-connections/llm/default", modulePrefix+
		`"authMethodIds":["anthropic","openai"],"configuration":{"anthropicWorkspaceId":"ws_1","model":"anthropic/claude-sonnet-5"},`+
		`"credentials":{},"keepCredentialFields":["anthropic_api_key","openai_api_key"]}`)

	catalog = listConnectorIntegConnections(t, harness)
	view := catalog.Connections[0]
	if view["status"] != "Ready" || !reflect.DeepEqual(view["authMethodIds"], []any{"anthropic", "openai"}) ||
		!reflect.DeepEqual(view["storedCredentialFields"], []any{"anthropic_api_key", "openai_api_key"}) ||
		!reflect.DeepEqual(view["configuration"], map[string]any{"anthropicWorkspaceId": "ws_1", "model": "anthropic/claude-sonnet-5"}) {
		t.Fatalf("connection view = %+v", view)
	}
}

func listConnectorIntegConnections(t *testing.T, harness *harness) connectorIntegConnections {
	t.Helper()
	response := get(t, harness.http.URL+"/api/v2/connector-connections")
	defer response.Body.Close()
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(body, "-secret") {
		t.Fatalf("connections status = %d: %s", response.StatusCode, body)
	}
	var catalog connectorIntegConnections
	if err := json.Unmarshal([]byte(body), &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func sendConnectorIntegRequest(
	t *testing.T,
	harness *harness,
	catalog connectorIntegConnections,
	method string,
	path string,
	body string,
) string {
	t.Helper()
	request, err := http.NewRequest(method, harness.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	origin, err := url.Parse(harness.http.URL)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	request.Header.Set("X-Dex-CSRF-Token", catalog.CSRFToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, catalog.DefinitionRevision)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody := readBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(responseBody, "-secret") {
		t.Fatalf("%s %s status = %d: %s", method, path, response.StatusCode, responseBody)
	}
	return responseBody
}

func writeConnectorIntegFile(t *testing.T, directory string, name string, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/web/api"
)

func TestStudioProviderCommandUsesDeclaredRequestWithoutReturningCredential(t *testing.T) {
	provider := connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{{
		ConnectorID: "slack", OperationID: "postThreadReply", OperationKind: "mutation",
		ConnectionName: "slack-workspace", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}})
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "unused", nil)
	connection.Credentials = map[string]json.RawMessage{"bot_token": json.RawMessage(`"xoxb-never-return"`)}
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	setup.providerHTTPClient = &http.Client{Transport: connectorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer xoxb-never-return" {
			t.Error("declared credential was not used")
		}
		if request.URL.Query().Get("limit") != "200" || request.URL.Query().Get("cursor") != "next" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"items":[{"id":"C123"}]}`)), Header: make(http.Header)}, nil
	})}
	setup.uiSessions["session"] = connectorUISession{
		connectorID: "slack", connectionName: "slack-workspace", expiresAt: time.Now().Add(time.Minute),
		commands: map[string]connectorManifestStudioCommand{"listResources": {
			ID: "listResources", Capability: "provider.resources-list",
			Request: connectorManifestStudioHTTPRequest{
				Method: http.MethodGet, URL: "https://provider.example/resources",
				Credential: connectorManifestStudioCredential{Field: "bot_token", Scheme: "bearer"},
				FixedQuery: map[string]string{"limit": "200"},
				Parameters: []connectorManifestStudioParameter{{Name: "cursor", Location: "query", Target: "cursor"}},
			},
		}},
	}
	request := authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-ui-sessions/session/commands/listResources", strings.NewReader(`{"parameters":{"cursor":"next"}}`))
	request.SetPathValue("sessionNonce", "session")
	request.SetPathValue("commandId", "listResources")
	recorder := httptest.NewRecorder()
	setup.handleStudioProviderCommand(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "xoxb-never-return") || !strings.Contains(recorder.Body.String(), `"C123"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestStudioProviderCommandRejectsCredentialMaterialInDecodedJSON(t *testing.T) {
	if !containsConnectorSecret(map[string]any{"nested": []any{map[string]any{"value": "prefix-xoxb-secret-suffix"}}}, "xoxb-secret") {
		t.Fatal("decoded credential material was not detected")
	}
	if containsConnectorSecret(map[string]any{"value": "safe provider data"}, "xoxb-secret") {
		t.Fatal("safe provider data was rejected")
	}
}

func TestStudioProviderCommandSendsReleaseDeclaredCredentialAndFixedHeaders(t *testing.T) {
	longHeaderValue := strings.Repeat("v", connectorProviderFixedHeaderValueLimit)
	tests := []struct {
		name                 string
		credential           connectorManifestStudioCredential
		credentialHeaderName string
		credentialHeaderText string
	}{
		{
			name:                 "header scheme",
			credential:           connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "x-goog-api-key"},
			credentialHeaderName: "X-Goog-Api-Key", credentialHeaderText: connectorStudioTestSecret,
		},
		{
			name:                 "bearer scheme",
			credential:           connectorManifestStudioCredential{Field: "api_key", Scheme: "bearer"},
			credentialHeaderName: "Authorization", credentialHeaderText: "Bearer " + connectorStudioTestSecret,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, archive := connectorStudioCommandTestRelease(t, connectorManifestStudioHTTPRequest{
				Method: http.MethodGet, URL: "https://provider.example/v1beta/models", Credential: test.credential,
				FixedQuery:   map[string]string{"pageSize": "100"},
				FixedHeaders: map[string]string{"anthropic-version": "2023-06-01", "X-Studio-Long": longHeaderValue},
			})
			var sentRequests []*http.Request
			setup, mux := connectorStudioCommandTestSetup(t, release, archive, connectorStudioTestSecret, func(request *http.Request) (*http.Response, error) {
				sentRequests = append(sentRequests, request)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"models":[{"name":"models/gemini-2.5-flash"}]}`)), Header: make(http.Header)}, nil
			})
			sessionRecorder := createConnectorStudioTestUISession(t, setup, mux)
			if sessionRecorder.Code != http.StatusOK || strings.Contains(sessionRecorder.Body.String(), connectorStudioTestSecret) {
				t.Fatalf("UI session response = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
			}
			recorder := invokeConnectorStudioTestCommand(t, setup, mux, connectorStudioTestSessionNonce(t, sessionRecorder), "listModels")
			if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), connectorStudioTestSecret) ||
				!strings.Contains(recorder.Body.String(), "models/gemini-2.5-flash") {
				t.Fatalf("command response = %d %s", recorder.Code, recorder.Body.String())
			}
			if len(sentRequests) != 1 {
				t.Fatalf("provider requests = %d", len(sentRequests))
			}
			sent := sentRequests[0]
			if sent.Header.Get(test.credentialHeaderName) != test.credentialHeaderText {
				t.Errorf("%s = %q", test.credentialHeaderName, sent.Header.Get(test.credentialHeaderName))
			}
			for name, values := range sent.Header {
				if name != test.credentialHeaderName && strings.Contains(strings.Join(values, "\n"), connectorStudioTestSecret) {
					t.Errorf("header %s carries the credential", name)
				}
			}
			if test.credentialHeaderName != "Authorization" && sent.Header.Get("Authorization") != "" {
				t.Errorf("Authorization = %q", sent.Header.Get("Authorization"))
			}
			if strings.Contains(sent.URL.String(), connectorStudioTestSecret) || sent.URL.Query().Get("pageSize") != "100" {
				t.Errorf("URL = %s", sent.URL)
			}
			if sent.Header.Get("Anthropic-Version") != "2023-06-01" || sent.Header.Get("X-Studio-Long") != longHeaderValue ||
				sent.Header.Get("Accept") != "application/json" {
				t.Errorf("headers = %v", sent.Header)
			}
		})
	}
}

func TestStudioProviderCommandRejectsInvalidHeaderDeclarationsBeforeSending(t *testing.T) {
	headerCredential := connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "x-goog-api-key"}
	type invalidHeaderDeclaration struct {
		name         string
		credential   connectorManifestStudioCredential
		fixedHeaders map[string]string
	}
	tests := []invalidHeaderDeclaration{}
	for _, headerName := range []string{
		"Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Proxy-Authorization",
		"proxy-connection", "TE", "Trailer", "Upgrade", "Cookie", "Set-Cookie", "Origin", "Referer",
		"Forwarded", "X-Forwarded-For", "x-forwarded-host", "Authorization", "authorization", "Accept",
		"X_Forwarded_For", "x_forwarded_host", "Proxy_Authorization", "Content_Length", "Transfer_Encoding",
		"Keep_Alive", "Set_Cookie", "X-HTTP-Method-Override", "x-http-method", "X_Method_Override",
	} {
		tests = append(tests,
			invalidHeaderDeclaration{
				name:       "credential header " + headerName,
				credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: headerName},
			},
			invalidHeaderDeclaration{
				name: "fixed header " + headerName, credential: headerCredential,
				fixedHeaders: map[string]string{headerName: "value"},
			},
		)
	}
	tests = append(tests,
		invalidHeaderDeclaration{name: "unknown scheme", credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "basic"}},
		invalidHeaderDeclaration{name: "bearer names header", credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "bearer", Header: "x-api-key"}},
		invalidHeaderDeclaration{name: "header scheme without name", credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header"}},
		invalidHeaderDeclaration{name: "credential header CRLF", credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "X-Api-Key\r\nX-Injected"}},
		invalidHeaderDeclaration{name: "credential header space", credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "X Api Key"}},
		invalidHeaderDeclaration{name: "fixed header name colon", credential: headerCredential, fixedHeaders: map[string]string{"X-Version:": "1"}},
		invalidHeaderDeclaration{name: "fixed header name CRLF", credential: headerCredential, fixedHeaders: map[string]string{"X-Version\r\nX-Injected": "1"}},
		invalidHeaderDeclaration{name: "fixed header value CRLF", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023-06-01\r\nX-Injected: 1"}},
		invalidHeaderDeclaration{name: "fixed header value NUL", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023\x00"}},
		invalidHeaderDeclaration{name: "fixed header value DEL", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023\x7f"}},
		invalidHeaderDeclaration{name: "fixed header value tab", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023\t06"}},
		invalidHeaderDeclaration{name: "fixed header value non-ASCII", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "versión"}},
		invalidHeaderDeclaration{name: "fixed header value empty", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": ""}},
		invalidHeaderDeclaration{name: "fixed header value blank", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "   "}},
		invalidHeaderDeclaration{name: "fixed header value leading space", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": " 2023-06-01"}},
		invalidHeaderDeclaration{name: "fixed header value trailing space", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023-06-01 "}},
		invalidHeaderDeclaration{name: "fixed header value too long", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": strings.Repeat("v", connectorProviderFixedHeaderValueLimit+1)}},
		invalidHeaderDeclaration{name: "fixed header repeats credential header", credential: headerCredential, fixedHeaders: map[string]string{"X-Goog-Api-Key": "other"}},
		invalidHeaderDeclaration{name: "fixed header repeats credential header with underscores", credential: headerCredential, fixedHeaders: map[string]string{"X_Goog_Api_Key": "other"}},
		invalidHeaderDeclaration{name: "fixed header case-insensitive duplicate", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023-06-01", "Anthropic-Version": "2023-06-01"}},
		invalidHeaderDeclaration{name: "fixed header underscore duplicate", credential: headerCredential, fixedHeaders: map[string]string{"anthropic-version": "2023-06-01", "anthropic_version": "2023-06-01"}},
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := connectorManifestStudioHTTPRequest{
				Method: http.MethodGet, URL: "https://provider.example/v1beta/models",
				Credential: test.credential, FixedHeaders: test.fixedHeaders,
			}
			release, archive := connectorStudioCommandTestRelease(t, request)
			providerCalls := 0
			setup, mux := connectorStudioCommandTestSetup(t, release, archive, connectorStudioTestSecret, func(*http.Request) (*http.Response, error) {
				providerCalls++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})
			sessionRecorder := createConnectorStudioTestUISession(t, setup, mux)
			if sessionRecorder.Code != http.StatusOK {
				t.Fatalf("UI session response = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
			}
			recorder := invokeConnectorStudioTestCommand(t, setup, mux, connectorStudioTestSessionNonce(t, sessionRecorder), "listModels")
			if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "CONNECTOR_PROVIDER_COMMAND_FAILED") || providerCalls != 0 {
				t.Fatalf("command response = %d %s, provider calls = %d", recorder.Code, recorder.Body.String(), providerCalls)
			}
			if _, err := newConnectorReleaseResolver(t.TempDir(), map[string]string{
				release.ConnectorID: connectorLocalReleaseTestDirectory(t, release, archive),
			}); err == nil || !strings.Contains(err.Error(), "request headers are invalid") {
				t.Fatalf("local override error = %v", err)
			}
		})
	}
}

func TestConnectorConnectionSetupIgnoresUnusableStudioCommand(t *testing.T) {
	release, archive := connectorStudioCommandTestRelease(t, connectorManifestStudioHTTPRequest{
		Method: http.MethodGet, URL: "https://provider.example/v1beta/models",
		Credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "x-goog-api-key"},
	})
	release.Manifest.Spec.Studio.Commands = append(release.Manifest.Spec.Studio.Commands,
		connectorManifestStudioCommand{ID: "listWithFutureScheme", Capability: "gemini.models-list", Request: connectorManifestStudioHTTPRequest{
			Method: http.MethodGet, URL: "https://provider.example/v1beta/models",
			Credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "query"},
		}},
		connectorManifestStudioCommand{ID: "listWithReservedHeader", Capability: "gemini.models-list", Request: connectorManifestStudioHTTPRequest{
			Method: http.MethodGet, URL: "https://provider.example/v1beta/models",
			Credential:   connectorManifestStudioCredential{Field: "api_key", Scheme: "bearer"},
			FixedHeaders: map[string]string{"X_Forwarded_Host": "attacker.example"},
		}},
	)
	release.Manifest.Spec.Auth.Type = "oauth2"
	release.Manifest.Spec.Auth.OAuth2 = &connectorManifestOAuth2{
		AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token",
		Scopes: []string{"models.read"}, PKCE: true,
		CredentialMappings: []connectorOAuthCredentialMapping{{Credential: "api_key", Source: "access_token"}},
	}
	providerCalls := 0
	setup, mux := connectorStudioCommandTestSetup(t, release, archive, connectorStudioTestSecret, func(*http.Request) (*http.Response, error) {
		providerCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Header: make(http.Header)}, nil
	})

	identity := connectorStudioCommandTestIdentity()
	connectionPath := "/api/v2/connector-connections/" + identity.ConnectorID + "/" + identity.ConnectionName
	putBody := `{"modulePath":"` + identity.ModulePath + `","moduleVersion":"` + identity.ModuleVersion +
		`","provider":"google","configuration":{},"credentials":{"api_key":"` + connectorStudioTestSecret + `"}}`
	putRecorder := httptest.NewRecorder()
	mux.ServeHTTP(putRecorder, authorizedConnectorRequest(t, setup, http.MethodPut, connectionPath, strings.NewReader(putBody)))
	if putRecorder.Code != http.StatusOK || !strings.Contains(putRecorder.Body.String(), `"status":"Ready"`) {
		t.Fatalf("PUT connection response = %d %s", putRecorder.Code, putRecorder.Body.String())
	}

	oauthBody := `{"clientId":"oauth-client-id","clientSecret":"oauth-client-secret","configuration":{},"credentialValues":{},"credentialSecrets":{}}`
	oauthRecorder := httptest.NewRecorder()
	mux.ServeHTTP(oauthRecorder, authorizedConnectorRequest(t, setup, http.MethodPost, connectionPath+"/oauth/start", strings.NewReader(oauthBody)))
	if oauthRecorder.Code != http.StatusOK || !strings.Contains(oauthRecorder.Body.String(), "https://provider.example/authorize") {
		t.Fatalf("OAuth start response = %d %s", oauthRecorder.Code, oauthRecorder.Body.String())
	}

	sessionRecorder := createConnectorStudioTestUISession(t, setup, mux)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("UI session response = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}
	sessionNonce := connectorStudioTestSessionNonce(t, sessionRecorder)
	for _, commandID := range []string{"listWithFutureScheme", "listWithReservedHeader"} {
		recorder := invokeConnectorStudioTestCommand(t, setup, mux, sessionNonce, commandID)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "CONNECTOR_PROVIDER_COMMAND_FAILED") || providerCalls != 0 {
			t.Fatalf("%s response = %d %s, provider calls = %d", commandID, recorder.Code, recorder.Body.String(), providerCalls)
		}
	}
	recorder := invokeConnectorStudioTestCommand(t, setup, mux, sessionNonce, "listModels")
	if recorder.Code != http.StatusOK || providerCalls != 1 {
		t.Fatalf("listModels response = %d %s, provider calls = %d", recorder.Code, recorder.Body.String(), providerCalls)
	}
}

func TestStudioProviderCommandRejectsCredentialEchoedByProvider(t *testing.T) {
	schemes := []struct {
		name               string
		credential         connectorManifestStudioCredential
		receivedCredential func(http.Header) string
	}{
		{
			name:       "header scheme",
			credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "x-goog-api-key"},
			receivedCredential: func(header http.Header) string {
				return header.Get("X-Goog-Api-Key")
			},
		},
		{
			name:       "bearer scheme",
			credential: connectorManifestStudioCredential{Field: "api_key", Scheme: "bearer"},
			receivedCredential: func(header http.Header) string {
				return strings.TrimSpace(strings.TrimPrefix(header.Get("Authorization"), "Bearer"))
			},
		},
	}
	storedCredentials := map[string]string{
		"exact credential":  connectorStudioTestSecret,
		"padded credential": "  " + connectorStudioTestSecret + "  ",
	}
	responseTemplates := map[string]string{
		"no echo":           `{"models":[{"name":"models/gemini-2.5-flash"}]}`,
		"echo in value":     `{"error":"invalid key RECEIVED_CREDENTIAL"}`,
		"echo in key":       `{"keys":{"RECEIVED_CREDENTIAL":true}}`,
		"echo in array key": `{"items":[{"RECEIVED_CREDENTIAL":1}]}`,
	}
	for _, scheme := range schemes {
		for storedName, storedCredential := range storedCredentials {
			for responseName, responseTemplate := range responseTemplates {
				t.Run(scheme.name+"/"+storedName+"/"+responseName, func(t *testing.T) {
					release, archive := connectorStudioCommandTestRelease(t, connectorManifestStudioHTTPRequest{
						Method: http.MethodGet, URL: "https://provider.example/v1beta/models", Credential: scheme.credential,
					})
					provider := &connectorStudioEchoProvider{
						receivedCredential: scheme.receivedCredential, responseTemplate: responseTemplate,
					}
					setup, mux := connectorStudioCommandTestSetup(t, release, archive, storedCredential, provider.RoundTrip)
					sessionRecorder := createConnectorStudioTestUISession(t, setup, mux)
					if sessionRecorder.Code != http.StatusOK {
						t.Fatalf("UI session response = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
					}
					recorder := invokeConnectorStudioTestCommand(t, setup, mux, connectorStudioTestSessionNonce(t, sessionRecorder), "listModels")
					if len(provider.receivedCredentials) != 1 || provider.receivedCredentials[0] != connectorStudioTestSecret {
						t.Fatalf("provider received credentials %q", provider.receivedCredentials)
					}
					if strings.Contains(recorder.Body.String(), connectorStudioTestSecret) {
						t.Fatalf("command response leaked the credential: %d %s", recorder.Code, recorder.Body.String())
					}
					expectedStatus := http.StatusBadGateway
					if responseName == "no echo" {
						expectedStatus = http.StatusOK
					}
					if recorder.Code != expectedStatus {
						t.Fatalf("command response = %d %s", recorder.Code, recorder.Body.String())
					}
				})
			}
		}
	}
}

func TestStudioProviderCommandRejectsCredentialInFixedHeaderOrHeaderValue(t *testing.T) {
	tests := []struct {
		name            string
		credentialValue string
		fixedHeaders    map[string]string
	}{
		{name: "fixed header contains credential", credentialValue: connectorStudioTestSecret, fixedHeaders: map[string]string{"X-Debug": "key=" + connectorStudioTestSecret}},
		{name: "fixed header equals credential", credentialValue: connectorStudioTestSecret, fixedHeaders: map[string]string{"X-Debug": connectorStudioTestSecret}},
		{name: "fixed header contains padded credential", credentialValue: "  " + connectorStudioTestSecret + " ", fixedHeaders: map[string]string{"X-Debug": connectorStudioTestSecret}},
		{name: "credential CRLF", credentialValue: connectorStudioTestSecret + "\r\nX-Injected: 1"},
		{name: "credential NUL", credentialValue: connectorStudioTestSecret + "\x00"},
		{name: "credential non-ASCII", credentialValue: connectorStudioTestSecret + "é"},
		{name: "credential only spaces", credentialValue: "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, archive := connectorStudioCommandTestRelease(t, connectorManifestStudioHTTPRequest{
				Method: http.MethodGet, URL: "https://provider.example/v1beta/models",
				Credential:   connectorManifestStudioCredential{Field: "api_key", Scheme: "header", Header: "x-goog-api-key"},
				FixedHeaders: test.fixedHeaders,
			})
			providerCalls := 0
			setup, mux := connectorStudioCommandTestSetup(t, release, archive, test.credentialValue, func(*http.Request) (*http.Response, error) {
				providerCalls++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})
			sessionRecorder := createConnectorStudioTestUISession(t, setup, mux)
			if sessionRecorder.Code != http.StatusOK {
				t.Fatalf("UI session response = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
			}
			recorder := invokeConnectorStudioTestCommand(t, setup, mux, connectorStudioTestSessionNonce(t, sessionRecorder), "listModels")
			if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "CONNECTOR_PROVIDER_COMMAND_FAILED") || providerCalls != 0 {
				t.Fatalf("command response = %d %s, provider calls = %d", recorder.Code, recorder.Body.String(), providerCalls)
			}
		})
	}
}

type connectorRoundTripFunc func(*http.Request) (*http.Response, error)

func (function connectorRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// connectorStudioEchoProvider parses the serialized request, so HTTP wire trimming applies before the echo.
type connectorStudioEchoProvider struct {
	receivedCredential  func(http.Header) string
	responseTemplate    string
	receivedCredentials []string
}

func (provider *connectorStudioEchoProvider) RoundTrip(request *http.Request) (*http.Response, error) {
	var wire bytes.Buffer
	if err := request.Write(&wire); err != nil {
		return nil, err
	}
	received, err := http.ReadRequest(bufio.NewReader(&wire))
	if err != nil {
		return nil, err
	}
	receivedCredential := provider.receivedCredential(received.Header)
	provider.receivedCredentials = append(provider.receivedCredentials, receivedCredential)
	body := strings.ReplaceAll(provider.responseTemplate, "RECEIVED_CREDENTIAL", receivedCredential)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

const connectorStudioTestSecret = "AIza-studio-secret"

func connectorStudioCommandTestIdentity() connectorDefinitionIdentity {
	return connectorDefinitionIdentity{
		ConnectorID: "gemini", OperationID: "generateContent", OperationKind: "mutation",
		ConnectionName: "gemini-api", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gemini",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}
}

func connectorStudioCommandTestRelease(t *testing.T, request connectorManifestStudioHTTPRequest) (connectorRelease, []byte) {
	t.Helper()
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Gemini</main>"))
	release := connectorTestRelease(connectorStudioCommandTestIdentity(), archive, connectorUIHostAPIRange)
	release.Manifest.Spec.Auth.Type = "apiKey"
	release.Manifest.Spec.Auth.Fields = []connectorManifestField{{Name: "api_key", Type: "secretString", Required: true}}
	release.Manifest.Spec.Studio.Commands = []connectorManifestStudioCommand{{
		ID: "listModels", Capability: "gemini.models-list", Request: request,
	}}
	return release, archive
}

func connectorStudioCommandTestSetup(
	t *testing.T,
	release connectorRelease,
	archive []byte,
	credentialValue string,
	providerTransport connectorRoundTripFunc,
) (*connectorSetup, *http.ServeMux) {
	t.Helper()
	identity := connectorStudioCommandTestIdentity()
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	metadata, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	server := connectorReleaseTestServer(t, metadata, archive)
	t.Cleanup(server.Close)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
	setup.providerHTTPClient = &http.Client{Transport: providerTransport}
	encodedCredential, err := json.Marshal(credentialValue)
	if err != nil {
		t.Fatal(err)
	}
	connection := testLocalConnectorConnection(identity.ConnectorID, identity.ConnectionName, "unused", nil)
	connection.Credentials = map[string]json.RawMessage{"api_key": encodedCredential}
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	return setup, mux
}

func createConnectorStudioTestUISession(t *testing.T, setup *connectorSetup, mux *http.ServeMux) *httptest.ResponseRecorder {
	t.Helper()
	identity := connectorStudioCommandTestIdentity()
	body := `{"connectorId":"` + identity.ConnectorID + `","connectionName":"` + identity.ConnectionName + `"}`
	request := authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-ui-sessions", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func invokeConnectorStudioTestCommand(
	t *testing.T,
	setup *connectorSetup,
	mux *http.ServeMux,
	sessionNonce string,
	commandID string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-ui-sessions/"+sessionNonce+"/commands/"+commandID, strings.NewReader(`{"parameters":{}}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func connectorStudioTestSessionNonce(t *testing.T, sessionRecorder *httptest.ResponseRecorder) string {
	t.Helper()
	var session connectorUISessionResponse
	if err := json.Unmarshal(sessionRecorder.Body.Bytes(), &session); err != nil || session.SessionNonce == "" {
		t.Fatalf("UI session response = %s, err = %v", sessionRecorder.Body.String(), err)
	}
	return session.SessionNonce
}

func TestTriggerBindingAPIWritesOnlyDeclaredBinding(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","threadTriggerMatcher":{"messageContains":"approval"}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/channelThreadCreated/slack-thread-approval-start", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "channelThreadCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-start")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("write status = %d: %s", recorder.Code, recorder.Body.String())
	}
	bindings, err := setup.store.listTriggerBindings("slack", "slack-workspace")
	if err != nil || len(bindings) != 1 || string(bindings[0].Configuration["channelId"]) != `"C123"` {
		t.Fatalf("bindings = %+v, err = %v", bindings, err)
	}

	request = authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/channelThreadCreated/undeclared", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "channelThreadCreated")
	request.SetPathValue("bindingName", "undeclared")
	recorder = httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("undeclared binding status = %d", recorder.Code)
	}
}

func TestTriggerBindingAPIAcceptsPartialUnitConfiguration(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","threadReplyMatcher":{"messageContains":"approve"}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/threadReplyCreated/slack-thread-approval-reply", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "threadReplyCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-reply")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("partial configuration status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestTriggerBindingAPIRejectsConfigurableRPCName(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","rpcName":"ApproveRequest","threadReplyMatcher":{"posterUserIds":["U123"]}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/threadReplyCreated/slack-thread-approval-reply", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "threadReplyCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-reply")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("configurable RPC name status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func authorizedConnectorRequest(t *testing.T, setup *connectorSetup, method string, target string, body *strings.Reader) *http.Request {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.Host = "127.0.0.1:8802"
	request.Header.Set("Origin", "http://127.0.0.1:8802")
	request.Header.Set(connectorCSRFHeader, setup.csrfToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, "sha256:test")
	return request
}

func connectorTriggerTestDefinitionProvider(t *testing.T) FlowDefinitionProvider {
	t.Helper()
	identity := connectorDefinitionIdentity{
		ConnectorID: "slack", OperationID: "postThreadReply", OperationKind: "mutation",
		ConnectionName: "slack-workspace", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}
	node := map[string]any{
		"id": "step:PostSlackCompletion", "name": "PostSlackCompletion", "kind": "step",
		"metadata": map[string]any{"connectorFactory": true, "connector": identity},
	}
	binding := connectorCatalogTriggerBinding{
		ConnectorID: "slack", TriggerName: "channelThreadCreated",
		ConnectionName: "slack-workspace", BindingName: "slack-thread-approval-start",
		ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, ConfigurationEnabled: true,
		ConfigurationUI: api.V2ConnectorConfigurationUI{Units: []api.V2ConnectorUIUnit{{
			ID: "channel", UnitID: "channelPicker", Label: "Channel", Bindings: []api.V2ConnectorUIBinding{{Port: "channelId", JSONPointer: "/channelId"}},
		}, {
			ID: "message", UnitID: "textInput", Label: "Message", Bindings: []api.V2ConnectorUIBinding{{Port: "text", JSONPointer: "/threadTriggerMatcher/messageContains"}},
		}}},
	}
	replyBinding := connectorCatalogTriggerBinding{
		ConnectorID: "slack", TriggerName: "threadReplyCreated",
		ConnectionName: "slack-workspace", BindingName: "slack-thread-approval-reply",
		ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, ConfigurationEnabled: true,
		ConfigurationUI: api.V2ConnectorConfigurationUI{Units: []api.V2ConnectorUIUnit{{
			ID: "channel", UnitID: "channelPicker", Label: "Channel", Bindings: []api.V2ConnectorUIBinding{{Port: "channelId", JSONPointer: "/channelId"}},
		}, {
			ID: "message", UnitID: "textInput", Label: "Message", Bindings: []api.V2ConnectorUIBinding{{Port: "text", JSONPointer: "/threadReplyMatcher/messageContains"}},
		}, {
			ID: "approvers", UnitID: "memberPicker", Label: "Approvers", Bindings: []api.V2ConnectorUIBinding{{Port: "memberIds", JSONPointer: "/threadReplyMatcher/posterUserIds"}},
		}}},
	}
	catalog, err := json.Marshal(map[string]any{
		"configured": true, "definitionRevision": "sha256:test", "definitions": []any{map[string]any{
			"flowName": "SlackEmailApprovalFlow", "graph": map[string]any{
				"nodes": []any{node}, "v2": map[string]any{"connectorTriggerBindings": []any{binding, replyBinding}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return staticFlowDefinitionProvider{snapshot: &FlowDefinitionSnapshot{
		Response: catalog, DefinitionRevision: "sha256:test", Source: "local", DefinitionCount: 1,
	}}
}

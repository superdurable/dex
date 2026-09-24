// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superdurable/dex/web/api"
)

func TestConnectorOAuthUsesPKCESingleUseStateAndDoesNotPersistClientOrRefreshSecret(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation", ConnectionName: "sender",
		ModulePath:    "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConfigurationEnabled: true,
	}
	var metadata []byte
	var receivedVerifier string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch filepath.Base(request.URL.Path) {
		case connectorReleaseDigestName:
			digest := sha256.Sum256(metadata)
			_, err := fmt.Fprintf(response, "%x  %s\n", digest, connectorReleaseMetadataName)
			if err != nil {
				t.Error(err)
			}
		case connectorReleaseMetadataName:
			_, err := response.Write(metadata)
			if err != nil {
				t.Error(err)
			}
		case "token":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			receivedVerifier = request.Form.Get("code_verifier")
			if request.Form.Get("client_secret") != "oauth-client-secret" || request.Form.Get("code") != "authorization-code" {
				t.Error("token exchange fields are missing")
			}
			response.Header().Set("Content-Type", "application/json")
			_, err := response.Write([]byte(`{"access_token":"provider-access-token","refresh_token":"must-not-persist","scope":"openid email https://www.googleapis.com/auth/gmail.send","expires_in":3600,"token_type":"Bearer"}`))
			if err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	release := connectorRelease{
		ConnectorID: identity.ConnectorID, ModulePath: identity.ModulePath, Version: identity.ModuleVersion,
		Tag: "connectors/google/gmail/v0.1.1", SourceSHA: "source-sha",
		ManifestSHA256: strings.Repeat("0", sha256.Size*2),
	}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = "gmail"
	release.Manifest.Spec.Provider = "google"
	release.Manifest.Spec.Auth.Type = "oauth2"
	release.Manifest.Spec.Auth.Fields = []connectorManifestField{
		{Name: "access_token", Type: "secretString", Required: true},
		{Name: "primary_email", Type: "string", Required: true},
	}
	release.Manifest.Spec.Auth.OAuth2 = &connectorManifestOAuth2{
		AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token",
		Scopes: []string{"openid", "email", "https://www.googleapis.com/auth/gmail.send"}, PKCE: true,
	}
	var err error
	metadata, err = json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	provider := connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity})
	setup := connectorTestSetup(t, t.TempDir(), provider)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()

	startBody := `{"clientId":"oauth-client-id","clientSecret":"oauth-client-secret","configuration":{},"credentialValues":{"primary_email":"owner@example.com"},"credentialSecrets":{}}`
	startRequest := httptest.NewRequest(http.MethodPost, "/api/v2/connector-connections/gmail/sender/oauth/start", strings.NewReader(startBody))
	startRequest.SetPathValue("connectorId", "gmail")
	startRequest.SetPathValue("connectionName", "sender")
	startRequest.Host = "127.0.0.1:8802"
	startRequest.Header.Set("Origin", "http://127.0.0.1:8802")
	startRequest.Header.Set(connectorCSRFHeader, setup.csrfToken)
	startRequest.Header.Set(api.V2DefinitionRevisionHeader, "sha256:test")
	startRecorder := httptest.NewRecorder()
	setup.handleOAuthStart(startRecorder, startRequest)
	if startRecorder.Code != http.StatusOK {
		t.Fatalf("OAuth start status = %d: %s", startRecorder.Code, startRecorder.Body.String())
	}
	var startResponse struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &startResponse); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(startResponse.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	challenge := authorizationURL.Query().Get("code_challenge")
	if state == "" || challenge == "" || authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %s", startResponse.AuthorizationURL)
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/v2/connector-oauth/callback?state="+url.QueryEscape(state)+"&code=authorization-code", nil)
	callbackRecorder := httptest.NewRecorder()
	setup.handleOAuthCallback(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusSeeOther {
		t.Fatalf("OAuth callback status = %d: %s", callbackRecorder.Code, callbackRecorder.Body.String())
	}
	verifierDigest := sha256.Sum256([]byte(receivedVerifier))
	if base64.RawURLEncoding.EncodeToString(verifierDigest[:]) != challenge {
		t.Fatal("PKCE verifier does not match authorization challenge")
	}
	connections, err := setup.store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || string(connections[0].Credentials["access_token"]) != `"provider-access-token"` {
		t.Fatalf("saved connections = %+v", connections)
	}
	encoded, err := json.Marshal(connections)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "oauth-client-secret") || strings.Contains(string(encoded), "must-not-persist") {
		t.Fatal("client or refresh secret persisted")
	}

	secondRecorder := httptest.NewRecorder()
	setup.handleOAuthCallback(secondRecorder, callback)
	if secondRecorder.Code != http.StatusBadRequest {
		t.Fatalf("reused OAuth state status = %d", secondRecorder.Code)
	}
}

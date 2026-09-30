// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
)

type unavailableProjectDefinitions struct{}

func (unavailableProjectDefinitions) Load(context.Context) (*FlowDefinitionSnapshot, error) {
	return nil, errors.New("no application build or FDG exists")
}

type projectOAuthProviderFixture struct {
	metadata         []byte
	exchanges        atomic.Int32
	receivedVerifier string
	failExchange     atomic.Bool
	afterExchange    func()
}

func (fixture *projectOAuthProviderFixture) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch filepath.Base(request.URL.Path) {
	case connectorReleaseDigestName:
		digest := sha256.Sum256(fixture.metadata)
		if _, err := fmt.Fprintf(response, "%x  %s\n", digest, connectorReleaseMetadataName); err != nil {
			panic(err)
		}
	case connectorReleaseMetadataName:
		if _, err := response.Write(fixture.metadata); err != nil {
			panic(err)
		}
	case "token":
		fixture.exchanges.Add(1)
		if err := request.ParseForm(); err != nil {
			http.Error(response, "invalid request", 400)
			return
		}
		fixture.receivedVerifier = request.Form.Get("code_verifier")
		if fixture.afterExchange != nil {
			fixture.afterExchange()
		}
		if fixture.failExchange.Load() {
			http.Error(response, "uncertain exchange", 503)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"access_token":"fixture-access-private","refresh_token":"fixture-refresh-private","scope":"openid email","expires_in":3600,"token_type":"Bearer"}`)); err != nil {
			panic(err)
		}
	case "userinfo":
		if request.Header.Get("Authorization") != "Bearer fixture-access-private" {
			http.Error(response, "unauthorized", 401)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"email":"owner@fixture.test","email_verified":true}`)); err != nil {
			panic(err)
		}
	default:
		http.NotFound(response, request)
	}
}

type projectWebFixture struct {
	bucket        string
	storagePrefix string
	test          *testing.T
	objects       projectconfig.ObjectStore
	scope         projectconfig.Scope
	provider      *httptest.Server
	providerState *projectOAuthProviderFixture
	setup         *connectorSetup
	handler       http.Handler
}

func newProjectWebFixture(test *testing.T) *projectWebFixture {
	test.Helper()
	endpoint, bucket := os.Getenv("DEX_PROJECT_CONFIG_TEST_ENDPOINT"), os.Getenv("DEX_PROJECT_CONFIG_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		test.Skip("set DEX_PROJECT_CONFIG_TEST_ENDPOINT and DEX_PROJECT_CONFIG_TEST_BUCKET for real versioned S3 integration")
	}
	cfg, err := awsconfig.LoadDefaultConfig(test.Context(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		test.Fatal(err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) { options.BaseEndpoint = aws.String(endpoint); options.UsePathStyle = true })
	prefix := "dex-web-integration/" + uuid.NewString()
	cleanup := &projectS3Cleanup{test: test, client: client, bucket: bucket, prefix: prefix + "/"}
	test.Cleanup(cleanup.run)
	objects, err := projectconfig.NewS3ObjectStore(test.Context(), &projectconfig.S3StoreConfig{Client: client, Bucket: bucket, Prefix: prefix, AllowUnencrypted: true})
	if err != nil {
		test.Fatal(err)
	}
	providerState := &projectOAuthProviderFixture{}
	provider := httptest.NewTLSServer(providerState)
	test.Cleanup(provider.Close)
	fixture := &projectWebFixture{bucket: bucket, storagePrefix: prefix, test: test, objects: objects, scope: projectconfig.Scope{ProjectID: "testproject", Kind: "live"}, provider: provider, providerState: providerState}
	release := connectorRelease{ConnectorID: "fixture", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/fixture", Version: "v1.0.0", Tag: "connectors/fixture/v1.0.0", SourceSHA: strings.Repeat("a", 40), ManifestSHA256: strings.Repeat("b", 64)}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = "fixture"
	release.Manifest.Metadata.DisplayName = "Fixture provider"
	release.Manifest.Spec.Provider = "fixture"
	release.Manifest.Spec.Auth.Methods = []connectorManifestAuthMethod{{ID: "oauth", Type: "oauth2", Fields: []connectorManifestField{{Name: "access_token", Type: "secretString", Required: true}, {Name: "refresh_token", Type: "secretString", Required: true}, {Name: "oauth_client_id", Type: "string", Required: true}, {Name: "oauth_client_secret", Type: "secretString", Required: true}, {Name: "primary_email", Type: "string", Required: true}}, OAuth2: &connectorManifestOAuth2{AuthorizationEndpoint: provider.URL + "/authorize", TokenEndpoint: provider.URL + "/token", Scopes: []string{"openid", "email"}, PKCE: true, ClientIDCredential: "oauth_client_id", ClientSecretCredential: "oauth_client_secret", CredentialMappings: []connectorOAuthCredentialMapping{{Credential: "access_token", Source: "access_token"}, {Credential: "refresh_token", Source: "refresh_token"}}, CredentialDerivations: []connectorOAuthCredentialDerivation{{Credential: "primary_email", Endpoint: provider.URL + "/userinfo", Source: "email", VerifiedBy: "email_verified"}}}}}
	providerState.metadata, err = json.Marshal(release)
	if err != nil {
		test.Fatal(err)
	}
	fixture.restart()
	return fixture
}

func (fixture *projectWebFixture) restart() {
	fixture.test.Helper()
	configuration, err := NewProjectConfiguration(&ProjectConfigurationConfig{Objects: fixture.objects, ProjectID: fixture.scope.ProjectID, ScopeKind: fixture.scope.Kind, SessionID: fixture.scope.SessionID, AdminToken: strings.Repeat("a", 32)})
	if err != nil {
		fixture.test.Fatal(err)
	}
	cfg := &Config{BindAddress: "0.0.0.0", ConnectorSetupEnabled: true, ConnectorSetupMode: ConnectorSetupModeProject, ConnectorCacheDirectory: fixture.test.TempDir(), ProjectConfiguration: configuration, TrustForwardedEmbeddingHeaders: true, WorkQueuePermissionMode: api.V2PermissionModeTrustedHeader}
	setup, err := newConnectorSetup(cfg, unavailableProjectDefinitions{})
	if err != nil {
		fixture.test.Fatal(err)
	}
	setup.releases.baseURL = fixture.provider.URL
	setup.releases.httpClient = fixture.provider.Client()
	setup.oauthHTTPClient = fixture.provider.Client()
	setup.oauthHTTPClient.CheckRedirect = rejectConnectorOAuthRedirect
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	mux.HandleFunc("GET /readyz", configuration.readinessHandler)
	mux.HandleFunc("GET /api/flow-definitions", serveFlowDefinitions(unavailableProjectDefinitions{}))
	fixture.setup, fixture.handler = setup, forwardedEmbeddingHandler(cfg, mux)
}

func (fixture *projectWebFixture) request(method, target string, body any, admin bool, actor string, configurationRevision, credentialRevision uint64) *httptest.ResponseRecorder {
	fixture.test.Helper()
	var contents []byte
	var err error
	if body != nil {
		contents, err = json.Marshal(body)
		if err != nil {
			fixture.test.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, "http://dex.internal"+target, bytes.NewReader(contents))
	request.Header.Set("X-Forwarded-Prefix", "/dex/projects/testproject/configuration/live")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "studio.fixture.test")
	request.Header.Set("X-Dex-Web-Embedded", "true")
	request.Header.Set("X-Dex-Web-CSRF-Token", "fixture-csrf")
	request.Header.Set(projectActorHeader, actor)
	request.Header.Set("Origin", "https://studio.fixture.test")
	request.Header.Set(connectorCSRFHeader, "fixture-csrf")
	request.Header.Set(projectManifestRevisionHeader, "1")
	request.Header.Set(projectConfigurationRevisionHeader, strconv.FormatUint(configurationRevision, 10))
	request.Header.Set(projectCredentialRevisionHeader, strconv.FormatUint(credentialRevision, 10))
	if admin {
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

func (fixture *projectWebFixture) installManifest() projectManifestWriteRequest {
	fixture.test.Helper()
	manifest := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz"},"flowDefinitions":[{"sourcePath":"internal/process/flow.go"}],"connectors":[{"connectorId":"fixture","modulePath":"github.com/superdurable/dex-connectors-library/connectors/fixture","connectionName":"sender","version":"v1.0.0","authMethodId":"oauth","operations":["send"]}]}`
	body := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(manifest)), Manifest: manifest}
	response := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", body, true, "owner", 0, 0)
	if response.Code != 200 {
		fixture.test.Fatalf("manifest status %d: %s", response.Code, response.Body.String())
	}
	return body
}

func (fixture *projectWebFixture) startOAuth() (*url.URL, string) {
	fixture.test.Helper()
	response := fixture.request(http.MethodPost, "/api/v2/connector-connections/fixture/sender/oauth/start", connectorOAuthStartRequest{AuthMethodID: "oauth", ClientID: "fixture-client", ClientSecret: "fixture-client-private", Configuration: map[string]json.RawMessage{}, CredentialValues: map[string]json.RawMessage{}, CredentialSecrets: map[string]string{}}, false, "owner", 0, 0)
	if response.Code != 200 {
		fixture.test.Fatalf("start status %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		fixture.test.Fatal(err)
	}
	target, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		fixture.test.Fatal(err)
	}
	return target, "/api/v2/connector-oauth/callback?state=" + url.QueryEscape(target.Query().Get("state")) + "&code=fixture-code"
}

func TestProjectConfigurationRealS3OAuthRestartAndSnapshot(test *testing.T) {
	fixture := newProjectWebFixture(test)
	manifest := fixture.installManifest()
	if response := fixture.request("GET", "/readyz", nil, false, "owner", 0, 0); response.Code != 200 {
		test.Fatalf("configuration readiness %d", response.Code)
	}
	if response := fixture.request("GET", "/api/flow-definitions", nil, false, "owner", 0, 0); response.Code != 503 {
		test.Fatalf("absent FDG unexpectedly authorized: %d", response.Code)
	}
	if response := fixture.request("GET", "/api/v2/connector-connections", nil, false, "owner", 0, 0); response.Code != 200 {
		test.Fatalf("before-build connections status %d", response.Code)
	}
	authorization, callback := fixture.startOAuth()
	fixture.restart()
	if response := fixture.request("GET", callback, nil, false, "wrong-actor", 1, 0); response.Code != 403 {
		test.Fatalf("wrong actor status %d", response.Code)
	}
	response := fixture.request("GET", callback, nil, false, "owner", 1, 0)
	if response.Code != 303 {
		test.Fatalf("restart callback status %d: %s", response.Code, response.Body.String())
	}
	challenge := sha256.Sum256([]byte(fixture.providerState.receivedVerifier))
	if base64.RawURLEncoding.EncodeToString(challenge[:]) != authorization.Query().Get("code_challenge") {
		test.Fatal("persisted PKCE verifier differs")
	}
	fixture.restart()
	if response = fixture.request("GET", callback, nil, false, "owner", 1, 0); response.Code != 303 {
		test.Fatalf("completed callback status %d", response.Code)
	}
	if fixture.providerState.exchanges.Load() != 1 {
		test.Fatal("OAuth code was exchanged more than once")
	}
	response = fixture.request("GET", "/api/v2/connector-connections", nil, false, "owner", 1, 0)
	if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-access-private") || strings.Contains(response.Body.String(), "fixture-refresh-private") || strings.Contains(response.Body.String(), "fixture-client-private") {
		test.Fatal("safe connection list failed or exposed credentials")
	}
	response = fixture.request("POST", "/api/v2/project-configuration/validate", projectConfigurationValidationRequest{AppManifestRevision: 1, ManifestDigest: manifest.ManifestDigest, ConfigurationRevision: 1}, true, "owner", 1, 0)
	if response.Code != 200 {
		test.Fatalf("validation status %d: %s", response.Code, response.Body.String())
	}
	var validated struct {
		Snapshot projectconfig.SnapshotRef `json:"snapshot"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &validated); err != nil {
		test.Fatal(err)
	}
	configurations, err := fixture.setup.project.configurationStore()
	if err != nil {
		test.Fatal(err)
	}
	document, err := configurations.ReadSnapshot(test.Context(), validated.Snapshot)
	if err != nil {
		test.Fatal(err)
	}
	contents, err := json.Marshal(document)
	if err != nil {
		test.Fatal(err)
	}
	if bytes.Contains(contents, []byte("private")) || bytes.Contains(contents, []byte("credentials")) {
		test.Fatal("validated ordinary snapshot contains credential material")
	}
	if len(document.Connections) != 1 || document.Connections[0].ConnectionName != "sender" {
		test.Fatal("logical credential reference missing")
	}
	if response = fixture.request("PUT", "/api/v2/project-configuration/app-manifest", manifest, true, "owner", 0, 0); response.Code != 409 {
		test.Fatalf("stale AppManifest CAS status %d", response.Code)
	}
	test.Log("real versioned S3: configuration before FDG, restart-safe OAuth, actor check, PKCE, single provider exchange, immutable secret-free snapshot passed")
}

func TestProjectConfigurationRealS3UncertainOAuthDoesNotReplay(test *testing.T) {
	fixture := newProjectWebFixture(test)
	fixture.installManifest()
	fixture.providerState.failExchange.Store(true)
	_, callback := fixture.startOAuth()
	response := fixture.request("GET", callback, nil, false, "owner", 1, 0)
	if response.Code != 503 {
		test.Fatalf("uncertain exchange status %d", response.Code)
	}
	fixture.restart()
	response = fixture.request("GET", callback, nil, false, "owner", 1, 0)
	if response.Code != 409 {
		test.Fatalf("pending exchange status %d: %s", response.Code, response.Body.String())
	}
	if fixture.providerState.exchanges.Load() != 1 {
		test.Fatal("ambiguous OAuth exchange was replayed")
	}
	test.Log("real versioned S3: uncertain provider outcome is fenced across restart")
}

type projectS3Cleanup struct {
	test           *testing.T
	client         *s3.Client
	bucket, prefix string
}

func (cleanup *projectS3Cleanup) run() {
	for {
		result, err := cleanup.client.ListObjectVersions(context.Background(), &s3.ListObjectVersionsInput{Bucket: aws.String(cleanup.bucket), Prefix: aws.String(cleanup.prefix), MaxKeys: aws.Int32(1000)})
		if err != nil {
			cleanup.test.Errorf("list fixture-owned versions: %v", err)
			return
		}
		objects := make([]types.ObjectIdentifier, 0, len(result.Versions)+len(result.DeleteMarkers))
		for _, version := range result.Versions {
			objects = append(objects, types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range result.DeleteMarkers {
			objects = append(objects, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(objects) == 0 {
			return
		}
		deleted, err := cleanup.client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{Bucket: aws.String(cleanup.bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
		if err != nil || len(deleted.Errors) > 0 {
			cleanup.test.Errorf("delete fixture-owned versions failed: %v", err)
			return
		}
	}
}

func TestProjectConfigurationRealS3ConcurrentCallbacksAndScopeIsolation(test *testing.T) {
	fixture := newProjectWebFixture(test)
	fixture.installManifest()
	_, callback := fixture.startOAuth()
	first := *fixture
	fixture.scope = projectconfig.Scope{ProjectID: "testproject", Kind: "preview", SessionID: "session-other"}
	fixture.restart()
	if response := fixture.request("GET", callback, nil, false, "owner", 1, 0); response.Code != 404 {
		test.Fatalf("cross-scope callback status %d", response.Code)
	}
	if fixture.providerState.exchanges.Load() != 0 {
		test.Fatal("cross-scope callback reached provider")
	}
	fixture.scope = projectconfig.Scope{ProjectID: "testproject", Kind: "live"}
	fixture.restart()
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- first.request("GET", callback, nil, false, "owner", 1, 0) }()
	go func() { responses <- fixture.request("GET", callback, nil, false, "owner", 1, 0) }()
	completed := 0
	for range 2 {
		response := <-responses
		if response.Code == 303 {
			completed++
		} else if response.Code != 409 {
			test.Fatalf("concurrent callback status %d: %s", response.Code, response.Body.String())
		}
	}
	if completed == 0 || fixture.providerState.exchanges.Load() != 1 {
		test.Fatal("concurrent callback admission did not permit exactly one exchange")
	}
	if response := fixture.request("GET", callback, nil, false, "owner", 1, 0); response.Code != 303 {
		test.Fatalf("completed callback recovery %d", response.Code)
	}
}

func TestProjectConfigurationRealS3ReplacementFencesOAuthBeforeProvider(test *testing.T) {
	fixture := newProjectWebFixture(test)
	fixture.installManifest()
	_, callback := fixture.startOAuth()
	_, err := fixture.setup.project.connections.ReplaceCredential(test.Context(), projectconfig.ConnectionKey{ConnectorID: "fixture", ConnectionName: "sender"}, 0, projectconfig.CredentialMaterial{ModuleVersion: "v1.0.0", AuthMethod: "oauth", Credentials: json.RawMessage(`{"access_token":"replacement-private"}`)})
	if err != nil {
		test.Fatal(err)
	}
	if response := fixture.request("GET", callback, nil, false, "owner", 1, 0); response.Code != 409 {
		test.Fatalf("replaced credential callback status %d", response.Code)
	}
	if fixture.providerState.exchanges.Load() != 0 {
		test.Fatal("stale OAuth callback reached provider after credential replacement")
	}
}

func TestProjectConfigurationRealS3APIKeyAndTriggerBeforeFDG(test *testing.T) {
	fixture := newProjectWebFixture(test)
	var release connectorRelease
	if err := json.Unmarshal(fixture.providerState.metadata, &release); err != nil {
		test.Fatal(err)
	}
	release.Manifest.Spec.Auth = connectorManifestAuth{Type: "apiKey", Fields: []connectorManifestField{{Name: "secret_key", Type: "secretString", Required: true}}}
	release.Manifest.Spec.Configuration.Fields = []connectorManifestField{{Name: "timeout", Type: "duration"}}
	release.Manifest.Spec.Triggers = []connectorManifestTrigger{{Name: "updated", Configuration: &connectorManifestAuthMethodConfiguration{Fields: []connectorManifestField{{Name: "eventTypes", Type: "stringList", Enum: []string{"created", "completed"}, UniqueItems: true}}}}}
	metadata, err := json.Marshal(release)
	if err != nil {
		test.Fatal(err)
	}
	fixture.providerState.metadata = metadata
	manifest := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz"},"flowDefinitions":[{"sourcePath":"internal/process/flow.go"}],"connectors":[{"connectorId":"fixture","modulePath":"github.com/superdurable/dex-connectors-library/connectors/fixture","connectionName":"sender","version":"v1.0.0","authMethodId":"default","operations":[],"triggerBindings":[{"triggerName":"updated","bindingName":"order-updates"}]}]}`
	manifestBody := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(manifest)), Manifest: manifest}
	if response := fixture.request("PUT", "/api/v2/project-configuration/app-manifest", manifestBody, true, "owner", 0, 0); response.Code != 200 {
		test.Fatalf("manifest status %d: %s", response.Code, response.Body.String())
	}
	body := connectorConnectionWriteRequest{ModulePath: release.ModulePath, ModuleVersion: release.Version, Provider: "fixture", AuthMethodID: "default", Configuration: map[string]json.RawMessage{"timeout": json.RawMessage(`"5m"`)}, Credentials: map[string]json.RawMessage{"secret_key": json.RawMessage(`"fixture-api-private"`)}}
	response := fixture.request("PUT", "/api/v2/connector-connections/fixture/sender", body, false, "owner", 0, 0)
	if response.Code != 200 {
		test.Fatalf("API key before FDG status %d: %s", response.Code, response.Body.String())
	}
	response = fixture.request("GET", "/api/v2/connector-connections", nil, false, "owner", 1, 1)
	var catalog connectorConnectionListResponse
	if err = json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		test.Fatal(err)
	}
	if response.Code != 200 || len(catalog.Connections) != 1 || len(catalog.Connections[0].TriggerUses) != 1 || !catalog.Connections[0].TriggerUses[0].SchemaAvailable {
		test.Fatalf("generic trigger schema missing before FDG: %s", response.Body.String())
	}
	validation := projectConfigurationValidationRequest{AppManifestRevision: 1, ManifestDigest: manifestBody.ManifestDigest, ConfigurationRevision: 1}
	if response = fixture.request("POST", "/api/v2/project-configuration/validate", validation, true, "owner", 1, 1); response.Code != 409 {
		test.Fatalf("missing trigger binding validation %d", response.Code)
	}
	triggerURL := "/api/v2/connector-trigger-bindings/fixture/sender/updated/order-updates"
	for _, invalid := range []string{`{"eventTypes":["unsupported"]}`, `{"eventTypes":["completed","completed"]}`, `{"undeclared":"secret"}`} {
		var values map[string]json.RawMessage
		if err = json.Unmarshal([]byte(invalid), &values); err != nil {
			test.Fatal(err)
		}
		response = fixture.request("PUT", triggerURL, connectorTriggerBindingWriteRequest{Configuration: values}, false, "owner", 1, 1)
		if response.Code != 400 {
			test.Fatalf("invalid trigger configuration status %d", response.Code)
		}
	}
	response = fixture.request("PUT", triggerURL, connectorTriggerBindingWriteRequest{Configuration: map[string]json.RawMessage{"eventTypes": json.RawMessage(`["completed"]`)}}, false, "owner", 1, 1)
	if response.Code != 200 {
		test.Fatalf("trigger before FDG status %d: %s", response.Code, response.Body.String())
	}
	validation.ConfigurationRevision = 2
	response = fixture.request("POST", "/api/v2/project-configuration/validate", validation, true, "owner", 2, 1)
	if response.Code != 200 {
		test.Fatalf("complete before-FDG validation %d: %s", response.Code, response.Body.String())
	}
	document, _, err := fixture.setup.project.readConfiguration(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(document.Connections[0].Configuration, &values); err != nil {
		test.Fatal(err)
	}
	if string(values["timeout"]) != "300000000000" {
		test.Fatal("duration was not normalized to exact nanoseconds")
	}
	if response = fixture.request("PUT", triggerURL, connectorTriggerBindingWriteRequest{Configuration: map[string]json.RawMessage{}}, false, "owner", 1, 1); response.Code != 409 {
		test.Fatalf("stale configuration CAS status %d", response.Code)
	}
	test.Log("real versioned S3: API-key fields, generic trigger schema/enum/uniqueness, duration normalization, exact draft CAS all work without FDG")
}

func TestProjectConfigurationRealS3ManifestChangeDuringOAuthFencesPublication(test *testing.T) {
	fixture := newProjectWebFixture(test)
	fixture.installManifest()
	_, callback := fixture.startOAuth()
	fixture.providerState.afterExchange = func() {
		record, object, err := fixture.setup.project.readManifest(test.Context())
		if err != nil {
			test.Error(err)
			return
		}
		record.Revision++
		contents, err := json.Marshal(record)
		if err != nil {
			test.Error(err)
			return
		}
		if _, err = fixture.setup.project.writeObject(test.Context(), fixture.setup.project.prefix+"/app-manifest/head", object.ETag, contents); err != nil {
			test.Error(err)
		}
	}
	response := fixture.request("GET", callback, nil, false, "owner", 1, 0)
	if response.Code != 409 {
		test.Fatalf("changed manifest callback status %d", response.Code)
	}
	connection, err := fixture.setup.project.connections.ReadConnection(test.Context(), projectconfig.ConnectionKey{ConnectorID: "fixture", ConnectionName: "sender"})
	if err != nil {
		test.Fatal(err)
	}
	if connection.Status != projectconfig.CredentialReauthorizationRequired || fixture.providerState.exchanges.Load() != 1 {
		test.Fatal("stale OAuth result was not fenced")
	}
}

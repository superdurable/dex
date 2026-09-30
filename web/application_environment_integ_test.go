// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"encoding/json"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type environmentUnknownWriteObjects struct{ projectconfig.ObjectStore }

func (store environmentUnknownWriteObjects) CreateObject(ctx context.Context, key string, data []byte) (projectconfig.Object, error) {
	object, err := store.ObjectStore.CreateObject(ctx, key, data)
	if err == nil {
		return projectconfig.Object{}, projectconfig.ErrOutcomeUnknown
	}
	return object, err
}
func (store environmentUnknownWriteObjects) CompareAndSwapObject(ctx context.Context, key, etag string, data []byte) (projectconfig.Object, error) {
	object, err := store.ObjectStore.CompareAndSwapObject(ctx, key, etag, data)
	if err == nil {
		return projectconfig.Object{}, projectconfig.ErrOutcomeUnknown
	}
	return object, err
}

func TestProjectApplicationEnvironmentVersionedStorage(t *testing.T) {
	fixture := newProjectWebFixture(t)
	raw := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz","environment":[{"name":"APP_ENV","required":true,"enum":["production","testing"]},{"name":"SIGNING_SECRET","secret":true,"required":true,"minLength":32}]},"flowDefinitions":[{"sourcePath":"internal/process/flow.go"}],"connectors":[]}`
	// Invalid declarations fail before any manifest or private object is accepted.
	for _, declaration := range []string{`{"name":"AWS_REGION"}`, `{"name":"HTTP_PROXY"}`, `{"name":"TOKEN","secret":true,"enum":["private"]}`, `{"name":"MODE","enum":["same","same"]}`, `{"name":"MODE","minLength":3,"enum":["a"]}`} {
		badRaw := strings.Replace(raw, `[{"name":"APP_ENV","required":true,"enum":["production","testing"]},{"name":"SIGNING_SECRET","secret":true,"required":true,"minLength":32}]`, "["+declaration+"]", 1)
		bad := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(badRaw)), Manifest: badRaw}
		result := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", bad, true, "owner", 0, 0)
		if result.Code != 400 {
			t.Fatalf("invalid declaration status %d", result.Code)
		}
	}
	manifest := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(raw)), Manifest: raw}
	response := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", manifest, true, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("manifest: %d %s", response.Code, response.Body.String())
	}
	var installed struct {
		EnvironmentContractDigest string              `json:"environmentContractDigest"`
		Scope                     projectconfig.Scope `json:"scope"`
		ConfigurationRevision     uint64              `json:"configurationRevision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	if installed.EnvironmentContractDigest != "sha256:607bf9a59849a614ac8c45886c3e4ff3cba7c4066fe4896f103943b5e02ca2e5" {
		t.Fatalf("semantic environment digest differs: %s", installed.EnvironmentContractDigest)
	}
	if installed.Scope != fixture.scope || installed.ConfigurationRevision != 0 {
		t.Fatal("admin manifest response lost project configuration scope/revision")
	}
	originalHandler := fixture.handler
	for _, header := range []string{connectorCSRFHeader, "Origin", projectActorHeader} {
		fixture.handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request.Header.Del(header)
			originalHandler.ServeHTTP(writer, request)
		})
		denied := fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"APP_ENV": "production"}}, false, "owner", 0, 0)
		if denied.Code != 403 {
			t.Fatalf("missing %s admitted: %d", header, denied.Code)
		}
	}
	fixture.handler = originalHandler
	validate := func(revision uint64) int {
		result := fixture.request(http.MethodPost, "/api/v2/project-configuration/validate", projectConfigurationValidationRequest{AppManifestRevision: 1, ManifestDigest: manifest.ManifestDigest, ConfigurationRevision: revision}, true, "owner", 0, 0)
		if result.Code == 200 {
			var metadata struct {
				Scope                 projectconfig.Scope `json:"scope"`
				ConfigurationRevision uint64              `json:"configurationRevision"`
			}
			if err := json.Unmarshal(result.Body.Bytes(), &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Scope != fixture.scope || metadata.ConfigurationRevision != revision {
				t.Fatal("validated metadata lost exact scope/revision")
			}
		}
		return result.Code
	}
	if status := validate(0); status != 409 {
		t.Fatalf("missing required fields: %d", status)
	}
	response = fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"APP_ENV": "production"}}, false, "", 0, 0)
	if response.Code != 403 {
		t.Fatalf("unauthenticated save: %d", response.Code)
	}
	secret := strings.Repeat("sensitive-original-", 3)
	baseObjects := fixture.objects
	fixture.objects = environmentUnknownWriteObjects{baseObjects}
	fixture.restart()
	response = fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"APP_ENV": "production"}, Secrets: map[string]string{"SIGNING_SECRET": secret}}, false, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("uncertain accepted save: %d %s", response.Code, response.Body.String())
	}
	safe := response.Body.String()
	for _, forbidden := range []string{secret, "secretRef", "app-secrets/", "sha256:"} {
		if strings.Contains(safe, forbidden) {
			t.Fatal("safe environment response exposes private material")
		}
	}
	if !strings.Contains(safe, `"configured":true`) || !strings.Contains(safe, `"value":"production"`) {
		t.Fatalf("safe values unavailable: %s", safe)
	}
	fixture.objects = baseObjects
	fixture.restart()
	if status := validate(1); status != 200 {
		t.Fatalf("configured fields: %d", status)
	}
	store, err := projectconfig.NewConfigurationStore(&projectconfig.ConfigurationStoreConfig{Objects: baseObjects, Scope: fixture.scope})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.FreezeConfiguration(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	object, err := baseObjects.ReadObject(t.Context(), snapshot.Key, snapshot.Version)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(object.Contents), secret) {
		t.Fatal("secret plaintext in accepted config")
	}
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for _, mode := range []string{"production", "testing"} {
		wait.Add(1)
		go func(mode string) {
			defer wait.Done()
			statuses <- fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"APP_ENV": mode}}, false, "owner", 1, 0).Code
		}(mode)
	}
	wait.Wait()
	close(statuses)
	success, conflict := 0, 0
	for status := range statuses {
		switch status {
		case 200:
			success++
		case 409:
			conflict++
		default:
			t.Fatalf("unexpected concurrent status %d", status)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS outcomes success=%d conflict=%d", success, conflict)
	}
	response = fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Secrets: map[string]string{"SIGNING_SECRET": strings.Repeat("replacement-private-", 3)}}, false, "owner", 2, 0)
	if response.Code != 200 {
		t.Fatalf("replace: %d %s", response.Code, response.Body.String())
	}
	accepted, err := store.ReadSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.ResolveApplicationEnvironment(t.Context(), accepted)
	if err != nil {
		t.Fatal(err)
	}
	original, ok := values.Lookup("SIGNING_SECRET")
	if !ok || original != secret {
		t.Fatal("accepted snapshot changed after replacement")
	}
	// The real application loader consumes the same exact historical document and private object versions.
	for name, value := range map[string]string{"DEX_PROJECT_ID": fixture.scope.ProjectID, "DEX_PROJECT_SCOPE": "live", "DEX_PROJECT_CONFIG_KEY": snapshot.Key, "DEX_PROJECT_CONFIG_VERSION": snapshot.Version, "DEX_PROJECT_CONFIG_DIGEST": snapshot.Digest, "DEX_PROJECT_STORAGE_BUCKET": fixture.bucket, "DEX_PROJECT_STORAGE_PREFIX": fixture.storagePrefix, "DEX_PROJECT_STORAGE_ENDPOINT": os.Getenv("DEX_PROJECT_CONFIG_TEST_ENDPOINT"), "DEX_PROJECT_ALLOW_LOCAL_STORAGE": "true", "DEX_PROJECT_STORAGE_KMS_KEY_ARN": "", "DEX_PROJECT_SESSION_ID": ""} {
		t.Setenv(name, value)
	}
	loaded, err := projectconfig.LoadFromEnvironment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, err := loaded.ResolveApplicationEnvironment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startupSecret, found := startup.Lookup("SIGNING_SECRET")
	if !found || startupSecret != secret {
		t.Fatal("startup did not preserve the accepted private version")
	}
	preview, err := projectconfig.NewConfigurationStore(&projectconfig.ConfigurationStoreConfig{Objects: baseObjects, Scope: projectconfig.Scope{ProjectID: fixture.scope.ProjectID, Kind: "preview", SessionID: "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = preview.ReadSnapshot(t.Context(), snapshot); err == nil {
		t.Fatal("preview read live snapshot")
	}
	for _, body := range []projectEnvironmentWriteRequest{{Values: map[string]string{"SIGNING_SECRET": "ordinary"}}, {Secrets: map[string]string{"SIGNING_SECRET": "short"}}, {Values: map[string]string{"HTTP_PROXY": "http://untrusted"}}, {Values: map[string]string{"APP_ENV": "unsupported"}}, {Values: map[string]string{"APP_ENV": "production"}, Remove: []string{"APP_ENV"}}} {
		response = fixture.request(http.MethodPut, "/api/v2/application-environment", body, false, "owner", 3, 0)
		if response.Code != 400 {
			t.Fatalf("invalid environment accepted: %d", response.Code)
		}
	}
	manifest.ExpectedRevision = 1
	manifest.SourceCommit = strings.Repeat("d", 40)
	response = fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", manifest, true, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("new manifest: %d", response.Code)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &installed); err != nil {
		t.Fatal(err)
	}
	if installed.Scope != fixture.scope || installed.ConfigurationRevision != 3 {
		t.Fatal("admin manifest response lost existing configuration revision")
	}
	response = fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"APP_ENV": "testing"}}, false, "owner", 3, 0)
	if response.Code != 409 {
		t.Fatalf("stale manifest save: %d", response.Code)
	}
	fixture.restart()
	response = fixture.request(http.MethodGet, "/api/v2/application-environment", nil, false, "owner", 0, 0)
	var result struct {
		AppManifestRevision   uint64                        `json:"appManifestRevision"`
		ConfigurationRevision uint64                        `json:"configurationRevision"`
		Fields                []projectEnvironmentFieldView `json:"fields"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || result.AppManifestRevision != 2 || result.ConfigurationRevision != 3 || len(result.Fields) != 2 {
		t.Fatalf("restart view: %d %s", response.Code, response.Body.String())
	}
}

type environmentManifestRaceObjects struct {
	projectconfig.ObjectStore
	beforeCommit func() error
}

func (store environmentManifestRaceObjects) CreateObject(ctx context.Context, key string, data []byte) (projectconfig.Object, error) {
	if strings.HasSuffix(key, "/configuration/head") {
		if err := store.beforeCommit(); err != nil {
			return projectconfig.Object{}, err
		}
	}
	return store.ObjectStore.CreateObject(ctx, key, data)
}
func (store environmentManifestRaceObjects) CompareAndSwapObject(ctx context.Context, key, etag string, data []byte) (projectconfig.Object, error) {
	if strings.HasSuffix(key, "/configuration/head") {
		if err := store.beforeCommit(); err != nil {
			return projectconfig.Object{}, err
		}
	}
	return store.ObjectStore.CompareAndSwapObject(ctx, key, etag, data)
}

func TestProjectApplicationEnvironmentManifestChangesDuringCAS(t *testing.T) {
	fixture := newProjectWebFixture(t)
	raw := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz","environment":[{"name":"SETTING","required":true}]},"flowDefinitions":[{"sourcePath":"internal/process/flow.go"}],"connectors":[]}`
	manifest := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(raw)), Manifest: raw}
	response := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", manifest, true, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("initial manifest: %d", response.Code)
	}
	changed := strings.Replace(raw, `"required":true`, `"required":true,"secret":true`, 1)
	next := projectManifestWriteRequest{ExpectedRevision: 1, SourceCommit: strings.Repeat("d", 40), ManifestDigest: projectDigest([]byte(changed)), Manifest: changed}
	objects := fixture.objects
	var once sync.Once
	fixture.objects = environmentManifestRaceObjects{ObjectStore: objects, beforeCommit: func() error {
		once.Do(func() {
			result := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", next, true, "owner", 0, 0)
			if result.Code != 200 {
				t.Fatalf("concurrent manifest: %d", result.Code)
			}
		})
		return nil
	}}
	fixture.restart()
	response = fixture.request(http.MethodPut, "/api/v2/application-environment", projectEnvironmentWriteRequest{Values: map[string]string{"SETTING": "previously-ordinary"}}, false, "owner", 0, 0)
	if response.Code != 409 {
		t.Fatalf("cross-manifest save appeared accepted: %d", response.Code)
	}
	fixture.objects = objects
	fixture.restart()
	response = fixture.request(http.MethodGet, "/api/v2/application-environment", nil, false, "owner", 0, 0)
	if response.Code != 200 || strings.Contains(response.Body.String(), "previously-ordinary") {
		t.Fatal("changed secret declaration exposed a previous ordinary value")
	}
	response = fixture.request(http.MethodPost, "/api/v2/project-configuration/validate", projectConfigurationValidationRequest{AppManifestRevision: 2, ManifestDigest: next.ManifestDigest, ConfigurationRevision: 1}, true, "owner", 0, 0)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "APPLICATION_ENVIRONMENT_NOT_READY") {
		t.Fatalf("changed declaration accepted stale configuration: %d %s", response.Code, response.Body.String())
	}
}

func TestProjectApplicationEnvironmentBrowser(t *testing.T) {
	if os.Getenv("DEX_PROJECT_CONFIG_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set an installed Playwright module path for actual browser acceptance")
	}
	fixture := newProjectWebFixture(t)
	raw := `{"schemaVersion":"superverse.dev/dex-app/v1","application":{"port":8080,"healthPath":"/healthz","environment":[{"name":"APP_ENV","required":true,"enum":["production"]},{"name":"SIGNING_SECRET","required":true,"secret":true,"minLength":32}]},"flowDefinitions":[{"sourcePath":"internal/process/flow.go"}],"connectors":[]}`
	manifest := projectManifestWriteRequest{SourceCommit: strings.Repeat("c", 40), ManifestDigest: projectDigest([]byte(raw)), Manifest: raw}
	response := fixture.request(http.MethodPut, "/api/v2/project-configuration/app-manifest", manifest, true, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("browser manifest: %d", response.Code)
	}
	mux := http.NewServeMux()
	fixture.setup.registerHandlers(mux)
	mux.Handle("/", spaHandler(os.DirFS("assets/dist"), api.V2PermissionModeTrustedHeader))
	forwarded := forwardedEmbeddingHandler(&Config{TrustForwardedEmbeddingHeaders: true}, mux)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Explicit authenticated embedding fixture; this does not substitute for platform BFF acceptance.
		request.Header.Set("X-Forwarded-Prefix", "/")
		request.Header.Set("X-Forwarded-Proto", "http")
		request.Header.Set("X-Forwarded-Host", request.Host)
		request.Header.Set("X-Dex-Web-Embedded", "true")
		request.Header.Set("X-Dex-Web-CSRF-Token", "fixture-csrf")
		request.Header.Set(projectActorHeader, "fixture-owner")
		forwarded.ServeHTTP(writer, request)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "script/application_environment_browser.mjs")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DEX_PROJECT_CONFIG_BROWSER_URL=" + server.URL, "DEX_PROJECT_CONFIG_PLAYWRIGHT_MODULE=" + os.Getenv("DEX_PROJECT_CONFIG_PLAYWRIGHT_MODULE"), "DEX_PROJECT_CONFIG_CHROMIUM=" + os.Getenv("DEX_PROJECT_CONFIG_CHROMIUM")}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native browser check: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	response = fixture.request(http.MethodPost, "/api/v2/project-configuration/validate", projectConfigurationValidationRequest{AppManifestRevision: 1, ManifestDigest: manifest.ManifestDigest, ConfigurationRevision: 1}, true, "owner", 0, 0)
	if response.Code != 200 {
		t.Fatalf("browser-saved environment did not validate: %d %s", response.Code, response.Body.String())
	}
}

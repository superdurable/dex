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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const standaloneTestModulePath = "github.com/superdurable/dex-connectors-library/connectors/google/gmail/go"

func standaloneTestIdentity() connectorDefinitionIdentity {
	return connectorDefinitionIdentity{
		ConnectorID: "gmail", ConnectionName: "support-mail", ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2",
	}
}

func TestStandaloneConnectionIsAddedAuthorizedListedAndDeletedWithoutAFlow(t *testing.T) {
	directory := t.TempDir()
	setup := connectorTestSetup(t, directory, connectorTestDefinitionProvider(t, nil))
	installConnectorTestReleaseWithAuth(t, setup, standaloneTestIdentity(), "google", connectorManifestAuth{
		Type: "apiKey", Fields: []connectorManifestField{{Name: "access_token", Type: "secretString", Required: true}},
	}, connectorManifestField{Name: "endpoint", Type: "string"})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	added := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections",
		`{"connectorId":"gmail","connectionName":"support-mail","modulePath":"`+standaloneTestModulePath+`","moduleVersion":"v0.25.2"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", added.Code, added.Body.String())
	}
	var addedView connectorConnectionView
	if err := json.Unmarshal(added.Body.Bytes(), &addedView); err != nil {
		t.Fatal(err)
	}
	if addedView.Status != "Missing" || !addedView.IsStandalone || addedView.ModuleVersion != "v0.25.2" {
		t.Fatalf("added view = %+v", addedView)
	}
	if _, err := os.Stat(filepath.Join(directory, connectorConnectionsFileName)); !os.IsNotExist(err) {
		t.Fatalf("adding a connection must not write the connection file, stat err = %v", err)
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Missing" || !views[0].IsStandalone || views[0].Uses == nil {
		t.Fatalf("pending views = %+v", views)
	}

	session := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-ui-sessions",
		`{"connectorId":"gmail","connectionName":"support-mail"}`)
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"provider":"google"`) {
		t.Fatalf("UI session status = %d: %s", session.Code, session.Body.String())
	}
	saved := sendStandaloneTestRequest(t, setup, mux, http.MethodPut, "/api/v2/connector-connections/gmail/support-mail",
		`{"modulePath":"`+standaloneTestModulePath+`","moduleVersion":"v0.25.2","provider":"google",`+
			`"configuration":{"endpoint":"https://gmail.example"},"credentials":{"access_token":"standalone-secret"}}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", saved.Code, saved.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "gmail", "support-mail")
	if stored.ModulePath != standaloneTestModulePath || stored.ModuleVersion != "v0.25.2" || stored.Provider != "google" ||
		string(stored.Configuration["endpoint"]) != `"https://gmail.example"` || string(stored.Credentials["access_token"]) != `"standalone-secret"` {
		t.Fatalf("stored connection = %+v", stored)
	}
	if len(setup.pendingConnectionSnapshot()) != 0 {
		t.Fatal("a saved connection must leave the pending set")
	}

	restarted := connectorTestSetup(t, directory, connectorTestDefinitionProvider(t, nil))
	installConnectorTestRelease(t, restarted, standaloneTestIdentity(), "google", nil)
	views = listStandaloneTestViews(t, restarted)
	if len(views) != 1 || views[0].Status != "Ready" || !views[0].IsStandalone || views[0].Provider != "google" ||
		len(views[0].StoredCredentialFields) != 1 {
		t.Fatalf("restarted views = %+v", views)
	}
	restartedMux := http.NewServeMux()
	restarted.registerHandlers(restartedMux)
	deleted := sendStandaloneTestRequest(t, restarted, restartedMux, http.MethodDelete, "/api/v2/connector-connections/gmail/support-mail", "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete status = %d: %s", deleted.Code, deleted.Body.String())
	}
	if views = listStandaloneTestViews(t, restarted); len(views) != 0 {
		t.Fatalf("views after delete = %+v", views)
	}
}

func TestStandaloneConnectionAddRejectsInvalidAndExistingConnections(t *testing.T) {
	flowIdentity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation", ConnectionName: "sender",
		ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2", ConfigurationEnabled: true,
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{flowIdentity}))
	installConnectorTestRelease(t, setup, standaloneTestIdentity(), "google", []connectorManifestField{
		{Name: "access_token", Type: "secretString", Required: true},
	})
	stored := testLocalConnectorConnection("gmail", "stored-mail", "token", nil)
	stored.ModulePath, stored.ModuleVersion = standaloneTestModulePath, "v0.25.2"
	if err := setup.store.put(stored, nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	addBody := func(connectionName string, modulePath string, moduleVersion string) string {
		encoded, err := json.Marshal(connectorConnectionAddRequest{
			ConnectorID: "gmail", ConnectionName: connectionName, ModulePath: modulePath, ModuleVersion: moduleVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, testCase := range []struct {
		name       string
		body       string
		statusCode int
		errorCode  string
	}{
		{"empty name", addBody("", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"surrounding space", addBody(" support ", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"slash", addBody("team/support", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"newline", addBody("support\nmail", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"too long", addBody(strings.Repeat("a", connectorConnectionNameLimit+1), standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"unofficial module", addBody("support", "example.com/gmail", "v0.25.2"), http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
		{"inexact version", addBody("support", standaloneTestModulePath, "latest"), http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
		{"used by a Flow", addBody("sender", standaloneTestModulePath, "v0.25.2"), http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS"},
		{"already stored", addBody("stored-mail", standaloneTestModulePath, "v0.25.2"), http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS"},
		{"unknown field", `{"connectorId":"gmail","connectionName":"support","modulePath":"` + standaloneTestModulePath + `","moduleVersion":"v0.25.2","extra":true}`, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
	} {
		recorder := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", testCase.body)
		if recorder.Code != testCase.statusCode || !strings.Contains(recorder.Body.String(), `"`+testCase.errorCode+`"`) {
			t.Fatalf("%s: status = %d: %s", testCase.name, recorder.Code, recorder.Body.String())
		}
	}

	forged := httptest.NewRequest(http.MethodPost, "/api/v2/connector-connections", strings.NewReader(addBody("support", standaloneTestModulePath, "v0.25.2")))
	forged.Host = "127.0.0.1:8802"
	forged.Header.Set("Origin", "http://attacker.example")
	forged.Header.Set(connectorCSRFHeader, setup.csrfToken)
	forgedRecorder := httptest.NewRecorder()
	mux.ServeHTTP(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin add status = %d: %s", forgedRecorder.Code, forgedRecorder.Body.String())
	}

	first := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.2"))
	again := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.2"))
	other := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.1"))
	if first.Code != http.StatusOK || again.Code != http.StatusOK || other.Code != http.StatusConflict {
		t.Fatalf("repeated add statuses = %d, %d, %d: %s", first.Code, again.Code, other.Code, other.Body.String())
	}
	removed := sendStandaloneTestRequest(t, setup, mux, http.MethodDelete, "/api/v2/connector-connections/gmail/support", "")
	if removed.Code != http.StatusOK || !strings.Contains(removed.Body.String(), `"deleted":true`) {
		t.Fatalf("remove pending status = %d: %s", removed.Code, removed.Body.String())
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 2 || views[0].ConnectionName != "sender" || views[0].IsStandalone ||
		views[1].ConnectionName != "stored-mail" || !views[1].IsStandalone || views[1].Status != "Ready" {
		t.Fatalf("views = %+v", views)
	}
}

func TestStandaloneSlackConnectionCompletesOAuthWithoutAFlow(t *testing.T) {
	setup, mux := newStandaloneSlackOAuthTest(t, nil)
	addStandaloneSlackTestConnection(t, setup, mux, "v1.3.0")
	callback := finishStandaloneSlackTestOAuth(t, mux, startStandaloneSlackTestOAuth(t, setup, mux))
	if callback.Code != http.StatusSeeOther || !strings.Contains(callback.Header().Get("Location"), "connectionName=support-slack") {
		t.Fatalf("OAuth callback status = %d: %s", callback.Code, callback.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "slack", "support-slack")
	if stored.ModulePath != standaloneSlackTestModulePath || stored.ModuleVersion != "v1.3.0" || stored.Provider != "slack" {
		t.Fatalf("stored Slack connection = %+v", stored)
	}
	assertConnectorTestCredentials(t, stored, map[string]any{
		"oauth_client_id": "client", "oauth_client_secret": "secret",
		"bot_token": "xoxb-bot", "user_token": "xoxp-user", "app_token": "xapp-app",
	})
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Ready" || !views[0].IsStandalone {
		t.Fatalf("views = %+v", views)
	}
}

func TestStandaloneSlackOAuthIsRefusedAfterTheConnectionIsDeletedAndAddedAgain(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		readdVersion    string
		isDuringOAuth   bool
		wantErrorCode   string
		wantPendingView string
	}{
		{"same release before the callback", "v1.3.0", false, "CONNECTOR_CONNECTION_CHANGED", "v1.3.0"},
		{"another release while tokens are exchanged", "v1.4.0", true, "CONNECTOR_CONNECTION_CHANGED", "v1.4.0"},
		{"another release before the callback", "v1.4.0", false, "FLOW_DEFINITION_REVISION_CONFLICT", "v1.4.0"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var setup *connectorSetup
			var mux *http.ServeMux
			readd := func() {
				deleted := sendStandaloneTestRequest(t, setup, mux, http.MethodDelete, "/api/v2/connector-connections/slack/support-slack", "")
				if deleted.Code != http.StatusOK {
					t.Errorf("delete status = %d: %s", deleted.Code, deleted.Body.String())
				}
				addStandaloneSlackTestConnection(t, setup, mux, testCase.readdVersion)
			}
			var onTokenRequest func()
			if testCase.isDuringOAuth {
				onTokenRequest = readd
			}
			setup, mux = newStandaloneSlackOAuthTest(t, onTokenRequest)
			addStandaloneSlackTestConnection(t, setup, mux, "v1.3.0")
			state := startStandaloneSlackTestOAuth(t, setup, mux)
			if !testCase.isDuringOAuth {
				readd()
			}
			callback := finishStandaloneSlackTestOAuth(t, mux, state)
			if callback.Code != http.StatusConflict || !strings.Contains(callback.Body.String(), `"`+testCase.wantErrorCode+`"`) {
				t.Fatalf("OAuth callback status = %d: %s", callback.Code, callback.Body.String())
			}
			if _, isStored, err := setup.store.get("slack", "support-slack"); err != nil || isStored {
				t.Fatalf("a stale OAuth callback stored the connection: stored = %v, err = %v", isStored, err)
			}
			views := listStandaloneTestViews(t, setup)
			if len(views) != 1 || views[0].Status != "Missing" || views[0].ModuleVersion != testCase.wantPendingView {
				t.Fatalf("the added connection must stay pending: %+v", views)
			}
		})
	}
}

func TestStandaloneConnectionSaveIsRefusedAfterAConcurrentReleaseChange(t *testing.T) {
	setup, mux := newStandaloneSlackOAuthTest(t, nil)
	addStandaloneSlackTestConnection(t, setup, mux, "v1.3.0")
	staleWrite := standaloneTestWriteExpectation(t, setup)
	deleted := sendStandaloneTestRequest(t, setup, mux, http.MethodDelete, "/api/v2/connector-connections/slack/support-slack", "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", deleted.Code, deleted.Body.String())
	}
	addStandaloneSlackTestConnection(t, setup, mux, "v1.4.0")
	connection := testLocalConnectorConnection("slack", "support-slack", "xoxb-stale", nil)
	connection.ModulePath, connection.ModuleVersion = standaloneSlackTestModulePath, "v1.3.0"
	ctx := context.Background()
	if err := setup.putConnectionIfCurrent(ctx, connection, nil, staleWrite); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("stale save err = %v", err)
	}
	connection.ModuleVersion = "v1.4.0"
	if err := setup.putConnectionIfCurrent(ctx, connection, nil, staleWrite); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("stale save with the new release err = %v", err)
	}
	currentWrite := standaloneTestWriteExpectation(t, setup)
	connection.ModuleVersion = "v1.3.0"
	if err := setup.putConnectionIfCurrent(ctx, connection, nil, currentWrite); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("save of another release than the added one err = %v", err)
	}
	connection.ModuleVersion = "v1.4.0"
	if err := setup.putConnectionIfCurrent(ctx, connection, nil, currentWrite); err != nil {
		t.Fatalf("current save err = %v", err)
	}
	if stored := getConnectorTestConnection(t, setup, "slack", "support-slack"); stored.ModuleVersion != "v1.4.0" {
		t.Fatalf("stored = %+v", stored)
	}
	if err := setup.putConnectionIfCurrent(ctx, connection, nil, currentWrite); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("a save that expects no record must not replace one: err = %v", err)
	}
}

func TestFlowConnectionSaveIsRefusedWhenTheFlowMovesToAnotherReleaseDuringIt(t *testing.T) {
	for _, path := range []string{"PUT", "OAuth"} {
		t.Run(path, func(t *testing.T) {
			race := newFlowReleaseRaceTest(t)
			var response *httptest.ResponseRecorder
			if path == "PUT" {
				race.setHook(&race.onReleaseDownload, race.moveFlowToV2AndSaveIt)
				response = sendStandaloneTestRequest(t, race.setup, race.mux, http.MethodPut, "/api/v2/connector-connections/slack/workspace",
					`{"modulePath":"`+standaloneSlackTestModulePath+`","moduleVersion":"v1.3.0","provider":"slack","configuration":{},`+
						`"credentials":{"bot_token":"xoxb-v1","user_token":"xoxp-v1"}}`)
			} else {
				state := startStandaloneSlackTestOAuthFor(t, race.setup, race.mux, "workspace")
				race.setHook(&race.onTokenRequest, race.moveFlowToV2AndSaveIt)
				response = finishStandaloneSlackTestOAuth(t, race.mux, state)
			}
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"CONNECTOR_CONNECTION_CHANGED"`) {
				t.Fatalf("%s status = %d: %s", path, response.Code, response.Body.String())
			}
			stored := getConnectorTestConnection(t, race.setup, "slack", "workspace")
			if stored.ModuleVersion != "v1.4.0" || string(stored.Credentials["bot_token"]) != `"xoxb-v2"` {
				t.Fatalf("the v1 save overwrote the v2 connection: %+v", stored)
			}
		})
	}
}

func TestTriggerBindingWriteIsRefusedAfterItsConnectionIsDeleted(t *testing.T) {
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	connection := testLocalConnectorConnection("slack", "workspace", "xoxb", nil)
	if err := setup.store.put(connection, nil); err != nil {
		t.Fatal(err)
	}
	binding := localConnectorTriggerBinding{
		ConnectorID: "slack", ConnectionName: "workspace", TriggerName: "threadReplyCreated", BindingName: "approvals",
		Configuration: map[string]json.RawMessage{},
	}
	if err := setup.store.putTriggerBinding(binding); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.deleteConnection("slack", "workspace"); err != nil {
		t.Fatal(err)
	}
	if err := setup.store.putTriggerBinding(binding); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("binding write after delete err = %v", err)
	}
	if _, err := setup.store.list(); err != nil {
		t.Fatalf("the connection file must stay loadable: %v", err)
	}

	if err := setup.store.put(connection, nil); err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for index := range 20 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if index%2 == 0 {
				if _, err := setup.deleteConnection("slack", "workspace"); err != nil {
					t.Error(err)
				}
				return
			}
			if err := setup.store.putTriggerBinding(binding); err != nil && !errors.Is(err, errConnectorConnectionChanged) {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	if _, err := setup.store.list(); err != nil {
		t.Fatalf("interleaved deletes and binding writes left an invalid file: %v", err)
	}
}

// flowReleaseRaceTest declares Slack v1.3.0 in a Flow that a hook moves to v1.4.0 while a save is in progress.
type flowReleaseRaceTest struct {
	t                 *testing.T
	hooksMu           sync.Mutex
	setup             *connectorSetup
	mux               *http.ServeMux
	provider          *switchableFlowDefinitionProvider
	v2Snapshot        *FlowDefinitionSnapshot
	onReleaseDownload func()
	onTokenRequest    func()
}

func newFlowReleaseRaceTest(t *testing.T) *flowReleaseRaceTest {
	t.Helper()
	race := &flowReleaseRaceTest{t: t}
	declare := func(version string) *FlowDefinitionSnapshot {
		return connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{{
			ConnectorID: "slack", OperationID: "postThreadReply", OperationKind: "mutation", ConnectionName: "workspace",
			ModulePath: standaloneSlackTestModulePath, ModuleVersion: version, ConfigurationEnabled: true,
		}}).(staticFlowDefinitionProvider).snapshot
	}
	race.provider = &switchableFlowDefinitionProvider{snapshot: declare("v1.3.0")}
	race.v2Snapshot = declare("v1.4.0")
	race.v2Snapshot.DefinitionRevision = "sha256:test-v2"
	setup, mux := newStandaloneSlackOAuthTestWithHooks(t, race.provider, func() {
		race.runHookOnce(&race.onReleaseDownload)
	}, func() {
		race.runHookOnce(&race.onTokenRequest)
	})
	race.setup, race.mux = setup, mux
	return race
}

func (race *flowReleaseRaceTest) setHook(hook *func(), action func()) {
	race.hooksMu.Lock()
	defer race.hooksMu.Unlock()
	*hook = action
}

func (race *flowReleaseRaceTest) runHookOnce(hook *func()) {
	race.hooksMu.Lock()
	action := *hook
	*hook = nil
	race.hooksMu.Unlock()
	if action != nil {
		action()
	}
}

// moveFlowToV2AndSaveIt plays another request that saves the connection after the Flow moved to v1.4.0.
func (race *flowReleaseRaceTest) moveFlowToV2AndSaveIt() {
	race.provider.replace(race.v2Snapshot)
	connection := testLocalConnectorConnection("slack", "workspace", "unused", nil)
	connection.ModulePath, connection.ModuleVersion, connection.Provider = standaloneSlackTestModulePath, "v1.4.0", "slack"
	connection.Credentials = map[string]json.RawMessage{"bot_token": json.RawMessage(`"xoxb-v2"`), "user_token": json.RawMessage(`"xoxp-v2"`)}
	if err := race.setup.store.put(connection, nil); err != nil {
		race.t.Error(err)
	}
}

type switchableFlowDefinitionProvider struct {
	mu       sync.Mutex
	snapshot *FlowDefinitionSnapshot
}

func (provider *switchableFlowDefinitionProvider) Load(context.Context) (*FlowDefinitionSnapshot, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.snapshot, nil
}

func (provider *switchableFlowDefinitionProvider) replace(snapshot *FlowDefinitionSnapshot) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.snapshot = snapshot
}

func standaloneTestWriteExpectation(t *testing.T, setup *connectorSetup) connectorConnectionWriteExpectation {
	t.Helper()
	generation := setup.connectionGeneration("slack", "support-slack")
	snapshot, err := setup.flowDefinitions.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	identity, _, _, err := setup.resolveConnectionIdentity(snapshot.Response, "slack", "support-slack")
	if err != nil {
		t.Fatal(err)
	}
	expectation, err := setup.newConnectorConnectionWriteExpectation(snapshot.Response, identity, generation)
	if err != nil {
		t.Fatal(err)
	}
	return expectation
}

func TestStandaloneConnectionAddRequiresTheCSRFToken(t *testing.T) {
	setup, mux := newStandaloneSlackOAuthTest(t, nil)
	body := `{"connectorId":"slack","connectionName":"support-slack","modulePath":"` + standaloneSlackTestModulePath + `","moduleVersion":"v1.3.0"}`
	for _, token := range []string{"", "wrong-token"} {
		request := authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-connections", strings.NewReader(body))
		if token == "" {
			request.Header.Del(connectorCSRFHeader)
		} else {
			request.Header.Set(connectorCSRFHeader, token)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `"CONNECTOR_WRITE_FORBIDDEN"`) {
			t.Fatalf("token %q: status = %d: %s", token, recorder.Code, recorder.Body.String())
		}
	}
	if views := listStandaloneTestViews(t, setup); len(views) != 0 {
		t.Fatalf("a refused add left views %+v", views)
	}
}

const standaloneSlackTestModulePath = "github.com/superdurable/dex-connectors-library/connectors/slack/go"

// newStandaloneSlackOAuthTest serves Slack releases v1.3.0 and v1.4.0 and a token endpoint that calls onTokenRequest first.
func newStandaloneSlackOAuthTest(t *testing.T, onTokenRequest func()) (*connectorSetup, *http.ServeMux) {
	t.Helper()
	return newStandaloneSlackOAuthTestWithHooks(t, connectorTestDefinitionProvider(t, nil), nil, onTokenRequest)
}

// newStandaloneSlackOAuthTestWithHooks also takes the Flow definitions and a hook that runs before each release download.
func newStandaloneSlackOAuthTestWithHooks(
	t *testing.T,
	flowDefinitions FlowDefinitionProvider,
	onReleaseDownload func(),
	onTokenRequest func(),
) (*connectorSetup, *http.ServeMux) {
	t.Helper()
	metadataByVersion := map[string][]byte{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		version := filepath.Base(filepath.Dir(request.URL.Path))
		switch filepath.Base(request.URL.Path) {
		case connectorReleaseDigestName:
			digest := sha256.Sum256(metadataByVersion[version])
			if _, err := fmt.Fprintf(response, "%x  %s\n", digest, connectorReleaseMetadataName); err != nil {
				t.Error(err)
			}
		case connectorReleaseMetadataName:
			if onReleaseDownload != nil {
				onReleaseDownload()
			}
			if _, err := response.Write(metadataByVersion[version]); err != nil {
				t.Error(err)
			}
		case "token":
			if onTokenRequest != nil {
				onTokenRequest()
			}
			if _, err := response.Write([]byte(`{"ok":true,"access_token":"xoxb-bot","scope":"chat:write",` +
				`"authed_user":{"access_token":"xoxp-user","scope":"channels:history"}}`)); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	for _, version := range []string{"v1.3.0", "v1.4.0"} {
		release := connectorRelease{
			ConnectorID: "slack", ModulePath: standaloneSlackTestModulePath, Version: version,
			Tag: "connectors/slack/go/" + version, SourceSHA: "source-sha", ManifestSHA256: strings.Repeat("0", sha256.Size*2),
		}
		release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
		release.Manifest.Kind = "Connector"
		release.Manifest.Metadata.Name = "slack"
		release.Manifest.Spec.Provider = "slack"
		release.Manifest.Spec.Auth.Type = "oauth2"
		release.Manifest.Spec.Auth.Fields = []connectorManifestField{
			{Name: "oauth_client_id", Type: "string"},
			{Name: "oauth_client_secret", Type: "secretString"},
			{Name: "bot_token", Type: "secretString", Required: true},
			{Name: "user_token", Type: "secretString", Required: true},
			{Name: "app_token", Type: "secretString"},
		}
		release.Manifest.Spec.Auth.OAuth2 = &connectorManifestOAuth2{
			AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token",
			ClientIDCredential: "oauth_client_id", ClientSecretCredential: "oauth_client_secret",
			Scopes: []string{"chat:write"}, UserScopes: []string{"channels:history"},
			CredentialMappings: []connectorOAuthCredentialMapping{
				{Credential: "bot_token", Source: "access_token"},
				{Credential: "user_token", Source: "authed_user.access_token"},
			},
		}
		metadata, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		metadataByVersion[version] = metadata
	}
	setup := connectorTestSetup(t, t.TempDir(), flowDefinitions)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	return setup, mux
}

func addStandaloneSlackTestConnection(t *testing.T, setup *connectorSetup, mux *http.ServeMux, version string) {
	t.Helper()
	added := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections",
		`{"connectorId":"slack","connectionName":"support-slack","modulePath":"`+standaloneSlackTestModulePath+`","moduleVersion":"`+version+`"}`)
	if added.Code != http.StatusOK {
		t.Errorf("add %s status = %d: %s", version, added.Code, added.Body.String())
	}
}

// startStandaloneSlackTestOAuth starts OAuth for support-slack and returns its state.
func startStandaloneSlackTestOAuth(t *testing.T, setup *connectorSetup, mux *http.ServeMux) string {
	t.Helper()
	return startStandaloneSlackTestOAuthFor(t, setup, mux, "support-slack")
}

// startStandaloneSlackTestOAuthFor starts OAuth for the named Slack connection and returns its state.
func startStandaloneSlackTestOAuthFor(t *testing.T, setup *connectorSetup, mux *http.ServeMux, connectionName string) string {
	t.Helper()
	started := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections/slack/"+connectionName+"/oauth/start",
		`{"clientId":"client","clientSecret":"secret","configuration":{},"credentialValues":{},"credentialSecrets":{"app_token":"xapp-app"}}`)
	if started.Code != http.StatusOK {
		t.Fatalf("OAuth start status = %d: %s", started.Code, started.Body.String())
	}
	var startResponse struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &startResponse); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(startResponse.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	return authorizationURL.Query().Get("state")
}

func finishStandaloneSlackTestOAuth(t *testing.T, mux *http.ServeMux, state string) *httptest.ResponseRecorder {
	t.Helper()
	callback := httptest.NewRecorder()
	mux.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/api/v2/connector-oauth/callback?state="+
		url.QueryEscape(state)+"&code=authorization-code", nil))
	return callback
}

func TestStandaloneStoredConnectionWithoutOfficialReleaseIsUnsupported(t *testing.T) {
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	connection := testLocalConnectorConnection("custom", "local", "token", nil)
	connection.ModulePath, connection.ModuleVersion = "example.com/custom", "v0.0.0-20260101000000-abcdef"
	if err := setup.store.put(connection, nil); err != nil {
		t.Fatal(err)
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Unsupported" || !views[0].IsStandalone {
		t.Fatalf("views = %+v", views)
	}
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	session := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-ui-sessions",
		`{"connectorId":"custom","connectionName":"local"}`)
	if session.Code != http.StatusNotFound || !strings.Contains(session.Body.String(), `"CONNECTOR_CONNECTION_UNSUPPORTED"`) {
		t.Fatalf("UI session status = %d: %s", session.Code, session.Body.String())
	}
}

func TestConnectorCatalogListsPublicReleasesAndLocalOverrides(t *testing.T) {
	overrideIdentity := connectorDefinitionIdentity{
		ConnectorID: "slack", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack/go", ModuleVersion: "v1.4.0-rc.1",
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Slack</main>"))
	release := connectorTestRelease(overrideIdentity, archive, connectorUIHostAPIRange)
	release.Manifest.Metadata.DisplayName = "Slack (local)"
	overrideDirectory := connectorLocalReleaseTestDirectory(t, release, archive)
	setup, err := newConnectorSetup(&Config{
		BindAddress: "127.0.0.1", ConnectorSetupEnabled: true, ConnectorConfigDirectory: t.TempDir(),
		ConnectorReleaseOverrides: map[string]string{"slack": overrideDirectory},
	}, connectorTestDefinitionProvider(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	catalogServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if _, err := response.Write([]byte(`{"apiVersion":"connectors.dex.dev/catalog/v1alpha1","kind":"ConnectorCatalog","connectors":[` +
			`{"id":"slack","name":"Slack","version":"v1.3.0","languages":{"go":"v1.3.0"},"directory":"connectors/slack"},` +
			`{"id":"gmail","name":"Gmail","description":"Send mail.","version":"v0.25.2","languages":{"go":"v0.25.2","python":"v0.1.0"},"directory":"connectors/google/gmail"},` +
			`{"id":"bad id","name":"Bad","version":"v0.1.0","languages":{"go":"v0.1.0"},"directory":"connectors/bad"},` +
			`{"id":"nogo","name":"No Go","version":"v0.1.0","languages":{},"directory":"connectors/nogo"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer catalogServer.Close()
	setup.publicCatalog.url = catalogServer.URL
	setup.publicCatalog.httpClient = catalogServer.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	var listed connectorCatalogListResponse
	for range 2 {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-catalog", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("catalog status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("public catalog requests = %d, want one cached fetch", requests)
	}
	want := []connectorCatalogEntry{
		{ConnectorID: "gmail", DisplayName: "Gmail", Description: "Send mail.", ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2"},
		{ConnectorID: "slack", DisplayName: "Slack (local)", ModulePath: overrideIdentity.ModulePath, ModuleVersion: "v1.4.0-rc.1", LocalOverride: true},
	}
	if !listed.IsPublicCatalogAvailable || fmt.Sprint(listed.Connectors) != fmt.Sprint(want) {
		t.Fatalf("catalog = %+v", listed)
	}

	unavailable := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	unavailable.publicCatalog.url = catalogServer.URL + "/missing"
	unavailable.publicCatalog.httpClient = &http.Client{Transport: connectorRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("offline")
	})}
	unavailableMux := http.NewServeMux()
	unavailable.registerHandlers(unavailableMux)
	recorder := httptest.NewRecorder()
	unavailableMux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-catalog", nil))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"CONNECTOR_CATALOG_UNAVAILABLE"`) {
		t.Fatalf("offline catalog status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func sendStandaloneTestRequest(
	t *testing.T,
	setup *connectorSetup,
	mux *http.ServeMux,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, authorizedConnectorRequest(t, setup, method, target, strings.NewReader(body)))
	return recorder
}

func listStandaloneTestViews(t *testing.T, setup *connectorSetup) []connectorConnectionView {
	t.Helper()
	views, _, err := setup.connectionViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return views
}

func TestConnectorCatalogSharesOneFetchAndRemembersAFailure(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	catalogServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		<-release
		if _, err := response.Write([]byte(`{"apiVersion":"connectors.dex.dev/catalog/v1alpha1","kind":"ConnectorCatalog","connectors":[` +
			`{"id":"gmail","name":"Gmail","version":"v0.25.2","languages":{"go":"v0.25.2"},"directory":"connectors/google/gmail"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer catalogServer.Close()
	catalog := newConnectorPublicCatalog()
	catalog.url = catalogServer.URL
	catalog.httpClient = catalogServer.Client()
	var waiters sync.WaitGroup
	results := make(chan int, 5)
	for range 5 {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			entries, err := catalog.list(context.Background())
			if err != nil {
				t.Error(err)
			}
			results <- len(entries)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for requests.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the shared fetch did not start")
		}
		runtime.Gosched()
	}
	close(release)
	waiters.Wait()
	close(results)
	for count := range results {
		if count != 1 {
			t.Fatalf("entries = %d", count)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("catalog requests = %d, want one shared fetch", requests.Load())
	}

	var attempts atomic.Int32
	offline := newConnectorPublicCatalog()
	offline.httpClient = &http.Client{Transport: connectorRoundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, fmt.Errorf("offline")
	})}
	for range 3 {
		if _, err := offline.list(context.Background()); err == nil {
			t.Fatal("an offline catalog returned entries")
		}
	}
	if attempts.Load() != 1 {
		t.Fatalf("offline attempts = %d, want one remembered failure", attempts.Load())
	}
	offline.mu.Lock()
	offline.failedAt = time.Now().Add(-connectorPublicCatalogRetryDelay)
	offline.mu.Unlock()
	if _, err := offline.list(context.Background()); err == nil || attempts.Load() != 2 {
		t.Fatalf("retry after the delay: err = %v, attempts = %d", err, attempts.Load())
	}
}

func TestConnectorCatalogServesAStaleCopyAndCoolsDownAfterAFailedRefresh(t *testing.T) {
	var attempts atomic.Int32
	var isOffline atomic.Bool
	catalogServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		if isOffline.Load() {
			http.Error(response, "offline", http.StatusServiceUnavailable)
			return
		}
		if _, err := response.Write([]byte(`{"apiVersion":"connectors.dex.dev/catalog/v1alpha1","kind":"ConnectorCatalog","connectors":[` +
			`{"id":"gmail","name":"Gmail","version":"v0.25.2","languages":{"go":"v0.25.2"},"directory":"connectors/google/gmail"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer catalogServer.Close()
	catalog := newConnectorPublicCatalog()
	catalog.url = catalogServer.URL
	catalog.httpClient = catalogServer.Client()
	if entries, err := catalog.list(context.Background()); err != nil || len(entries) != 1 {
		t.Fatalf("first list: %v, %v", entries, err)
	}
	isOffline.Store(true)
	catalog.mu.Lock()
	catalog.fetchedAt = time.Now().Add(-connectorPublicCatalogTTL)
	catalog.mu.Unlock()
	for range 3 {
		if entries, err := catalog.list(context.Background()); err != nil || len(entries) != 1 {
			t.Fatalf("stale list: %v, %v", entries, err)
		}
	}
	if attempts.Load() != 2 {
		t.Fatalf("catalog requests = %d, want one failed refresh and then a cooldown", attempts.Load())
	}
	catalog.mu.Lock()
	catalog.failedAt = time.Now().Add(-connectorPublicCatalogRetryDelay)
	catalog.mu.Unlock()
	isOffline.Store(false)
	if entries, err := catalog.list(context.Background()); err != nil || len(entries) != 1 || attempts.Load() != 3 {
		t.Fatalf("refresh after the cooldown: %v, %v, attempts = %d", entries, err, attempts.Load())
	}
}

func TestConnectorCatalogFetchOutlivesTheCallerThatStartedIt(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	catalogServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		<-release
		if _, err := response.Write([]byte(`{"apiVersion":"connectors.dex.dev/catalog/v1alpha1","kind":"ConnectorCatalog","connectors":[` +
			`{"id":"gmail","name":"Gmail","version":"v0.25.2","languages":{"go":"v0.25.2"},"directory":"connectors/google/gmail"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer catalogServer.Close()
	catalog := newConnectorPublicCatalog()
	catalog.url = catalogServer.URL
	catalog.httpClient = catalogServer.Client()
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := catalog.list(firstCtx)
		firstResult <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for requests.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the shared fetch did not start")
		}
		runtime.Gosched()
	}
	secondResult := make(chan []connectorCatalogEntry, 1)
	go func() {
		entries, err := catalog.list(context.Background())
		if err != nil {
			t.Error(err)
		}
		secondResult <- entries
	}()
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller err = %v", err)
	}
	close(release)
	if entries := <-secondResult; len(entries) != 1 {
		t.Fatalf("the second caller's entries = %v", entries)
	}
	if requests.Load() != 1 {
		t.Fatalf("catalog requests = %d, want the canceled caller's fetch to serve the second caller", requests.Load())
	}
}

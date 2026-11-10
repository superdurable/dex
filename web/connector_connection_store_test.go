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
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConnectorConnectionStoreRoundTripsAuthMethodIDsAndCopiesKeptCredentials(t *testing.T) {
	directory := t.TempDir()
	store, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	connection := testLocalConnectorConnection("llm", "default", "unused", nil)
	connection.AuthMethodIDs = []string{"anthropic", "openai"}
	connection.Credentials = map[string]json.RawMessage{
		"auth_methods":      json.RawMessage(`["anthropic","openai"]`),
		"openai_api_key":    json.RawMessage(`"openai-secret"`),
		"anthropic_api_key": json.RawMessage(`"anthropic-secret"`),
	}
	if err := store.putCurrentForTest(connection, nil); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	connections, err := reloaded.list()
	if err != nil || len(connections) != 1 {
		t.Fatalf("connections = %+v, err = %v", connections, err)
	}
	if !reflect.DeepEqual(connections[0].AuthMethodIDs, []string{"anthropic", "openai"}) ||
		!reflect.DeepEqual(connections[0].StoredCredentialFields, []string{"anthropic_api_key", "openai_api_key"}) {
		t.Fatalf("reloaded connection = %+v", connections[0])
	}

	next := testLocalConnectorConnection("llm", "default", "unused", nil)
	next.AuthMethodIDs = []string{"openai"}
	next.Credentials = map[string]json.RawMessage{"auth_methods": json.RawMessage(`["openai"]`)}
	if err := reloaded.putCurrentForTest(next, []string{"openai_api_key"}); err != nil {
		t.Fatal(err)
	}
	stored, found, err := reloaded.get("llm", "default")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	assertConnectorTestCredentials(t, stored, map[string]any{"auth_methods": []any{"openai"}, "openai_api_key": "openai-secret"})

	removed := testLocalConnectorConnection("llm", "default", "unused", nil)
	removed.Credentials = map[string]json.RawMessage{}
	if err := reloaded.putCurrentForTest(removed, []string{"anthropic_api_key"}); !errors.Is(err, errConnectorConfigurationRevisionConflict) {
		t.Fatalf("keep of a field no longer stored error = %v", err)
	}
	unsaved := testLocalConnectorConnection("llm", "unsaved", "unused", nil)
	if err := reloaded.putCurrentForTest(unsaved, []string{"access_token"}); !errors.Is(err, errConnectorConfigurationRevisionConflict) {
		t.Fatalf("keep without a stored record error = %v", err)
	}
	both := testLocalConnectorConnection("llm", "default", "unused", nil)
	both.AuthMethodID = "openai"
	both.AuthMethodIDs = []string{"openai"}
	if err := validateLocalConnectorConnection(both); err == nil {
		t.Fatal("a record with authMethodId and authMethodIds was accepted")
	}
}

func TestConnectorConnectionStorePersistsAcrossRestartAndIsolatesDirectories(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "shared")
	firstStore, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("connection directory permissions = %o", directoryInfo.Mode().Perm())
	}
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	connection := testLocalConnectorConnection("gmail", "sender", "token-one", &expiresAt)
	if err := firstStore.putCurrentForTest(connection, nil); err != nil {
		t.Fatal(err)
	}

	secondStore, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	connections, err := secondStore.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].ConnectionName != "sender" {
		t.Fatalf("restarted store connections = %+v", connections)
	}

	isolatedStore, err := newConnectorConnectionStore(filepath.Join(t.TempDir(), "isolated"))
	if err != nil {
		t.Fatal(err)
	}
	isolatedConnections, err := isolatedStore.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(isolatedConnections) != 0 {
		t.Fatalf("isolated store connections = %+v", isolatedConnections)
	}

	info, err := os.Stat(firstStore.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("connection file permissions = %o", info.Mode().Perm())
	}
}

func TestConnectorConnectionStoreRejectsSymlinkAndWidePermissions(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	contents := []byte(`{"schemaVersion":"connectors.dex.dev/local-connections/v1alpha1","connections":[]}`)
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, connectorConnectionsFileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := newConnectorConnectionStore(directory); err == nil {
		t.Fatal("expected symlink credential file to fail")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newConnectorConnectionStore(directory); err == nil {
		t.Fatal("expected wide credential file permissions to fail")
	}
}

func TestConnectorConnectionStoreAtomicallyReplacesAndDeletesRecord(t *testing.T) {
	store, err := newConnectorConnectionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connection := testLocalConnectorConnection("github", "reviewer", "token-one", nil)
	if err := store.putCurrentForTest(connection, nil); err != nil {
		t.Fatal(err)
	}
	connection.Credentials["access_token"] = json.RawMessage(`"token-two"`)
	if err := store.putCurrentForTest(connection, nil); err != nil {
		t.Fatal(err)
	}
	connections, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || string(connections[0].Credentials["access_token"]) != `"token-two"` {
		t.Fatalf("updated connections = %+v", connections)
	}
	deleted, err := store.deleteCurrentForTest("github", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected connection to be deleted")
	}
}

func TestConnectorConnectionStoreKeepsTriggerBindingsSeparateAndDeletesThemWithConnection(t *testing.T) {
	store, err := newConnectorConnectionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.putCurrentForTest(testLocalConnectorConnection("slack", "slack-workspace", "bot-token", nil), nil); err != nil {
		t.Fatal(err)
	}
	binding := localConnectorTriggerBinding{
		ConnectorID: "slack", ConnectionName: "slack-workspace", TriggerName: "channelThreadCreated",
		BindingName: "slack-email-approval-start", Configuration: map[string]json.RawMessage{"channelId": json.RawMessage(`"C123"`)},
	}
	if err := store.putTriggerBindingCurrentForTest(binding); err != nil {
		t.Fatal(err)
	}
	bindings, err := store.listTriggerBindings("slack", "slack-workspace")
	if err != nil || len(bindings) != 1 || bindings[0].BindingName != binding.BindingName {
		t.Fatalf("bindings = %+v, err = %v", bindings, err)
	}
	deleted, err := store.deleteCurrentForTest("slack", "slack-workspace")
	if err != nil || !deleted {
		t.Fatalf("deleted = %v, err = %v", deleted, err)
	}
	bindings, err = store.listTriggerBindings("slack", "slack-workspace")
	if err != nil || len(bindings) != 0 {
		t.Fatalf("bindings after delete = %+v, err = %v", bindings, err)
	}
}

func TestConnectorConnectionStoreAuthorizationIDChangesOnlyWithAuthorizations(t *testing.T) {
	store, err := newConnectorConnectionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connection := testLocalConnectorConnection("slack", "workspace", "token-one", nil)
	connection.AuthorizationID = "ignored-caller-value"
	if err := store.putCurrentForTest(connection, nil); err != nil {
		t.Fatal(err)
	}
	added := requireConnectorTestAuthorizationID(t, store, "slack", "workspace")

	configurationEdit := testLocalConnectorConnection("slack", "workspace", "unused", nil)
	configurationEdit.Configuration = map[string]json.RawMessage{"teamDomain": json.RawMessage(`"example"`)}
	configurationEdit.Credentials = map[string]json.RawMessage{}
	if err := store.putCurrentForTest(configurationEdit, []string{"access_token"}); err != nil {
		t.Fatal(err)
	}
	if got := requireConnectorTestAuthorizationID(t, store, "slack", "workspace"); got != added {
		t.Fatalf("authorizationId after a configuration edit = %q, want %q", got, added)
	}

	binding := localConnectorTriggerBinding{
		ConnectorID: "slack", ConnectionName: "workspace", TriggerName: "channelThreadCreated",
		BindingName: "approvals", Configuration: map[string]json.RawMessage{"channelId": json.RawMessage(`"C123"`)},
	}
	if err := store.putTriggerBindingCurrentForTest(binding); err != nil {
		t.Fatal(err)
	}
	if got := requireConnectorTestAuthorizationID(t, store, "slack", "workspace"); got != added {
		t.Fatalf("authorizationId after a Trigger binding write = %q, want %q", got, added)
	}

	credentialEdit := testLocalConnectorConnection("slack", "workspace", "token-two", nil)
	if err := store.putCurrentForTest(credentialEdit, nil); err != nil {
		t.Fatal(err)
	}
	edited := requireConnectorTestAuthorizationID(t, store, "slack", "workspace")
	if edited == added {
		t.Fatal("a credential edit kept the authorizationId")
	}

	fileLock, err := store.lockConnectionsFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.revision("slack", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	err = store.put(fileLock, credentialEdit, nil, revision, true)
	fileLock.release()
	if err != nil {
		t.Fatal(err)
	}
	if got := requireConnectorTestAuthorizationID(t, store, "slack", "workspace"); got == edited {
		t.Fatal("a completed authorization with the same credentials kept the authorizationId")
	}
}

func TestConnectorConnectionStoreKeepsRecordsWithoutOrWithExternalAuthorizationID(t *testing.T) {
	directory := t.TempDir()
	contents := `{
  "schemaVersion": "connectors.dex.dev/local-connections/v1alpha1",
  "connections": [
    {
      "connectorId": "github",
      "modulePath": "github.com/superdurable/dex-connectors-library/connectors/github",
      "moduleVersion": "v0.1.1",
      "provider": "github",
      "connectionName": "legacy",
      "configuration": {},
      "credentials": {
        "app_key": {
          "id": 7,
          "secret": "legacy-secret"
        }
      }
    },
    {
      "connectorId": "slack",
      "modulePath": "github.com/superdurable/dex-connectors-library/connectors/slack",
      "moduleVersion": "v0.1.1",
      "provider": "slack",
      "connectionName": "workspace",
      "configuration": {},
      "credentials": {
        "access_token": "application-refreshed-token"
      },
      "authorizationId": "8d6f5f1e-3b52-4c3e-9a4e-2f0a7c1d9b10"
    }
  ]
}
`
	if err := os.WriteFile(filepath.Join(directory, connectorConnectionsFileName), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	legacyRevision, err := store.revision("github", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.putCurrentForTest(testLocalConnectorConnection("gmail", "sender", "gmail-token", nil), nil); err != nil {
		t.Fatal(err)
	}
	binding := localConnectorTriggerBinding{
		ConnectorID: "slack", ConnectionName: "workspace", TriggerName: "channelThreadCreated",
		BindingName: "approvals", Configuration: map[string]json.RawMessage{},
	}
	if err := store.putTriggerBindingCurrentForTest(binding); err != nil {
		t.Fatal(err)
	}
	legacyRevisionAfterWrites, err := store.revision("github", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if legacyRevisionAfterWrites != legacyRevision {
		t.Fatal("writing other records changed the revision of a record without authorizationId")
	}
	assertConnectorTestStoredAuthorizationID(t, store, "github", "legacy", "")
	assertConnectorTestStoredAuthorizationID(t, store, "slack", "workspace", "8d6f5f1e-3b52-4c3e-9a4e-2f0a7c1d9b10")
	slack, _, err := store.get("slack", "workspace")
	if err != nil || string(slack.Credentials["access_token"]) != `"application-refreshed-token"` {
		t.Fatalf("slack credentials = %s, err = %v", slack.Credentials, err)
	}

	legacyEdit := localConnectorConnection{
		ConnectorID: "github", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/github",
		ModuleVersion: "v0.1.1", Provider: "github", ConnectionName: "legacy",
		Configuration: map[string]json.RawMessage{"owner": json.RawMessage(`"example"`)},
		Credentials:   map[string]json.RawMessage{},
	}
	if err := store.putCurrentForTest(legacyEdit, []string{"app_key"}); err != nil {
		t.Fatal(err)
	}
	assertConnectorTestStoredAuthorizationID(t, store, "github", "legacy", "")
	legacyEdit.Credentials = map[string]json.RawMessage{"app_key": json.RawMessage(`{"id":7,"secret":"legacy-secret"}`)}
	legacyEdit.Configuration = map[string]json.RawMessage{}
	if err := store.putCurrentForTest(legacyEdit, nil); err != nil {
		t.Fatal(err)
	}
	assertConnectorTestStoredAuthorizationID(t, store, "github", "legacy", "")
}

// requireConnectorTestAuthorizationID returns the stored authorizationId after checking it is an RFC 4122 UUIDv4.
func requireConnectorTestAuthorizationID(t *testing.T, store *connectorConnectionStore, connectorID string, connectionName string) string {
	t.Helper()
	stored, found, err := store.get(connectorID, connectionName)
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	parsed, err := uuid.Parse(stored.AuthorizationID)
	if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != stored.AuthorizationID {
		t.Fatalf("authorizationId = %q is not a canonical UUIDv4: %v", stored.AuthorizationID, err)
	}
	return stored.AuthorizationID
}

func assertConnectorTestStoredAuthorizationID(
	t *testing.T,
	store *connectorConnectionStore,
	connectorID string,
	connectionName string,
	expected string,
) {
	t.Helper()
	stored, found, err := store.get(connectorID, connectionName)
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if stored.AuthorizationID != expected {
		t.Fatalf("%s/%s authorizationId = %q, want %q", connectorID, connectionName, stored.AuthorizationID, expected)
	}
}

func testLocalConnectorConnection(
	connectorID string,
	connectionName string,
	token string,
	expiresAt *time.Time,
) localConnectorConnection {
	return localConnectorConnection{
		ConnectorID: connectorID, ModulePath: "github.com/superdurable/dex-connectors-library/connectors/" + connectorID,
		ModuleVersion: "v0.1.1", Provider: connectorID, ConnectionName: connectionName,
		Configuration:       map[string]json.RawMessage{},
		Credentials:         map[string]json.RawMessage{"access_token": json.RawMessage(`"` + token + `"`)},
		CredentialExpiresAt: expiresAt,
	}
}

// putCurrentForTest saves connection against the record revision stored now.
func (store *connectorConnectionStore) putCurrentForTest(connection localConnectorConnection, keepCredentialFields []string) error {
	fileLock, err := store.lockConnectionsFile(context.Background())
	if err != nil {
		return err
	}
	defer fileLock.release()
	revision, err := store.revision(connection.ConnectorID, connection.ConnectionName)
	if err != nil {
		return err
	}
	return store.put(fileLock, connection, keepCredentialFields, revision, false)
}

// putLockedForTest takes the connections file lock under ctx and saves connection against expectedRevision.
func (store *connectorConnectionStore) putLockedForTest(
	ctx context.Context,
	connection localConnectorConnection,
	expectedRevision string,
) error {
	fileLock, err := store.lockConnectionsFile(ctx)
	if err != nil {
		return err
	}
	defer fileLock.release()
	return store.put(fileLock, connection, nil, expectedRevision, false)
}

// putTriggerBindingCurrentForTest saves binding against the connection and binding revisions stored now.
func (store *connectorConnectionStore) putTriggerBindingCurrentForTest(binding localConnectorTriggerBinding) error {
	fileLock, err := store.lockConnectionsFile(context.Background())
	if err != nil {
		return err
	}
	defer fileLock.release()
	connectionRevision, err := store.revision(binding.ConnectorID, binding.ConnectionName)
	if err != nil {
		return err
	}
	bindingRevision, err := store.triggerBindingRevision(binding)
	if err != nil {
		return err
	}
	return store.putTriggerBinding(fileLock, binding, connectionRevision, bindingRevision)
}

// deleteCurrentForTest deletes the connection against the record revision stored now.
func (store *connectorConnectionStore) deleteCurrentForTest(connectorID string, connectionName string) (bool, error) {
	fileLock, err := store.lockConnectionsFile(context.Background())
	if err != nil {
		return false, err
	}
	defer fileLock.release()
	revision, err := store.revision(connectorID, connectionName)
	if err != nil {
		return false, err
	}
	return store.delete(fileLock, connectorID, connectionName, revision)
}

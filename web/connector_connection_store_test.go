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
	"testing"
	"time"
)

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
	if err := firstStore.put(connection); err != nil {
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
	if err := store.put(connection); err != nil {
		t.Fatal(err)
	}
	connection.Credentials["access_token"] = json.RawMessage(`"token-two"`)
	if err := store.put(connection); err != nil {
		t.Fatal(err)
	}
	connections, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || string(connections[0].Credentials["access_token"]) != `"token-two"` {
		t.Fatalf("updated connections = %+v", connections)
	}
	deleted, err := store.delete("github", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected connection to be deleted")
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

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build darwin || linux

package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	connectionsLockHolderLockPathEnvironment        = "DEX_WEB_TEST_CONNECTIONS_LOCK_PATH"
	connectionsLockHolderReplacementPathEnvironment = "DEX_WEB_TEST_CONNECTIONS_REPLACEMENT_PATH"
	connectionsLockHolderTargetPathEnvironment      = "DEX_WEB_TEST_CONNECTIONS_TARGET_PATH"
)

// TestConnectorConnectionsFileLockHolderProcess is the external lock holder, using plain flock(2) like the connectors Go SDK.
func TestConnectorConnectionsFileLockHolderProcess(t *testing.T) {
	lockPath := os.Getenv(connectionsLockHolderLockPathEnvironment)
	if lockPath == "" {
		return
	}
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if replacementPath := os.Getenv(connectionsLockHolderReplacementPathEnvironment); replacementPath != "" {
		if err := os.Rename(replacementPath, os.Getenv(connectionsLockHolderTargetPathEnvironment)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lockFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorConnectionStoreWriteWaitsForExternalLockAndKeepsExternalWrite(t *testing.T) {
	directory := t.TempDir()
	store, err := newConnectorConnectionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.putCurrentForTest(testLocalConnectorConnection("github", "reviewer", "token-one", nil), nil); err != nil {
		t.Fatal(err)
	}
	external := readConnectorConnectionsFileForTest(t, store)
	external.Connections = append(external.Connections, testLocalConnectorConnection("slack", "workspace", "xoxb", nil))
	holder := startConnectionsLockHolderForTest(t, store, &external)

	done := make(chan error, 1)
	go func() {
		done <- store.put(t.Context(), testLocalConnectorConnection("gmail", "sender", "gmail-token", nil), nil, "")
	}()
	select {
	case err := <-done:
		t.Fatalf("write finished while another process held the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	holder.release(t)
	if err := receiveConnectorWriteResultForTest(t, done); err != nil {
		t.Fatal(err)
	}

	connections, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(connections))
	for _, connection := range connections {
		names = append(names, connection.ConnectorID+"/"+connection.ConnectionName)
	}
	if strings.Join(names, ",") != "github/reviewer,gmail/sender,slack/workspace" {
		t.Fatalf("connections after the external write = %v", names)
	}
}

func TestConnectorConnectionStoreRefusesStaleWriteAfterExternalCredentialWriteBack(t *testing.T) {
	store, err := newConnectorConnectionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.putCurrentForTest(testLocalConnectorConnection("github", "reviewer", "token-one", nil), nil); err != nil {
		t.Fatal(err)
	}
	authorizedRevision, err := store.revision("github", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	external := readConnectorConnectionsFileForTest(t, store)
	external.Connections[0].Credentials["access_token"] = json.RawMessage(`"token-refreshed"`)
	holder := startConnectionsLockHolderForTest(t, store, &external)

	done := make(chan error, 1)
	go func() {
		edited := testLocalConnectorConnection("github", "reviewer", "token-from-browser", nil)
		done <- store.put(t.Context(), edited, nil, authorizedRevision)
	}()
	holder.release(t)
	if err := receiveConnectorWriteResultForTest(t, done); !errors.Is(err, errConnectorConnectionChanged) {
		t.Fatalf("stale write error = %v, want errConnectorConnectionChanged", err)
	}
	stored, found, err := store.get("github", "reviewer")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got := string(stored.Credentials["access_token"]); got != `"token-refreshed"` {
		t.Fatalf("access_token = %s, want the external write-back", got)
	}
}

func TestConnectorConnectionStoreWriteTimesOutWhileExternalLockIsHeld(t *testing.T) {
	store, err := newConnectorConnectionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.putCurrentForTest(testLocalConnectorConnection("github", "reviewer", "token-one", nil), nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	holder := startConnectionsLockHolderForTest(t, store, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err = store.put(ctx, testLocalConnectorConnection("gmail", "sender", "gmail-token", nil), nil, "")
	if !errors.Is(err, errConnectorConnectionsFileBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write error = %v, want errConnectorConnectionsFileBusy", err)
	}
	recorder := httptest.NewRecorder()
	writeConnectorConnectionPutError(recorder, err, "CONNECTOR_CONNECTION_WRITE_FAILED", "Connector connection could not be saved")
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "CONNECTOR_CONNECTIONS_FILE_BUSY") {
		t.Fatalf("busy response = %d %s", recorder.Code, recorder.Body.String())
	}
	holder.release(t)

	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("timed out write changed the file:\n%s", after)
	}
	if err := store.put(t.Context(), testLocalConnectorConnection("gmail", "sender", "gmail-token", nil), nil, ""); err != nil {
		t.Fatalf("write after the lock was released: %v", err)
	}
}

func TestConnectorConnectionStoresSharingAFileSerializeWritesAndReplaceItAtomically(t *testing.T) {
	directory := t.TempDir()
	stores := make([]*connectorConnectionStore, 2)
	for index := range stores {
		store, err := newConnectorConnectionStore(directory)
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
	}
	const writesPerStore = 20

	readerContext, stopReader := context.WithCancel(t.Context())
	readerResult := make(chan error, 1)
	go func() {
		readerResult <- readConnectorConnectionsFileUntilDoneForTest(readerContext, stores[0].path)
	}()
	var writers sync.WaitGroup
	writeErrors := make(chan error, len(stores)*writesPerStore)
	for storeIndex, store := range stores {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for writeIndex := range writesPerStore {
				name := fmt.Sprintf("store-%d-connection-%02d", storeIndex, writeIndex)
				writeErrors <- store.put(t.Context(), testLocalConnectorConnection("github", name, "token", nil), nil, "")
			}
		}()
	}
	writers.Wait()
	stopReader()
	close(writeErrors)
	for err := range writeErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-readerResult; err != nil {
		t.Fatal(err)
	}

	connections, err := stores[1].list()
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != len(stores)*writesPerStore {
		t.Fatalf("stored %d connections, want %d: a write was lost", len(connections), len(stores)*writesPerStore)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "connections.json,connections.json.lock" {
		t.Fatalf("directory entries = %v, want no temporary files", names)
	}
	lockInfo, err := os.Stat(stores[0].lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("lock file permissions = %o, want 0600", lockInfo.Mode().Perm())
	}
}

// readConnectorConnectionsFileUntilDoneForTest decodes the file until ctx ends, failing on any partial document.
func readConnectorConnectionsFileUntilDoneForTest(ctx context.Context, path string) error {
	for ctx.Err() == nil {
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var file connectorConnectionsFile
		if err := decodeStrictConnectorJSON(contents, &file); err != nil {
			return fmt.Errorf("read a partial connections file: %w", err)
		}
	}
	return nil
}

func readConnectorConnectionsFileForTest(t *testing.T, store *connectorConnectionStore) connectorConnectionsFile {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func receiveConnectorWriteResultForTest(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("write did not finish after the external lock was released")
		return nil
	}
}

type connectionsLockHolderForTest struct {
	command *exec.Cmd
	stdin   io.WriteCloser
}

// startConnectionsLockHolderForTest returns once another process holds the store's lock.
// A non-nil replacement replaces connections.json on release.
func startConnectionsLockHolderForTest(
	t *testing.T,
	store *connectorConnectionStore,
	replacement *connectorConnectionsFile,
) *connectionsLockHolderForTest {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestConnectorConnectionsFileLockHolderProcess$", "-test.count=1")
	command.Env = append(os.Environ(), connectionsLockHolderLockPathEnvironment+"="+store.lockPath)
	if replacement != nil {
		replacementPath := filepath.Join(store.directory, ".external-write.json")
		contents, err := json.MarshalIndent(replacement, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacementPath, append(contents, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		command.Env = append(command.Env,
			connectionsLockHolderReplacementPathEnvironment+"="+replacementPath,
			connectionsLockHolderTargetPathEnvironment+"="+store.path)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	holder := &connectionsLockHolderForTest{command: command, stdin: stdin}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			if err := command.Process.Kill(); err != nil {
				t.Errorf("kill lock holder: %v", err)
			}
			if err := command.Wait(); err != nil {
				t.Logf("killed lock holder: %v", err)
			}
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("lock holder output = %q, err = %v", line, err)
	}
	return holder
}

// release lets the holder write its replacement and exit, which releases the lock.
func (holder *connectionsLockHolderForTest) release(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(holder.stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	if err := holder.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := holder.command.Wait(); err != nil {
		t.Fatalf("lock holder failed: %v", err)
	}
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	callStateSchemaVersion = "dexcli.connectors-mcp/call-state/v1"
	callStateDirectoryName = "calls"
	// callStateRetention bounds how long a finished call's state answers retries with the same key.
	callStateRetention = 30 * 24 * time.Hour
)

// callStateRecord is the persisted state of one keyed Mutation call: its fence checkpoint and terminal result.
type callStateRecord struct {
	SchemaVersion  string `json:"schemaVersion"`
	ConnectorID    string `json:"connectorId"`
	OperationID    string `json:"operationId"`
	ConnectionName string `json:"connectionName"`
	// RequestDigest binds the idempotency key to the tool name and canonical arguments it was first used with.
	RequestDigest string `json:"requestDigest"`
	// Attempt is the last attempt started; it only grows, across process restarts too.
	Attempt int32 `json:"attempt"`
	// Heartbeat is the last single-dispatch checkpoint the connector recorded.
	Heartbeat json.RawMessage `json:"heartbeat,omitempty"`
	// Result is the terminal MCP result, returned again to a retry with the same key.
	Result    json.RawMessage `json:"result,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// callStateStore keeps one owner-only state file per keyed call, locked per key across processes.
type callStateStore struct {
	directory string

	mu        sync.Mutex
	keyLocks  map[string]*keyLock
	createdAt time.Time
}

type keyLock struct {
	mu      sync.Mutex
	holders int
}

func newCallStateStore(stateDirectory string) (*callStateStore, error) {
	directory := filepath.Join(stateDirectory, callStateDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create connector call state directory: %w", err)
	}
	return &callStateStore{directory: directory, keyLocks: make(map[string]*keyLock), createdAt: time.Now()}, nil
}

// lock serializes every call with stateKey and returns the function that releases it.
func (store *callStateStore) lock(stateKey string) (func() error, error) {
	store.mu.Lock()
	entry := store.keyLocks[stateKey]
	if entry == nil {
		entry = &keyLock{}
		store.keyLocks[stateKey] = entry
	}
	entry.holders++
	store.mu.Unlock()
	entry.mu.Lock()
	fileLock, err := lockFile(filepath.Join(store.directory, stateKey+".lock"))
	if err != nil {
		store.releaseKeyLock(stateKey, entry)
		return nil, fmt.Errorf("lock connector call state: %w", err)
	}
	return func() error {
		err := fileLock.unlock()
		store.releaseKeyLock(stateKey, entry)
		return err
	}, nil
}

func (store *callStateStore) releaseKeyLock(stateKey string, entry *keyLock) {
	entry.mu.Unlock()
	store.mu.Lock()
	defer store.mu.Unlock()
	entry.holders--
	if entry.holders == 0 {
		delete(store.keyLocks, stateKey)
	}
}

// read returns the record of stateKey and whether it exists. The caller holds the key's lock.
func (store *callStateStore) read(stateKey string) (callStateRecord, bool, error) {
	contents, err := os.ReadFile(store.path(stateKey))
	if errors.Is(err, os.ErrNotExist) {
		return callStateRecord{}, false, nil
	}
	if err != nil {
		return callStateRecord{}, false, fmt.Errorf("read connector call state: %w", err)
	}
	var record callStateRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		return callStateRecord{}, false, fmt.Errorf("decode connector call state: %w", err)
	}
	if record.SchemaVersion != callStateSchemaVersion {
		return callStateRecord{}, false, fmt.Errorf("unsupported connector call state schema version %q", record.SchemaVersion)
	}
	return record, true, nil
}

// write replaces the record of stateKey durably: the new file and its directory entry are synced before it returns.
func (store *callStateStore) write(stateKey string, record callStateRecord) (returnErr error) {
	record.SchemaVersion = callStateSchemaVersion
	record.UpdatedAt = time.Now().UTC()
	contents, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode connector call state: %w", err)
	}
	temporaryFile, err := os.CreateTemp(store.directory, stateKey+".*.tmp")
	if err != nil {
		return fmt.Errorf("create connector call state: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, removeIfExists(temporaryPath))
		}
	}()
	if _, err := temporaryFile.Write(contents); err != nil {
		return errors.Join(fmt.Errorf("write connector call state: %w", err), temporaryFile.Close())
	}
	if err := temporaryFile.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync connector call state: %w", err), temporaryFile.Close())
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close connector call state: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path(stateKey)); err != nil {
		return fmt.Errorf("replace connector call state: %w", err)
	}
	return syncDirectory(store.directory)
}

// pruneExpired removes state and lock files untouched for callStateRetention, and leftover temporary files.
func (store *callStateStore) pruneExpired() error {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return fmt.Errorf("list connector call state: %w", err)
	}
	var pruneErr error
	for _, entry := range entries {
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			pruneErr = errors.Join(pruneErr, err)
			continue
		}
		isExpired := store.createdAt.Sub(info.ModTime()) > callStateRetention
		isStaleTemporary := strings.HasSuffix(entry.Name(), ".tmp") && store.createdAt.Sub(info.ModTime()) > time.Hour
		if isExpired || isStaleTemporary {
			pruneErr = errors.Join(pruneErr, removeIfExists(filepath.Join(store.directory, entry.Name())))
		}
	}
	return pruneErr
}

func (store *callStateStore) path(stateKey string) string {
	return filepath.Join(store.directory, stateKey+".json")
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open connector call state directory: %w", err)
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return fmt.Errorf("sync connector call state directory: %w", syncErr)
	}
	return closeErr
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

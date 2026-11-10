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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	connectorConnectionsFileName       = "connections.json"
	connectorConnectionsSchema         = "connectors.dex.dev/local-connections/v1alpha1"
	connectorUseConfigurationsFileName = "use-configurations.json"
	connectorUseConfigurationsSchema   = "connectors.dex.dev/local-use-configurations/v1alpha1"
)

var errConnectorConfigurationRevisionConflict = errors.New("Connector configuration revision conflict")

// connectorConnectionStore reads and writes the local connection files. Its lock works within one process only;
// connections file writes also take the cross-process lock at lockPath, under which the callers' compare-and-set
// checks run against the file as read. One Dex Web process owns a connections directory at a time.
type connectorConnectionStore struct {
	directory             string
	path                  string
	lockPath              string
	useConfigurationsPath string
	mu                    sync.Mutex
}

type connectorConnectionsFile struct {
	SchemaVersion   string                         `json:"schemaVersion"`
	Connections     []localConnectorConnection     `json:"connections"`
	TriggerBindings []localConnectorTriggerBinding `json:"triggerBindings,omitempty"`
}

type localConnectorConnection struct {
	ConnectorID         string                     `json:"connectorId"`
	AuthMethodID        string                     `json:"authMethodId,omitempty"`
	AuthMethodIDs       []string                   `json:"authMethodIds,omitempty"`
	ModulePath          string                     `json:"modulePath"`
	ModuleVersion       string                     `json:"moduleVersion"`
	Provider            string                     `json:"provider"`
	ConnectionName      string                     `json:"connectionName"`
	Configuration       map[string]json.RawMessage `json:"configuration"`
	Credentials         map[string]json.RawMessage `json:"credentials"`
	CredentialExpiresAt *time.Time                 `json:"credentialExpiresAt,omitempty"`
	// AuthorizationID is a non-secret UUIDv4 that each completed authorization replaces; credential refreshes keep it.
	AuthorizationID string `json:"authorizationId,omitempty"`
}

// Local stores derive stored credential names without returning values.
type storedConnectorConnection struct {
	localConnectorConnection
	StoredCredentialFields []string `json:"storedCredentialFields"`
}

type localConnectorTriggerBinding struct {
	ConnectorID    string                     `json:"connectorId"`
	ConnectionName string                     `json:"connectionName"`
	TriggerName    string                     `json:"triggerName"`
	BindingName    string                     `json:"bindingName"`
	Configuration  map[string]json.RawMessage `json:"configuration"`
}

type connectorUseConfigurationsFile struct {
	SchemaVersion           string                           `json:"schemaVersion"`
	OperationConfigurations []localConnectorUseConfiguration `json:"operationConfigurations"`
}

type localConnectorUseConfiguration struct {
	ConnectorID    string                     `json:"connectorId"`
	ConnectionName string                     `json:"connectionName"`
	OperationID    string                     `json:"operationId"`
	FlowType       string                     `json:"flowType"`
	StepType       string                     `json:"stepType"`
	Configuration  map[string]json.RawMessage `json:"configuration"`
}

func newConnectorConnectionStore(directory string) (*connectorConnectionStore, error) {
	absoluteDirectory, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return nil, fmt.Errorf("resolve Connector configuration directory: %w", err)
	}
	if err := os.MkdirAll(absoluteDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create Connector configuration directory: %w", err)
	}
	info, err := os.Lstat(absoluteDirectory)
	if err != nil {
		return nil, fmt.Errorf("inspect Connector configuration directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("Connector configuration path must be a directory")
	}
	path := filepath.Join(absoluteDirectory, connectorConnectionsFileName)
	store := &connectorConnectionStore{
		directory: absoluteDirectory, path: path, lockPath: path + connectorConnectionsFileLockSuffix,
		useConfigurationsPath: filepath.Join(absoluteDirectory, connectorUseConfigurationsFileName),
	}
	if _, err := store.load(); err != nil {
		return nil, err
	}
	if _, err := store.loadUseConfigurations(); err != nil {
		return nil, err
	}
	if err := store.verifyWritableDirectory(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *connectorConnectionStore) verifyWritableDirectory() (returnErr error) {
	temporaryFile, err := os.CreateTemp(store.directory, ".dex-connector-write-check-*")
	if err != nil {
		return fmt.Errorf("Connector configuration directory is not writable: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove Connector write check: %w", removeErr))
		}
	}()
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close Connector write check: %w", err)
	}
	return nil
}

func (store *connectorConnectionStore) list() ([]storedConnectorConnection, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return nil, err
	}
	connections := make([]storedConnectorConnection, 0, len(file.Connections))
	for _, connection := range file.Connections {
		connections = append(connections, newStoredLocalConnectorConnection(connection))
	}
	return connections, nil
}

func (store *connectorConnectionStore) get(connectorID string, connectionName string) (storedConnectorConnection, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return storedConnectorConnection{}, false, err
	}
	for _, connection := range file.Connections {
		if connection.ConnectorID == connectorID && connection.ConnectionName == connectionName {
			return newStoredLocalConnectorConnection(connection), true, nil
		}
	}
	return storedConnectorConnection{}, false, nil
}

// revision returns the stored record's revision, or "" when the file holds no record for the key.
func (store *connectorConnectionStore) revision(connectorID string, connectionName string) (string, error) {
	stored, found, err := store.get(connectorID, connectionName)
	if err != nil || !found {
		return "", err
	}
	return connectorConnectionRevision(stored.localConnectorConnection)
}

// triggerBindingRevision returns the stored binding's revision for the binding's key, or "" when the file holds none.
func (store *connectorConnectionStore) triggerBindingRevision(binding localConnectorTriggerBinding) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return "", err
	}
	return storedTriggerBindingRevision(file, binding)
}

// storedConnectionRevision returns the revision of the key's record, or "" when the file holds none.
func storedConnectionRevision(file connectorConnectionsFile, connectorID string, connectionName string) (string, error) {
	for _, connection := range file.Connections {
		if connection.ConnectorID == connectorID && connection.ConnectionName == connectionName {
			return connectorConnectionRevision(connection)
		}
	}
	return "", nil
}

// storedTriggerBindingRevision returns the revision of the binding's stored copy, or "" when absent.
func storedTriggerBindingRevision(file connectorConnectionsFile, binding localConnectorTriggerBinding) (string, error) {
	for _, stored := range file.TriggerBindings {
		if stored.ConnectorID != binding.ConnectorID || stored.ConnectionName != binding.ConnectionName ||
			stored.TriggerName != binding.TriggerName || stored.BindingName != binding.BindingName {
			continue
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			return "", fmt.Errorf("encode Connector Trigger binding for its revision: %w", err)
		}
		digest := sha256.Sum256(encoded)
		return hex.EncodeToString(digest[:]), nil
	}
	return "", nil
}

// connectorConnectionRevision hashes a record's JSON encoding, so the revision is the same after the file is read again.
func connectorConnectionRevision(connection localConnectorConnection) (string, error) {
	encoded, err := json.Marshal(connection)
	if err != nil {
		return "", fmt.Errorf("encode Connector connection for its revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (store *connectorConnectionStore) listTriggerBindings(connectorID string, connectionName string) ([]localConnectorTriggerBinding, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return nil, err
	}
	bindings := make([]localConnectorTriggerBinding, 0)
	for _, binding := range file.TriggerBindings {
		if binding.ConnectorID == connectorID && binding.ConnectionName == connectionName {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

// putTriggerBinding saves the binding while the file keeps both expected revisions ("" for absent).
// It returns errConnectorConnectionChanged when either revision differs or the connection is absent.
func (store *connectorConnectionStore) putTriggerBinding(
	fileLock *connectorConnectionsFileLock,
	binding localConnectorTriggerBinding,
	expectedConnectionRevision string,
	expectedBindingRevision string,
) error {
	return store.updateConnections(fileLock, func(file *connectorConnectionsFile) (bool, error) {
		if err := requireStoredConnectionRevision(*file, binding.ConnectorID, binding.ConnectionName, expectedConnectionRevision); err != nil {
			return false, err
		}
		bindingRevision, err := storedTriggerBindingRevision(*file, binding)
		if err != nil {
			return false, err
		}
		if bindingRevision != expectedBindingRevision {
			return false, errConnectorConnectionChanged
		}
		return true, putTriggerBindingInFile(file, binding)
	})
}

func putTriggerBindingInFile(file *connectorConnectionsFile, binding localConnectorTriggerBinding) error {
	// A binding needs its connection in the same file; a load rejects a file that breaks this.
	if !slices.ContainsFunc(file.Connections, func(connection localConnectorConnection) bool {
		return connection.ConnectorID == binding.ConnectorID && connection.ConnectionName == binding.ConnectionName
	}) {
		return errConnectorConnectionChanged
	}
	found := false
	for index := range file.TriggerBindings {
		current := file.TriggerBindings[index]
		if current.ConnectorID == binding.ConnectorID && current.ConnectionName == binding.ConnectionName &&
			current.TriggerName == binding.TriggerName && current.BindingName == binding.BindingName {
			file.TriggerBindings[index] = binding
			found = true
			break
		}
	}
	if !found {
		file.TriggerBindings = append(file.TriggerBindings, binding)
	}
	return nil
}

func (store *connectorConnectionStore) listUseConfigurations(connectorID string, connectionName string) ([]localConnectorUseConfiguration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.loadUseConfigurations()
	if err != nil {
		return nil, err
	}
	configurations := make([]localConnectorUseConfiguration, 0)
	for _, configuration := range file.OperationConfigurations {
		if configuration.ConnectorID == connectorID && configuration.ConnectionName == connectionName {
			configurations = append(configurations, configuration)
		}
	}
	return configurations, nil
}

func (store *connectorConnectionStore) putUseConfiguration(configuration localConnectorUseConfiguration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.loadUseConfigurations()
	if err != nil {
		return err
	}
	found := false
	for index := range file.OperationConfigurations {
		current := file.OperationConfigurations[index]
		if sameConnectorUse(current, configuration) {
			file.OperationConfigurations[index] = configuration
			found = true
			break
		}
	}
	if !found {
		file.OperationConfigurations = append(file.OperationConfigurations, configuration)
	}
	return store.writeUseConfigurations(file)
}

// put replaces the connection, copying each keepCredentialFields value from the stored record.
// It assigns a new authorizationId on isAuthorizationCompleted, a new record, or changed credentials.
// It returns errConnectorConnectionChanged unless the stored record keeps expectedRevision ("" for absent).
func (store *connectorConnectionStore) put(
	fileLock *connectorConnectionsFileLock,
	connection localConnectorConnection,
	keepCredentialFields []string,
	expectedRevision string,
	isAuthorizationCompleted bool,
) error {
	newAuthorizationID, err := newConnectorAuthorizationID()
	if err != nil {
		return err
	}
	return store.updateConnections(fileLock, func(file *connectorConnectionsFile) (bool, error) {
		if err := requireStoredConnectionRevision(*file, connection.ConnectorID, connection.ConnectionName, expectedRevision); err != nil {
			return false, err
		}
		return true, putConnectionInFile(file, connection, keepCredentialFields, newAuthorizationID, isAuthorizationCompleted)
	})
}

// newConnectorAuthorizationID returns a random UUIDv4 read from crypto/rand.
func newConnectorAuthorizationID() (string, error) {
	authorizationID, err := uuid.NewRandomFromReader(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate Connector connection authorizationId: %w", err)
	}
	return authorizationID.String(), nil
}

func putConnectionInFile(
	file *connectorConnectionsFile,
	connection localConnectorConnection,
	keepCredentialFields []string,
	newAuthorizationID string,
	isAuthorizationCompleted bool,
) error {
	storedIndex := -1
	for index := range file.Connections {
		current := file.Connections[index]
		if current.ConnectorID == connection.ConnectorID && current.ConnectionName == connection.ConnectionName {
			storedIndex = index
			break
		}
	}
	for _, name := range keepCredentialFields {
		if storedIndex < 0 {
			return errConnectorConfigurationRevisionConflict
		}
		value, found := file.Connections[storedIndex].Credentials[name]
		if !found {
			return errConnectorConfigurationRevisionConflict
		}
		connection.Credentials[name] = value
	}
	connection.AuthorizationID = newAuthorizationID
	if storedIndex >= 0 && !isAuthorizationCompleted {
		isSameCredentials, err := hasSameConnectorCredentials(file.Connections[storedIndex].Credentials, connection.Credentials)
		if err != nil {
			return err
		}
		if isSameCredentials {
			connection.AuthorizationID = file.Connections[storedIndex].AuthorizationID
		}
	}
	if storedIndex < 0 {
		file.Connections = append(file.Connections, connection)
	} else {
		file.Connections[storedIndex] = connection
	}
	return nil
}

// hasSameConnectorCredentials compares compacted values, because values read back from the file keep its indentation.
func hasSameConnectorCredentials(stored map[string]json.RawMessage, next map[string]json.RawMessage) (bool, error) {
	if len(stored) != len(next) {
		return false, nil
	}
	for name, storedValue := range stored {
		nextValue, found := next[name]
		if !found {
			return false, nil
		}
		var compactStored, compactNext bytes.Buffer
		if err := json.Compact(&compactStored, storedValue); err != nil {
			return false, fmt.Errorf("compact stored Connector credential %q: %w", name, err)
		}
		if err := json.Compact(&compactNext, nextValue); err != nil {
			return false, fmt.Errorf("compact Connector credential %q: %w", name, err)
		}
		if !bytes.Equal(compactStored.Bytes(), compactNext.Bytes()) {
			return false, nil
		}
	}
	return true, nil
}

// delete deletes the connection with its Trigger bindings and use configurations, and reports whether it existed.
// It returns errConnectorConnectionChanged unless the stored record keeps expectedRevision ("" for absent).
func (store *connectorConnectionStore) delete(
	fileLock *connectorConnectionsFileLock,
	connectorID string,
	connectionName string,
	expectedRevision string,
) (bool, error) {
	isDeleted := false
	err := store.updateConnections(fileLock, func(file *connectorConnectionsFile) (bool, error) {
		if err := requireStoredConnectionRevision(*file, connectorID, connectionName, expectedRevision); err != nil {
			return false, err
		}
		isDeleted = deleteConnectionFromFile(file, connectorID, connectionName)
		return isDeleted, nil
	})
	if err != nil || !isDeleted {
		return false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	useConfigurations, err := store.loadUseConfigurations()
	if err != nil {
		return false, err
	}
	filteredUseConfigurations := useConfigurations.OperationConfigurations[:0]
	for _, configuration := range useConfigurations.OperationConfigurations {
		if configuration.ConnectorID != connectorID || configuration.ConnectionName != connectionName {
			filteredUseConfigurations = append(filteredUseConfigurations, configuration)
		}
	}
	useConfigurations.OperationConfigurations = filteredUseConfigurations
	return true, store.writeUseConfigurations(useConfigurations)
}

// deleteConnectionFromFile removes the connection and its Trigger bindings, reporting whether it existed.
func deleteConnectionFromFile(file *connectorConnectionsFile, connectorID string, connectionName string) bool {
	filtered := file.Connections[:0]
	deleted := false
	for _, connection := range file.Connections {
		if connection.ConnectorID == connectorID && connection.ConnectionName == connectionName {
			deleted = true
			continue
		}
		filtered = append(filtered, connection)
	}
	if !deleted {
		return false
	}
	file.Connections = filtered
	filteredBindings := file.TriggerBindings[:0]
	for _, binding := range file.TriggerBindings {
		if binding.ConnectorID == connectorID && binding.ConnectionName == connectionName {
			continue
		}
		filteredBindings = append(filteredBindings, binding)
	}
	file.TriggerBindings = filteredBindings
	return true
}

// connectorConnectionsFileLock is a held cross-process lock of the connections file.
type connectorConnectionsFileLock struct {
	// file is the open lock file, or nil on a platform without file locks.
	file *os.File
}

// lockConnectionsFile waits for the connections file lock; see lockConnectorConnectionsFile for the wait bound.
func (store *connectorConnectionStore) lockConnectionsFile(ctx context.Context) (*connectorConnectionsFileLock, error) {
	file, err := lockConnectorConnectionsFile(ctx, store.lockPath)
	if err != nil {
		return nil, err
	}
	return &connectorConnectionsFileLock{file: file}, nil
}

// release releases the lock.
func (fileLock *connectorConnectionsFileLock) release() {
	if fileLock.file == nil {
		return
	}
	// Closing the only descriptor of the lock file releases its lock even when Close reports an error.
	if err := fileLock.file.Close(); err != nil {
		slog.Warn("close Connector connections lock file", "error", err)
	}
}

// updateConnections is the only connections file write path; change runs under store.mu and must not call store methods.
func (store *connectorConnectionStore) updateConnections(
	fileLock *connectorConnectionsFileLock,
	change func(file *connectorConnectionsFile) (isChanged bool, err error),
) error {
	if fileLock == nil {
		panic("connections file write requires the connections file lock")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return err
	}
	isChanged, err := change(&file)
	if err != nil || !isChanged {
		return err
	}
	return store.write(file)
}

// requireStoredConnectionRevision returns errConnectorConnectionChanged unless the record has expectedRevision ("" for absent).
func requireStoredConnectionRevision(
	file connectorConnectionsFile,
	connectorID string,
	connectionName string,
	expectedRevision string,
) error {
	storedRevision, err := storedConnectionRevision(file, connectorID, connectionName)
	if err != nil {
		return err
	}
	if storedRevision != expectedRevision {
		return errConnectorConnectionChanged
	}
	return nil
}

func (store *connectorConnectionStore) load() (connectorConnectionsFile, error) {
	info, err := os.Lstat(store.path)
	if errors.Is(err, fs.ErrNotExist) {
		return connectorConnectionsFile{SchemaVersion: connectorConnectionsSchema, Connections: []localConnectorConnection{}}, nil
	}
	if err != nil {
		return connectorConnectionsFile{}, fmt.Errorf("inspect Connector configuration file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return connectorConnectionsFile{}, fmt.Errorf("Connector configuration file must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return connectorConnectionsFile{}, fmt.Errorf("Connector configuration file permissions must be 0600")
	}
	contents, err := os.ReadFile(store.path)
	if err != nil {
		return connectorConnectionsFile{}, fmt.Errorf("read Connector configuration file: %w", err)
	}
	var file connectorConnectionsFile
	if err := decodeStrictConnectorJSON(contents, &file); err != nil {
		return connectorConnectionsFile{}, fmt.Errorf("decode Connector configuration file: %w", err)
	}
	if file.SchemaVersion != connectorConnectionsSchema {
		return connectorConnectionsFile{}, fmt.Errorf("unsupported Connector configuration schema version %q", file.SchemaVersion)
	}
	seen := make(map[string]bool, len(file.Connections))
	for index, connection := range file.Connections {
		if err := validateLocalConnectorConnection(connection); err != nil {
			return connectorConnectionsFile{}, fmt.Errorf("validate Connector connection %d: %w", index, err)
		}
		key := connection.ConnectorID + "\x00" + connection.ConnectionName
		if seen[key] {
			return connectorConnectionsFile{}, fmt.Errorf("Connector connection %s/%s is duplicated", connection.ConnectorID, connection.ConnectionName)
		}
		seen[key] = true
	}
	seenBindings := make(map[string]bool, len(file.TriggerBindings))
	for index, binding := range file.TriggerBindings {
		if err := validateLocalConnectorTriggerBinding(binding); err != nil {
			return connectorConnectionsFile{}, fmt.Errorf("validate Connector Trigger binding %d: %w", index, err)
		}
		connectionKey := binding.ConnectorID + "\x00" + binding.ConnectionName
		if !seen[connectionKey] {
			return connectorConnectionsFile{}, fmt.Errorf("Connector Trigger binding references an unknown connection")
		}
		key := connectionKey + "\x00" + binding.TriggerName + "\x00" + binding.BindingName
		if seenBindings[key] {
			return connectorConnectionsFile{}, fmt.Errorf("Connector Trigger binding %s/%s is duplicated", binding.TriggerName, binding.BindingName)
		}
		seenBindings[key] = true
	}
	return file, nil
}

func (store *connectorConnectionStore) write(file connectorConnectionsFile) (returnErr error) {
	sort.Slice(file.Connections, func(left int, right int) bool {
		if file.Connections[left].ConnectorID == file.Connections[right].ConnectorID {
			return file.Connections[left].ConnectionName < file.Connections[right].ConnectionName
		}
		return file.Connections[left].ConnectorID < file.Connections[right].ConnectorID
	})
	sort.Slice(file.TriggerBindings, func(left int, right int) bool {
		leftKey := file.TriggerBindings[left].ConnectorID + "\x00" + file.TriggerBindings[left].ConnectionName + "\x00" + file.TriggerBindings[left].TriggerName + "\x00" + file.TriggerBindings[left].BindingName
		rightKey := file.TriggerBindings[right].ConnectorID + "\x00" + file.TriggerBindings[right].ConnectionName + "\x00" + file.TriggerBindings[right].TriggerName + "\x00" + file.TriggerBindings[right].BindingName
		return leftKey < rightKey
	})
	contents, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Connector configuration file: %w", err)
	}
	contents = append(contents, '\n')
	temporaryFile, err := os.CreateTemp(store.directory, ".connections-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Connector configuration file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	isClosed := false
	defer func() {
		if !isClosed {
			returnErr = errors.Join(returnErr, temporaryFile.Close())
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary Connector configuration file: %w", removeErr))
		}
	}()
	if err := temporaryFile.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary Connector configuration permissions: %w", err)
	}
	if _, err := temporaryFile.Write(contents); err != nil {
		return fmt.Errorf("write temporary Connector configuration file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary Connector configuration file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close temporary Connector configuration file: %w", err)
	}
	isClosed = true
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace Connector configuration file: %w", err)
	}
	directory, err := os.Open(store.directory)
	if err != nil {
		return fmt.Errorf("open Connector configuration directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		return errors.Join(fmt.Errorf("sync Connector configuration directory: %w", err), closeErr)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close Connector configuration directory: %w", err)
	}
	return nil
}

func validateLocalConnectorConnection(connection localConnectorConnection) error {
	if strings.TrimSpace(connection.ConnectorID) == "" || strings.TrimSpace(connection.ConnectionName) == "" {
		return fmt.Errorf("connectorId and connectionName are required")
	}
	if strings.TrimSpace(connection.ModulePath) == "" || strings.TrimSpace(connection.ModuleVersion) == "" {
		return fmt.Errorf("modulePath and moduleVersion are required")
	}
	if strings.TrimSpace(connection.Provider) == "" {
		return fmt.Errorf("provider is required")
	}
	if connection.Configuration == nil || connection.Credentials == nil {
		return fmt.Errorf("configuration and credentials are required")
	}
	if connection.AuthMethodID != "" && len(connection.AuthMethodIDs) > 0 {
		return fmt.Errorf("authMethodId and authMethodIds are mutually exclusive")
	}
	return nil
}

func newStoredLocalConnectorConnection(connection localConnectorConnection) storedConnectorConnection {
	names := make([]string, 0, len(connection.Credentials))
	for name := range connection.Credentials {
		if !isConnectorAuthMethodCredentialName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return storedConnectorConnection{localConnectorConnection: connection, StoredCredentialFields: names}
}

func validateLocalConnectorTriggerBinding(binding localConnectorTriggerBinding) error {
	if strings.TrimSpace(binding.ConnectorID) == "" || strings.TrimSpace(binding.ConnectionName) == "" ||
		strings.TrimSpace(binding.TriggerName) == "" || strings.TrimSpace(binding.BindingName) == "" {
		return fmt.Errorf("connectorId, connectionName, triggerName, and bindingName are required")
	}
	if binding.Configuration == nil {
		return fmt.Errorf("configuration is required")
	}
	return nil
}

func decodeStrictConnectorJSON(contents []byte, destination any) error {
	return decodeStrictConnectorJSONReader(bytes.NewReader(contents), destination)
}

func decodeStrictConnectorJSONReader(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

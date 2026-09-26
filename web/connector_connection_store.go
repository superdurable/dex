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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	connectorConnectionsFileName       = "connections.json"
	connectorConnectionsSchema         = "connectors.dex.dev/local-connections/v1alpha1"
	connectorUseConfigurationsFileName = "use-configurations.json"
	connectorUseConfigurationsSchema   = "connectors.dex.dev/local-use-configurations/v1alpha1"
)

type connectorConnectionStore struct {
	directory             string
	path                  string
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
	ModulePath          string                     `json:"modulePath"`
	ModuleVersion       string                     `json:"moduleVersion"`
	Provider            string                     `json:"provider"`
	ConnectionName      string                     `json:"connectionName"`
	Configuration       map[string]json.RawMessage `json:"configuration"`
	Credentials         map[string]json.RawMessage `json:"credentials"`
	CredentialExpiresAt *time.Time                 `json:"credentialExpiresAt,omitempty"`
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
	store := &connectorConnectionStore{
		directory: absoluteDirectory, path: filepath.Join(absoluteDirectory, connectorConnectionsFileName),
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

func (store *connectorConnectionStore) list() ([]localConnectorConnection, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return nil, err
	}
	return file.Connections, nil
}

func (store *connectorConnectionStore) get(connectorID string, connectionName string) (localConnectorConnection, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return localConnectorConnection{}, false, err
	}
	for _, connection := range file.Connections {
		if connection.ConnectorID == connectorID && connection.ConnectionName == connectionName {
			return connection, true, nil
		}
	}
	return localConnectorConnection{}, false, nil
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

func (store *connectorConnectionStore) putTriggerBinding(binding localConnectorTriggerBinding) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return err
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
	return store.write(file)
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

func (store *connectorConnectionStore) put(connection localConnectorConnection) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return err
	}
	found := false
	for index := range file.Connections {
		current := file.Connections[index]
		if current.ConnectorID == connection.ConnectorID && current.ConnectionName == connection.ConnectionName {
			file.Connections[index] = connection
			found = true
			break
		}
	}
	if !found {
		file.Connections = append(file.Connections, connection)
	}
	return store.write(file)
}

func (store *connectorConnectionStore) delete(connectorID string, connectionName string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	file, err := store.load()
	if err != nil {
		return false, err
	}
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
		return false, nil
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
	if err := store.write(file); err != nil {
		return false, err
	}
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
	return nil
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

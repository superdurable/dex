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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (store *connectorConnectionStore) loadUseConfigurations() (connectorUseConfigurationsFile, error) {
	info, err := os.Lstat(store.useConfigurationsPath)
	if errors.Is(err, fs.ErrNotExist) {
		return connectorUseConfigurationsFile{SchemaVersion: connectorUseConfigurationsSchema, OperationConfigurations: []localConnectorUseConfiguration{}}, nil
	}
	if err != nil {
		return connectorUseConfigurationsFile{}, fmt.Errorf("inspect Connector use configuration file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return connectorUseConfigurationsFile{}, fmt.Errorf("Connector use configuration file must be a regular 0600 file")
	}
	contents, err := os.ReadFile(store.useConfigurationsPath)
	if err != nil {
		return connectorUseConfigurationsFile{}, fmt.Errorf("read Connector use configuration file: %w", err)
	}
	var file connectorUseConfigurationsFile
	if err := decodeStrictConnectorJSON(contents, &file); err != nil {
		return connectorUseConfigurationsFile{}, fmt.Errorf("decode Connector use configuration file: %w", err)
	}
	if file.SchemaVersion != connectorUseConfigurationsSchema {
		return connectorUseConfigurationsFile{}, fmt.Errorf("unsupported Connector use configuration schema version %q", file.SchemaVersion)
	}
	seen := make(map[string]bool, len(file.OperationConfigurations))
	for index, configuration := range file.OperationConfigurations {
		if err := validateLocalConnectorUseConfiguration(configuration); err != nil {
			return connectorUseConfigurationsFile{}, fmt.Errorf("validate Connector use configuration %d: %w", index, err)
		}
		key := connectorUseKey(configuration)
		if seen[key] {
			return connectorUseConfigurationsFile{}, fmt.Errorf("Connector use configuration is duplicated")
		}
		seen[key] = true
	}
	return file, nil
}

func (store *connectorConnectionStore) writeUseConfigurations(file connectorUseConfigurationsFile) (returnErr error) {
	sort.Slice(file.OperationConfigurations, func(left int, right int) bool {
		return connectorUseKey(file.OperationConfigurations[left]) < connectorUseKey(file.OperationConfigurations[right])
	})
	contents, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Connector use configuration file: %w", err)
	}
	contents = append(contents, '\n')
	temporaryFile, err := os.CreateTemp(store.directory, ".use-configurations-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Connector use configuration file: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	closed := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporaryFile.Close())
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary Connector use configuration file: %w", removeErr))
		}
	}()
	if err := temporaryFile.Chmod(0o600); err != nil {
		return fmt.Errorf("secure Connector use configuration file: %w", err)
	}
	if _, err := temporaryFile.Write(contents); err != nil {
		return fmt.Errorf("write Connector use configuration file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		return fmt.Errorf("sync Connector use configuration file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close Connector use configuration file: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, store.useConfigurationsPath); err != nil {
		return fmt.Errorf("replace Connector use configuration file: %w", err)
	}
	directory, err := os.Open(filepath.Dir(store.useConfigurationsPath))
	if err != nil {
		return fmt.Errorf("open Connector use configuration directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync Connector use configuration directory: %w", err), directory.Close())
	}
	return directory.Close()
}

func validateLocalConnectorUseConfiguration(configuration localConnectorUseConfiguration) error {
	if strings.TrimSpace(configuration.ConnectorID) == "" || strings.TrimSpace(configuration.ConnectionName) == "" ||
		strings.TrimSpace(configuration.OperationID) == "" || strings.TrimSpace(configuration.FlowType) == "" ||
		strings.TrimSpace(configuration.StepType) == "" {
		return fmt.Errorf("connectorId, connectionName, operationId, flowType, and stepType are required")
	}
	if configuration.Configuration == nil {
		return fmt.Errorf("configuration is required")
	}
	return nil
}

func sameConnectorUse(left localConnectorUseConfiguration, right localConnectorUseConfiguration) bool {
	return connectorUseKey(left) == connectorUseKey(right)
}

func connectorUseKey(configuration localConnectorUseConfiguration) string {
	return configuration.ConnectorID + "\x00" + configuration.ConnectionName + "\x00" + configuration.OperationID + "\x00" + configuration.FlowType + "\x00" + configuration.StepType
}

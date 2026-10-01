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
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
)

var errConnectorConfigurationRevisionConflict = projectconfig.ErrConflict

type projectConnectorStore struct {
	owner                      *ProjectConfiguration
	ctx                        context.Context
	expectedRevision           uint64
	expectedCredentialRevision uint64
	admissionError             error
	document                   projectconfig.Configuration
	loaded                     bool
}

func (setup *connectorSetup) requestStore(request *http.Request) connectorConfigurationStore {
	if setup.project == nil {
		return setup.store
	}
	store := &projectConnectorStore{owner: setup.project, ctx: request.Context()}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		store.expectedRevision, store.admissionError = projectRevision(request.Header.Get(projectConfigurationRevisionHeader))
		if store.admissionError == nil && (request.Method == http.MethodDelete || request.Method == http.MethodPut) {
			store.expectedCredentialRevision, store.admissionError = projectRevision(request.Header.Get(projectCredentialRevisionHeader))
		}
	}
	return store
}

func (store *projectConnectorStore) load() error {
	if store.loaded {
		return nil
	}
	document, _, err := store.owner.readConfiguration(store.ctx)
	if err != nil {
		return err
	}
	store.document, store.loaded = document, true
	return nil
}

func (store *projectConnectorStore) state() connectorConfigurationStoreState {
	return connectorConfigurationStoreState{Mode: ConnectorSetupModeProject, ConfigurationRevision: strconv.FormatUint(store.document.Revision, 10), ConfigurationState: "Draft"}
}

func (store *projectConnectorStore) list() ([]storedConnectorConnection, error) {
	if err := store.load(); err != nil {
		return nil, err
	}
	result := make([]storedConnectorConnection, 0, len(store.document.Connections))
	for _, entry := range store.document.Connections {
		connection, found, err := store.get(entry.ConnectorID, entry.ConnectionName)
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, connection)
		}
	}
	return result, nil
}

func (store *projectConnectorStore) get(connectorID, connectionName string) (storedConnectorConnection, bool, error) {
	if err := store.load(); err != nil {
		return storedConnectorConnection{}, false, err
	}
	for _, entry := range store.document.Connections {
		if entry.ConnectorID != connectorID || entry.ConnectionName != connectionName {
			continue
		}
		result := storedConnectorConnection{localConnectorConnection: localConnectorConnection{ConnectorID: entry.ConnectorID, ConnectionName: entry.ConnectionName, ModulePath: entry.ModulePath, ModuleVersion: entry.ModuleVersion, Provider: entry.Provider, AuthMethodID: entry.AuthMethodID, AuthMethodIDs: entry.AuthMethodIDs}}
		if err := json.Unmarshal(entry.Configuration, &result.Configuration); err != nil {
			return result, false, err
		}
		key := projectconfig.ConnectionKey{ConnectorID: connectorID, ConnectionName: connectionName}
		metadata, err := store.owner.connections.ReadConnection(store.ctx, key)
		if errors.Is(err, projectconfig.ErrObjectNotFound) {
			return result, true, nil
		}
		if err != nil {
			return result, false, err
		}
		result.CredentialExpiresAt, result.CredentialStatus = metadata.ExpiresAt, string(metadata.Status)
		if metadata.Status == projectconfig.CredentialReady {
			material, _, readErr := store.owner.connections.ReadCredentialMaterial(store.ctx, key)
			if readErr != nil {
				return result, false, readErr
			}
			if err = json.Unmarshal(material.Credentials, &result.Credentials); err != nil {
				return result, false, err
			}
			for field := range result.Credentials {
				if field != "auth_method" && field != "auth_methods" {
					result.StoredCredentialFields = append(result.StoredCredentialFields, field)
				}
			}
			sort.Strings(result.StoredCredentialFields)
		}
		return result, true, nil
	}
	return storedConnectorConnection{}, false, nil
}

func (store *projectConnectorStore) put(connection localConnectorConnection, keepFields []string) error {
	if store.admissionError != nil {
		return projectconfig.ErrConflict
	}
	if err := store.load(); err != nil {
		return err
	}
	if store.document.Revision != store.expectedRevision {
		return projectconfig.ErrConflict
	}
	if len(keepFields) > 0 {
		previous, found, err := store.get(connection.ConnectorID, connection.ConnectionName)
		if err != nil {
			return err
		}
		if !found {
			return projectconfig.ErrConflict
		}
		for _, field := range keepFields {
			value, exists := previous.Credentials[field]
			if !exists {
				return projectconfig.ErrConflict
			}
			connection.Credentials[field] = value
		}
	}
	configurationBytes, err := json.Marshal(connection.Configuration)
	if err != nil {
		return err
	}
	entry := projectconfig.ConnectionConfiguration{ConnectorID: connection.ConnectorID, ConnectionName: connection.ConnectionName, ModulePath: connection.ModulePath, ModuleVersion: connection.ModuleVersion, Provider: connection.Provider, AuthMethodID: connection.AuthMethodID, AuthMethodIDs: connection.AuthMethodIDs, Configuration: configurationBytes}
	if authority, ok := store.owner.localArtifacts[connection.ConnectorID]; ok {
		if authority.ModulePath != connection.ModulePath || authority.BaselineVersion != connection.ModuleVersion {
			return projectconfig.ErrConflict
		}
		pin := authority.LocalConnectorArtifact
		entry.LocalArtifact = &pin
	}
	replaced := false
	for index, current := range store.document.Connections {
		if current.ConnectorID == entry.ConnectorID && current.ConnectionName == entry.ConnectionName {
			store.document.Connections[index] = entry
			replaced = true
		}
	}
	if !replaced {
		store.document.Connections = append(store.document.Connections, entry)
	}
	if err = store.save(); err != nil {
		return err
	}
	credentials, err := json.Marshal(connection.Credentials)
	if err != nil {
		return err
	}
	method := connection.AuthMethodID
	if len(connection.AuthMethodIDs) > 0 {
		method = strings.Join(connection.AuthMethodIDs, ",")
	}
	_, err = store.owner.connections.ReplaceCredential(store.ctx, projectconfig.ConnectionKey{ConnectorID: connection.ConnectorID, ConnectionName: connection.ConnectionName}, store.expectedCredentialRevision, projectconfig.CredentialMaterial{Credentials: credentials, ExpiresAt: connection.CredentialExpiresAt, ModuleVersion: connection.ModuleVersion, AuthMethod: method})
	return err
}

func (store *projectConnectorStore) delete(connectorID, connectionName string) (bool, error) {
	if store.admissionError != nil {
		return false, projectconfig.ErrConflict
	}
	if err := store.load(); err != nil {
		return false, err
	}
	if store.document.Revision != store.expectedRevision {
		return false, projectconfig.ErrConflict
	}
	key := projectconfig.ConnectionKey{ConnectorID: connectorID, ConnectionName: connectionName}
	if _, err := store.owner.connections.RevokeConnection(store.ctx, key, store.expectedCredentialRevision); err != nil {
		return false, err
	}
	return true, nil
}

func (store *projectConnectorStore) listTriggerBindings(connectorID, connectionName string) ([]localConnectorTriggerBinding, error) {
	if err := store.load(); err != nil {
		return nil, err
	}
	result := []localConnectorTriggerBinding{}
	for _, entry := range store.document.TriggerBindings {
		if entry.ConnectorID == connectorID && entry.ConnectionName == connectionName {
			value := localConnectorTriggerBinding{ConnectorID: connectorID, ConnectionName: connectionName, TriggerName: entry.TriggerName, BindingName: entry.BindingName}
			if err := json.Unmarshal(entry.Configuration, &value.Configuration); err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
	return result, nil
}

func (store *projectConnectorStore) putTriggerBinding(binding localConnectorTriggerBinding) error {
	if err := store.load(); err != nil {
		return err
	}
	contents, err := json.Marshal(binding.Configuration)
	if err != nil {
		return err
	}
	entry := projectconfig.TriggerConfiguration{ConnectorID: binding.ConnectorID, ConnectionName: binding.ConnectionName, TriggerName: binding.TriggerName, BindingName: binding.BindingName, Configuration: contents}
	replaced := false
	for index, current := range store.document.TriggerBindings {
		if current.ConnectorID == entry.ConnectorID && current.ConnectionName == entry.ConnectionName && current.TriggerName == entry.TriggerName && current.BindingName == entry.BindingName {
			store.document.TriggerBindings[index] = entry
			replaced = true
		}
	}
	if !replaced {
		store.document.TriggerBindings = append(store.document.TriggerBindings, entry)
	}
	return store.save()
}

func (store *projectConnectorStore) listUseConfigurations(connectorID, connectionName string) ([]localConnectorUseConfiguration, error) {
	if err := store.load(); err != nil {
		return nil, err
	}
	result := []localConnectorUseConfiguration{}
	for _, entry := range store.document.OperationConfigurations {
		if entry.ConnectorID == connectorID && entry.ConnectionName == connectionName {
			value := localConnectorUseConfiguration{ConnectorID: connectorID, ConnectionName: connectionName, OperationID: entry.OperationID, FlowType: entry.FlowType, StepType: entry.StepType}
			if err := json.Unmarshal(entry.Configuration, &value.Configuration); err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
	return result, nil
}

func (store *projectConnectorStore) putUseConfiguration(value localConnectorUseConfiguration) error {
	if err := store.load(); err != nil {
		return err
	}
	contents, err := json.Marshal(value.Configuration)
	if err != nil {
		return err
	}
	entry := projectconfig.OperationConfiguration{ConnectorID: value.ConnectorID, ConnectionName: value.ConnectionName, OperationID: value.OperationID, FlowType: value.FlowType, StepType: value.StepType, Configuration: contents}
	replaced := false
	for index, current := range store.document.OperationConfigurations {
		if current.ConnectorID == entry.ConnectorID && current.ConnectionName == entry.ConnectionName && current.OperationID == entry.OperationID && current.FlowType == entry.FlowType && current.StepType == entry.StepType {
			store.document.OperationConfigurations[index] = entry
			replaced = true
		}
	}
	if !replaced {
		store.document.OperationConfigurations = append(store.document.OperationConfigurations, entry)
	}
	return store.save()
}

func (store *projectConnectorStore) save() error {
	if store.admissionError != nil || store.document.Revision != store.expectedRevision {
		return projectconfig.ErrConflict
	}
	configurations, err := store.owner.configurationStore()
	if err != nil {
		return err
	}
	document, _, err := configurations.WriteConfiguration(store.ctx, store.expectedRevision, store.document)
	if err != nil {
		return err
	}
	store.document = document
	return nil
}

func (store *projectConnectorStore) saveOAuthConfiguration(identity connectorDefinitionIdentity, provider, method string, values map[string]json.RawMessage) error {
	if err := store.load(); err != nil {
		return err
	}
	contents, err := json.Marshal(values)
	if err != nil {
		return err
	}
	entry := projectconfig.ConnectionConfiguration{ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName, ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, Provider: provider, AuthMethodID: method, Configuration: contents}
	found := false
	for index, current := range store.document.Connections {
		if current.ConnectorID == entry.ConnectorID && current.ConnectionName == entry.ConnectionName {
			store.document.Connections[index] = entry
			found = true
		}
	}
	if !found {
		store.document.Connections = append(store.document.Connections, entry)
	}
	return store.save()
}

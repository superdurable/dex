// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/superdurable/dex/web/api"
)

// connectorConnectionNameLimit matches the longest project connection name the Connector SDKs accept.
const connectorConnectionNameLimit = 256

var (
	errConnectorConnectionStoreUnavailable = errors.New("Connector connection store is unavailable")
	errConnectorConnectionExists           = errors.New("Connector connection already exists")
	errConnectorConnectionChanged          = errors.New("Connector connection was deleted or added again")
)

// handleAddConnection adds a named connection that no loaded Flow uses, so a developer can authorize it for agent tools.
func (setup *connectorSetup) handleAddConnection(response http.ResponseWriter, request *http.Request) {
	if !setup.hasConnectorWriteAuthority(request) {
		api.WriteCodedError(response, http.StatusForbidden, "CONNECTOR_WRITE_FORBIDDEN", "Connector write origin or CSRF token is invalid")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
	var body connectorConnectionAddRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector connection request is invalid")
		return
	}
	if err := validateAddedConnectorConnectionName(body.ConnectionName); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID", err.Error())
		return
	}
	identity, localOverride, err := setup.addedConnectionIdentity(body)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", err.Error())
		return
	}
	snapshot, err := setup.flowDefinitions.Load(request.Context())
	if err != nil {
		api.WriteCodedError(response, http.StatusServiceUnavailable, "FLOW_DEFINITION_SOURCE_UNAVAILABLE", "Flow Definition source is unavailable")
		return
	}
	_, isDeclared, _, err := connectorDefinitionForKey(
		snapshot.Response, identity.ConnectorID, identity.ConnectionName, setup.releases.overrideIdentities(),
	)
	if err != nil {
		api.WriteCodedError(response, http.StatusServiceUnavailable, "FLOW_DEFINITION_SOURCE_UNAVAILABLE", "Flow Definition source is unavailable")
		return
	}
	if isDeclared {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS", "A loaded Flow already uses this connection; set it up from its row")
		return
	}
	_, isStored, err := setup.store.get(identity.ConnectorID, identity.ConnectionName)
	if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_CONNECTIONS_UNAVAILABLE", "Connector connections are unavailable")
		return
	}
	if isStored {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS", "This connection already exists")
		return
	}
	if setup.hasConflictingPendingConnection(identity) {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS", "This connection was already added with another Connector release")
		return
	}
	release, err := setup.releases.releaseMetadata(request.Context(), identity)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_RELEASE_UNAVAILABLE", "Connector release metadata is unavailable")
		return
	}
	if err := setup.rememberPendingConnection(identity); errors.Is(err, errConnectorConnectionExists) {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS", "This connection already exists with another Connector release")
		return
	} else if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_CONNECTIONS_UNAVAILABLE", "Connector connections are unavailable")
		return
	}
	view := pendingConnectorView(identity, localOverride)
	view.DisplayName = strings.TrimSpace(release.Manifest.Metadata.DisplayName)
	writeWebJSON(response, http.StatusOK, view)
}

// addedConnectionIdentity returns the release identity of an added connection; a local release override wins.
func (setup *connectorSetup) addedConnectionIdentity(body connectorConnectionAddRequest) (connectorDefinitionIdentity, bool, error) {
	if !connectorReleaseIDPattern.MatchString(body.ConnectorID) {
		return connectorDefinitionIdentity{}, false, fmt.Errorf("Connector ID is invalid")
	}
	if override, found := setup.releases.overrideIdentities()[body.ConnectorID]; found {
		override.ConnectionName = body.ConnectionName
		return override, true, nil
	}
	if !officialConnectorModulePattern.MatchString(body.ModulePath) ||
		!exactConnectorReleaseVersionPattern.MatchString(body.ModuleVersion) {
		return connectorDefinitionIdentity{}, false, fmt.Errorf("Connector module must be an exact official release")
	}
	return connectorDefinitionIdentity{
		ConnectorID: body.ConnectorID, ConnectionName: body.ConnectionName,
		ModulePath: body.ModulePath, ModuleVersion: body.ModuleVersion, ConfigurationEnabled: true,
	}, false, nil
}

// resolveConnectionIdentity returns the identity a loaded Flow declares for a connection, or else its standalone
// identity. The error wraps errConnectorConnectionStoreUnavailable when only the local store failed.
func (setup *connectorSetup) resolveConnectionIdentity(
	catalogJSON []byte,
	connectorID string,
	connectionName string,
) (connectorDefinitionIdentity, bool, bool, error) {
	overrides := setup.releases.overrideIdentities()
	identity, found, conflict, err := connectorDefinitionForKey(catalogJSON, connectorID, connectionName, overrides)
	if err != nil || found {
		return identity, found, conflict, err
	}
	stored, isStored, err := setup.store.get(connectorID, connectionName)
	if err != nil {
		return connectorDefinitionIdentity{}, false, false, errors.Join(errConnectorConnectionStoreUnavailable, err)
	}
	if isStored {
		view := standaloneStoredConnectorView(stored, time.Now(), overrides)
		return connectorDefinitionIdentity{
			ConnectorID: connectorID, ConnectionName: connectionName, ModulePath: view.ModulePath,
			ModuleVersion: view.ModuleVersion, ConfigurationEnabled: view.Status != "Unsupported",
		}, true, false, nil
	}
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	pending, isPending := setup.pendingConnections[connectorConnectionKey(connectorID, connectionName)]
	return pending, isPending, false, nil
}

// connectionGeneration returns the key's current generation; it changes when the key is added or deleted.
func (setup *connectorSetup) connectionGeneration(connectorID string, connectionName string) uint64 {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	return setup.connectionGenerations[connectorConnectionKey(connectorID, connectionName)]
}

// rememberPendingConnection records an added connection under a new generation. It returns
// errConnectorConnectionExists when the file or another added release already holds the key, and
// errConnectorConnectionStoreUnavailable when the file cannot be read. Adding the same release again is a no-op.
func (setup *connectorSetup) rememberPendingConnection(identity connectorDefinitionIdentity) error {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	key := connectorConnectionKey(identity.ConnectorID, identity.ConnectionName)
	_, isStored, err := setup.store.get(identity.ConnectorID, identity.ConnectionName)
	if err != nil {
		return errors.Join(errConnectorConnectionStoreUnavailable, err)
	}
	if isStored || setup.doHasConflictingPendingConnection(identity) {
		return errConnectorConnectionExists
	}
	if _, isPending := setup.pendingConnections[key]; isPending {
		return nil
	}
	setup.pendingConnections[key] = identity
	setup.connectionGenerations[key]++
	return nil
}

func (setup *connectorSetup) hasConflictingPendingConnection(identity connectorDefinitionIdentity) bool {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	return setup.doHasConflictingPendingConnection(identity)
}

// doHasConflictingPendingConnection requires connectionsMu.
func (setup *connectorSetup) doHasConflictingPendingConnection(identity connectorDefinitionIdentity) bool {
	existing, found := setup.pendingConnections[connectorConnectionKey(identity.ConnectorID, identity.ConnectionName)]
	return found && (existing.ModulePath != identity.ModulePath || existing.ModuleVersion != identity.ModuleVersion)
}

// putConnectionIfCurrent saves a connection only while its key keeps the generation the write was authorized
// under. It returns errConnectorConnectionChanged when the key was deleted or added again since then.
func (setup *connectorSetup) putConnectionIfCurrent(
	connection localConnectorConnection,
	keepCredentialFields []string,
	generation uint64,
) error {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	key := connectorConnectionKey(connection.ConnectorID, connection.ConnectionName)
	if setup.connectionGenerations[key] != generation {
		return errConnectorConnectionChanged
	}
	if pending, isPending := setup.pendingConnections[key]; isPending &&
		(pending.ModulePath != connection.ModulePath || pending.ModuleVersion != connection.ModuleVersion) {
		return errConnectorConnectionChanged
	}
	if err := setup.store.put(connection, keepCredentialFields); err != nil {
		return err
	}
	delete(setup.pendingConnections, key)
	return nil
}

// deleteConnection deletes the stored record and any added connection for the key, and reports whether either existed.
func (setup *connectorSetup) deleteConnection(connectorID string, connectionName string) (bool, error) {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	key := connectorConnectionKey(connectorID, connectionName)
	// A write authorized before this delete must not recreate the key, even when the delete fails part way.
	setup.connectionGenerations[key]++
	deleted, err := setup.store.delete(connectorID, connectionName)
	if err != nil {
		return false, err
	}
	if _, isPending := setup.pendingConnections[key]; isPending {
		delete(setup.pendingConnections, key)
		deleted = true
	}
	return deleted, nil
}

func writeConnectorConnectionPutError(response http.ResponseWriter, err error, code string, message string) {
	if errors.Is(err, errConnectorConnectionChanged) {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_CHANGED", "Connector connection was deleted or added again; set it up again")
		return
	}
	writeConnectorConfigurationStoreError(response, err, code, message)
}

func (setup *connectorSetup) pendingConnectionSnapshot() map[string]connectorDefinitionIdentity {
	setup.connectionsMu.Lock()
	defer setup.connectionsMu.Unlock()
	snapshot := make(map[string]connectorDefinitionIdentity, len(setup.pendingConnections))
	for key, identity := range setup.pendingConnections {
		snapshot[key] = identity
	}
	return snapshot
}

// standaloneConnectorViews lists the stored and pending connections that none of graphViews covers.
func standaloneConnectorViews(
	graphViews []connectorConnectionView,
	stored map[string]storedConnectorConnection,
	pending map[string]connectorDefinitionIdentity,
	now time.Time,
	overrides map[string]connectorDefinitionIdentity,
) []connectorConnectionView {
	isListed := make(map[string]bool, len(graphViews)+len(stored))
	for _, view := range graphViews {
		isListed[connectorConnectionKey(view.ConnectorID, view.ConnectionName)] = true
	}
	views := make([]connectorConnectionView, 0)
	for key, connection := range stored {
		if isListed[key] {
			continue
		}
		isListed[key] = true
		views = append(views, standaloneStoredConnectorView(connection, now, overrides))
	}
	for key, identity := range pending {
		if isListed[key] {
			continue
		}
		_, localOverride := overrides[identity.ConnectorID]
		views = append(views, pendingConnectorView(identity, localOverride))
	}
	return views
}

// standaloneStoredConnectorView describes a stored connection by its own record; a local override replaces its release.
func standaloneStoredConnectorView(
	connection storedConnectorConnection,
	now time.Time,
	overrides map[string]connectorDefinitionIdentity,
) connectorConnectionView {
	view := connectorConnectionView{
		ConnectorID: connection.ConnectorID, ConnectionName: connection.ConnectionName,
		ModulePath: connection.ModulePath, ModuleVersion: connection.ModuleVersion,
		Provider: connection.Provider, AuthMethodID: connection.AuthMethodID, AuthMethodIDs: connection.AuthMethodIDs,
		Configuration: connection.Configuration, StoredCredentialFields: connection.StoredCredentialFields,
		CredentialExpiresAt: connection.CredentialExpiresAt, Uses: []connectorConnectionStepUse{}, IsStandalone: true,
	}
	if override, found := overrides[connection.ConnectorID]; found {
		view.ModulePath, view.ModuleVersion, view.LocalOverride = override.ModulePath, override.ModuleVersion, true
	}
	switch {
	case !view.LocalOverride && (!officialConnectorModulePattern.MatchString(view.ModulePath) ||
		!exactConnectorReleaseVersionPattern.MatchString(view.ModuleVersion)):
		view.Status = "Unsupported"
	case connection.CredentialExpiresAt != nil && !now.Before(*connection.CredentialExpiresAt):
		view.Status = "Expired"
	default:
		view.Status = "Ready"
	}
	return view
}

func pendingConnectorView(identity connectorDefinitionIdentity, localOverride bool) connectorConnectionView {
	return connectorConnectionView{
		ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName,
		ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, LocalOverride: localOverride,
		Status: "Missing", Uses: []connectorConnectionStepUse{}, IsStandalone: true,
	}
}

// validateAddedConnectorConnectionName accepts only names every Connector SDK accepts as a project connection name.
func validateAddedConnectorConnectionName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("Connection name is required")
	case !utf8.ValidString(name) || strings.TrimSpace(name) != name:
		return fmt.Errorf("Connection name must not start or end with a space")
	case utf8.RuneCountInString(name) > connectorConnectionNameLimit:
		return fmt.Errorf("Connection name must have at most %d characters", connectorConnectionNameLimit)
	case strings.Contains(name, "/") || strings.IndexFunc(name, unicode.IsControl) >= 0:
		return fmt.Errorf("Connection name must not contain a slash or a control character")
	}
	return nil
}

func connectorConnectionKey(connectorID string, connectionName string) string {
	return connectorID + "\x00" + connectionName
}

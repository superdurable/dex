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

// connectorConnectionWriteExpectation is the state a connection save was authorized against.
type connectorConnectionWriteExpectation struct {
	generation uint64
	// isFlowDeclared is true when a loaded Flow declared the connection at authorization.
	isFlowDeclared bool
	// storedRevision is the file record's revision at authorization, or empty when the file held none.
	storedRevision string
	// modulePath and moduleVersion are the release the write was authorized for.
	modulePath    string
	moduleVersion string
}

// newConnectorConnectionWriteExpectation records the Flow declaration and the stored record of a connection
// whose identity was just resolved, with the generation read before that resolution.
func (setup *connectorSetup) newConnectorConnectionWriteExpectation(
	catalogJSON []byte,
	identity connectorDefinitionIdentity,
	generation uint64,
) (connectorConnectionWriteExpectation, error) {
	_, isFlowDeclared, _, err := connectorDefinitionForKey(
		catalogJSON, identity.ConnectorID, identity.ConnectionName, setup.releases.overrideIdentities(),
	)
	if err != nil {
		return connectorConnectionWriteExpectation{}, err
	}
	storedRevision, err := setup.store.revision(identity.ConnectorID, identity.ConnectionName)
	if err != nil {
		return connectorConnectionWriteExpectation{}, err
	}
	return connectorConnectionWriteExpectation{
		generation: generation, isFlowDeclared: isFlowDeclared, storedRevision: storedRevision,
		modulePath: identity.ModulePath, moduleVersion: identity.ModuleVersion,
	}, nil
}

// putConnectionIfCurrent saves a connection of the authorized release as a compare-and-set under connectionsMu;
// see doCheckConnectionWriteIsCurrent. Otherwise it saves nothing and returns errConnectorConnectionChanged.
func (setup *connectorSetup) putConnectionIfCurrent(
	ctx context.Context,
	connection localConnectorConnection,
	keepCredentialFields []string,
	expectation connectorConnectionWriteExpectation,
) error {
	if !hasConnectorModule(connectorDefinitionIdentity{ModulePath: expectation.modulePath, ModuleVersion: expectation.moduleVersion},
		connection.ModulePath, connection.ModuleVersion) {
		return errConnectorConnectionChanged
	}
	fileLock, err := setup.beginConnectionWriteIfCurrent(ctx, connection.ConnectorID, connection.ConnectionName, expectation)
	if err != nil {
		return err
	}
	defer setup.endConnectionWrite(fileLock)
	if err := setup.store.put(fileLock, connection, keepCredentialFields, expectation.storedRevision); err != nil {
		return err
	}
	delete(setup.pendingConnections, connectorConnectionKey(connection.ConnectorID, connection.ConnectionName))
	return nil
}

// beginConnectionWriteIfCurrent locks in order: connections file lock, connectionsMu, then store.mu.
func (setup *connectorSetup) beginConnectionWriteIfCurrent(
	ctx context.Context,
	connectorID string,
	connectionName string,
	expectation connectorConnectionWriteExpectation,
) (*connectorConnectionsFileLock, error) {
	snapshot, err := setup.flowDefinitions.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load Flow definitions before writing a Connector connection: %w", err)
	}
	fileLock, err := setup.store.lockConnectionsFile(ctx)
	if err != nil {
		return nil, err
	}
	setup.connectionsMu.Lock()
	if err := setup.doCheckConnectionWriteIsCurrent(snapshot.Response, connectorID, connectionName, expectation); err != nil {
		setup.endConnectionWrite(fileLock)
		return nil, err
	}
	return fileLock, nil
}

// endConnectionWrite releases the locks that beginConnectionWriteIfCurrent took.
func (setup *connectorSetup) endConnectionWrite(fileLock *connectorConnectionsFileLock) {
	setup.connectionsMu.Unlock()
	fileLock.release()
}

func hasConnectorModule(identity connectorDefinitionIdentity, modulePath string, moduleVersion string) bool {
	return identity.ModulePath == modulePath && identity.ModuleVersion == moduleVersion
}

// doCheckConnectionWriteIsCurrent requires connectionsMu. It returns errConnectorConnectionChanged unless the key
// keeps the generation and the file keeps the record revision that the write was authorized against, and the
// current Flow definitions and any added connection still name the authorized release.
// Callers hold the connections file lock, so the revision check holds until their write.
func (setup *connectorSetup) doCheckConnectionWriteIsCurrent(
	catalogJSON []byte,
	connectorID string,
	connectionName string,
	expectation connectorConnectionWriteExpectation,
) error {
	key := connectorConnectionKey(connectorID, connectionName)
	if setup.connectionGenerations[key] != expectation.generation {
		return errConnectorConnectionChanged
	}
	storedRevision, err := setup.store.revision(connectorID, connectionName)
	if err != nil {
		return err
	}
	if storedRevision != expectation.storedRevision {
		return errConnectorConnectionChanged
	}
	declared, isFlowDeclared, isConflict, err := connectorDefinitionForKey(
		catalogJSON, connectorID, connectionName, setup.releases.overrideIdentities(),
	)
	if err != nil {
		return err
	}
	if isFlowDeclared != expectation.isFlowDeclared || isConflict ||
		(isFlowDeclared && !hasConnectorModule(declared, expectation.modulePath, expectation.moduleVersion)) {
		return errConnectorConnectionChanged
	}
	if pending, isPending := setup.pendingConnections[key]; isPending &&
		!hasConnectorModule(pending, expectation.modulePath, expectation.moduleVersion) {
		return errConnectorConnectionChanged
	}
	return nil
}

// putTriggerBindingIfCurrent saves a Trigger binding while its connection's write is still current and the binding
// keeps the revision it had at authorization, empty when it was absent.
func (setup *connectorSetup) putTriggerBindingIfCurrent(
	ctx context.Context,
	binding localConnectorTriggerBinding,
	expectation connectorConnectionWriteExpectation,
	bindingRevision string,
) error {
	fileLock, err := setup.beginConnectionWriteIfCurrent(ctx, binding.ConnectorID, binding.ConnectionName, expectation)
	if err != nil {
		return err
	}
	defer setup.endConnectionWrite(fileLock)
	return setup.store.putTriggerBinding(fileLock, binding, expectation.storedRevision, bindingRevision)
}

// deleteConnection deletes the stored record and any added connection for the key while the delete is still
// current, and reports whether either existed. It returns errConnectorConnectionChanged when it is not.
func (setup *connectorSetup) deleteConnection(
	ctx context.Context,
	connectorID string,
	connectionName string,
	expectation connectorConnectionWriteExpectation,
) (bool, error) {
	fileLock, err := setup.beginConnectionWriteIfCurrent(ctx, connectorID, connectionName, expectation)
	if err != nil {
		return false, err
	}
	defer setup.endConnectionWrite(fileLock)
	key := connectorConnectionKey(connectorID, connectionName)
	// A write authorized before this delete must not recreate the key, even when the delete fails part way.
	setup.connectionGenerations[key]++
	deleted, err := setup.store.delete(fileLock, connectorID, connectionName, expectation.storedRevision)
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

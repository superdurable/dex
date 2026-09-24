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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/web/api"
)

const connectorCSRFHeader = "X-Dex-CSRF-Token"

type connectorSetup struct {
	store           *connectorConnectionStore
	flowDefinitions FlowDefinitionProvider
	releases        *connectorReleaseResolver
	csrfToken       string
	uiSessions      map[string]connectorUISession
	uiSessionsMu    sync.Mutex
	oauthSessions   map[string]connectorOAuthSession
	oauthSessionsMu sync.Mutex
	slackAPIBaseURL string
	slackHTTPClient *http.Client
}

type connectorUISession struct {
	root       string
	entrypoint string
	expiresAt  time.Time
}

type connectorCatalogDocument struct {
	DefinitionRevision string                       `json:"definitionRevision"`
	Definitions        []connectorCatalogDefinition `json:"definitions"`
}

type connectorCatalogDefinition struct {
	FlowName string `json:"flowName"`
	Graph    struct {
		Nodes []connectorCatalogNode `json:"nodes"`
		V2    *struct {
			ConnectorTriggerBindings []connectorCatalogTriggerBinding `json:"connectorTriggerBindings"`
		} `json:"v2,omitempty"`
	} `json:"graph"`
}

type connectorCatalogTriggerBinding struct {
	ConnectorID          string `json:"connectorId"`
	TriggerName          string `json:"triggerName"`
	ConnectionName       string `json:"connectionName"`
	BindingName          string `json:"bindingName"`
	ModulePath           string `json:"modulePath"`
	ModuleVersion        string `json:"moduleVersion"`
	ConfigurationEnabled bool   `json:"configurationEnabled"`
}

type connectorCatalogNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Metadata struct {
		ConnectorFactory bool                         `json:"connectorFactory"`
		Connector        *connectorDefinitionIdentity `json:"connector"`
	} `json:"metadata"`
}

type connectorDefinitionIdentity struct {
	ConnectorID          string `json:"connectorId"`
	OperationID          string `json:"operationId"`
	OperationKind        string `json:"operationKind"`
	ConnectionName       string `json:"connectionName"`
	ModulePath           string `json:"modulePath"`
	ModuleVersion        string `json:"moduleVersion"`
	ConfigurationEnabled bool   `json:"configurationEnabled"`
}

type connectorConnectionView struct {
	ConnectorID         string                          `json:"connectorId"`
	ConnectionName      string                          `json:"connectionName"`
	ModulePath          string                          `json:"modulePath,omitempty"`
	ModuleVersion       string                          `json:"moduleVersion,omitempty"`
	Provider            string                          `json:"provider,omitempty"`
	Status              string                          `json:"status"`
	Configuration       map[string]json.RawMessage      `json:"configuration,omitempty"`
	CredentialExpiresAt *time.Time                      `json:"credentialExpiresAt,omitempty"`
	Uses                []connectorConnectionStepUse    `json:"uses"`
	TriggerUses         []connectorConnectionTriggerUse `json:"triggerUses,omitempty"`
}

type connectorConnectionStepUse struct {
	FlowName      string `json:"flowName"`
	StepID        string `json:"stepId"`
	StepName      string `json:"stepName"`
	OperationID   string `json:"operationId"`
	OperationKind string `json:"operationKind"`
}

type connectorConnectionTriggerUse struct {
	FlowName    string `json:"flowName"`
	TriggerName string `json:"triggerName"`
	BindingName string `json:"bindingName"`
}

type connectorConnectionListResponse struct {
	Enabled            bool                      `json:"enabled"`
	Directory          string                    `json:"directory"`
	FilePath           string                    `json:"filePath"`
	DefinitionRevision string                    `json:"definitionRevision"`
	CSRFToken          string                    `json:"csrfToken"`
	LaunchCommand      string                    `json:"launchCommand"`
	Connections        []connectorConnectionView `json:"connections"`
}

type connectorConnectionWriteRequest struct {
	ModulePath          string                     `json:"modulePath"`
	ModuleVersion       string                     `json:"moduleVersion"`
	Provider            string                     `json:"provider"`
	Configuration       map[string]json.RawMessage `json:"configuration"`
	Credentials         map[string]json.RawMessage `json:"credentials"`
	CredentialExpiresAt *time.Time                 `json:"credentialExpiresAt"`
}

type connectorUISessionRequest struct {
	ConnectorID    string `json:"connectorId"`
	ConnectionName string `json:"connectionName"`
}

type connectorUISessionResponse struct {
	ConnectorID     string                                           `json:"connectorId"`
	ConnectionName  string                                           `json:"connectionName"`
	SessionNonce    string                                           `json:"sessionNonce,omitempty"`
	EntrypointURL   string                                           `json:"entrypointUrl,omitempty"`
	Manifest        connectorReleaseManifest                         `json:"manifest"`
	TriggerBindings map[string]map[string]map[string]json.RawMessage `json:"triggerBindings,omitempty"`
}

type connectorTriggerBindingWriteRequest struct {
	Configuration map[string]json.RawMessage `json:"configuration"`
}

func newConnectorSetup(cfg *Config, flowDefinitions FlowDefinitionProvider) (*connectorSetup, error) {
	if !cfg.ConnectorSetupEnabled {
		return nil, nil
	}
	bindIP := net.ParseIP(cfg.BindAddress)
	if bindIP == nil || !bindIP.IsLoopback() {
		return nil, fmt.Errorf("Connector setup requires a loopback Dex Web bind address")
	}
	source := strings.TrimSpace(cfg.FlowRenderingSource)
	if source != "" && source != FlowRenderingSourceLocal {
		return nil, fmt.Errorf("Connector setup requires local Flow Definitions")
	}
	store, err := newConnectorConnectionStore(cfg.ConnectorConfigDirectory)
	if err != nil {
		return nil, err
	}
	csrfToken, err := randomConnectorToken(32)
	if err != nil {
		return nil, fmt.Errorf("create Connector CSRF token: %w", err)
	}
	releases, err := newConnectorReleaseResolver(store.directory)
	if err != nil {
		return nil, err
	}
	return &connectorSetup{
		store: store, flowDefinitions: flowDefinitions, releases: releases, csrfToken: csrfToken,
		uiSessions: make(map[string]connectorUISession), oauthSessions: make(map[string]connectorOAuthSession),
		slackAPIBaseURL: "https://slack.com/api", slackHTTPClient: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (setup *connectorSetup) registerHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/connector-connections", setup.handleListConnections)
	mux.HandleFunc("PUT /api/v2/connector-connections/{connectorId}/{connectionName}", setup.handlePutConnection)
	mux.HandleFunc("DELETE /api/v2/connector-connections/{connectorId}/{connectionName}", setup.handleDeleteConnection)
	mux.HandleFunc("POST /api/v2/connector-ui-sessions", setup.handleCreateUISession)
	mux.HandleFunc("GET /api/v2/connector-ui-sessions/{sessionNonce}/{assetPath...}", setup.handleUIAsset)
	mux.HandleFunc("PUT /api/v2/connector-trigger-bindings/{connectorId}/{connectionName}/{triggerName}/{bindingName}", setup.handlePutTriggerBinding)
	mux.HandleFunc("GET /api/v2/connector-connections/{connectorId}/{connectionName}/slack/channels", setup.handleListSlackChannels)
	mux.HandleFunc("GET /api/v2/connector-connections/{connectorId}/{connectionName}/slack/users", setup.handleListSlackUsers)
	mux.HandleFunc("POST /api/v2/connector-connections/{connectorId}/{connectionName}/oauth/start", setup.handleOAuthStart)
	mux.HandleFunc("GET /api/v2/connector-oauth/callback", setup.handleOAuthCallback)
}

func (setup *connectorSetup) handleListConnections(response http.ResponseWriter, request *http.Request) {
	views, revision, err := setup.connectionViews(request.Context())
	if err != nil {
		api.WriteCodedError(response, http.StatusServiceUnavailable, "CONNECTOR_CONNECTIONS_UNAVAILABLE", "Connector connections are unavailable")
		return
	}
	writeWebJSON(response, http.StatusOK, connectorConnectionListResponse{
		Enabled: true, Directory: setup.store.directory, FilePath: setup.store.path,
		DefinitionRevision: revision, CSRFToken: setup.csrfToken,
		LaunchCommand: "DEX_CONNECTOR_CONFIG_FILE=" + shellQuote(setup.store.path) + " <your-app-command>",
		Connections:   views,
	})
}

func (setup *connectorSetup) handlePutConnection(response http.ResponseWriter, request *http.Request) {
	snapshot, requested, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body connectorConnectionWriteRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector connection request is invalid")
		return
	}
	if body.ModulePath != requested.ModulePath || body.ModuleVersion != requested.ModuleVersion {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_VERSION_CONFLICT", "Connector module does not match the current Flow Definition")
		return
	}
	resolved, err := setup.releases.resolve(request.Context(), requested)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_RELEASE_UNAVAILABLE", "Connector release metadata is unavailable")
		return
	}
	manifest := resolved.release.Manifest
	if body.Provider != "" && body.Provider != manifest.Spec.Provider {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector provider does not match the official release")
		return
	}
	if err := validateRawConnectorFields(manifest.Spec.Configuration.Fields, body.Configuration, false); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector configuration is invalid")
		return
	}
	if err := validateRawConnectorFields(manifest.Spec.Auth.Fields, body.Credentials, true); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector credentials are invalid")
		return
	}
	connection := localConnectorConnection{
		ConnectorID: request.PathValue("connectorId"), ModulePath: body.ModulePath,
		ModuleVersion: body.ModuleVersion, Provider: manifest.Spec.Provider,
		ConnectionName: request.PathValue("connectionName"), Configuration: body.Configuration,
		Credentials: body.Credentials, CredentialExpiresAt: body.CredentialExpiresAt,
	}
	if err := validateLocalConnectorConnection(connection); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector connection request is invalid")
		return
	}
	if err := setup.store.put(connection); err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_CONNECTION_WRITE_FAILED", "Connector connection could not be saved")
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{
		"connectorId": connection.ConnectorID, "connectionName": connection.ConnectionName,
		"status": "Ready", "filePath": setup.store.path, "definitionRevision": snapshot.DefinitionRevision,
		"credentialExpiresAt": connection.CredentialExpiresAt,
	})
}

func (setup *connectorSetup) handleDeleteConnection(response http.ResponseWriter, request *http.Request) {
	_, _, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return
	}
	deleted, err := setup.store.delete(request.PathValue("connectorId"), request.PathValue("connectionName"))
	if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_CONNECTION_DELETE_FAILED", "Local Connector credentials could not be deleted")
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"deleted": deleted, "remoteGrantRevoked": false})
}

func (setup *connectorSetup) handleCreateUISession(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
	var body connectorUISessionRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector UI session request is invalid")
		return
	}
	_, identity, ok := setup.authorizeConnectionKey(response, request, body.ConnectorID, body.ConnectionName)
	if !ok {
		return
	}
	resolved, err := setup.releases.resolve(request.Context(), identity)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_RELEASE_UNAVAILABLE", "Connector release metadata or UI is unavailable")
		return
	}
	result := connectorUISessionResponse{
		ConnectorID: body.ConnectorID, ConnectionName: body.ConnectionName, Manifest: resolved.release.Manifest,
	}
	bindings, err := setup.store.listTriggerBindings(body.ConnectorID, body.ConnectionName)
	if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_TRIGGER_BINDINGS_UNAVAILABLE", "Connector Trigger bindings are unavailable")
		return
	}
	if len(bindings) > 0 {
		result.TriggerBindings = make(map[string]map[string]map[string]json.RawMessage)
		for _, binding := range bindings {
			if result.TriggerBindings[binding.TriggerName] == nil {
				result.TriggerBindings[binding.TriggerName] = make(map[string]map[string]json.RawMessage)
			}
			result.TriggerBindings[binding.TriggerName][binding.BindingName] = binding.Configuration
		}
	}
	if resolved.uiRoot != "" && resolved.release.UI != nil {
		nonce, err := randomConnectorToken(24)
		if err != nil {
			api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_UI_SESSION_FAILED", "Connector UI session could not be created")
			return
		}
		setup.uiSessionsMu.Lock()
		setup.deleteExpiredUISessions(time.Now())
		setup.uiSessions[nonce] = connectorUISession{
			root: resolved.uiRoot, entrypoint: resolved.release.UI.Entrypoint, expiresAt: time.Now().Add(10 * time.Minute),
		}
		setup.uiSessionsMu.Unlock()
		result.SessionNonce = nonce
		result.EntrypointURL = "/api/v2/connector-ui-sessions/" + nonce + "/" + resolved.release.UI.Entrypoint
	}
	writeWebJSON(response, http.StatusOK, result)
}

func (setup *connectorSetup) handlePutTriggerBinding(response http.ResponseWriter, request *http.Request) {
	snapshot, identity, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return
	}
	triggerName := request.PathValue("triggerName")
	bindingName := request.PathValue("bindingName")
	binding, found, conflict, err := connectorTriggerDefinitionForKey(
		snapshot.Response, identity.ConnectorID, identity.ConnectionName, triggerName, bindingName,
	)
	if err != nil || !found || !binding.ConfigurationEnabled {
		api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_TRIGGER_BINDING_UNSUPPORTED", "Connector Trigger binding is not configurable")
		return
	}
	if conflict || binding.ModulePath != identity.ModulePath || binding.ModuleVersion != identity.ModuleVersion {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_VERSION_CONFLICT", "Connector Trigger binding conflicts with the current Flow Definition")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body connectorTriggerBindingWriteRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil || body.Configuration == nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID", "Connector Trigger binding request is invalid")
		return
	}
	if err := validateConnectorTriggerBindingConfiguration(identity.ConnectorID, triggerName, body.Configuration); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_TRIGGER_CONFIGURATION_INVALID", err.Error())
		return
	}
	localBinding := localConnectorTriggerBinding{
		ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName,
		TriggerName: triggerName, BindingName: bindingName, Configuration: body.Configuration,
	}
	if err := setup.store.putTriggerBinding(localBinding); err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_TRIGGER_BINDING_WRITE_FAILED", "Connector Trigger binding could not be saved")
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{
		"connectorId": identity.ConnectorID, "connectionName": identity.ConnectionName,
		"triggerName": triggerName, "bindingName": bindingName,
	})
}

func (setup *connectorSetup) handleUIAsset(response http.ResponseWriter, request *http.Request) {
	nonce := request.PathValue("sessionNonce")
	setup.uiSessionsMu.Lock()
	setup.deleteExpiredUISessions(time.Now())
	session, found := setup.uiSessions[nonce]
	setup.uiSessionsMu.Unlock()
	if !found {
		api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_UI_SESSION_NOT_FOUND", "Connector UI session is missing or expired")
		return
	}
	serveConnectorUIAsset(response, request, request.PathValue("assetPath"), session.root)
}

func (setup *connectorSetup) deleteExpiredUISessions(now time.Time) {
	for nonce, session := range setup.uiSessions {
		if !now.Before(session.expiresAt) {
			delete(setup.uiSessions, nonce)
		}
	}
}

func (setup *connectorSetup) authorizeConnectionWrite(
	response http.ResponseWriter,
	request *http.Request,
) (*FlowDefinitionSnapshot, connectorDefinitionIdentity, bool) {
	snapshot, identity, ok := setup.authorizeConnectionKey(
		response,
		request,
		request.PathValue("connectorId"),
		request.PathValue("connectionName"),
	)
	if !ok {
		return nil, connectorDefinitionIdentity{}, false
	}
	return snapshot, identity, true
}

func (setup *connectorSetup) authorizeConnectionKey(
	response http.ResponseWriter,
	request *http.Request,
	connectorID string,
	connectionName string,
) (*FlowDefinitionSnapshot, connectorDefinitionIdentity, bool) {
	if request.Header.Get(connectorCSRFHeader) != setup.csrfToken || !hasStrictConnectorOrigin(request) {
		api.WriteCodedError(response, http.StatusForbidden, "CONNECTOR_WRITE_FORBIDDEN", "Connector write origin or CSRF token is invalid")
		return nil, connectorDefinitionIdentity{}, false
	}
	snapshot, err := setup.flowDefinitions.Load(request.Context())
	if err != nil {
		api.WriteCodedError(response, http.StatusServiceUnavailable, "FLOW_DEFINITION_SOURCE_UNAVAILABLE", "Flow Definition source is unavailable")
		return nil, connectorDefinitionIdentity{}, false
	}
	if snapshot.DefinitionRevision == "" || request.Header.Get(api.V2DefinitionRevisionHeader) != snapshot.DefinitionRevision {
		api.WriteCodedError(response, http.StatusConflict, "FLOW_DEFINITION_REVISION_CONFLICT", "Flow Definition revision is stale")
		return nil, connectorDefinitionIdentity{}, false
	}
	requested, found, conflict, err := connectorDefinitionForKey(
		snapshot.Response,
		connectorID,
		connectionName,
	)
	if err != nil {
		api.WriteCodedError(response, http.StatusServiceUnavailable, "FLOW_DEFINITION_SOURCE_UNAVAILABLE", "Flow Definition source is unavailable")
		return nil, connectorDefinitionIdentity{}, false
	}
	if !found || !requested.ConfigurationEnabled {
		api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_CONNECTION_UNSUPPORTED", "Connector connection is not configurable")
		return nil, connectorDefinitionIdentity{}, false
	}
	if conflict {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_VERSION_CONFLICT", "Connector connection has conflicting module versions")
		return nil, connectorDefinitionIdentity{}, false
	}
	return snapshot, requested, true
}

func (setup *connectorSetup) connectionViews(ctx context.Context) ([]connectorConnectionView, string, error) {
	snapshot, err := setup.flowDefinitions.Load(ctx)
	if err != nil {
		return nil, "", err
	}
	connections, err := setup.store.list()
	if err != nil {
		return nil, "", err
	}
	stored := make(map[string]localConnectorConnection, len(connections))
	for _, connection := range connections {
		stored[connection.ConnectorID+"\x00"+connection.ConnectionName] = connection
	}
	views, err := connectorViewsFromCatalog(snapshot.Response, stored, time.Now())
	if err != nil {
		return nil, "", err
	}
	return views, snapshot.DefinitionRevision, nil
}

func connectorViewsFromCatalog(
	catalogJSON []byte,
	stored map[string]localConnectorConnection,
	now time.Time,
) ([]connectorConnectionView, error) {
	var catalog connectorCatalogDocument
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return nil, fmt.Errorf("decode Flow Definition catalog for Connector connections: %w", err)
	}
	type aggregate struct {
		view     connectorConnectionView
		versions map[string]bool
		modules  map[string]bool
		enabled  bool
	}
	aggregates := make(map[string]*aggregate)
	for _, definition := range catalog.Definitions {
		for _, node := range definition.Graph.Nodes {
			identity := node.Metadata.Connector
			if node.Kind != "step" || !node.Metadata.ConnectorFactory || identity == nil {
				continue
			}
			key := identity.ConnectorID + "\x00" + identity.ConnectionName
			current := aggregates[key]
			if current == nil {
				current = &aggregate{
					view: connectorConnectionView{
						ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName,
						ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion,
					},
					versions: make(map[string]bool), modules: make(map[string]bool), enabled: true,
				}
				aggregates[key] = current
			}
			current.enabled = current.enabled && identity.ConfigurationEnabled
			current.versions[identity.ModuleVersion] = true
			current.modules[identity.ModulePath] = true
			current.view.Uses = append(current.view.Uses, connectorConnectionStepUse{
				FlowName: definition.FlowName, StepID: node.ID, StepName: node.Name,
				OperationID: identity.OperationID, OperationKind: identity.OperationKind,
			})
		}
		if definition.Graph.V2 == nil {
			continue
		}
		for _, binding := range definition.Graph.V2.ConnectorTriggerBindings {
			key := binding.ConnectorID + "\x00" + binding.ConnectionName
			current := aggregates[key]
			if current == nil {
				current = &aggregate{
					view: connectorConnectionView{
						ConnectorID: binding.ConnectorID, ConnectionName: binding.ConnectionName,
						ModulePath: binding.ModulePath, ModuleVersion: binding.ModuleVersion,
					},
					versions: make(map[string]bool), modules: make(map[string]bool), enabled: true,
				}
				aggregates[key] = current
			}
			current.enabled = current.enabled && binding.ConfigurationEnabled
			current.versions[binding.ModuleVersion] = true
			current.modules[binding.ModulePath] = true
			current.view.TriggerUses = append(current.view.TriggerUses, connectorConnectionTriggerUse{
				FlowName: definition.FlowName, TriggerName: binding.TriggerName, BindingName: binding.BindingName,
			})
		}
	}
	views := make([]connectorConnectionView, 0, len(aggregates))
	for key, current := range aggregates {
		current.view.Status = "Missing"
		if !current.enabled || current.view.ConnectionName == "" {
			current.view.Status = "Unsupported"
		} else if len(current.versions) > 1 || len(current.modules) > 1 {
			current.view.Status = "Conflict"
		} else if connection, ok := stored[key]; ok {
			current.view.Provider = connection.Provider
			current.view.Configuration = connection.Configuration
			current.view.CredentialExpiresAt = connection.CredentialExpiresAt
			if connection.ModulePath != current.view.ModulePath || connection.ModuleVersion != current.view.ModuleVersion {
				current.view.Status = "Conflict"
			} else if connection.CredentialExpiresAt != nil && !now.Before(*connection.CredentialExpiresAt) {
				current.view.Status = "Expired"
			} else {
				current.view.Status = "Ready"
			}
		}
		sort.Slice(current.view.Uses, func(left int, right int) bool {
			if current.view.Uses[left].FlowName == current.view.Uses[right].FlowName {
				return current.view.Uses[left].StepID < current.view.Uses[right].StepID
			}
			return current.view.Uses[left].FlowName < current.view.Uses[right].FlowName
		})
		sort.Slice(current.view.TriggerUses, func(left int, right int) bool {
			if current.view.TriggerUses[left].FlowName != current.view.TriggerUses[right].FlowName {
				return current.view.TriggerUses[left].FlowName < current.view.TriggerUses[right].FlowName
			}
			return current.view.TriggerUses[left].BindingName < current.view.TriggerUses[right].BindingName
		})
		views = append(views, current.view)
	}
	sort.Slice(views, func(left int, right int) bool {
		if views[left].ConnectorID == views[right].ConnectorID {
			return views[left].ConnectionName < views[right].ConnectionName
		}
		return views[left].ConnectorID < views[right].ConnectorID
	})
	return views, nil
}

func connectorTriggerDefinitionForKey(
	catalogJSON []byte,
	connectorID string,
	connectionName string,
	triggerName string,
	bindingName string,
) (connectorCatalogTriggerBinding, bool, bool, error) {
	var catalog connectorCatalogDocument
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return connectorCatalogTriggerBinding{}, false, false, fmt.Errorf("decode Flow Definition catalog for Connector Trigger bindings: %w", err)
	}
	var matched connectorCatalogTriggerBinding
	found := false
	conflict := false
	for _, definition := range catalog.Definitions {
		if definition.Graph.V2 == nil {
			continue
		}
		for _, binding := range definition.Graph.V2.ConnectorTriggerBindings {
			if binding.ConnectorID != connectorID || binding.ConnectionName != connectionName ||
				binding.TriggerName != triggerName || binding.BindingName != bindingName {
				continue
			}
			if found && (matched.ModulePath != binding.ModulePath || matched.ModuleVersion != binding.ModuleVersion) {
				conflict = true
			}
			matched = binding
			found = true
		}
	}
	return matched, found, conflict, nil
}

func connectorDefinitionForKey(catalogJSON []byte, connectorID string, connectionName string) (connectorDefinitionIdentity, bool, bool, error) {
	views, err := connectorViewsFromCatalog(catalogJSON, nil, time.Now())
	if err != nil {
		return connectorDefinitionIdentity{}, false, false, err
	}
	for _, view := range views {
		if view.ConnectorID != connectorID || view.ConnectionName != connectionName {
			continue
		}
		return connectorDefinitionIdentity{
			ConnectorID: connectorID, ConnectionName: connectionName, ModulePath: view.ModulePath,
			ModuleVersion: view.ModuleVersion, ConfigurationEnabled: view.Status != "Unsupported",
		}, true, view.Status == "Conflict", nil
	}
	return connectorDefinitionIdentity{}, false, false, nil
}

func hasStrictConnectorOrigin(request *http.Request) bool {
	originValue := request.Header.Get("Origin")
	origin, err := url.Parse(originValue)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.Path != "" {
		return false
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return origin.Scheme == scheme && origin.Host == request.Host
}

func randomConnectorToken(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

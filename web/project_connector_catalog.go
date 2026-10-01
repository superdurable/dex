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
	"strconv"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
)

func (setup *connectorSetup) authorizeProjectConnection(response http.ResponseWriter, request *http.Request, connectorID, connectionName string) (*connectorAuthorizationSnapshot, connectorDefinitionIdentity, bool) {
	if _, err := projectActor(request); err != nil {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Authenticated project actor is required")
		return nil, connectorDefinitionIdentity{}, false
	}
	csrfToken := webRequestConfigFromContext(request.Context()).csrfToken
	if csrfToken == "" || request.Header.Get(connectorCSRFHeader) != csrfToken || request.Header.Get("Origin") != webRequestConfigFromContext(request.Context()).publicOrigin {
		api.WriteCodedError(response, 403, "CONNECTOR_WRITE_FORBIDDEN", "Connector origin or CSRF token is invalid")
		return nil, connectorDefinitionIdentity{}, false
	}
	manifest, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return nil, connectorDefinitionIdentity{}, false
	}
	revision, err := projectRevision(request.Header.Get(projectManifestRevisionHeader))
	if err != nil || revision != manifest.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return nil, connectorDefinitionIdentity{}, false
	}
	for _, declaration := range manifest.Manifest.Connectors {
		if declaration.ConnectorID == connectorID && declaration.ConnectionName == connectionName {
			snapshot := &connectorAuthorizationSnapshot{AppManifestRevision: manifest.Revision}
			if request.PathValue("operationId") != "" {
				definitions, loadErr := setup.flowDefinitions.Load(request.Context())
				if loadErr != nil {
					writeFlowDefinitionSourceError(response, loadErr)
					return nil, connectorDefinitionIdentity{}, false
				}
				if definitions.DefinitionRevision == "" || request.Header.Get(api.V2DefinitionRevisionHeader) != definitions.DefinitionRevision {
					api.WriteCodedError(response, 409, "FLOW_DEFINITION_REVISION_CONFLICT", "Flow Definition revision is stale")
					return nil, connectorDefinitionIdentity{}, false
				}
				snapshot.DefinitionRevision, snapshot.Response = definitions.DefinitionRevision, definitions.Response
			}
			return snapshot, projectConnectorIdentity(declaration), true
		}
	}
	api.WriteCodedError(response, 404, "CONNECTOR_CONNECTION_UNSUPPORTED", "Connection is not declared by the AppManifest")
	return nil, connectorDefinitionIdentity{}, false
}

func (setup *connectorSetup) handleListProjectConnections(response http.ResponseWriter, request *http.Request) {
	if !setup.project.authorizeReader(response, request) {
		return
	}
	manifest, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	store := &projectConnectorStore{owner: setup.project, ctx: request.Context()}
	if err = store.load(); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	views := make([]connectorConnectionView, 0, len(manifest.Manifest.Connectors))
	for _, declaration := range manifest.Manifest.Connectors {
		identity := projectConnectorIdentity(declaration)
		view := connectorConnectionView{ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName, ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, AuthMethodID: declaration.AuthMethodID, Status: "Missing", Uses: []connectorConnectionStepUse{}}
		stored, found, readErr := store.get(identity.ConnectorID, identity.ConnectionName)
		if readErr != nil {
			writeProjectConfigurationError(response, readErr)
			return
		}
		if found {
			view.Configuration, view.Provider, view.StoredCredentialFields, view.CredentialStatus, view.CredentialExpiresAt = stored.Configuration, stored.Provider, stored.StoredCredentialFields, stored.CredentialStatus, stored.CredentialExpiresAt
		}
		metadata, readErr := setup.project.connections.ReadConnection(request.Context(), projectconfig.ConnectionKey{ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName})
		if readErr != nil && !errors.Is(readErr, projectconfig.ErrObjectNotFound) {
			writeProjectConfigurationError(response, readErr)
			return
		}
		if readErr == nil {
			view.CredentialRevision = metadata.Revision
			if metadata.ModuleVersion != identity.ModuleVersion || metadata.AuthMethod != declaration.AuthMethodID || (found && stored.ModulePath != identity.ModulePath) {
				view.Status = "Conflict"
			} else if metadata.Status == projectconfig.CredentialReady {
				view.Status = "Ready"
				if metadata.ExpiresAt != nil && !time.Now().Before(*metadata.ExpiresAt) {
					view.Status = "Expired"
				}
			}
		}

		if len(declaration.TriggerBindings) > 0 {
			resolved, resolveErr := setup.resolveConnectorRelease(request.Context(), identity)
			if resolveErr != nil {
				api.WriteCodedError(response, 502, "CONNECTOR_RELEASE_UNAVAILABLE", "Verified connector release is unavailable")
				return
			}
			bindings, readErr := store.listTriggerBindings(identity.ConnectorID, identity.ConnectionName)
			if readErr != nil {
				writeProjectConfigurationError(response, readErr)
				return
			}
			for _, binding := range declaration.TriggerBindings {
				use := connectorConnectionTriggerUse{TriggerName: binding.TriggerName, BindingName: binding.BindingName, Configuration: map[string]json.RawMessage{}}
				trigger, found := projectTriggerSchema(resolved.release.Manifest, binding.TriggerName)
				if found && trigger.Configuration != nil {
					use.SchemaAvailable = true
					use.ConfigurationFields = trigger.Configuration.Fields
				}
				for _, stored := range bindings {
					if stored.TriggerName == binding.TriggerName && stored.BindingName == binding.BindingName {
						use.Configuration = stored.Configuration
						use.Configured = true
					}
				}
				view.TriggerUses = append(view.TriggerUses, use)
			}
		}
		views = append(views, view)
	}
	setup.applyConnectorDisplayNames(request.Context(), views)
	state := store.state()
	result := connectorConnectionListResponse{Enabled: true, Mode: ConnectorSetupModeProject, AppManifestRevision: manifest.Revision, ConfigurationRevision: state.ConfigurationRevision, ConfigurationState: state.ConfigurationState, CSRFToken: webRequestConfigFromContext(request.Context()).csrfToken, Connections: views}
	writeWebJSON(response, 200, result)
}

func projectConnectorIdentity(declaration projectConnectorDeclaration) connectorDefinitionIdentity {
	return connectorDefinitionIdentity{ConnectorID: declaration.ConnectorID, ConnectionName: declaration.ConnectionName, ModulePath: declaration.ModulePath, ModuleVersion: declaration.Version, ConfigurationEnabled: true}
}

type projectConfigurationValidationRequest struct {
	AppManifestRevision   uint64 `json:"appManifestRevision"`
	ManifestDigest        string `json:"manifestDigest"`
	ConfigurationRevision uint64 `json:"configurationRevision"`
}

func (setup *connectorSetup) handleValidateProjectConfiguration(response http.ResponseWriter, request *http.Request) {
	if !setup.project.authorizeAdmin(response, request) {
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
	var body projectConfigurationValidationRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, 400, "PROJECT_CONFIGURATION_INVALID", "Configuration validation request is invalid")
		return
	}
	manifest, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if manifest.Revision != body.AppManifestRevision || manifest.ManifestDigest != body.ManifestDigest {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	store := &projectConnectorStore{owner: setup.project, ctx: request.Context()}
	if err = store.load(); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if store.document.Revision != body.ConfigurationRevision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	for _, declaration := range manifest.Manifest.Connectors {
		identity := projectConnectorIdentity(declaration)
		resolved, resolveErr := setup.resolveConnectorRelease(request.Context(), identity)
		if resolveErr != nil {
			api.WriteCodedError(response, 502, "CONNECTOR_RELEASE_UNAVAILABLE", "Verified connector release is unavailable")
			return
		}
		stored, found, readErr := store.get(identity.ConnectorID, identity.ConnectionName)
		if readErr != nil {
			writeProjectConfigurationError(response, readErr)
			return
		}
		for _, entry := range store.document.Connections {
			if entry.ConnectorID == identity.ConnectorID && entry.ConnectionName == identity.ConnectionName && !sameLocalConnectorArtifact(entry.LocalArtifact, resolved.release.LocalArtifact) {
				api.WriteCodedError(response, 409, "LOCAL_CONNECTOR_SOURCE_CHANGED", "Reconfigure the connection for the reviewed local source")
				return
			}
		}
		method, declared := resolved.release.Manifest.Spec.Auth.method(declaration.AuthMethodID)
		if !found || !declared || stored.ModulePath != identity.ModulePath || stored.Provider != resolved.release.Manifest.Spec.Provider || stored.ModuleVersion != identity.ModuleVersion || stored.AuthMethodID != declaration.AuthMethodID || stored.CredentialStatus != string(projectconfig.CredentialReady) {
			api.WriteCodedError(response, 409, "PROJECT_CONFIGURATION_NOT_READY", "A declared connection requires configuration or authorization")
			return
		}
		if err = validateProjectFields(connectorConfigurationFieldsForMethods(resolved.release.Manifest.Spec.Configuration.Fields, []connectorManifestAuthMethod{method}), stored.Configuration); err != nil {
			api.WriteCodedError(response, 409, "PROJECT_CONFIGURATION_NOT_READY", "A declared connection configuration is incomplete")
			return
		}
		credentials := make(map[string]json.RawMessage, len(stored.Credentials))
		for name, value := range stored.Credentials {
			if name != "auth_method" && name != "auth_methods" {
				credentials[name] = value
			}
		}
		if err = validateRawConnectorFields(method.Fields, credentials, true, nil); err != nil {
			api.WriteCodedError(response, 409, "PROJECT_CONFIGURATION_NOT_READY", "A declared connection requires authorization")
			return
		}
		bindings, readErr := store.listTriggerBindings(identity.ConnectorID, identity.ConnectionName)
		if readErr != nil {
			writeProjectConfigurationError(response, readErr)
			return
		}
		for _, binding := range declaration.TriggerBindings {
			trigger, exists := projectTriggerSchema(resolved.release.Manifest, binding.TriggerName)
			if !exists || trigger.Configuration == nil {
				api.WriteCodedError(response, 409, "CONNECTOR_TRIGGER_SCHEMA_UNAVAILABLE", "The pinned connector release does not publish this trigger configuration schema")
				return
			}
			foundBinding := false
			for _, stored := range bindings {
				if stored.TriggerName == binding.TriggerName && stored.BindingName == binding.BindingName {
					foundBinding = true
					if validationErr := validateProjectFields(trigger.Configuration.Fields, stored.Configuration); validationErr != nil {
						api.WriteCodedError(response, 409, "CONNECTOR_TRIGGER_CONFIGURATION_INVALID", "Trigger configuration is invalid")
						return
					}
				}
			}
			if !foundBinding {
				api.WriteCodedError(response, 409, "CONNECTOR_TRIGGER_CONFIGURATION_REQUIRED", "Configure every declared trigger binding before validation")
				return
			}
		}

	}
	configurations, err := setup.project.configurationStore()
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if err = configurations.ValidateApplicationEnvironment(request.Context(), manifest.Manifest.Application.Environment, store.document); err != nil {
		api.WriteCodedError(response, 409, "APPLICATION_ENVIRONMENT_NOT_READY", "Declared application environment is incomplete or invalid")
		return
	}
	if store.document.Revision == 0 {
		document, _, writeErr := configurations.WriteConfiguration(request.Context(), 0, store.document)
		if writeErr != nil {
			writeProjectConfigurationError(response, writeErr)
			return
		}
		store.document = document
	}
	snapshot, err := configurations.FreezeConfiguration(request.Context(), store.document.Revision)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	current, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if current.Revision != manifest.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	result := projectManifestSummary(manifest)
	result["scope"] = setup.project.scope
	result["configurationRevision"], result["status"], result["snapshot"] = store.document.Revision, "READY", snapshot
	writeWebJSON(response, 200, result)
}

func (setup *connectorSetup) requireDeclaredProjectAuth(request *http.Request, identity connectorDefinitionIdentity, method string) bool {
	if setup.project == nil {
		return true
	}
	record, _, err := setup.project.readManifest(request.Context())
	if err != nil || strconv.FormatUint(record.Revision, 10) != request.Header.Get(projectManifestRevisionHeader) {
		return false
	}
	for _, declaration := range record.Manifest.Connectors {
		if declaration.ConnectorID == identity.ConnectorID && declaration.ConnectionName == identity.ConnectionName {
			return declaration.AuthMethodID == method
		}
	}
	return false
}

func (setup *connectorSetup) resolveConnectorRelease(ctx context.Context, identity connectorDefinitionIdentity) (resolvedConnectorRelease, error) {
	resolved, err := setup.releases.resolve(ctx, identity)
	if err != nil || setup.project == nil {
		return resolved, err
	}
	auth := resolved.release.Manifest.Spec.Auth
	if len(auth.Methods) == 0 {
		method, found := auth.method("")
		if !found {
			return resolved, errors.New("connector authorization declaration is absent")
		}
		method.ID = "default"
		method.DisplayName = "Default authorization"
		auth.Methods = []connectorManifestAuthMethod{method}
		auth.DefaultMethod = "default"
		resolved.release.Manifest.Spec.Auth = auth
	}
	return resolved, nil
}

func sameLocalConnectorArtifact(left, right *projectconfig.LocalConnectorArtifact) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
)

const projectManifestSchema = "dex.dev/project-app-manifest/v1"
const projectActorHeader = "X-Dex-Actor-ID"
const projectManifestRevisionHeader = "X-Dex-App-Manifest-Revision"
const projectConfigurationRevisionHeader = "X-Dex-Configuration-Revision"
const projectCredentialRevisionHeader = "X-Dex-Credential-Revision"

// ProjectConfigurationConfig fixes storage, scope, and internal admission for the process lifetime.
type ProjectConfigurationConfig struct {
	// Objects is required and owns conditional, encrypted, versioned object storage.
	Objects projectconfig.ObjectStore
	// ProjectID is required and cannot be selected by an HTTP request.
	ProjectID string
	// ScopeKind is required and must be live or preview.
	ScopeKind string
	// SessionID is required only for Preview and cannot change after startup.
	SessionID string
	// AdminToken admits internal manifest and validation requests. Required, never exposed to browsers.
	AdminToken string
}

// ProjectConfiguration owns project configuration independently of application Releases and Flow Definitions.
type ProjectConfiguration struct {
	objects        projectconfig.ObjectStore
	connections    *projectconfig.ConnectionStore
	scope          projectconfig.Scope
	prefix         string
	adminToken     string
	localArtifacts map[string]projectconfig.LocalConnectorAuthority
}

type projectAppManifest struct {
	SchemaVersion string `json:"schemaVersion"`
	Application   struct {
		Port        int                                    `json:"port"`
		HealthPath  string                                 `json:"healthPath"`
		Environment []projectconfig.EnvironmentDeclaration `json:"environment,omitempty"`
	} `json:"application"`
	FlowDefinitions []struct {
		SourcePath string `json:"sourcePath"`
	} `json:"flowDefinitions"`
	Connectors []projectConnectorDeclaration `json:"connectors"`
}

type projectTriggerBinding struct {
	TriggerName string `json:"triggerName"`
	BindingName string `json:"bindingName"`
}

type projectConnectorDeclaration struct {
	TriggerBindings []projectTriggerBinding `json:"triggerBindings,omitempty"`
	ModulePath      string                  `json:"modulePath"`
	ConnectionName  string                  `json:"connectionName"`
	ConnectorID     string                  `json:"connectorId"`
	Version         string                  `json:"version"`
	AuthMethodID    string                  `json:"authMethodId"`
	Operations      []string                `json:"operations"`
}

type projectManifestRecord struct {
	SchemaVersion             string              `json:"schemaVersion"`
	Scope                     projectconfig.Scope `json:"scope"`
	Revision                  uint64              `json:"appManifestRevision"`
	SourceCommit              string              `json:"sourceCommit"`
	ManifestDigest            string              `json:"manifestDigest"`
	ConnectorContractDigest   string              `json:"connectorContractDigest"`
	EnvironmentContractDigest string              `json:"environmentContractDigest"`
	Manifest                  projectAppManifest  `json:"manifest"`
}

type projectManifestWriteRequest struct {
	ExpectedRevision uint64 `json:"expectedRevision"`
	SourceCommit     string `json:"sourceCommit"`
	ManifestDigest   string `json:"manifestDigest"`
	Manifest         string `json:"manifest"`
}

type projectObjectReference struct {
	Key       string `json:"key"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
}

// NewProjectConfiguration validates the fixed scope without creating or modifying stored state.
func NewProjectConfiguration(config *ProjectConfigurationConfig) (*ProjectConfiguration, error) {
	if config == nil || config.Objects == nil || len(config.AdminToken) < 32 {
		return nil, errors.New("project storage and an admin token of at least 32 bytes are required")
	}
	scope := projectconfig.Scope{ProjectID: config.ProjectID, Kind: config.ScopeKind, SessionID: config.SessionID}
	prefix, err := scope.Prefix()
	if err != nil {
		return nil, err
	}
	connections, err := projectconfig.NewConnectionStore(&projectconfig.ConnectionStoreConfig{Objects: config.Objects, Scope: scope})
	if err != nil {
		return nil, err
	}
	return &ProjectConfiguration{objects: config.Objects, connections: connections, scope: scope, prefix: prefix, adminToken: config.AdminToken}, nil
}

func (configuration *ProjectConfiguration) registerHandlers(mux *http.ServeMux, setup *connectorSetup) {
	mux.HandleFunc("PUT /api/v2/project-configuration/app-manifest", configuration.handlePutManifest)
	mux.HandleFunc("GET /api/v2/project-configuration", configuration.handleGetConfiguration)
	mux.HandleFunc("GET /api/v2/application-environment", configuration.handleGetEnvironment)
	mux.HandleFunc("PUT /api/v2/application-environment", configuration.handlePutEnvironment)
	mux.HandleFunc("POST /api/v2/project-configuration/validate", setup.handleValidateProjectConfiguration)
}

func (configuration *ProjectConfiguration) handlePutManifest(response http.ResponseWriter, request *http.Request) {
	if !configuration.authorizeAdmin(response, request) {
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body projectManifestWriteRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, 400, "APP_MANIFEST_INVALID", "AppManifest request is invalid")
		return
	}
	manifest, err := parseProjectManifest(body)
	if err != nil {
		api.WriteCodedError(response, 400, "APP_MANIFEST_INVALID", err.Error())
		return
	}
	current, object, err := configuration.readManifest(request.Context())
	if errors.Is(err, projectconfig.ErrObjectNotFound) && body.ExpectedRevision == 0 {
		current = projectManifestRecord{}
	} else if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if current.Revision != body.ExpectedRevision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	manifest.Revision, manifest.Scope = current.Revision+1, configuration.scope
	contents, err := json.Marshal(manifest)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if _, err = configuration.writeObject(request.Context(), configuration.prefix+"/app-manifest/head", object.ETag, contents); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	document, _, err := configuration.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	result := projectManifestSummary(manifest)
	result["configurationRevision"] = document.Revision
	result["scope"] = configuration.scope
	writeWebJSON(response, http.StatusOK, result)
}

func (configuration *ProjectConfiguration) handleGetConfiguration(response http.ResponseWriter, request *http.Request) {
	if !configuration.authorizeReader(response, request) {
		return
	}
	manifest, _, err := configuration.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	document, _, err := configuration.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	result := projectManifestSummary(manifest)
	result["configurationRevision"] = document.Revision
	result["scope"] = configuration.scope
	writeWebJSON(response, http.StatusOK, result)
}

func (configuration *ProjectConfiguration) authorizeAdmin(response http.ResponseWriter, request *http.Request) bool {
	actual := request.Header.Get("Authorization")
	expected := "Bearer " + configuration.adminToken
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Internal project configuration authorization is required")
		return false
	}
	return true
}

func (configuration *ProjectConfiguration) authorizeReader(response http.ResponseWriter, request *http.Request) bool {
	if request.Header.Get("Authorization") == "Bearer "+configuration.adminToken {
		return true
	}
	if _, err := projectActor(request); err != nil {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Authenticated project actor is required")
		return false
	}
	return true
}

func (configuration *ProjectConfiguration) readManifest(ctx context.Context) (projectManifestRecord, projectconfig.Object, error) {
	object, err := configuration.objects.ReadObject(ctx, configuration.prefix+"/app-manifest/head", "")
	if err != nil {
		return projectManifestRecord{}, projectconfig.Object{}, err
	}
	var record projectManifestRecord
	if err = decodeStrictConnectorJSON(object.Contents, &record); err != nil {
		return record, object, err
	}
	if record.SchemaVersion != projectManifestSchema || record.Scope != configuration.scope || record.Revision == 0 || object.Version == "" || object.ETag == "" {
		return record, object, errors.New("project AppManifest identity is invalid")
	}
	return record, object, nil
}

func (configuration *ProjectConfiguration) writeObject(ctx context.Context, key, etag string, contents []byte) (projectconfig.Object, error) {
	var result projectconfig.Object
	var err error
	if etag == "" {
		result, err = configuration.objects.CreateObject(ctx, key, contents)
	} else {
		result, err = configuration.objects.CompareAndSwapObject(ctx, key, etag, contents)
	}
	if errors.Is(err, projectconfig.ErrOutcomeUnknown) {
		observed, readErr := configuration.objects.ReadObject(ctx, key, "")
		if readErr == nil && bytes.Equal(observed.Contents, contents) {
			return observed, nil
		}
		if readErr != nil {
			return result, errors.Join(err, readErr)
		}
	}
	return result, err
}

func (configuration *ProjectConfiguration) immutableObject(ctx context.Context, key string, contents []byte) (projectObjectReference, error) {
	object, err := configuration.objects.CreateObject(ctx, key, contents)
	if errors.Is(err, projectconfig.ErrConflict) || errors.Is(err, projectconfig.ErrOutcomeUnknown) {
		object, err = configuration.objects.ReadObject(ctx, key, "")
		if err == nil && !bytes.Equal(object.Contents, contents) {
			return projectObjectReference{}, projectconfig.ErrConflict
		}
	}
	if err != nil {
		return projectObjectReference{}, err
	}
	if object.Version == "" {
		return projectObjectReference{}, errors.New("immutable configuration requires a version")
	}
	return projectObjectReference{Key: key, Version: object.Version, Digest: projectDigest(contents), MediaType: "application/json"}, nil
}

func parseProjectManifest(body projectManifestWriteRequest) (projectManifestRecord, error) {
	if len(body.SourceCommit) != 40 || strings.Trim(body.SourceCommit, "0123456789abcdef") != "" || projectDigest([]byte(body.Manifest)) != body.ManifestDigest {
		return projectManifestRecord{}, errors.New("AppManifest source identity or digest is invalid")
	}
	var manifest projectAppManifest
	if err := decodeStrictConnectorJSON([]byte(body.Manifest), &manifest); err != nil {
		return projectManifestRecord{}, errors.New("AppManifest contains invalid or unsupported fields")
	}
	if manifest.SchemaVersion != "superverse.dev/dex-app/v1" || manifest.Connectors == nil || len(manifest.FlowDefinitions) == 0 || manifest.Application.Port < 1 || manifest.Application.Port > 65535 || !strings.HasPrefix(manifest.Application.HealthPath, "/") || strings.ContainsAny(manifest.Application.HealthPath, "?#") {
		return projectManifestRecord{}, errors.New("AppManifest application or schema is invalid")
	}
	for _, definition := range manifest.FlowDefinitions {
		if definition.SourcePath == "" || strings.HasPrefix(definition.SourcePath, "/") || path.Clean(definition.SourcePath) != definition.SourcePath || strings.Contains(definition.SourcePath, "..") || !strings.HasSuffix(definition.SourcePath, ".go") {
			return projectManifestRecord{}, errors.New("AppManifest Flow Definition source path is invalid")
		}
	}
	names := make(map[string]bool)
	contracts := make([]map[string]any, 0, len(manifest.Connectors))
	sort.Slice(manifest.Connectors, func(left, right int) bool {
		return manifest.Connectors[left].ConnectionName < manifest.Connectors[right].ConnectionName
	})
	for index := range manifest.Connectors {
		connector := &manifest.Connectors[index]
		if !connectorReleaseIDPattern.MatchString(connector.ConnectorID) || !officialConnectorModulePattern.MatchString(connector.ModulePath) || !validProjectConnectionName(connector.ConnectionName) || !exactConnectorReleaseVersionPattern.MatchString(connector.Version) || connector.AuthMethodID == "" || (len(connector.Operations) == 0 && len(connector.TriggerBindings) == 0) || names[connector.ConnectionName] {
			return projectManifestRecord{}, errors.New("AppManifest connector declaration is invalid")
		}
		names[connector.ConnectionName] = true
		sort.Strings(connector.Operations)
		operations := make([]string, 0, len(connector.Operations))
		for _, operation := range connector.Operations {
			if operation == "" {
				return projectManifestRecord{}, errors.New("AppManifest connector operation is empty")
			}
			if len(operations) == 0 || operations[len(operations)-1] != operation {
				operations = append(operations, operation)
			}
		}
		connector.Operations = operations
		if connector.TriggerBindings == nil {
			connector.TriggerBindings = []projectTriggerBinding{}
		}
		sort.Slice(connector.TriggerBindings, func(left, right int) bool {
			a, b := connector.TriggerBindings[left], connector.TriggerBindings[right]
			if a.TriggerName != b.TriggerName {
				return a.TriggerName < b.TriggerName
			}
			return a.BindingName < b.BindingName
		})
		for index, binding := range connector.TriggerBindings {
			if !validProjectConnectionName(binding.TriggerName) || !validProjectConnectionName(binding.BindingName) || (index > 0 && binding == connector.TriggerBindings[index-1]) {
				return projectManifestRecord{}, errors.New("AppManifest trigger binding is invalid or duplicated")
			}
		}

		bindingContracts := make([]map[string]any, 0, len(connector.TriggerBindings))
		for _, binding := range connector.TriggerBindings {
			bindingContracts = append(bindingContracts, map[string]any{"triggerName": binding.TriggerName, "bindingName": binding.BindingName})
		}
		contracts = append(contracts, map[string]any{"connectionName": connector.ConnectionName, "connectorId": connector.ConnectorID, "modulePath": connector.ModulePath, "version": connector.Version, "authMethodId": connector.AuthMethodID, "operations": operations, "triggerBindings": bindingContracts})
	}
	connectorBytes, err := json.Marshal(contracts)
	if err != nil {
		return projectManifestRecord{}, err
	}
	manifest.Application.Environment, err = projectconfig.ValidateAndSortEnvironmentDeclarations(manifest.Application.Environment)
	if err != nil {
		return projectManifestRecord{}, err
	}
	environmentContract := map[string]any{"port": manifest.Application.Port, "healthPath": manifest.Application.HealthPath, "publicBaseUrlRequired": true, "connectorConfigurationRequired": len(contracts) > 0}
	if len(manifest.Application.Environment) > 0 {
		declarations, declarationErr := projectconfig.CanonicalEnvironmentDeclarations(manifest.Application.Environment)
		if declarationErr != nil {
			return projectManifestRecord{}, declarationErr
		}
		environmentContract["environment"] = declarations
	}
	environmentBytes, err := json.Marshal(environmentContract)
	if err != nil {
		return projectManifestRecord{}, err
	}
	return projectManifestRecord{SchemaVersion: projectManifestSchema, SourceCommit: body.SourceCommit, ManifestDigest: body.ManifestDigest, ConnectorContractDigest: projectDigest(connectorBytes), EnvironmentContractDigest: projectDigest(environmentBytes), Manifest: manifest}, nil
}

func projectManifestSummary(record projectManifestRecord) map[string]any {
	return map[string]any{"appManifestRevision": record.Revision, "sourceCommit": record.SourceCommit, "manifestDigest": record.ManifestDigest, "connectorContractDigest": record.ConnectorContractDigest, "environmentContractDigest": record.EnvironmentContractDigest}
}
func projectDigest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func projectActor(request *http.Request) (string, error) {
	value := request.Header.Values(projectActorHeader)
	if len(value) != 1 || len(value[0]) == 0 || len(value[0]) > 128 || strings.ContainsAny(value[0], "\r\n\x00") || webRequestConfigFromContext(request.Context()).publicOrigin == "" {
		return "", errors.New("trusted project actor is required")
	}
	return value[0], nil
}
func validProjectConnectionName(name string) bool {
	return name != "" && len(name) <= 128 && strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}
func projectRevision(value string) (uint64, error) {
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(revision, 10) != value {
		return 0, errors.New("canonical revision is required")
	}
	return revision, nil
}
func writeProjectConfigurationError(response http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "PROJECT_CONFIGURATION_UNAVAILABLE", "Project configuration is unavailable"
	if errors.Is(err, projectconfig.ErrConflict) {
		status, code, message = http.StatusConflict, "PROJECT_CONFIGURATION_REVISION_CONFLICT", "Project configuration changed; reload before continuing"
	}
	if errors.Is(err, projectconfig.ErrObjectNotFound) {
		status, code, message = http.StatusNotFound, "PROJECT_CONFIGURATION_NOT_FOUND", "Project configuration has not been prepared"
	}
	api.WriteCodedError(response, status, code, message)
}

func (configuration *ProjectConfiguration) readinessHandler(response http.ResponseWriter, request *http.Request) {
	_, _, err := configuration.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	writeWebJSON(response, 200, map[string]any{"status": "ready", "configuration": true})
}

func (configuration *ProjectConfiguration) configurationStore() (*projectconfig.ConfigurationStore, error) {
	return projectconfig.NewConfigurationStore(&projectconfig.ConfigurationStoreConfig{Objects: configuration.objects, Scope: configuration.scope})
}

func (configuration *ProjectConfiguration) readConfiguration(ctx context.Context) (projectconfig.Configuration, projectconfig.Object, error) {
	store, err := configuration.configurationStore()
	if err != nil {
		return projectconfig.Configuration{}, projectconfig.Object{}, err
	}
	document, object, err := store.ReadConfiguration(ctx)
	if errors.Is(err, projectconfig.ErrObjectNotFound) {
		return projectconfig.Configuration{SchemaVersion: projectconfig.ConfigurationSchemaVersion, Scope: configuration.scope, Connections: []projectconfig.ConnectionConfiguration{}, TriggerBindings: []projectconfig.TriggerConfiguration{}, OperationConfigurations: []projectconfig.OperationConfiguration{}}, projectconfig.Object{}, nil
	}
	return document, object, err
}

type projectEnvironmentWriteRequest struct {
	Values  map[string]string `json:"values"`
	Secrets map[string]string `json:"secrets"`
	Remove  []string          `json:"remove"`
}

type projectEnvironmentFieldView struct {
	projectconfig.EnvironmentDeclaration
	Value      *string `json:"value,omitempty"`
	Configured bool    `json:"configured"`
}

func (configuration *ProjectConfiguration) handleGetEnvironment(response http.ResponseWriter, request *http.Request) {
	if !configuration.authorizeReader(response, request) {
		return
	}
	manifest, _, err := configuration.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	document, _, err := configuration.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	fields := make([]projectEnvironmentFieldView, 0, len(manifest.Manifest.Application.Environment))
	known := map[string]bool{}
	for _, declaration := range manifest.Manifest.Application.Environment {
		known[declaration.Name] = true
		entry, present := document.Environment[declaration.Name]
		view := projectEnvironmentFieldView{EnvironmentDeclaration: declaration, Configured: present && (entry.SecretRef != nil) == declaration.Secret}
		if !declaration.Secret && entry.SecretRef == nil {
			view.Value = entry.Value
		}
		fields = append(fields, view)
	}
	obsolete := []string{}
	for name := range document.Environment {
		if !known[name] {
			obsolete = append(obsolete, name)
		}
	}
	sort.Strings(obsolete)
	writeWebJSON(response, 200, map[string]any{"appManifestRevision": manifest.Revision, "configurationRevision": document.Revision, "csrfToken": webRequestConfigFromContext(request.Context()).csrfToken, "fields": fields, "obsolete": obsolete})
}

func (configuration *ProjectConfiguration) handlePutEnvironment(response http.ResponseWriter, request *http.Request) {
	if _, err := projectActor(request); err != nil {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Authenticated project actor is required")
		return
	}
	webConfig := webRequestConfigFromContext(request.Context())
	if webConfig.csrfToken == "" || request.Header.Get(connectorCSRFHeader) != webConfig.csrfToken || request.Header.Get("Origin") != webConfig.publicOrigin {
		api.WriteCodedError(response, 403, "APPLICATION_ENVIRONMENT_FORBIDDEN", "Application environment origin or CSRF token is invalid")
		return
	}
	manifest, _, err := configuration.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	manifestRevision, err := projectRevision(request.Header.Get(projectManifestRevisionHeader))
	if err != nil || manifest.Revision != manifestRevision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	revision, err := projectRevision(request.Header.Get(projectConfigurationRevisionHeader))
	if err != nil {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	document, _, err := configuration.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if revision != document.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body projectEnvironmentWriteRequest
	if decodeStrictConnectorJSONReader(request.Body, &body) != nil {
		api.WriteCodedError(response, 400, "APPLICATION_ENVIRONMENT_INVALID", "Application environment request is invalid")
		return
	}
	declarations := map[string]projectconfig.EnvironmentDeclaration{}
	for _, field := range manifest.Manifest.Application.Environment {
		declarations[field.Name] = field
	}
	touched := map[string]bool{}
	for name, value := range body.Values {
		field, exists := declarations[name]
		if !exists || field.Secret || touched[name] || projectconfig.ValidateEnvironmentValue(field, value) != nil {
			api.WriteCodedError(response, 400, "APPLICATION_ENVIRONMENT_INVALID", "An ordinary environment value is invalid")
			return
		}
		touched[name] = true
	}
	for name, value := range body.Secrets {
		field, exists := declarations[name]
		if !exists || !field.Secret || touched[name] || projectconfig.ValidateEnvironmentValue(field, value) != nil {
			api.WriteCodedError(response, 400, "APPLICATION_ENVIRONMENT_INVALID", "A private environment value is invalid")
			return
		}
		touched[name] = true
	}
	for _, name := range body.Remove {
		// Previously declared entries may be removed after a manifest changes.
		_, declared := declarations[name]
		_, configured := document.Environment[name]
		if (!declared && !configured) || touched[name] {
			api.WriteCodedError(response, 400, "APPLICATION_ENVIRONMENT_INVALID", "Environment removal is invalid")
			return
		}
		touched[name] = true
	}
	if len(touched) == 0 {
		api.WriteCodedError(response, 400, "APPLICATION_ENVIRONMENT_INVALID", "No environment changes were supplied")
		return
	}
	if document.Environment == nil {
		document.Environment = map[string]projectconfig.EnvironmentValue{}
	}
	store, err := configuration.configurationStore()
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	for name, value := range body.Values {
		document.Environment[name] = projectconfig.EnvironmentValue{Value: &value}
	}
	for name, value := range body.Secrets {
		reference, writeErr := store.CreateApplicationSecret(request.Context(), name, value)
		if writeErr != nil {
			writeProjectConfigurationError(response, writeErr)
			return
		}
		document.Environment[name] = projectconfig.EnvironmentValue{SecretRef: &reference}
	}
	for _, name := range body.Remove {
		delete(document.Environment, name)
	}
	current, _, err := configuration.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if current.Revision != manifest.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	if _, _, err = store.WriteConfiguration(request.Context(), revision, document); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	current, _, err = configuration.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if current.Revision != manifest.Revision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	configuration.handleGetEnvironment(response, request)
}

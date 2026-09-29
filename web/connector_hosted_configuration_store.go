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
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const connectorHostedConfigurationResponseLimit = 4 << 20

var errConnectorConfigurationRevisionConflict = errors.New("Connector configuration revision conflict")

type hostedConnectorConfigurationStore struct {
	baseURL      *url.URL
	projectID    string
	environment  string
	releaseID    string
	serviceToken string
	httpClient   *http.Client

	mu       sync.RWMutex
	snapshot hostedConnectorConfigurationSnapshot
}

type hostedConnectorConfigurationSnapshot struct {
	ConfigurationRevision   string                           `json:"configurationRevision"`
	ConfigurationState      string                           `json:"configurationState"`
	ApplicationRevision     string                           `json:"applicationRevision,omitempty"`
	Connections             []storedConnectorConnection      `json:"connections"`
	TriggerBindings         []localConnectorTriggerBinding   `json:"triggerBindings"`
	OperationConfigurations []localConnectorUseConfiguration `json:"operationConfigurations"`
}

// The backend copies each kept credential value from its stored record and rejects missing ones as conflicts.
type hostedConnectorConnectionWriteRequest struct {
	localConnectorConnection
	KeepCredentialFields []string `json:"keepCredentialFields,omitempty"`
}

type hostedConnectorConfigurationDeleteResponse struct {
	Deleted  bool                                 `json:"deleted"`
	Snapshot hostedConnectorConfigurationSnapshot `json:"snapshot"`
}

type hostedConnectorProviderCommandExecutor interface {
	executeProviderCommand(connectorID string, connectionName string, command connectorManifestStudioCommand, parameters map[string]string) (map[string]any, error)
}

type hostedConnectorOAuthExecutor interface {
	startOAuth(connectorID string, connectionName string, request hostedConnectorOAuthStartRequest) (hostedConnectorOAuthStartResponse, error)
	completeOAuth(state string, code string, providerError string) (hostedConnectorOAuthCallbackResponse, error)
}

type hostedConnectorOAuthStartRequest struct {
	DefinitionRevision string                     `json:"definitionRevision"`
	ModulePath         string                     `json:"modulePath"`
	ModuleVersion      string                     `json:"moduleVersion"`
	Provider           string                     `json:"provider"`
	AuthMethodID       string                     `json:"authMethodId"`
	OAuth2             connectorManifestOAuth2    `json:"oauth2"`
	ClientID           string                     `json:"clientId"`
	ClientSecret       string                     `json:"clientSecret"`
	RedirectURI        string                     `json:"redirectUri"`
	Configuration      map[string]json.RawMessage `json:"configuration"`
	CredentialValues   map[string]json.RawMessage `json:"credentialValues"`
	CredentialSecrets  map[string]string          `json:"credentialSecrets"`
}

type hostedConnectorOAuthStartResponse struct {
	AuthorizationURL string    `json:"authorizationUrl"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type hostedConnectorOAuthCallbackResponse struct {
	ConnectorID    string `json:"connectorId"`
	ConnectionName string `json:"connectionName"`
}

func newHostedConnectorConfigurationStore(cfg *Config) (*hostedConnectorConfigurationStore, error) {
	baseURL, err := url.Parse(strings.TrimSpace(cfg.ConnectorHostedBaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("hosted Connector configuration requires an absolute HTTP(S) backend URL")
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("hosted Connector configuration backend URL must not contain credentials, query, or fragment")
	}
	projectID := strings.TrimSpace(cfg.ConnectorHostedProjectID)
	environment := strings.TrimSpace(cfg.ConnectorHostedEnvironment)
	releaseID := strings.TrimSpace(cfg.ConnectorHostedReleaseID)
	serviceToken := strings.TrimSpace(cfg.ConnectorHostedServiceToken)
	if projectID == "" || environment == "" || releaseID == "" || serviceToken == "" {
		return nil, fmt.Errorf("hosted Connector configuration requires project, environment, release, and service token")
	}
	if !isSafeConnectorConfigurationPathSegment(projectID) || !isSafeConnectorConfigurationPathSegment(environment) ||
		!isSafeConnectorConfigurationPathSegment(releaseID) {
		return nil, fmt.Errorf("hosted Connector project, environment, and release must be safe URL path segments")
	}
	httpClient := cfg.ConnectorHostedHTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("hosted Connector configuration redirects are not allowed")
			},
		}
	}
	return &hostedConnectorConfigurationStore{
		baseURL: baseURL, projectID: projectID, environment: environment,
		releaseID: releaseID, serviceToken: serviceToken, httpClient: httpClient,
	}, nil
}

func (store *hostedConnectorConfigurationStore) state() connectorConfigurationStoreState {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return connectorConfigurationStoreState{
		Mode: ConnectorSetupModeHosted, ConfigurationRevision: store.snapshot.ConfigurationRevision,
		ConfigurationState: store.snapshot.ConfigurationState, ApplicationRevision: store.snapshot.ApplicationRevision,
	}
}

func (store *hostedConnectorConfigurationStore) list() ([]storedConnectorConnection, error) {
	snapshot, err := store.load()
	if err != nil {
		return nil, err
	}
	return snapshot.Connections, nil
}

func (store *hostedConnectorConfigurationStore) get(connectorID string, connectionName string) (storedConnectorConnection, bool, error) {
	snapshot, err := store.load()
	if err != nil {
		return storedConnectorConnection{}, false, err
	}
	for _, connection := range snapshot.Connections {
		if connection.ConnectorID == connectorID && connection.ConnectionName == connectionName {
			return connection, true, nil
		}
	}
	return storedConnectorConnection{}, false, nil
}

func (store *hostedConnectorConfigurationStore) listTriggerBindings(connectorID string, connectionName string) ([]localConnectorTriggerBinding, error) {
	snapshot, err := store.load()
	if err != nil {
		return nil, err
	}
	bindings := make([]localConnectorTriggerBinding, 0)
	for _, binding := range snapshot.TriggerBindings {
		if binding.ConnectorID == connectorID && binding.ConnectionName == connectionName {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

func (store *hostedConnectorConfigurationStore) putTriggerBinding(binding localConnectorTriggerBinding) error {
	return store.putResource([]string{"trigger-bindings", binding.ConnectorID, binding.ConnectionName, binding.TriggerName, binding.BindingName}, binding)
}

func (store *hostedConnectorConfigurationStore) listUseConfigurations(connectorID string, connectionName string) ([]localConnectorUseConfiguration, error) {
	snapshot, err := store.load()
	if err != nil {
		return nil, err
	}
	configurations := make([]localConnectorUseConfiguration, 0)
	for _, configuration := range snapshot.OperationConfigurations {
		if configuration.ConnectorID == connectorID && configuration.ConnectionName == connectionName {
			configurations = append(configurations, configuration)
		}
	}
	return configurations, nil
}

func (store *hostedConnectorConfigurationStore) putUseConfiguration(configuration localConnectorUseConfiguration) error {
	return store.putResource([]string{
		"operation-configurations", configuration.ConnectorID, configuration.ConnectionName,
		configuration.OperationID, configuration.FlowType, configuration.StepType,
	}, configuration)
}

func (store *hostedConnectorConfigurationStore) put(connection localConnectorConnection, keepCredentialFields []string) error {
	return store.putResource([]string{"connections", connection.ConnectorID, connection.ConnectionName}, hostedConnectorConnectionWriteRequest{
		localConnectorConnection: connection, KeepCredentialFields: keepCredentialFields,
	})
}

func (store *hostedConnectorConfigurationStore) delete(connectorID string, connectionName string) (bool, error) {
	revision, err := store.currentRevision()
	if err != nil {
		return false, err
	}
	request, err := store.request(http.MethodDelete, []string{"connections", connectorID, connectionName}, nil, revision)
	if err != nil {
		return false, err
	}
	var result hostedConnectorConfigurationDeleteResponse
	if err := store.do(request, &result); err != nil {
		return false, err
	}
	store.saveSnapshot(result.Snapshot)
	return result.Deleted, nil
}

func (store *hostedConnectorConfigurationStore) executeProviderCommand(
	connectorID string,
	connectionName string,
	command connectorManifestStudioCommand,
	parameters map[string]string,
) (map[string]any, error) {
	revision, err := store.currentRevision()
	if err != nil {
		return nil, err
	}
	contents, err := json.Marshal(hostedConnectorStudioCommandRequest{
		Command: command, Parameters: parameters,
	})
	if err != nil {
		return nil, fmt.Errorf("encode hosted Connector setup command: %w", err)
	}
	request, err := store.request(http.MethodPost, []string{
		"releases", store.releaseID, "connections", connectorID, connectionName,
		"setup-commands", command.ID,
	}, bytes.NewReader(contents), revision)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := store.do(request, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (store *hostedConnectorConfigurationStore) startOAuth(
	connectorID string,
	connectionName string,
	startRequest hostedConnectorOAuthStartRequest,
) (hostedConnectorOAuthStartResponse, error) {
	revision, err := store.currentRevision()
	if err != nil {
		return hostedConnectorOAuthStartResponse{}, err
	}
	contents, err := json.Marshal(startRequest)
	if err != nil {
		return hostedConnectorOAuthStartResponse{}, fmt.Errorf("encode hosted Connector OAuth start: %w", err)
	}
	request, err := store.request(http.MethodPost, []string{
		"releases", store.releaseID, "connections", connectorID, connectionName, "oauth", "start",
	}, bytes.NewReader(contents), revision)
	if err != nil {
		return hostedConnectorOAuthStartResponse{}, err
	}
	var result hostedConnectorOAuthStartResponse
	if err := store.do(request, &result); err != nil {
		return hostedConnectorOAuthStartResponse{}, err
	}
	return result, nil
}

func (store *hostedConnectorConfigurationStore) completeOAuth(
	state string,
	code string,
	providerError string,
) (hostedConnectorOAuthCallbackResponse, error) {
	contents, err := json.Marshal(map[string]string{
		"state": state, "code": code, "error": providerError,
	})
	if err != nil {
		return hostedConnectorOAuthCallbackResponse{}, fmt.Errorf("encode hosted Connector OAuth callback: %w", err)
	}
	request, err := store.request(http.MethodPost, []string{
		"releases", store.releaseID, "oauth", "callback",
	}, bytes.NewReader(contents), "")
	if err != nil {
		return hostedConnectorOAuthCallbackResponse{}, err
	}
	var result hostedConnectorOAuthCallbackResponse
	if err := store.do(request, &result); err != nil {
		return hostedConnectorOAuthCallbackResponse{}, err
	}
	return result, nil
}

func (store *hostedConnectorConfigurationStore) load() (hostedConnectorConfigurationSnapshot, error) {
	request, err := store.request(http.MethodGet, nil, nil, "")
	if err != nil {
		return hostedConnectorConfigurationSnapshot{}, err
	}
	var snapshot hostedConnectorConfigurationSnapshot
	if err := store.do(request, &snapshot); err != nil {
		return hostedConnectorConfigurationSnapshot{}, err
	}
	store.saveSnapshot(snapshot)
	return snapshot, nil
}

func (store *hostedConnectorConfigurationStore) putResource(segments []string, value any) error {
	revision, err := store.currentRevision()
	if err != nil {
		return err
	}
	contents, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode hosted Connector configuration: %w", err)
	}
	request, err := store.request(http.MethodPut, segments, bytes.NewReader(contents), revision)
	if err != nil {
		return err
	}
	var snapshot hostedConnectorConfigurationSnapshot
	if err := store.do(request, &snapshot); err != nil {
		return err
	}
	store.saveSnapshot(snapshot)
	return nil
}

func (store *hostedConnectorConfigurationStore) currentRevision() (string, error) {
	store.mu.RLock()
	revision := store.snapshot.ConfigurationRevision
	store.mu.RUnlock()
	if revision != "" {
		return revision, nil
	}
	snapshot, err := store.load()
	if err != nil {
		return "", err
	}
	return snapshot.ConfigurationRevision, nil
}

func (store *hostedConnectorConfigurationStore) request(method string, segments []string, body io.Reader, revision string) (*http.Request, error) {
	for _, segment := range segments {
		if !isSafeConnectorConfigurationPathSegment(segment) {
			return nil, fmt.Errorf("hosted Connector configuration resource identity is invalid")
		}
	}
	endpoint := *store.baseURL
	allSegments := []string{"api", "system", "projects", store.projectID, "environments", store.environment, "connector-configuration"}
	allSegments = append(allSegments, segments...)
	endpoint.Path = path.Join(append([]string{endpoint.Path}, allSegments...)...)
	request, err := http.NewRequest(method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+store.serviceToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if revision != "" {
		request.Header.Set("If-Match", revision)
	}
	return request, nil
}

func isSafeConnectorConfigurationPathSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\")
}

func (store *hostedConnectorConfigurationStore) do(request *http.Request, destination any) error {
	response, err := store.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call hosted Connector configuration backend: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, connectorHostedConfigurationResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read hosted Connector configuration response: %w", err)
	}
	if len(contents) > connectorHostedConfigurationResponseLimit {
		return fmt.Errorf("hosted Connector configuration response exceeds size limit")
	}
	if response.StatusCode == http.StatusPreconditionFailed || response.StatusCode == http.StatusConflict {
		return errConnectorConfigurationRevisionConflict
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("hosted Connector configuration backend returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return fmt.Errorf("decode hosted Connector configuration response: %w", err)
	}
	return nil
}

func (store *hostedConnectorConfigurationStore) saveSnapshot(snapshot hostedConnectorConfigurationSnapshot) {
	store.mu.Lock()
	store.snapshot = snapshot
	store.mu.Unlock()
}

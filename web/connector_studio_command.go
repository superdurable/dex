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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/superdurable/dex/web/api"
)

const connectorProviderResponseLimit = 4 << 20

var connectorProviderParameterPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

type connectorStudioCommandRequest struct {
	Parameters map[string]string `json:"parameters"`
}

func newConnectorProviderHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			first := via[0].URL
			if len(via) >= 5 || request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), first.Hostname()) {
				return fmt.Errorf("Connector provider redirect is not allowed")
			}
			return nil
		},
	}
}

func (setup *connectorSetup) handleStudioProviderCommand(response http.ResponseWriter, request *http.Request) {
	session, found := setup.connectorUISession(request.PathValue("sessionNonce"))
	if !found {
		api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_UI_SESSION_NOT_FOUND", "Connector UI session is missing or expired")
		return
	}
	command, found := session.commands[request.PathValue("commandId")]
	if !found {
		api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_STUDIO_COMMAND_NOT_FOUND", "Connector Studio command is not declared by this release")
		return
	}
	if _, _, ok := setup.authorizeConnectionKey(response, request, session.connectorID, session.connectionName); !ok {
		return
	}
	connection, found, err := setup.store.get(session.connectorID, session.connectionName)
	if err != nil || !found {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_NOT_READY", "Connector connection is not configured")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
	var body connectorStudioCommandRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_STUDIO_COMMAND_INVALID", "Connector Studio command parameters are invalid")
		return
	}
	value, err := setup.executeStudioProviderCommand(request, command, body.Parameters, connection.Credentials)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_PROVIDER_COMMAND_FAILED", "Connector provider command failed")
		return
	}
	writeWebJSON(response, http.StatusOK, value)
}

func (setup *connectorSetup) connectorUISession(nonce string) (connectorUISession, bool) {
	setup.uiSessionsMu.Lock()
	defer setup.uiSessionsMu.Unlock()
	setup.deleteExpiredUISessions(time.Now())
	session, found := setup.uiSessions[nonce]
	return session, found
}

func (setup *connectorSetup) executeStudioProviderCommand(
	request *http.Request,
	command connectorManifestStudioCommand,
	parameters map[string]string,
	credentials map[string]json.RawMessage,
) (map[string]any, error) {
	if command.ID == "" || command.Capability == "" || command.Request.Method != http.MethodGet || command.Request.Credential.Scheme != "bearer" {
		return nil, fmt.Errorf("Connector Studio command declaration is invalid")
	}
	targetText := command.Request.URL
	queryParameters := map[string]string{}
	declared := make(map[string]bool, len(command.Request.Parameters))
	for _, parameter := range command.Request.Parameters {
		declared[parameter.Name] = true
		value := parameters[parameter.Name]
		switch parameter.Location {
		case "path":
			if value == "" || !connectorProviderParameterPattern.MatchString(value) {
				return nil, fmt.Errorf("Connector provider path parameter is invalid")
			}
			placeholder := "{" + parameter.Target + "}"
			if strings.Count(targetText, placeholder) != 1 {
				return nil, fmt.Errorf("Connector provider path parameter is not declared by URL")
			}
			targetText = strings.Replace(targetText, placeholder, url.PathEscape(value), 1)
		case "query":
			if value != "" {
				queryParameters[parameter.Target] = value
			}
		default:
			return nil, fmt.Errorf("Connector provider parameter location is invalid")
		}
	}
	for name := range parameters {
		if !declared[name] {
			return nil, fmt.Errorf("Connector provider parameter is not declared")
		}
	}
	target, err := url.Parse(targetText)
	if err != nil || !safeConnectorProviderURL(target) || target.RawQuery != "" || strings.ContainsAny(target.Path, "{}") {
		return nil, fmt.Errorf("Connector provider URL is invalid")
	}
	query := target.Query()
	for name, value := range command.Request.FixedQuery {
		query.Set(name, value)
	}
	for name, value := range queryParameters {
		if _, fixed := command.Request.FixedQuery[name]; fixed {
			return nil, fmt.Errorf("Connector provider parameter cannot replace fixed query")
		}
		query.Set(name, value)
	}
	target.RawQuery = query.Encode()
	var credential string
	if err := json.Unmarshal(credentials[command.Request.Credential.Field], &credential); err != nil || strings.TrimSpace(credential) == "" {
		return nil, fmt.Errorf("Connector provider credential is unavailable")
	}
	providerRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	providerRequest.Header.Set("Authorization", "Bearer "+credential)
	providerRequest.Header.Set("Accept", "application/json")
	providerResponse, err := setup.providerHTTPClient.Do(providerRequest)
	if err != nil {
		return nil, err
	}
	defer providerResponse.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(providerResponse.Body, connectorProviderResponseLimit+1))
	if err != nil || len(contents) > connectorProviderResponseLimit || providerResponse.StatusCode < 200 || providerResponse.StatusCode >= 300 {
		return nil, fmt.Errorf("Connector provider response is invalid")
	}
	var value map[string]any
	if err := json.Unmarshal(contents, &value); err != nil || value == nil {
		return nil, fmt.Errorf("Connector provider response must be a JSON object")
	}
	if containsConnectorSecret(value, credential) {
		return nil, fmt.Errorf("Connector provider response contains credential material")
	}
	return value, nil
}

func containsConnectorSecret(value any, secret string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, secret)
	case []any:
		for _, item := range typed {
			if containsConnectorSecret(item, secret) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsConnectorSecret(item, secret) {
				return true
			}
		}
	}
	return false
}

func safeConnectorProviderURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if address := net.ParseIP(host); address != nil && (address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsUnspecified()) {
		return false
	}
	return true
}

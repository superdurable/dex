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

const (
	connectorProviderResponseLimit         = 4 << 20
	connectorProviderFixedHeaderValueLimit = 256
)

var connectorProviderParameterPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// RFC 7230 token characters.
var connectorProviderHeaderNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// Dex owns credentials, routing, framing, and browser security headers.
var connectorProviderForbiddenHeaderNames = map[string]bool{
	"accept": true, "authorization": true, "connection": true, "content-length": true, "cookie": true,
	"forwarded": true, "host": true, "keep-alive": true, "origin": true, "referer": true,
	"set-cookie": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true,
	"x-http-method": true, "x-http-method-override": true, "x-method-override": true,
}

var connectorProviderForbiddenHeaderPrefixes = []string{"proxy-", "x-forwarded-"}

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
	request.Body = http.MaxBytesReader(response, request.Body, 1<<16)
	var body connectorStudioCommandRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_STUDIO_COMMAND_INVALID", "Connector Studio command parameters are invalid")
		return
	}
	connection, found, loadErr := setup.requestStore(request).get(session.connectorID, session.connectionName)
	if loadErr != nil || !found {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CONNECTION_NOT_READY", "Connector connection is not configured")
		return
	}
	if setup.project != nil && (connection.CredentialStatus != "READY" || (connection.CredentialExpiresAt != nil && !time.Now().Before(*connection.CredentialExpiresAt))) {
		api.WriteCodedError(response, http.StatusConflict, "CONNECTOR_CREDENTIAL_UNAVAILABLE", "Connector credential is expired or unavailable")
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
	if command.ID == "" || command.Capability == "" || command.Request.Method != http.MethodGet {
		return nil, fmt.Errorf("Connector Studio command declaration is invalid")
	}
	if err := validateConnectorStudioRequestHeaders(command.Request); err != nil {
		return nil, fmt.Errorf("Connector Studio command declaration is invalid: %w", err)
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
	var storedCredential string
	if err := json.Unmarshal(credentials[command.Request.Credential.Field], &storedCredential); err != nil {
		return nil, fmt.Errorf("Connector provider credential is unavailable")
	}
	// HTTP trims surrounding spaces on the wire; reflection checks must match the sent value.
	credential := strings.Trim(storedCredential, " ")
	if credential == "" {
		return nil, fmt.Errorf("Connector provider credential is unavailable")
	}
	if !isPrintableASCII(credential) {
		return nil, fmt.Errorf("Connector provider credential is not a valid header value")
	}
	for _, value := range command.Request.FixedHeaders {
		if strings.Contains(value, credential) {
			return nil, fmt.Errorf("Connector provider fixed header contains credential material")
		}
	}
	providerRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	for name, value := range command.Request.FixedHeaders {
		providerRequest.Header.Set(name, value)
	}
	switch command.Request.Credential.Scheme {
	case "bearer":
		providerRequest.Header.Set("Authorization", "Bearer "+credential)
	case "header":
		providerRequest.Header.Set(command.Request.Credential.Header, credential)
	}
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

func validateConnectorReleaseStudioCommands(studio *connectorManifestStudio) error {
	if studio == nil {
		return nil
	}
	for _, command := range studio.Commands {
		if err := validateConnectorStudioRequestHeaders(command.Request); err != nil {
			return fmt.Errorf("Connector Studio command %q request headers are invalid: %w", command.ID, err)
		}
	}
	return nil
}

func validateConnectorStudioRequestHeaders(request connectorManifestStudioHTTPRequest) error {
	credentialHeaderName := ""
	switch request.Credential.Scheme {
	case "bearer":
		if request.Credential.Header != "" {
			return fmt.Errorf("bearer credential cannot name a header")
		}
	case "header":
		if !isAllowedConnectorProviderHeaderName(request.Credential.Header) {
			return fmt.Errorf("credential header name %q is not allowed", request.Credential.Header)
		}
		credentialHeaderName = foldConnectorProviderHeaderName(request.Credential.Header)
	default:
		return fmt.Errorf("credential scheme must be bearer or header")
	}
	fixedHeaderNames := make(map[string]bool, len(request.FixedHeaders))
	for name, value := range request.FixedHeaders {
		foldedName := foldConnectorProviderHeaderName(name)
		if !isAllowedConnectorProviderHeaderName(name) || foldedName == credentialHeaderName || fixedHeaderNames[foldedName] {
			return fmt.Errorf("fixed header name %q is not allowed", name)
		}
		fixedHeaderNames[foldedName] = true
		if !isConnectorProviderFixedHeaderValue(value) {
			return fmt.Errorf("fixed header %q value is invalid", name)
		}
	}
	return nil
}

func isAllowedConnectorProviderHeaderName(name string) bool {
	if !connectorProviderHeaderNamePattern.MatchString(name) {
		return false
	}
	foldedName := foldConnectorProviderHeaderName(name)
	if connectorProviderForbiddenHeaderNames[foldedName] {
		return false
	}
	for _, prefix := range connectorProviderForbiddenHeaderPrefixes {
		if strings.HasPrefix(foldedName, prefix) {
			return false
		}
	}
	return true
}

// CGI-style servers merge "_" and "-" in header names, so compare them as equal.
func foldConnectorProviderHeaderName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}

func isConnectorProviderFixedHeaderValue(value string) bool {
	if value == "" || len(value) > connectorProviderFixedHeaderValueLimit ||
		value[0] == ' ' || value[len(value)-1] == ' ' {
		return false
	}
	return isPrintableASCII(value)
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
		for key, item := range typed {
			if strings.Contains(key, secret) || containsConnectorSecret(item, secret) {
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

func isPrintableASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

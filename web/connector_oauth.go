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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/superdurable/dex/web/api"
)

type connectorOAuthStartRequest struct {
	ClientID          string                     `json:"clientId"`
	ClientSecret      string                     `json:"clientSecret"`
	Configuration     map[string]json.RawMessage `json:"configuration"`
	CredentialValues  map[string]json.RawMessage `json:"credentialValues"`
	CredentialSecrets map[string]string          `json:"credentialSecrets"`
}

type connectorOAuthSession struct {
	identity           connectorDefinitionIdentity
	release            connectorRelease
	definitionRevision string
	clientID           string
	clientSecret       string
	codeVerifier       string
	redirectURI        string
	configuration      map[string]json.RawMessage
	credentialValues   map[string]json.RawMessage
	credentialSecrets  map[string]string
	expiresAt          time.Time
}

type connectorOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	ExpiresIn   int64  `json:"expires_in"`
	Error       string `json:"error"`
	AuthedUser  struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	} `json:"authed_user"`
	raw map[string]any
}

func (setup *connectorSetup) handleOAuthStart(response http.ResponseWriter, request *http.Request) {
	snapshot, identity, ok := setup.authorizeConnectionWrite(response, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var body connectorOAuthStartRequest
	if err := decodeStrictConnectorJSONReader(request.Body, &body); err != nil ||
		strings.TrimSpace(body.ClientID) == "" || strings.TrimSpace(body.ClientSecret) == "" {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_REQUEST_INVALID", "Connector OAuth request is invalid")
		return
	}
	resolved, err := setup.releases.resolve(request.Context(), identity)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_RELEASE_UNAVAILABLE", "Connector release metadata is unavailable")
		return
	}
	oauth := resolved.release.Manifest.Spec.Auth.OAuth2
	if resolved.release.Manifest.Spec.Auth.Type != "oauth2" || oauth == nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_UNSUPPORTED", "Connector release does not support local OAuth")
		return
	}
	if err := validateManifestValueMaps(resolved.release.Manifest, body); err != nil {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_REQUEST_INVALID", "Connector OAuth configuration is invalid")
		return
	}
	if body.Configuration == nil {
		body.Configuration = map[string]json.RawMessage{}
	}
	if body.CredentialValues == nil {
		body.CredentialValues = map[string]json.RawMessage{}
	}
	if body.CredentialSecrets == nil {
		body.CredentialSecrets = map[string]string{}
	}
	authorizationURL, err := url.Parse(oauth.AuthorizationEndpoint)
	if err != nil || authorizationURL.Scheme != "https" || authorizationURL.Host == "" {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_RELEASE_INVALID", "Connector OAuth endpoint is invalid")
		return
	}
	state, err := randomConnectorToken(32)
	if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_OAUTH_START_FAILED", "Connector OAuth could not start")
		return
	}
	verifier, err := randomConnectorToken(32)
	if err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_OAUTH_START_FAILED", "Connector OAuth could not start")
		return
	}
	redirectURI := connectorOAuthRedirectURI(request)
	expiresAt := time.Now().Add(10 * time.Minute)
	setup.oauthSessionsMu.Lock()
	setup.deleteExpiredOAuthSessions(time.Now())
	setup.oauthSessions[state] = connectorOAuthSession{
		identity: identity, release: resolved.release, definitionRevision: snapshot.DefinitionRevision,
		clientID: body.ClientID, clientSecret: body.ClientSecret, codeVerifier: verifier, redirectURI: redirectURI,
		configuration: body.Configuration, credentialValues: body.CredentialValues,
		credentialSecrets: body.CredentialSecrets, expiresAt: expiresAt,
	}
	setup.oauthSessionsMu.Unlock()
	query := authorizationURL.Query()
	query.Set("client_id", body.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(oauth.Scopes, " "))
	if len(oauth.UserScopes) > 0 {
		query.Set("user_scope", strings.Join(oauth.UserScopes, " "))
	}
	query.Set("state", state)
	if oauth.PKCE {
		challenge := sha256.Sum256([]byte(verifier))
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		query.Set("code_challenge_method", "S256")
	}
	authorizationURL.RawQuery = query.Encode()
	writeWebJSON(response, http.StatusOK, map[string]any{
		"authorizationUrl": authorizationURL.String(), "expiresAt": expiresAt,
	})
}

func (setup *connectorSetup) handleOAuthCallback(response http.ResponseWriter, request *http.Request) {
	state := request.URL.Query().Get("state")
	code := request.URL.Query().Get("code")
	if state == "" || code == "" || request.URL.Query().Get("error") != "" {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_CALLBACK_INVALID", "Connector OAuth callback is invalid")
		return
	}
	setup.oauthSessionsMu.Lock()
	setup.deleteExpiredOAuthSessions(time.Now())
	session, found := setup.oauthSessions[state]
	delete(setup.oauthSessions, state)
	setup.oauthSessionsMu.Unlock()
	if !found {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_SESSION_INVALID", "Connector OAuth session is missing, expired, or already used")
		return
	}
	snapshot, err := setup.flowDefinitions.Load(request.Context())
	if err != nil || snapshot.DefinitionRevision != session.definitionRevision {
		api.WriteCodedError(response, http.StatusConflict, "FLOW_DEFINITION_REVISION_CONFLICT", "Flow Definition changed during Connector OAuth")
		return
	}
	currentIdentity, found, conflict, err := connectorDefinitionForKey(
		snapshot.Response,
		session.identity.ConnectorID,
		session.identity.ConnectionName,
	)
	if err != nil || !found || conflict || currentIdentity.ModulePath != session.identity.ModulePath ||
		currentIdentity.ModuleVersion != session.identity.ModuleVersion {
		api.WriteCodedError(response, http.StatusConflict, "FLOW_DEFINITION_REVISION_CONFLICT", "Connector definition changed during OAuth")
		return
	}
	token, err := setup.exchangeConnectorOAuthToken(request.Context(), session, code)
	if err != nil {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_OAUTH_TOKEN_EXCHANGE_FAILED", "Connector OAuth token exchange failed")
		return
	}
	if !hasRequiredConnectorScopes(token.Scope, session.release.Manifest.Spec.Auth.OAuth2.Scopes) {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_SCOPE_INSUFFICIENT", "Connector OAuth grant is missing required scopes")
		return
	}
	if !hasRequiredConnectorScopes(token.AuthedUser.Scope, session.release.Manifest.Spec.Auth.OAuth2.UserScopes) {
		api.WriteCodedError(response, http.StatusBadRequest, "CONNECTOR_OAUTH_SCOPE_INSUFFICIENT", "Connector OAuth user grant is missing required scopes")
		return
	}
	credentials := make(map[string]json.RawMessage, 1+len(session.credentialValues)+len(session.credentialSecrets))
	mappings := session.release.Manifest.Spec.Auth.OAuth2.CredentialMappings
	if len(mappings) == 0 {
		mappings = []connectorOAuthCredentialMapping{{Credential: "access_token", Source: "access_token"}}
	}
	for _, mapping := range mappings {
		value, found := connectorOAuthResponseValue(token.raw, mapping.Source)
		if !found {
			api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_OAUTH_TOKEN_EXCHANGE_FAILED", "Connector OAuth token response is missing a mapped credential")
			return
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_OAUTH_WRITE_FAILED", "Connector OAuth credentials could not be saved")
			return
		}
		credentials[mapping.Credential] = encoded
	}
	for name, value := range session.credentialValues {
		credentials[name] = value
	}
	for name, value := range session.credentialSecrets {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_OAUTH_WRITE_FAILED", "Connector OAuth credentials could not be saved")
			return
		}
		credentials[name] = encoded
	}
	var expiresAt *time.Time
	if token.ExpiresIn > 0 {
		value := time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC()
		expiresAt = &value
	}
	connection := localConnectorConnection{
		ConnectorID: session.identity.ConnectorID, ModulePath: session.identity.ModulePath,
		ModuleVersion: session.identity.ModuleVersion, Provider: session.release.Manifest.Spec.Provider,
		ConnectionName: session.identity.ConnectionName, Configuration: session.configuration,
		Credentials: credentials, CredentialExpiresAt: expiresAt,
	}
	if err := setup.store.put(connection); err != nil {
		api.WriteCodedError(response, http.StatusInternalServerError, "CONNECTOR_OAUTH_WRITE_FAILED", "Connector OAuth credentials could not be saved")
		return
	}
	location := "/v2/connections?oauth=success&connectorId=" + url.QueryEscape(connection.ConnectorID) +
		"&connectionName=" + url.QueryEscape(connection.ConnectionName)
	http.Redirect(response, request, location, http.StatusSeeOther)
}

func (setup *connectorSetup) exchangeConnectorOAuthToken(
	ctx context.Context,
	session connectorOAuthSession,
	code string,
) (connectorOAuthTokenResponse, error) {
	oauth := session.release.Manifest.Spec.Auth.OAuth2
	tokenURL, err := url.Parse(oauth.TokenEndpoint)
	if err != nil || tokenURL.Scheme != "https" || tokenURL.Host == "" {
		return connectorOAuthTokenResponse{}, fmt.Errorf("Connector OAuth token endpoint is invalid")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {session.redirectURI},
		"client_id":     {session.clientID},
		"client_secret": {session.clientSecret},
	}
	if oauth.PKCE {
		form.Set("code_verifier", session.codeVerifier)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return connectorOAuthTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := setup.releases.httpClient.Do(request)
	if err != nil {
		return connectorOAuthTokenResponse{}, err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return connectorOAuthTokenResponse{}, err
	}
	if response.StatusCode != http.StatusOK || len(contents) > 1<<20 {
		return connectorOAuthTokenResponse{}, fmt.Errorf("Connector OAuth provider returned HTTP %d", response.StatusCode)
	}
	var token connectorOAuthTokenResponse
	if err := json.Unmarshal(contents, &token); err != nil {
		return connectorOAuthTokenResponse{}, err
	}
	if err := json.Unmarshal(contents, &token.raw); err != nil {
		return connectorOAuthTokenResponse{}, err
	}
	if token.Error != "" {
		return connectorOAuthTokenResponse{}, fmt.Errorf("Connector OAuth provider rejected token exchange")
	}
	return token, nil
}

func validateManifestValueMaps(manifest connectorReleaseManifest, request connectorOAuthStartRequest) error {
	if request.Configuration == nil {
		request.Configuration = map[string]json.RawMessage{}
	}
	if request.CredentialValues == nil {
		request.CredentialValues = map[string]json.RawMessage{}
	}
	if request.CredentialSecrets == nil {
		request.CredentialSecrets = map[string]string{}
	}
	if err := validateConnectorFieldValues(manifest.Spec.Configuration.Fields, request.Configuration, nil, nil); err != nil {
		return err
	}
	mappedCredentials := make(map[string]bool)
	if manifest.Spec.Auth.OAuth2 != nil {
		for _, mapping := range manifest.Spec.Auth.OAuth2.CredentialMappings {
			mappedCredentials[mapping.Credential] = true
		}
		if len(manifest.Spec.Auth.OAuth2.CredentialMappings) == 0 {
			mappedCredentials["access_token"] = true
		}
	}
	return validateConnectorFieldValues(manifest.Spec.Auth.Fields, request.CredentialValues, request.CredentialSecrets, mappedCredentials)
}

func validateConnectorFieldValues(
	fields []connectorManifestField,
	values map[string]json.RawMessage,
	secrets map[string]string,
	mappedCredentials map[string]bool,
) error {
	known := make(map[string]connectorManifestField, len(fields))
	for _, field := range fields {
		known[field.Name] = field
	}
	for name := range values {
		field, found := known[name]
		if !found || field.Type == "secretString" {
			return fmt.Errorf("field %q is not a non-secret manifest field", name)
		}
	}
	for name := range secrets {
		field, found := known[name]
		if !found || field.Type != "secretString" || mappedCredentials[name] {
			return fmt.Errorf("field %q is not a host-supplied secret field", name)
		}
	}
	for _, field := range fields {
		if !field.Required || field.Default != nil {
			continue
		}
		if mappedCredentials[field.Name] {
			continue
		}
		if field.Type == "secretString" {
			if strings.TrimSpace(secrets[field.Name]) == "" {
				return fmt.Errorf("required secret field %q is missing", field.Name)
			}
		} else if len(values[field.Name]) == 0 {
			return fmt.Errorf("required field %q is missing", field.Name)
		}
	}
	return nil
}

func connectorOAuthResponseValue(response map[string]any, path string) (string, bool) {
	var current any = response
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[segment]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func validateRawConnectorFields(
	fields []connectorManifestField,
	values map[string]json.RawMessage,
	allowSecrets bool,
) error {
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	known := make(map[string]connectorManifestField, len(fields))
	for _, field := range fields {
		known[field.Name] = field
	}
	for name, value := range values {
		field, found := known[name]
		if !found || (field.Type == "secretString" && !allowSecrets) {
			return fmt.Errorf("field %q is not allowed", name)
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return fmt.Errorf("field %q is invalid", name)
		}
		if (field.Type == "string" || field.Type == "url" || field.Type == "duration" ||
			field.Type == "enum" || field.Type == "secretString") && decoded != nil {
			text, ok := decoded.(string)
			if !ok || (field.Required && strings.TrimSpace(text) == "") {
				return fmt.Errorf("field %q must be a non-empty string", name)
			}
		}
	}
	for _, field := range fields {
		if field.Required && field.Default == nil && len(values[field.Name]) == 0 {
			return fmt.Errorf("required field %q is missing", field.Name)
		}
	}
	return nil
}

func hasRequiredConnectorScopes(granted string, required []string) bool {
	grantedScopes := make(map[string]bool)
	for _, scope := range strings.FieldsFunc(granted, func(character rune) bool {
		return character == ' ' || character == ','
	}) {
		grantedScopes[scope] = true
	}
	for _, scope := range required {
		if !grantedScopes[scope] {
			return false
		}
	}
	return true
}

func connectorOAuthRedirectURI(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + request.Host + "/api/v2/connector-oauth/callback"
}

func (setup *connectorSetup) deleteExpiredOAuthSessions(now time.Time) {
	for state, session := range setup.oauthSessions {
		if !now.Before(session.expiresAt) {
			delete(setup.oauthSessions, state)
		}
	}
}

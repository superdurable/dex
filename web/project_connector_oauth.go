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
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
	"github.com/superdurable/dex/web/api"
)

const projectOAuthSchema = "dex.dev/project-oauth/v1"

type projectOAuthRecord struct {
	SchemaVersion         string                      `json:"schemaVersion"`
	Scope                 projectconfig.Scope         `json:"scope"`
	ActorID               string                      `json:"actorId"`
	AttemptID             string                      `json:"attemptId"`
	Key                   projectconfig.ConnectionKey `json:"connection"`
	AppManifestRevision   uint64                      `json:"appManifestRevision"`
	ConfigurationRevision uint64                      `json:"configurationRevision"`
	CredentialRevision    uint64                      `json:"credentialRevision"`
	ExpiresAt             time.Time                   `json:"expiresAt"`
	Status                string                      `json:"status"`
	Seed                  projectObjectReference      `json:"seed"`
}

type projectOAuthSeed struct {
	Identity          connectorDefinitionIdentity `json:"identity"`
	Release           connectorRelease            `json:"release"`
	AuthMethod        connectorManifestAuthMethod `json:"authMethod"`
	ClientID          string                      `json:"clientId"`
	ClientSecret      string                      `json:"clientSecret"`
	Verifier          string                      `json:"verifier"`
	RedirectURI       string                      `json:"redirectUri"`
	Configuration     map[string]json.RawMessage  `json:"configuration"`
	CredentialValues  map[string]json.RawMessage  `json:"credentialValues"`
	CredentialSecrets map[string]string           `json:"credentialSecrets"`
}

type projectOAuthResult struct {
	ReceivedAt time.Time      `json:"receivedAt"`
	Token      map[string]any `json:"token"`
}

func (setup *connectorSetup) startProjectOAuth(response http.ResponseWriter, request *http.Request, snapshot *connectorAuthorizationSnapshot, identity connectorDefinitionIdentity, release connectorRelease, method connectorManifestAuthMethod, body connectorOAuthStartRequest) {
	actor, err := projectActor(request)
	if err != nil {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Authenticated actor is required")
		return
	}
	credentialRevision, err := projectRevision(request.Header.Get(projectCredentialRevisionHeader))
	if err != nil {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	state, err := randomConnectorToken(32)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	verifier, err := randomConnectorToken(32)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	authorizationURL, err := url.Parse(method.OAuth2.AuthorizationEndpoint)
	if err != nil || authorizationURL.Scheme != "https" || authorizationURL.Host == "" {
		api.WriteCodedError(response, 502, "CONNECTOR_RELEASE_INVALID", "OAuth authorization endpoint is invalid")
		return
	}
	query := authorizationURL.Query()
	for name, value := range method.OAuth2.AuthorizationParameters {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" || isReservedConnectorOAuthAuthorizationParameter(name) {
			api.WriteCodedError(response, 502, "CONNECTOR_RELEASE_INVALID", "OAuth authorization parameters are invalid")
			return
		}
		query.Set(name, value)
	}
	redirectURI := connectorOAuthRedirectURI(request)
	query.Set("client_id", body.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(method.OAuth2.Scopes, " "))
	query.Set("state", state)
	if len(method.OAuth2.UserScopes) > 0 {
		query.Set("user_scope", strings.Join(method.OAuth2.UserScopes, " "))
	}
	if method.OAuth2.PKCE {
		challenge := sha256.Sum256([]byte(verifier))
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		query.Set("code_challenge_method", "S256")
	}
	authorizationURL.RawQuery = query.Encode()
	if err = validateProjectFields(connectorConfigurationFieldsForMethods(release.Manifest.Spec.Configuration.Fields, []connectorManifestAuthMethod{method}), body.Configuration); err != nil {
		api.WriteCodedError(response, 400, "CONNECTOR_CONFIGURATION_INVALID", err.Error())
		return
	}
	store := setup.requestStore(request).(*projectConnectorStore)
	if err = store.saveOAuthConfiguration(identity, release.Manifest.Spec.Provider, method.ID, body.Configuration); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	attempt := projectOAuthAttempt(state)
	key := setup.project.prefix + "/oauth/" + attempt
	seed := projectOAuthSeed{Identity: identity, Release: release, AuthMethod: method, ClientID: body.ClientID, ClientSecret: body.ClientSecret, Verifier: verifier, RedirectURI: redirectURI, Configuration: body.Configuration, CredentialValues: body.CredentialValues, CredentialSecrets: body.CredentialSecrets}
	contents, err := json.Marshal(seed)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	seedRef, err := setup.project.immutableObject(request.Context(), key+"/seed", contents)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	record := projectOAuthRecord{SchemaVersion: projectOAuthSchema, Scope: setup.project.scope, ActorID: actor, AttemptID: attempt, Key: projectconfig.ConnectionKey{ConnectorID: identity.ConnectorID, ConnectionName: identity.ConnectionName}, AppManifestRevision: snapshot.AppManifestRevision, ConfigurationRevision: store.document.Revision, CredentialRevision: credentialRevision, ExpiresAt: time.Now().UTC().Add(10 * time.Minute), Status: "PENDING", Seed: seedRef}
	contents, err = json.Marshal(record)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if _, err = setup.project.writeObject(request.Context(), key+"/head", "", contents); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	writeWebJSON(response, 200, map[string]any{"authorizationUrl": authorizationURL.String(), "expiresAt": record.ExpiresAt})
}

func (setup *connectorSetup) completeProjectOAuth(response http.ResponseWriter, request *http.Request) {
	actor, err := projectActor(request)
	if err != nil {
		api.WriteCodedError(response, 403, "PROJECT_CONFIGURATION_FORBIDDEN", "Authenticated actor is required")
		return
	}
	query := request.URL.Query()
	if len(query["state"]) != 1 || len(query["code"]) > 1 || len(query["error"]) > 1 || len(query.Get("state")) != 43 || (query.Get("code") != "" && query.Get("error") != "") {
		api.WriteCodedError(response, 400, "CONNECTOR_OAUTH_CALLBACK_INVALID", "OAuth callback is invalid")
		return
	}
	attempt := projectOAuthAttempt(query.Get("state"))
	key := setup.project.prefix + "/oauth/" + attempt
	object, err := setup.project.objects.ReadObject(request.Context(), key+"/head", "")
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	var record projectOAuthRecord
	if err = decodeStrictConnectorJSON(object.Contents, &record); err != nil || record.SchemaVersion != projectOAuthSchema || record.Scope != setup.project.scope || record.AttemptID != attempt || record.ActorID != actor || !time.Now().Before(record.ExpiresAt) || record.Seed.Key != key+"/seed" {
		api.WriteCodedError(response, 403, "CONNECTOR_OAUTH_SESSION_INVALID", "OAuth state, actor, scope, or expiry is invalid")
		return
	}
	seedObject, err := setup.project.objects.ReadObject(request.Context(), record.Seed.Key, record.Seed.Version)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	var seed projectOAuthSeed
	if projectDigest(seedObject.Contents) != record.Seed.Digest || decodeStrictConnectorJSON(seedObject.Contents, &seed) != nil || seed.RedirectURI != connectorOAuthRedirectURI(request) {
		api.WriteCodedError(response, 403, "CONNECTOR_OAUTH_SESSION_INVALID", "OAuth session integrity or callback origin is invalid")
		return
	}
	if record.Status == "COMPLETED" {
		redirectProjectOAuth(response, request, record.Key)
		return
	}
	if record.Status == "FAILED" {
		api.WriteCodedError(response, 409, "CONNECTOR_OAUTH_REAUTHORIZE", "Start authorization again")
		return
	}
	manifest, _, err := setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	document, _, err := setup.project.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if manifest.Revision != record.AppManifestRevision || document.Revision != record.ConfigurationRevision {
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	if query.Get("error") != "" {
		record.Status = "FAILED"
		if err = setup.writeOAuthRecord(request.Context(), key, object.ETag, record); err != nil {
			writeProjectConfigurationError(response, err)
			return
		}
		api.WriteCodedError(response, 400, "CONNECTOR_OAUTH_PROVIDER_DENIED", "OAuth provider denied authorization")
		return
	}
	if record.Status == "PENDING" {
		record.Status = "EXCHANGING"
		if err = setup.writeOAuthRecord(request.Context(), key, object.ETag, record); err != nil {
			writeProjectConfigurationError(response, err)
			return
		}
	}
	admission, err := setup.project.connections.BeginCredentialExchange(request.Context(), record.Key, record.CredentialRevision, record.AttemptID, record.ExpiresAt)
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if !admission.ProviderDispatchAllowed {
		if _, recoverErr := setup.project.connections.RecoverCredentialExchange(request.Context(), admission); recoverErr == nil {
			setup.finishProjectOAuth(response, request, key, record)
			return
		} else if !errors.Is(recoverErr, projectconfig.ErrExchangePending) {
			writeProjectConfigurationError(response, recoverErr)
			return
		}
	}
	result, err := setup.projectOAuthTokenResult(request.Context(), key, seed, query.Get("code"), admission.ProviderDispatchAllowed)
	if err != nil {
		if errors.Is(err, projectconfig.ErrExchangePending) {
			api.WriteCodedError(response, 409, "CONNECTOR_OAUTH_PENDING", "OAuth exchange is still pending; retry this callback without restarting authorization")
			return
		}
		api.WriteCodedError(response, 503, "CONNECTOR_OAUTH_OUTCOME_UNCONFIRMED", "OAuth exchange outcome is unconfirmed; retry this callback")
		return
	}

	material, err := setup.projectOAuthMaterial(request.Context(), seed, result)
	if err != nil {
		if failureErr := setup.project.connections.FailCredentialExchange(request.Context(), admission); failureErr != nil {
			err = errors.Join(err, failureErr)
		}
		api.WriteCodedError(response, 409, "CONNECTOR_OAUTH_REAUTHORIZE", "OAuth grant or identity verification failed; start authorization again")
		return
	}
	manifest, _, err = setup.project.readManifest(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	document, _, err = setup.project.readConfiguration(request.Context())
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	if manifest.Revision != record.AppManifestRevision || document.Revision != record.ConfigurationRevision {
		if failureErr := setup.project.connections.FailCredentialExchange(request.Context(), admission); failureErr != nil && !errors.Is(failureErr, projectconfig.ErrConflict) {
			writeProjectConfigurationError(response, failureErr)
			return
		}
		writeProjectConfigurationError(response, projectconfig.ErrConflict)
		return
	}
	if _, err = setup.project.connections.CommitCredentialExchange(request.Context(), admission, material); err != nil {
		if _, recoverErr := setup.project.connections.RecoverCredentialExchange(request.Context(), admission); recoverErr != nil {
			writeProjectConfigurationError(response, errors.Join(err, recoverErr))
			return
		}
	}
	setup.finishProjectOAuth(response, request, key, record)
}

func (setup *connectorSetup) projectOAuthTokenResult(ctx context.Context, key string, seed projectOAuthSeed, code string, dispatch bool) (projectOAuthResult, error) {
	resultObject, err := setup.project.objects.ReadObject(ctx, key+"/result", "")
	if err == nil {
		var result projectOAuthResult
		if decodeStrictConnectorJSON(resultObject.Contents, &result) != nil || result.ReceivedAt.IsZero() {
			return result, errors.New("OAuth result integrity is invalid")
		}
		return result, nil
	}
	if !errors.Is(err, projectconfig.ErrObjectNotFound) {
		return projectOAuthResult{}, err
	}
	if !dispatch {
		return projectOAuthResult{}, projectconfig.ErrExchangePending
	}
	token, err := setup.exchangeConnectorOAuthToken(ctx, connectorOAuthSession{authMethod: seed.AuthMethod, clientID: seed.ClientID, clientSecret: seed.ClientSecret, codeVerifier: seed.Verifier, redirectURI: seed.RedirectURI}, code)
	if err != nil {
		return projectOAuthResult{}, err
	}
	result := projectOAuthResult{ReceivedAt: time.Now().UTC(), Token: token.raw}
	contents, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = setup.project.immutableObject(ctx, key+"/result", contents); err != nil {
		return result, err
	}
	return result, nil
}

func (setup *connectorSetup) projectOAuthMaterial(ctx context.Context, seed projectOAuthSeed, result projectOAuthResult) (projectconfig.CredentialMaterial, error) {
	tokenBytes, err := json.Marshal(result.Token)
	if err != nil {
		return projectconfig.CredentialMaterial{}, err
	}
	var token connectorOAuthTokenResponse
	if err = json.Unmarshal(tokenBytes, &token); err != nil {
		return projectconfig.CredentialMaterial{}, err
	}
	token.raw = result.Token
	oauth := seed.AuthMethod.OAuth2
	if !hasRequiredConnectorScopes(token.raw, oauth.Scopes) ||
		(len(oauth.UserScopes) > 0 && !hasRequiredConnectorUserScopes(token.raw, oauth.UserScopes)) {
		return projectconfig.CredentialMaterial{}, errors.New("OAuth grant scope is insufficient")
	}
	credentials := make(map[string]json.RawMessage)
	values := map[string]string{"auth_method": seed.AuthMethod.ID}
	if oauth.ClientIDCredential != "" {
		values[oauth.ClientIDCredential] = seed.ClientID
	}
	if oauth.ClientSecretCredential != "" {
		values[oauth.ClientSecretCredential] = seed.ClientSecret
	}
	for name, value := range seed.CredentialSecrets {
		values[name] = value
	}
	for name, value := range values {
		contents, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return projectconfig.CredentialMaterial{}, marshalErr
		}
		credentials[name] = contents
	}
	for name, value := range seed.CredentialValues {
		credentials[name] = value
	}
	mappings := oauth.CredentialMappings
	if len(mappings) == 0 {
		mappings = []connectorOAuthCredentialMapping{{Credential: "access_token", Source: "access_token"}}
	}
	for _, mapping := range mappings {
		value, found := connectorOAuthResponseValue(token.raw, mapping.Source)
		if !found {
			return projectconfig.CredentialMaterial{}, errors.New("OAuth mapped credential is absent")
		}
		contents, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return projectconfig.CredentialMaterial{}, marshalErr
		}
		credentials[mapping.Credential] = contents
	}
	derived, err := setup.deriveConnectorOAuthCredentials(ctx, token, oauth.CredentialDerivations)
	if err != nil {
		return projectconfig.CredentialMaterial{}, err
	}
	for name, value := range derived {
		credentials[name] = value
	}
	contents, err := json.Marshal(credentials)
	if err != nil {
		return projectconfig.CredentialMaterial{}, err
	}
	material := projectconfig.CredentialMaterial{Credentials: contents, ModuleVersion: seed.Identity.ModuleVersion, AuthMethod: seed.AuthMethod.ID}
	if token.ExpiresIn > 0 {
		expires := result.ReceivedAt.Add(time.Duration(token.ExpiresIn) * time.Second)
		material.ExpiresAt = &expires
	}
	return material, nil
}

func (setup *connectorSetup) finishProjectOAuth(response http.ResponseWriter, request *http.Request, key string, record projectOAuthRecord) {
	current, err := setup.project.objects.ReadObject(request.Context(), key+"/head", "")
	if err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	record.Status = "COMPLETED"
	if err = setup.writeOAuthRecord(request.Context(), key, current.ETag, record); err != nil {
		writeProjectConfigurationError(response, err)
		return
	}
	redirectProjectOAuth(response, request, record.Key)
}

func (setup *connectorSetup) writeOAuthRecord(ctx context.Context, key, etag string, record projectOAuthRecord) error {
	contents, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = setup.project.writeObject(ctx, key+"/head", etag, contents)
	return err
}
func projectOAuthAttempt(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}
func redirectProjectOAuth(response http.ResponseWriter, request *http.Request, key projectconfig.ConnectionKey) {
	http.Redirect(response, request, webPathFromContext(request.Context(), "/v2/connectors")+"?oauth=success&connectorId="+url.QueryEscape(key.ConnectorID)+"&connectionName="+url.QueryEscape(key.ConnectionName), http.StatusSeeOther)
}

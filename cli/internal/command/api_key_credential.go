// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiKeyExchangeTimeout          = 10 * time.Second
	apiKeyRefreshRetryInterval     = 5 * time.Second
	maxAPIKeyExchangeResponseBytes = 1 << 20
	maxAPIKeyExchangeErrorRunes    = 200
	redactedAPIKeySecret           = "[REDACTED]"
	// Capping the lifetime keeps expiry arithmetic far from time.Duration overflow.
	maxAPIKeyTokenLifetimeSeconds = math.MaxInt32
)

// apiKeyCredential follows the SDK API key contract: one shared exchange at a time, run outside the mutex.
type apiKeyCredential struct {
	tokenURL   *url.URL
	serverID   string
	apiKey     string
	httpClient *http.Client
	// now must return monotonic readings; tests replace it before the first call.
	now func() time.Time

	mu                   sync.Mutex
	token                string
	refreshAt            time.Time
	expiresAt            time.Time
	nextRefreshAttemptAt time.Time
	inFlightExchange     *apiKeyExchange
	// issuedTokens keeps the latest two tokens so that echoed tokens stay redacted.
	issuedTokens [2]string
}

// apiKeyExchange is one token exchange; done closes after token and err are set.
type apiKeyExchange struct {
	done  chan struct{}
	token string
	err   error
}

func newAPIKeyCredential(tokenURL string, serverID string, apiKey string) (*apiKeyCredential, error) {
	parsedTokenURL, err := parseAPIKeyTokenURL(tokenURL)
	if err != nil {
		return nil, err
	}
	if serverID == "" {
		return nil, errors.New("API key server ID is empty")
	}
	if apiKey == "" {
		return nil, errors.New("API key is empty")
	}
	return &apiKeyCredential{
		tokenURL:   parsedTokenURL,
		serverID:   serverID,
		apiKey:     apiKey,
		httpClient: &http.Client{CheckRedirect: refuseAPIKeyExchangeRedirect},
		now:        time.Now,
	}, nil
}

func parseAPIKeyTokenURL(tokenURL string) (*url.URL, error) {
	if tokenURL == "" {
		return nil, errors.New("token URL is empty")
	}
	parsedTokenURL, err := url.Parse(tokenURL)
	if err != nil {
		return nil, fmt.Errorf("token URL is invalid: %w", err)
	}
	if parsedTokenURL.Host == "" {
		return nil, fmt.Errorf("token URL %s must be an absolute URL with a host", parsedTokenURL.Redacted())
	}
	switch parsedTokenURL.Scheme {
	case "https":
		return parsedTokenURL, nil
	case "http":
		if isLoopbackHost(parsedTokenURL.Hostname()) {
			return parsedTokenURL, nil
		}
	}
	return nil, fmt.Errorf(
		"token URL %s must use https; http is allowed only for localhost, 127.0.0.0/8, and ::1",
		parsedTokenURL.Redacted(),
	)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// refuseAPIKeyExchangeRedirect keeps the API key from reaching any URL other than the configured one.
func refuseAPIKeyExchangeRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// GetRequestMetadata returns a valid cached token at once; a refresh after 80% of its lifetime runs in the background.
func (c *apiKeyCredential) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	c.mu.Lock()
	now := c.now()
	if c.token != "" && now.Before(c.expiresAt) {
		token := c.token
		if !now.Before(c.refreshAt) && !now.Before(c.nextRefreshAttemptAt) {
			c.startExchangeLocked()
		}
		c.mu.Unlock()
		return map[string]string{"authorization": "Bearer " + token}, nil
	}
	exchange := c.startExchangeLocked()
	c.mu.Unlock()
	select {
	case <-exchange.done:
		if exchange.err != nil {
			return nil, exchange.err
		}
		return map[string]string{"authorization": "Bearer " + exchange.token}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*apiKeyCredential) RequireTransportSecurity() bool { return false }

// startExchangeLocked returns the in-flight exchange, starting one when none is running.
func (c *apiKeyCredential) startExchangeLocked() *apiKeyExchange {
	if c.inFlightExchange != nil {
		return c.inFlightExchange
	}
	exchange := &apiKeyExchange{done: make(chan struct{})}
	c.inFlightExchange = exchange
	go c.runExchange(exchange, newAPIKeySecrets(c.apiKey, c.issuedTokens[0], c.issuedTokens[1]))
	return exchange
}

// runExchange publishes one exchange result and never caches a token that expired before the response arrived.
func (c *apiKeyCredential) runExchange(exchange *apiKeyExchange, secrets apiKeySecrets) {
	// The exchange is shared by every waiting call, so no single call's context may cancel it.
	exchangeCtx, cancel := context.WithTimeout(context.Background(), apiKeyExchangeTimeout)
	defer cancel()
	startedAt := c.now()
	token, lifetime, err := c.exchange(exchangeCtx, secrets)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlightExchange = nil
	finishedAt := c.now()
	if err == nil {
		c.issuedTokens = [2]string{token, c.issuedTokens[0]}
		if !finishedAt.Before(startedAt.Add(lifetime)) {
			token = ""
			err = c.newExchangeError(http.StatusOK, fmt.Sprintf(
				"the access token expired before the response arrived; expires_in was %s and the exchange took %s",
				lifetime, finishedAt.Sub(startedAt).Round(time.Millisecond),
			), nil)
		}
	}
	if err != nil {
		c.nextRefreshAttemptAt = finishedAt.Add(apiKeyRefreshRetryInterval)
	} else {
		c.token = token
		c.refreshAt = startedAt.Add(lifetime - lifetime/5)
		c.expiresAt = startedAt.Add(lifetime)
		c.nextRefreshAttemptAt = time.Time{}
	}
	exchange.token, exchange.err = token, err
	close(exchange.done)
}

// exchange returns the access token and its lifetime, or an *apiKeyExchangeError without secrets.
func (c *apiKeyCredential) exchange(exchangeCtx context.Context, secrets apiKeySecrets) (string, time.Duration, error) {
	requestBody, err := json.Marshal(apiKeyExchangeRequest{ServerID: c.serverID, APIKey: c.apiKey})
	if err != nil {
		return "", 0, c.newExchangeError(0, "encode the exchange request", secrets.redactError(err))
	}
	request, err := http.NewRequestWithContext(exchangeCtx, http.MethodPost, c.tokenURL.String(), bytes.NewReader(requestBody))
	if err != nil {
		return "", 0, c.newExchangeError(0, "create the exchange request", secrets.redactError(err))
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", 0, c.newTransportError(exchangeCtx, err, secrets)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxAPIKeyExchangeResponseBytes+1))
	if closeErr := response.Body.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return "", 0, c.newTransportError(exchangeCtx, readErr, secrets)
	}
	return c.parseExchangeResponse(response.StatusCode, responseBody, secrets)
}

type apiKeyExchangeRequest struct {
	ServerID string `json:"server_id"`
	APIKey   string `json:"api_key"`
}

func (c *apiKeyCredential) parseExchangeResponse(statusCode int, responseBody []byte, secrets apiKeySecrets) (string, time.Duration, error) {
	if len(responseBody) > maxAPIKeyExchangeResponseBytes {
		return "", 0, c.newExchangeError(statusCode, "response body exceeds 1 MiB", nil)
	}
	var fields map[string]json.RawMessage
	isObject := json.Unmarshal(responseBody, &fields) == nil && fields != nil
	exchangeError := c.newExchangeError(statusCode, "", nil)
	if isObject {
		if responseToken, hasResponseToken := jsonStringField(fields, "access_token"); hasResponseToken {
			secrets = newAPIKeySecrets(append(slices.Clone(secrets), responseToken)...)
		}
		exchangeError.errorCode = redactedJSONStringField(fields, "error", secrets)
		exchangeError.errorDescription = redactedJSONStringField(fields, "error_description", secrets)
	}
	switch {
	case statusCode != http.StatusOK:
		return "", 0, exchangeError
	case !isObject:
		exchangeError.problem = "response is not a JSON object"
		return "", 0, exchangeError
	}
	accessToken, hasAccessToken := jsonStringField(fields, "access_token")
	if !hasAccessToken || accessToken == "" {
		exchangeError.problem = "response has no non-empty access_token string"
		return "", 0, exchangeError
	}
	tokenType, hasTokenType := jsonStringField(fields, "token_type")
	if !hasTokenType || !strings.EqualFold(tokenType, "Bearer") {
		exchangeError.problem = "response token_type is not Bearer"
		return "", 0, exchangeError
	}
	expiresInSeconds, err := strconv.ParseInt(string(bytes.TrimSpace(fields["expires_in"])), 10, 64)
	if errors.Is(err, strconv.ErrRange) && expiresInSeconds > 0 {
		err = nil
	}
	if err != nil || expiresInSeconds <= 0 {
		exchangeError.problem = "response expires_in is not a positive integer"
		return "", 0, exchangeError
	}
	expiresInSeconds = min(expiresInSeconds, maxAPIKeyTokenLifetimeSeconds)
	return accessToken, time.Duration(expiresInSeconds) * time.Second, nil
}

func jsonStringField(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, hasField := fields[name]
	if !hasField {
		return "", false
	}
	var value string
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

// redactedJSONStringField redacts before truncating, so a cut can never leave part of a secret.
func redactedJSONStringField(fields map[string]json.RawMessage, name string, secrets apiKeySecrets) string {
	value, _ := jsonStringField(fields, name)
	runes := []rune(secrets.redact(value))
	if len(runes) > maxAPIKeyExchangeErrorRunes {
		return string(runes[:maxAPIKeyExchangeErrorRunes])
	}
	return string(runes)
}

func (c *apiKeyCredential) newTransportError(exchangeCtx context.Context, err error, secrets apiKeySecrets) *apiKeyExchangeError {
	if errors.Is(exchangeCtx.Err(), context.DeadlineExceeded) {
		return c.newExchangeError(0, "timed out", secrets.redactError(err))
	}
	return c.newExchangeError(0, "request failed", secrets.redactError(err))
}

func (c *apiKeyCredential) newExchangeError(statusCode int, problem string, cause error) *apiKeyExchangeError {
	return &apiKeyExchangeError{tokenURL: c.tokenURL.Redacted(), statusCode: statusCode, problem: problem, cause: cause}
}

// String identifies the credential by token URL and server ID; it never prints a secret.
func (c *apiKeyCredential) String() string {
	return fmt.Sprintf("APIKeyCredential{tokenURL: %s, serverID: %q}", c.tokenURL.Redacted(), c.serverID)
}

// GoString keeps %#v from printing the API key or an access token.
func (c *apiKeyCredential) GoString() string {
	return c.String()
}

// apiKeyExchangeError describes a failed exchange without the API key, any token, or the raw response body.
type apiKeyExchangeError struct {
	tokenURL         string
	statusCode       int
	problem          string
	errorCode        string
	errorDescription string
	cause            error
}

func (e *apiKeyExchangeError) Error() string {
	var message strings.Builder
	fmt.Fprintf(&message, "API key exchange with %s failed", e.tokenURL)
	if e.statusCode != 0 {
		fmt.Fprintf(&message, " with HTTP %d", e.statusCode)
	}
	if e.problem != "" {
		fmt.Fprintf(&message, ": %s", e.problem)
	}
	if e.errorCode != "" {
		fmt.Fprintf(&message, "; error %q", e.errorCode)
	}
	if e.errorDescription != "" {
		fmt.Fprintf(&message, "; error_description %q", e.errorDescription)
	}
	if e.cause != nil {
		fmt.Fprintf(&message, ": %v", e.cause)
	}
	return message.String()
}

// GoString keeps %#v on the readable, redacted message.
func (e *apiKeyExchangeError) GoString() string {
	return e.Error()
}

func (e *apiKeyExchangeError) Unwrap() error {
	return e.cause
}

// apiKeySecrets holds the API key and known access tokens, longest first, without empty values.
type apiKeySecrets []string

func newAPIKeySecrets(values ...string) apiKeySecrets {
	secrets := apiKeySecrets(slices.DeleteFunc(values, func(value string) bool { return value == "" }))
	// Longest first, so a secret that contains another secret is replaced whole.
	slices.SortFunc(secrets, func(first, second string) int { return len(second) - len(first) })
	return secrets
}

func (secrets apiKeySecrets) redact(text string) string {
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, redactedAPIKeySecret)
	}
	return text
}

func (secrets apiKeySecrets) redactError(err error) error {
	return &redactedAPIKeyExchangeCause{message: secrets.redact(err.Error()), cause: err}
}

// redactedAPIKeyExchangeCause is a transport error whose text has every known secret redacted.
type redactedAPIKeyExchangeCause struct {
	message string
	cause   error
}

func (cause *redactedAPIKeyExchangeCause) Error() string {
	return cause.message
}

// GoString keeps %#v from printing the unredacted cause.
func (cause *redactedAPIKeyExchangeCause) GoString() string {
	return cause.message
}

// Is matches the unredacted cause without an Unwrap that would expose its text.
func (cause *redactedAPIKeyExchangeCause) Is(target error) bool {
	return errors.Is(cause.cause, target)
}

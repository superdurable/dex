// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

func apiError(status int, headers http.Header) error {
	return &openai.Error{StatusCode: status, Response: &http.Response{StatusCode: status, Header: headers}}
}

func TestModelErrorsAreClassifiedByStatusAndConnection(t *testing.T) {
	t.Parallel()
	for _, status := range []int{408, 409, 429, 500, 502, 503, 529} {
		kind, _ := classifyModelError(fmt.Errorf("the model call: %w", apiError(status, nil)))
		require.Equal(t, modelErrorTransient, kind, status)
	}
	for _, status := range []int{400, 401, 403, 404, 413, 422} {
		kind, _ := classifyModelError(apiError(status, nil))
		require.Equal(t, modelErrorPermanent, kind, status)
	}
	kind, _ := classifyModelError(errors.Join(errors.New("stream"), syscall.ECONNRESET))
	require.Equal(t, modelErrorTransient, kind)
	kind, _ = classifyModelError(errors.New("the runner has a bug"))
	require.Equal(t, modelErrorUnknown, kind)
}

func TestRetryAfterIsReadInSecondsMillisecondsOrADate(t *testing.T) {
	t.Parallel()
	_, wait := classifyModelError(apiError(429, http.Header{"Retry-After": {"7"}}))
	require.Equal(t, 7*time.Second, wait)
	_, wait = classifyModelError(apiError(429, http.Header{"Retry-After-Ms": {"2500"}}))
	require.Equal(t, 2500*time.Millisecond, wait)
	date := time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)
	_, wait = classifyModelError(apiError(503, http.Header{"Retry-After": {date}}))
	require.Greater(t, wait, 15*time.Second)
	require.LessOrEqual(t, wait, 20*time.Second)
}

func TestDefaultModelRetryPolicyWaitsAboutTwoMinutes(t *testing.T) {
	t.Parallel()
	policy, err := resolveModelRetryPolicy(ModelRetryPolicy{})
	require.NoError(t, err)
	// 1 + 2 + 4 + 8 + 16 + 30 + 30 = 91 seconds; one more wait passes 120.
	require.Equal(t, int32(7), policy.transientRetries)
	require.Equal(t, int32(10), policy.stepMaximumAttempts())
	disabled, err := resolveModelRetryPolicy(ModelRetryPolicy{TotalDuration: -1})
	require.NoError(t, err)
	require.Equal(t, int32(0), disabled.transientRetries)
	for _, invalid := range []ModelRetryPolicy{
		{InitialInterval: 1500 * time.Millisecond},
		{MaximumInterval: 500 * time.Millisecond},
		{InitialInterval: 10 * time.Second, MaximumInterval: 5 * time.Second},
	} {
		_, err := resolveModelRetryPolicy(invalid)
		require.Error(t, err, "%+v", invalid)
	}
}

func TestTransientErrorWaitsWithJitterAndAtLeastRetryAfter(t *testing.T) {
	t.Parallel()
	policy, err := resolveModelRetryPolicy(ModelRetryPolicy{})
	require.NoError(t, err)
	waits := func(random float64) []time.Duration {
		var result []time.Duration
		for attempt := int32(1); attempt <= 8; attempt++ {
			var retryAfter *dex.RetryAfterError
			require.ErrorAs(t, decideModelCallError(apiError(503, nil), attempt, policy, random), &retryAfter)
			result = append(result, retryAfter.After)
		}
		return result
	}
	seconds := func(values ...int) []time.Duration {
		result := make([]time.Duration, len(values))
		for index, value := range values {
			result[index] = time.Duration(value) * time.Second
		}
		return result
	}
	require.Equal(t, seconds(1, 2, 4, 8, 16, 30, 30, 30), waits(0.999))
	require.Equal(t, seconds(1, 1, 2, 4, 8, 15, 15, 15), waits(0))
	var retryAfter *dex.RetryAfterError
	require.ErrorAs(t, decideModelCallError(apiError(429, http.Header{"Retry-After": {"12"}}), 1, policy, 0), &retryAfter)
	require.Equal(t, 12*time.Second, retryAfter.After)
}

func TestPermanentErrorEndsTheTurnAndAnUnknownOneGoesToTheAttemptLimit(t *testing.T) {
	t.Parallel()
	policy, err := resolveModelRetryPolicy(ModelRetryPolicy{})
	require.NoError(t, err)
	var endsTurn *errModelCallEndsTurn
	require.ErrorAs(t, decideModelCallError(apiError(401, nil), 1, policy, 0), &endsTurn)
	bug := errors.New("the runner has a bug")
	require.Same(t, bug, decideModelCallError(bug, 2, policy, 0))
	require.Same(t, bug, decideModelCallError(bug, 3, policy, 0))
}

// goOpenAIStyleAPIError has the fields of go-openai's APIError, which eino-ext OpenAI-family models return.
type goOpenAIStyleAPIError struct {
	Code           any
	Message        string
	HTTPStatus     string
	HTTPStatusCode int
}

func (apiError *goOpenAIStyleAPIError) Error() string { return apiError.Message }

// goOpenAIStyleRequestError has the fields of go-openai's RequestError, which wraps the transport error.
type goOpenAIStyleRequestError struct {
	HTTPStatus     string
	HTTPStatusCode int
	Err            error
	Body           []byte
}

func (requestError *goOpenAIStyleRequestError) Error() string { return "request failed" }

func (requestError *goOpenAIStyleRequestError) Unwrap() error { return requestError.Err }

// arkStyleAPIError has the fields of the Volcengine Ark SDK's APIError, which the eino-ext ark model returns.
type arkStyleAPIError struct {
	Code           string
	Message        string
	HTTPStatusCode int
	RequestId      string
}

func (apiError arkStyleAPIError) Error() string { return apiError.Message }

// codeOnlyError has only a Code field, as many errors that are not HTTP answers do.
type codeOnlyError struct {
	Code int
}

func (codeError *codeOnlyError) Error() string { return fmt.Sprintf("code %d", codeError.Code) }

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestModelErrorsWithAnHTTPStatusCodeFieldAreClassifiedByStatus(t *testing.T) {
	t.Parallel()
	wrapped := func(err error) error { return fmt.Errorf("[NodeRunError] failed to create chat completion: %w", err) }
	for _, status := range []int{408, 409, 429, 500, 503} {
		kind, _ := classifyModelError(wrapped(&goOpenAIStyleAPIError{Code: "rate_limit", HTTPStatusCode: status}))
		require.Equal(t, modelErrorTransient, kind, "go-openai APIError %d", status)
		kind, _ = classifyModelError(wrapped(&goOpenAIStyleRequestError{HTTPStatusCode: status, Err: errors.New("bad gateway")}))
		require.Equal(t, modelErrorTransient, kind, "go-openai RequestError %d", status)
		kind, _ = classifyModelError(wrapped(arkStyleAPIError{Code: "ServerOverloaded", HTTPStatusCode: status}))
		require.Equal(t, modelErrorTransient, kind, "Ark APIError %d", status)
	}
	for _, status := range []int{400, 401, 403, 404} {
		kind, _ := classifyModelError(wrapped(&goOpenAIStyleAPIError{Code: 400, HTTPStatusCode: status}))
		require.Equal(t, modelErrorPermanent, kind, "go-openai APIError %d", status)
		kind, _ = classifyModelError(wrapped(arkStyleAPIError{Code: "InvalidParameter", HTTPStatusCode: status}))
		require.Equal(t, modelErrorPermanent, kind, "Ark APIError %d", status)
	}
	var endsTurn *errModelCallEndsTurn
	policy, err := resolveModelRetryPolicy(ModelRetryPolicy{})
	require.NoError(t, err)
	require.ErrorAs(t, decideModelCallError(wrapped(&goOpenAIStyleAPIError{HTTPStatusCode: 401}), 1, policy, 0), &endsTurn)
	var retryAfter *dex.RetryAfterError
	require.ErrorAs(t, decideModelCallError(wrapped(&goOpenAIStyleAPIError{HTTPStatusCode: 429}), 1, policy, 0), &retryAfter)
}

func TestARequestErrorWithoutAStatusIsClassifiedByWhatItWraps(t *testing.T) {
	t.Parallel()
	kind, _ := classifyModelError(&goOpenAIStyleRequestError{Err: &net.OpError{Op: "dial", Err: timeoutError{}}})
	require.Equal(t, modelErrorTransient, kind)
	kind, _ = classifyModelError(&goOpenAIStyleRequestError{Err: errors.New("unexpected response")})
	require.Equal(t, modelErrorUnknown, kind)
}

func TestACodeFieldIsNotReadAsAnHTTPStatus(t *testing.T) {
	t.Parallel()
	for _, code := range []int{400, 429, 503} {
		kind, _ := classifyModelError(&codeOnlyError{Code: code})
		require.Equal(t, modelErrorUnknown, kind, code)
	}
	kind, _ := classifyModelError(&goOpenAIStyleAPIError{Code: 429, Message: "no HTTP answer"})
	require.Equal(t, modelErrorUnknown, kind, "APIError.Code is the provider's error code, not the status")
}

// statusCodeAndHTTPStatusCodeError has both status fields; StatusCode wins when it holds a status.
type statusCodeAndHTTPStatusCodeError struct {
	StatusCode     int
	HTTPStatusCode int
}

func (statusError *statusCodeAndHTTPStatusCodeError) Error() string { return "two status fields" }

func TestStatusCodeWinsOverHTTPStatusCode(t *testing.T) {
	t.Parallel()
	kind, _ := classifyModelError(&statusCodeAndHTTPStatusCodeError{StatusCode: 429, HTTPStatusCode: 400})
	require.Equal(t, modelErrorTransient, kind)
	kind, _ = classifyModelError(&statusCodeAndHTTPStatusCodeError{HTTPStatusCode: 400})
	require.Equal(t, modelErrorPermanent, kind, "a zero StatusCode is no status")
}

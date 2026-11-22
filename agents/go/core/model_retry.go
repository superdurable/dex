// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"syscall"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	defaultModelRetryInitialInterval = time.Second
	defaultModelRetryMaximumInterval = 30 * time.Second
	defaultModelRetryTotalDuration   = 2 * time.Minute
)

// ModelRetryPolicy sets how RunModel retries a model call that failed with a
// transient model API error.
//
// A transient error is an HTTP 408, 409, 429, or 5xx answer from the model
// API, or a connection error or timeout on the way to it. Dex retries such a
// call with exponential backoff and jitter. The first wait is InitialInterval.
// Each next wait doubles, up to MaximumInterval. Each wait is drawn between
// half of that value and all of it, so sessions that failed together do not
// all retry together. When the answer carries Retry-After or retry-after-ms,
// the wait is at least that long.
//
// Retries stop after the nominal waits add up to more than TotalDuration. Then
// the turn fails, as before. The defaults wait about two minutes in all. That
// rides out a typical provider outage or rate-limit window, which usually
// clears within a minute, and still ends a turn whose provider is down instead
// of keeping it waiting.
//
// Any other HTTP 4xx answer, such as 400 or 401, fails the turn at once: a
// retry would get the same answer. Other errors, method timeouts, and Worker
// crashes keep RunModel's three attempts.
//
// The HTTP status and headers come from an error in the chain whose struct has
// a StatusCode int field or a Response *http.Response field, as the OpenAI and
// Anthropic Go SDKs' errors do. An error whose struct has neither, or whose
// status there is not an HTTP status, gives its status through an
// HTTPStatusCode int field instead, as the errors of go-openai and the
// Volcengine Ark SDK do; CloudWeGo eino-ext's OpenAI-family and Ark model
// clients return those errors. Such errors carry no headers, so Retry-After
// does not apply to them. A field named Code is never read as a status: SDKs
// put the provider's own error code there. A net.Error, io.ErrUnexpectedEOF,
// ECONNRESET, ECONNREFUSED, or context.DeadlineExceeded from the model call is
// a connection error.
//
// Example:
//
//	flow, err := dexagents.NewAgentSessionFlow(runner, "SupportAgent",
//		dexagents.WithModelRetryPolicy(dexagents.ModelRetryPolicy{
//			MaximumInterval: time.Minute,
//			TotalDuration:   5 * time.Minute,
//		}))
//
// Since agents 0.6.16.
type ModelRetryPolicy struct {
	// InitialInterval is the wait before the first retry, in whole seconds.
	// Zero uses one second.
	InitialInterval time.Duration
	// MaximumInterval is the longest wait between two attempts, in whole
	// seconds. Zero uses 30 seconds. It must not be shorter than
	// InitialInterval.
	MaximumInterval time.Duration
	// TotalDuration caps the sum of the nominal waits, in whole seconds. Zero
	// uses two minutes. A negative value gives transient errors only RunModel's
	// three attempts.
	TotalDuration time.Duration
}

// WithModelRetryPolicy sets the backoff of RunModel for transient model API errors.
//
// The default is ModelRetryPolicy{}: waits from 1 to 30 seconds, about two
// minutes in all. See ModelRetryPolicy.
//
// Since agents 0.6.16.
func WithModelRetryPolicy(policy ModelRetryPolicy) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.modelRetry = policy
	})
}

// resolvedModelRetryPolicy is a ModelRetryPolicy with its defaults and the transient retries it allows.
type resolvedModelRetryPolicy struct {
	initialInterval  time.Duration
	maximumInterval  time.Duration
	totalDuration    time.Duration
	transientRetries int32
}

func resolveModelRetryPolicy(policy ModelRetryPolicy) (resolvedModelRetryPolicy, error) {
	resolved := resolvedModelRetryPolicy{
		initialInterval: policy.InitialInterval,
		maximumInterval: policy.MaximumInterval,
		totalDuration:   policy.TotalDuration,
	}
	if resolved.initialInterval == 0 {
		resolved.initialInterval = defaultModelRetryInitialInterval
	}
	if resolved.maximumInterval == 0 {
		resolved.maximumInterval = defaultModelRetryMaximumInterval
	}
	if resolved.totalDuration == 0 {
		resolved.totalDuration = defaultModelRetryTotalDuration
	}
	for _, value := range []time.Duration{resolved.initialInterval, resolved.maximumInterval} {
		if value < time.Second || value%time.Second != 0 || value > math.MaxInt32*time.Second {
			return resolvedModelRetryPolicy{}, fmt.Errorf("model retry intervals must be whole seconds, at least one, got %v", value)
		}
	}
	if resolved.totalDuration > 0 && resolved.totalDuration%time.Second != 0 {
		return resolvedModelRetryPolicy{}, fmt.Errorf("model retry total duration must be whole seconds, got %v", resolved.totalDuration)
	}
	if resolved.maximumInterval < resolved.initialInterval {
		return resolvedModelRetryPolicy{}, errors.New("model retry maximum interval must not be shorter than the initial interval")
	}
	waited := time.Duration(0)
	for {
		waited += resolved.nominalInterval(resolved.transientRetries + 1)
		if waited > resolved.totalDuration {
			return resolved, nil
		}
		resolved.transientRetries++
	}
}

// nominalInterval returns the wait before a one-based retry before jitter.
func (policy resolvedModelRetryPolicy) nominalInterval(retry int32) time.Duration {
	interval := policy.initialInterval
	for range min(retry-1, 40) {
		interval *= 2
		if interval >= policy.maximumInterval {
			return policy.maximumInterval
		}
	}
	return min(interval, policy.maximumInterval)
}

// stepMaximumAttempts returns the attempts of RunModel's Dex retry policy.
func (policy resolvedModelRetryPolicy) stepMaximumAttempts() int32 {
	return modelCallMaximumAttempts + policy.transientRetries
}

type modelErrorKind uint8

const (
	modelErrorUnknown modelErrorKind = iota
	modelErrorTransient
	modelErrorPermanent
)

// classifyModelError returns what a model call's error says about calling
// again, and the provider's requested wait, or zero.
func classifyModelError(err error) (modelErrorKind, time.Duration) {
	var kind modelErrorKind
	var wait time.Duration
	walkErrorChain(err, func(current error) bool {
		if status, headers, ok := readHTTPStatus(current); ok {
			switch {
			case status == http.StatusRequestTimeout, status == http.StatusConflict,
				status == http.StatusTooManyRequests, status >= 500:
				kind, wait = modelErrorTransient, readRetryAfter(headers)
				return true
			case status >= 400:
				kind = modelErrorPermanent
				return true
			}
		}
		var netError net.Error
		if errors.As(current, &netError) || errors.Is(current, io.ErrUnexpectedEOF) ||
			errors.Is(current, syscall.ECONNRESET) || errors.Is(current, syscall.ECONNREFUSED) ||
			errors.Is(current, context.DeadlineExceeded) {
			kind = modelErrorTransient
			return true
		}
		return false
	})
	return kind, wait
}

// walkErrorChain visits err and every error it wraps, depth first, until visit returns true.
func walkErrorChain(err error, visit func(error) bool) bool {
	for err != nil {
		if visit(err) {
			return true
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				if walkErrorChain(inner, visit) {
					return true
				}
			}
			return false
		case interface{ Unwrap() error }:
			err = wrapped.Unwrap()
		default:
			return false
		}
	}
	return false
}

func readHTTPStatus(err error) (int, http.Header, bool) {
	value := reflect.ValueOf(err)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return 0, nil, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, nil, false
	}
	var headers http.Header
	status := 0
	if field := value.FieldByName("Response"); field.IsValid() && field.CanInterface() {
		if response, ok := field.Interface().(*http.Response); ok && response != nil {
			status, headers = response.StatusCode, response.Header
		}
	}
	if field := value.FieldByName("StatusCode"); field.IsValid() && field.CanInt() {
		status = int(field.Int())
	}
	// go-openai and the Volcengine Ark SDK, which eino-ext models use, name the field this way.
	if field := value.FieldByName("HTTPStatusCode"); !isHTTPStatus(status) && field.IsValid() && field.CanInt() {
		status = int(field.Int())
	}
	return status, headers, isHTTPStatus(status)
}

func isHTTPStatus(status int) bool {
	return status >= 100 && status <= 599
}

func readRetryAfter(headers http.Header) time.Duration {
	if milliseconds, err := strconv.ParseFloat(headers.Get("Retry-After-Ms"), 64); err == nil && milliseconds > 0 {
		return time.Duration(milliseconds * float64(time.Millisecond))
	}
	value := headers.Get("Retry-After")
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(time.Until(date), 0)
	}
	return 0
}

// errModelCallEndsTurn marks a model call error that fails the turn without another attempt.
type errModelCallEndsTurn struct{ cause error }

func (failure *errModelCallEndsTurn) Error() string { return failure.cause.Error() }

func (failure *errModelCallEndsTurn) Unwrap() error { return failure.cause }

// decideModelCallError returns the error a RunModel attempt reports for its model call's error:
// a RetryAfterError for a transient error, an *errModelCallEndsTurn for a permanent error, else
// the error itself.
func decideModelCallError(err error, attempt int32, policy resolvedModelRetryPolicy, random float64) error {
	kind, retryAfter := classifyModelError(err)
	switch kind {
	case modelErrorPermanent:
		return &errModelCallEndsTurn{cause: err}
	case modelErrorTransient:
		nominal := policy.nominalInterval(attempt)
		wait := max(nominal/2+time.Duration(float64(nominal/2)*random), retryAfter)
		seconds := max(math.Ceil(wait.Seconds()), 1)
		return dex.RetryAfter(time.Duration(min(seconds, math.MaxInt32))*time.Second, err)
	default:
		// RunModel ends the turn on its last attempt, for every error.
		return err
	}
}

func randomJitter() float64 { return rand.Float64() }

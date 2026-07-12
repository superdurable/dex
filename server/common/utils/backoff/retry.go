// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package backoff

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/clock"
)

type retryCountKeyType string

const retryCountKey = retryCountKeyType("retryCount")

// Operation is a retriable function that returns a plain error.
// Used as the callback for retry.Do.
type Operation func(ctx context.Context) error

// CategorizedOperation is a retriable function that returns a CategorizedError.
// Used as the callback for retry.DoCategorized; the retry loop automatically
// inspects CategorizedError.IsRetriable() to decide whether to retry.
type CategorizedOperation func(ctx context.Context) errors.CategorizedError

// IsRetryable is a predicate that decides whether a given error should be
// retried. Return true to retry, false to stop immediately.
type IsRetryable func(error) bool

// AlwaysRetry is a convenience IsRetryable that retries every error.
//
// Example:
//
//	backoff.NewRetry(
//	    backoff.WithRetryableError(backoff.AlwaysRetry),
//	)
func AlwaysRetry(_ error) bool { return true }

type retryOption func(*retry)

type retry struct {
	policy         *RetryPolicy
	isRetryable    IsRetryable
	throttlePolicy *RetryPolicy
	isThrottle     IsRetryable
	clock          clock.TimeSource
}

// NewRetry creates a retry executor with the given options.
//
// Without any options the executor uses a default RetryPolicy (50ms initial
// interval, 2s max interval, no total timeout, unlimited attempts) and treats
// all errors as non-retryable. At minimum you should provide a retry policy
// and either WithRetryableError or use DoCategorized (which checks
// CategorizedError.IsRetriable() automatically).
//
// Example — retry CategorizedErrors with a custom policy:
//
//	r := backoff.NewRetry(
//	    backoff.WithRetryPolicy(&backoff.RetryPolicy{
//	        InitialInterval: 100 * time.Millisecond,
//	        MaximumInterval: 30 * time.Second,
//	        TotalTimeout:    1 * time.Hour,
//	    }),
//	)
//	err := r.DoCategorized(ctx, func(ctx context.Context) errors.CategorizedError {
//	    return doWork(ctx)
//	})
//
// Example — retry all errors with the default policy:
//
//	r := backoff.NewRetry(
//	    backoff.WithRetryableError(backoff.AlwaysRetry),
//	)
//	err := r.Do(ctx, func(ctx context.Context) error {
//	    return doWork(ctx)
//	})
func NewRetry(opts ...retryOption) *retry {
	tr := &retry{
		policy: &RetryPolicy{
			InitialInterval: 50 * time.Millisecond,
			MaximumInterval: 2 * time.Second,
		},
		isRetryable: func(_ error) bool {
			return false
		},
		throttlePolicy: &RetryPolicy{
			InitialInterval: time.Second,
			MaximumInterval: 10 * time.Second,
		},
		isThrottle: func(err error) bool {
			return false
		},
		clock: clock.NewRealTimeSource(),
	}
	for _, opt := range opts {
		opt(tr)
	}
	return tr
}

// WithRetryPolicy sets the primary retry policy that controls delay intervals,
// maximum attempts, and total timeout.
func WithRetryPolicy(policy *RetryPolicy) retryOption {
	return func(tr *retry) {
		tr.policy = policy
	}
}

// WithThrottlePolicy sets a secondary retry policy used when the operation
// returns a throttle error (as identified by WithThrottleError). When a
// throttle error occurs, the delay is the maximum of the primary policy's
// delay and the throttle policy's delay.
func WithThrottlePolicy(throttlePolicy *RetryPolicy) retryOption {
	return func(tr *retry) {
		tr.throttlePolicy = throttlePolicy
	}
}

// WithRetryableError sets the predicate that determines whether an error
// returned by the Operation should be retried. Only used by Do; DoCategorized
// uses CategorizedError.IsRetriable() instead.
func WithRetryableError(isRetryable IsRetryable) retryOption {
	return func(tr *retry) {
		tr.isRetryable = isRetryable
	}
}

// WithThrottleError sets the predicate that identifies throttle/rate-limit
// errors. When matched, the throttle policy's backoff is applied on top of
// the primary backoff to give the downstream service more breathing room.
func WithThrottleError(isThrottle IsRetryable) retryOption {
	return func(tr *retry) {
		tr.isThrottle = isThrottle
	}
}

// WithClock overrides the time source (useful for deterministic testing).
func WithClock(clock clock.TimeSource) retryOption {
	return func(tr *retry) {
		tr.clock = clock
	}
}

// Do executes op and retries on failure according to the configured policy.
// Retries only if the IsRetryable predicate (set via WithRetryableError)
// returns true.
//
// When retries are exhausted, Do returns the second-to-last error if one
// exists (the final error is typically a timeout and less informative).
// The operation's context is augmented with a retry count retrievable via
// the retryCountKey context value.
func (tr *retry) Do(ctx context.Context, op Operation) error {
	var prevErr error
	var err error
	var next time.Duration

	r := newRetrier(tr.policy, tr.clock)
	t := newRetrier(tr.throttlePolicy, tr.clock)
	retryCount := 0
	for {
		prevErr = err

		ctx = context.WithValue(ctx, retryCountKey, retryCount)
		if err = op(ctx); err == nil {
			return nil
		}
		retryCount++

		if !tr.isRetryable(err) {
			// The returned error will be preferred to a previous one if one exists. That's because the
			// very last error is very likely a timeout error, and it's not useful for logging/troubleshooting
			if prevErr != nil {
				return prevErr
			}
			return err
		}

		if next = r.nextBackOff(); next == done {
			if prevErr != nil {
				return prevErr
			}
			return err
		}

		if tr.isThrottle(err) {
			throttleBackOff := t.nextBackOff()
			if throttleBackOff != done && throttleBackOff > next {
				next = throttleBackOff
			}
		}

		select {
		case <-ctx.Done():
			if prevErr != nil {
				return prevErr
			}
			return err
		case <-tr.clock.After(next):
		}
	}
}

// DoCategorized is like Do but works with CategorizedOperation and
// CategorizedError. Instead of relying on an IsRetryable predicate, it
// calls CategorizedError.IsRetriable() directly to decide whether to retry.
func (tr *retry) DoCategorized(ctx context.Context, op CategorizedOperation) errors.CategorizedError {
	var prevErr errors.CategorizedError
	var err errors.CategorizedError
	var next time.Duration

	r := newRetrier(tr.policy, tr.clock)
	t := newRetrier(tr.throttlePolicy, tr.clock)
	retryCount := 0
	for {
		prevErr = err

		ctx = context.WithValue(ctx, retryCountKey, retryCount)
		if err = op(ctx); err == nil {
			return nil
		}
		retryCount++

		if !err.IsRetriable() {
			if prevErr != nil {
				return prevErr
			}
			return err
		}

		if next = r.nextBackOff(); next == done {
			if prevErr != nil {
				return prevErr
			}
			return err
		}

		if tr.isThrottle != nil && tr.isThrottle(err) {
			throttleBackOff := t.nextBackOff()
			if throttleBackOff != done && throttleBackOff > next {
				next = throttleBackOff
			}
		}

		select {
		case <-ctx.Done():
			if prevErr != nil {
				return prevErr
			}
			return err
		case <-tr.clock.After(next):
		}
	}
}

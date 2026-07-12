// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package dex

import (
	"context"
	"math/rand"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Transient-error retry policy for RawClient RPCs: exponential backoff with
// jitter, 100ms initial interval, ×2 growth, capped at 5s per wait, up to ~1min
// total elapsed before giving up.
const (
	retryInitialInterval = 100 * time.Millisecond
	retryMaxInterval     = 5 * time.Second
	retryMaxElapsed      = time.Minute
	retryBackoffFactor   = 2.0

	// retryMinRemainingBudget is the minimum ctx time-to-deadline required to
	// attempt another retry. Below this, a retry has no realistic chance of
	// completing (especially for a long-poll RPC, whose own attempt can take
	// seconds) — return the last error immediately instead of burning the
	// remaining budget on a doomed call.
	retryMinRemainingBudget = 100 * time.Millisecond
)

// retryableCode reports whether a gRPC status code is a transient server error
// worth retrying. DeadlineExceeded is retryable, but callWithRetry aborts as
// soon as ctx is done — so a caller-deadline expiry (including a long-poll that
// blocked until the caller's ctx) returns promptly instead of looping.
func retryableCode(code codes.Code) bool {
	switch code {
	case codes.Unavailable, // server draining / not yet ready / connection refused
		codes.Internal,          // transient server-side infrastructure failure
		codes.DeadlineExceeded,  // per-call deadline / slow dependency
		codes.Aborted,           // optimistic-lock (CAS) conflict on the run row
		codes.ResourceExhausted: // backpressure / rate limit
		return true
	default:
		return false
	}
}

// callWithRetry invokes fn, retrying transient server errors (see retryableCode)
// with exponential backoff + jitter. It returns fn's result as soon as it
// succeeds or fails with a non-retryable error, and otherwise gives up on ctx
// cancellation, once retryMaxElapsed has passed, or once ctx's remaining time
// drops below retryMinRemainingBudget — returning the last result/error.
// Non-retryable errors (NotFound, InvalidArgument, …) are returned immediately,
// unwrapped, so callers can still inspect their gRPC code.
func callWithRetry[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	start := time.Now()
	interval := retryInitialInterval
	for {
		result, err := fn(ctx)
		if err == nil || !retryableCode(status.Code(err)) {
			return result, err
		}
		if time.Since(start) >= retryMaxElapsed {
			return result, err
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < retryMinRemainingBudget {
			return result, err
		}
		wait := interval + time.Duration(rand.Int63n(int64(interval)+1))
		if wait > retryMaxInterval {
			wait = retryMaxInterval
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(wait):
		}
		if interval < retryMaxInterval {
			interval = time.Duration(float64(interval) * retryBackoffFactor)
			if interval > retryMaxInterval {
				interval = retryMaxInterval
			}
		}
	}
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dex

import (
	"fmt"
	"time"
)

// RetryAfterError requests a delay before the next retry while preserving the
// current failure. Throw the value returned by RetryAfter from waitFor or
// execute. Dex schedules the next retry after the requested delay while
// keeping the wrapped failure as the reported Worker error.
type RetryAfterError struct {
	// After is the delay Dex waits before retrying the Step method.
	After time.Duration
	// Cause is the current attempt failure reported to Dex.
	Cause error
}

// Error returns the underlying attempt failure message.
func (retryAfter *RetryAfterError) Error() string {
	return retryAfter.Cause.Error()
}

// Unwrap returns the underlying attempt failure for errors.Is and errors.As.
func (retryAfter *RetryAfterError) Unwrap() error {
	return retryAfter.Cause
}

// RetryAfter creates a retry request while preserving the current attempt
// failure. after must be positive. cause is the Step method failure reported
// to Dex.
func RetryAfter(after time.Duration, cause error) *RetryAfterError {
	if after <= 0 {
		panic(fmt.Sprintf("RetryAfter: after must be positive, got %v", after))
	}
	if cause == nil {
		panic("RetryAfter: cause must not be nil")
	}
	return &RetryAfterError{After: after, Cause: cause}
}

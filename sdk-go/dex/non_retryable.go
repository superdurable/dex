// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

// NonRetryableError fails the current Step method attempt without further
// retries. Return the value from NonRetryable from WaitFor or Execute when a
// failure cannot succeed on a later attempt, such as rejected input or an
// invalid request that a provider reports as permanent.
//
// Dex skips the remaining attempts of the method's retry policy and applies
// the Step's failure policy at once. By default the Flow fails. A WaitFor
// configured to proceed on failure runs Execute, and an Execute configured to
// proceed to a recovery Step starts that Step. Dex reports Cause as the Worker
// error, so the recovery Step sees the Cause's type, message, and stack trace.
//
// The error may wrap or be wrapped by a RetryAfterError, fmt.Errorf with %w, or
// errors.Join; NonRetryableError wins and no retry runs. Dex reports the Cause
// of the marker it finds. Returned from an RPC handler, it has no effect,
// because RPC handlers never retry.
//
// It needs server-zd 0.1.38 or later. An older server ignores the request and
// retries the attempt under the retry policy.
//
// Since 1.45.17.
//
// Example:
//
//	func (step chargeStep) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
//		receipt, err := step.payments.Charge(ctx, order)
//		if errors.Is(err, payments.ErrCardRejected) {
//			return nil, dex.NonRetryable(err)
//		}
//		if err != nil {
//			return nil, err
//		}
//		return dex.GracefulComplete(receipt), nil
//	}
type NonRetryableError struct {
	// Cause is the current attempt failure reported to Dex. Since 1.45.17.
	Cause error
}

// Error returns the underlying attempt failure message. Since 1.45.17.
func (nonRetryable *NonRetryableError) Error() string {
	return nonRetryable.Cause.Error()
}

// Unwrap returns the underlying attempt failure for errors.Is and errors.As.
// Since 1.45.17.
func (nonRetryable *NonRetryableError) Unwrap() error {
	return nonRetryable.Cause
}

// NonRetryable marks cause as a permanent failure of the current Step method
// attempt. See NonRetryableError for the behavior and an example.
//
// Since 1.45.17.
//
// cause is the Step method failure reported to Dex. It must not be nil.
// NonRetryable returns the error to return from WaitFor or Execute. It panics
// when cause is nil.
func NonRetryable(cause error) *NonRetryableError {
	if cause == nil {
		panic("NonRetryable: cause must not be nil")
	}
	return &NonRetryableError{Cause: cause}
}

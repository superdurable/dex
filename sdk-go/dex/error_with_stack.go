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
	"io"
	"runtime/debug"
)

// ErrorWithStack wraps err with a stack captured at this call site.
//
// Use it when returning a plain Go error from waitFor or execute and you want
// Dex to record an origin stack. Plain errors are not Dex-owned, so capture is
// opt-in; without this wrapper Dex reports that no application stack was
// captured instead of inventing a Worker wrap-site stack.
//
// Prefer nesting ErrorWithStack inside RetryAfter so RetryAfter remains the
// protocol marker and the stack lives on the cause:
//
//	return nil, dex.RetryAfter(30*time.Second, dex.ErrorWithStack(err))
//
// Unwrap returns the original err. Error returns the original message. Formatting
// with %+v includes the captured stack. A nil err returns nil.
func ErrorWithStack(err error) error {
	if err == nil {
		return nil
	}
	return &errorWithStack{
		cause: err,
		stack: debug.Stack(),
	}
}

type errorWithStack struct {
	cause error
	stack []byte
}

// Error returns the wrapped failure message unchanged.
func (err *errorWithStack) Error() string {
	return err.cause.Error()
}

// Unwrap returns the original failure for errors.Is and errors.As.
func (err *errorWithStack) Unwrap() error {
	return err.cause
}

// Format writes the message for %s/%v and appends the origin stack for %+v.
func (err *errorWithStack) Format(state fmt.State, verb rune) {
	switch verb {
	case 'v':
		if state.Flag('+') {
			fmt.Fprintf(state, "%s\n%s", err.cause.Error(), err.stack)
			return
		}
		fallthrough
	case 's':
		_, _ = io.WriteString(state, err.Error())
	case 'q':
		fmt.Fprintf(state, "%q", err.Error())
	}
}

func (err *errorWithStack) stackTrace() string {
	return fmt.Sprintf("%s\n%s", err.cause.Error(), err.stack)
}

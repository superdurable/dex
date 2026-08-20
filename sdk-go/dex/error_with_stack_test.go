// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dex

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorWithStackUnwrapAndError(t *testing.T) {
	cause := errors.New("payment failed")
	wrapped := ErrorWithStack(cause)
	if wrapped == nil {
		t.Fatal("expected wrapped error")
	}
	if wrapped.Error() != "payment failed" {
		t.Fatalf("Error() = %q", wrapped.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("expected errors.Is to match cause")
	}
	if unwrapped := errors.Unwrap(wrapped); unwrapped != cause {
		t.Fatalf("Unwrap() = %v, want cause", unwrapped)
	}
}

func TestErrorWithStackPlusVIncludesOriginStack(t *testing.T) {
	rendered := fmt.Sprintf("%+v", originStackMarker())
	if !strings.Contains(rendered, "payment failed") {
		t.Fatalf("missing message: %q", rendered)
	}
	if !strings.Contains(rendered, "originStackMarker") {
		t.Fatalf("missing origin frame: %q", rendered)
	}
}

func TestErrorWithStackNil(t *testing.T) {
	if ErrorWithStack(nil) != nil {
		t.Fatal("expected nil")
	}
}

func originStackMarker() error {
	return ErrorWithStack(errors.New("payment failed"))
}

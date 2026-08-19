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
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/status"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any) {}
func (testLogger) Info(string, ...any)  {}
func (testLogger) Warn(string, ...any)  {}
func (testLogger) Error(string, ...any) {}

func TestFinishWorkerCallAttachesStackTraceAndRetryAfter(t *testing.T) {
	cause := errors.New("boom")
	err := finishWorkerCall(testLogger{}, nil, RetryAfter(7*time.Second, cause))
	if err == nil {
		t.Fatal("expected error")
	}

	rpcStatus, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status, got %T", err)
	}
	details := rpcStatus.Details()
	if len(details) != 1 {
		t.Fatalf("expected one detail, got %d", len(details))
	}
	workerError, ok := details[0].(*dexpb.WorkerErrorResponse)
	if !ok {
		t.Fatalf("expected WorkerErrorResponse, got %T", details[0])
	}
	if workerError.GetDetail() != "boom" {
		t.Fatalf("detail = %q, want boom", workerError.GetDetail())
	}
	if workerError.GetRetryAfterSeconds() != 7 {
		t.Fatalf("retry after = %d, want 7", workerError.GetRetryAfterSeconds())
	}
	if workerError.GetStackTrace() == "" {
		t.Fatal("expected stack trace")
	}
	if !strings.Contains(workerError.GetStackTrace(), "boom") {
		t.Fatalf("stack trace missing cause: %q", workerError.GetStackTrace())
	}
}

func TestTruncateStackTrace(t *testing.T) {
	large := strings.Repeat("x", maxWorkerStackTraceBytes+100)
	truncated := truncateStackTrace(large)
	if len(truncated) > maxWorkerStackTraceBytes+len(stackTraceTruncationMarker)+10 {
		t.Fatalf("truncated length %d exceeds limit", len(truncated))
	}
	if !strings.Contains(truncated, string(stackTraceTruncationMarker)) {
		t.Fatal("expected truncation marker")
	}
}

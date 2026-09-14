// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
	assertMissingApplicationStack(t, workerError.GetStackTrace())
}

func TestFinishWorkerCallPlainErrorSkipsWrapSiteStack(t *testing.T) {
	err := finishWorkerCall(testLogger{}, nil, errors.New("plain boom"))
	if err == nil {
		t.Fatal("expected error")
	}
	rpcStatus, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status, got %T", err)
	}
	workerError, ok := rpcStatus.Details()[0].(*dexpb.WorkerErrorResponse)
	if !ok {
		t.Fatalf("expected WorkerErrorResponse, got %T", rpcStatus.Details()[0])
	}
	if workerError.GetDetail() != "plain boom" {
		t.Fatalf("detail = %q, want plain boom", workerError.GetDetail())
	}
	assertMissingApplicationStack(t, workerError.GetStackTrace())
}

func TestFinishWorkerCallPrefersOriginStack(t *testing.T) {
	err := finishWorkerCall(testLogger{}, nil, originFailureWithStack())
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
	stackTrace := workerError.GetStackTrace()
	if !strings.Contains(stackTrace, "origin boom") {
		t.Fatalf("stack missing detail: %q", stackTrace)
	}
	if !strings.Contains(stackTrace, "originFailureWithStack") {
		t.Fatalf("stack missing origin frame: %q", stackTrace)
	}
	if strings.Contains(stackTrace, "finishWorkerCall") {
		t.Fatalf("expected origin stack, not wrap-site: %q", stackTrace)
	}
}

func TestFinishWorkerCallPrefersOriginStackInsideRetryAfter(t *testing.T) {
	err := finishWorkerCall(
		testLogger{},
		nil,
		RetryAfter(3*time.Second, originFailureWithStack()),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	rpcStatus, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status, got %T", err)
	}
	workerError, ok := rpcStatus.Details()[0].(*dexpb.WorkerErrorResponse)
	if !ok {
		t.Fatalf("expected WorkerErrorResponse, got %T", rpcStatus.Details()[0])
	}
	if workerError.GetRetryAfterSeconds() != 3 {
		t.Fatalf("retry after = %d, want 3", workerError.GetRetryAfterSeconds())
	}
	if !strings.Contains(workerError.GetStackTrace(), "originFailureWithStack") {
		t.Fatalf("stack missing origin frame: %q", workerError.GetStackTrace())
	}
	if strings.Contains(workerError.GetStackTrace(), "finishWorkerCall") {
		t.Fatalf("expected origin stack, not wrap-site: %q", workerError.GetStackTrace())
	}
}

func TestTruncateStackTrace(t *testing.T) {
	large := strings.Repeat("x", maxWorkerStackTraceBytes+100)
	truncated := truncateWorkerFailureField(large, maxWorkerStackTraceBytes, stackTraceTruncationMarker)
	if len(truncated) > maxWorkerStackTraceBytes {
		t.Fatalf("truncated length %d exceeds limit", len(truncated))
	}
	if !strings.Contains(truncated, string(stackTraceTruncationMarker)) {
		t.Fatal("expected truncation marker")
	}
}

func TestFinishWorkerCallBoundsLargeWorkerFailureFields(t *testing.T) {
	large := strings.Repeat("世界", maxWorkerStackTraceBytes)
	err := finishWorkerCall(testLogger{}, nil, ErrorWithStack(errors.New(large)))
	if err == nil {
		t.Fatal("expected error")
	}

	rpcStatus, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status, got %T", err)
	}
	workerError, ok := rpcStatus.Details()[0].(*dexpb.WorkerErrorResponse)
	if !ok {
		t.Fatalf("expected WorkerErrorResponse, got %T", rpcStatus.Details()[0])
	}
	if len(workerError.GetDetail()) > maxWorkerErrorDetailBytes {
		t.Fatalf("detail length %d exceeds limit", len(workerError.GetDetail()))
	}
	if !strings.Contains(workerError.GetDetail(), string(errorDetailTruncationMarker)) {
		t.Fatal("expected detail truncation marker")
	}
	if len(workerError.GetStackTrace()) > maxWorkerStackTraceBytes {
		t.Fatalf("stack trace length %d exceeds limit", len(workerError.GetStackTrace()))
	}
	if !strings.Contains(workerError.GetStackTrace(), string(stackTraceTruncationMarker)) {
		t.Fatal("expected stack trace truncation marker")
	}
	if len(workerError.GetErrorType()) > maxWorkerErrorTypeBytes {
		t.Fatalf("error type length %d exceeds limit", len(workerError.GetErrorType()))
	}
	if rpcStatus.Message() != workerError.GetDetail() {
		t.Fatalf("status message %q does not match bounded detail", rpcStatus.Message())
	}
	if size := proto.Size(rpcStatus.Proto()); size >= 7*1024 {
		t.Fatalf("encoded worker status size %d exceeds 7 KiB budget", size)
	}
	if strings.Contains(workerError.GetDetail(), "\ufffd") || strings.Contains(workerError.GetStackTrace(), "\ufffd") {
		t.Fatal("expected UTF-8 boundaries to remain intact")
	}
}

func TestWorkerStatusErrorBoundsLargeErrorTypeAtUTF8Boundary(t *testing.T) {
	largeType := strings.Repeat("世界", maxWorkerErrorTypeBytes)
	err := workerStatusError(
		testLogger{},
		codes.Unknown,
		errors.New("boom"),
		largeType,
		"stack",
		nil,
		"boom",
	)
	rpcStatus, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status, got %T", err)
	}
	workerError, ok := rpcStatus.Details()[0].(*dexpb.WorkerErrorResponse)
	if !ok {
		t.Fatalf("expected WorkerErrorResponse, got %T", rpcStatus.Details()[0])
	}
	if len(workerError.GetErrorType()) > maxWorkerErrorTypeBytes {
		t.Fatalf("error type length %d exceeds limit", len(workerError.GetErrorType()))
	}
	if !strings.Contains(workerError.GetErrorType(), string(errorTypeTruncationMarker)) {
		t.Fatal("expected error type truncation marker")
	}
	if strings.Contains(workerError.GetErrorType(), "\ufffd") {
		t.Fatal("expected UTF-8 boundary to remain intact")
	}
}

func originFailureWithStack() error {
	return ErrorWithStack(errors.New("origin boom"))
}

func assertMissingApplicationStack(t *testing.T, stackTrace string) {
	t.Helper()
	if !strings.Contains(stackTrace, "dex.ErrorWithStack") {
		t.Fatalf("stack missing ErrorWithStack hint: %q", stackTrace)
	}
	if strings.Contains(stackTrace, "finishWorkerCall") {
		t.Fatalf("plain error must not include wrap-site stack: %q", stackTrace)
	}
	if strings.Contains(stackTrace, "stackTraceFromError") {
		t.Fatalf("plain error must not include wrap-site stack: %q", stackTrace)
	}
}

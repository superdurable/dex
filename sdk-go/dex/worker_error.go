// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workerFailure struct {
	code  codes.Code
	cause error
}

func newWorkerFailure(code codes.Code, cause error) error {
	return &workerFailure{code: code, cause: cause}
}

func (failure *workerFailure) Error() string {
	return failure.cause.Error()
}

func (failure *workerFailure) Unwrap() error {
	return failure.cause
}

func finishWorkerCall(logger Logger, recovered any, err error) error {
	if recovered != nil {
		logger.Error(
			"Worker handler panic",
			"panic", recovered,
			"stack", string(debug.Stack()),
		)
		return workerStatusError(
			logger,
			codes.Internal,
			fmt.Errorf("panic: %v", recovered),
			fmt.Sprintf("%T", recovered),
		)
	}
	if err == nil {
		return nil
	}

	code, cause := classifyWorkerError(err)
	detail := cause.Error()
	if rpcStatus, ok := status.FromError(cause); ok {
		detail = rpcStatus.Message()
	}
	return workerStatusError(logger, code, cause, fmt.Sprintf("%T", cause), detail)
}

func classifyWorkerError(err error) (codes.Code, error) {
	var failure *workerFailure
	if errors.As(err, &failure) {
		return failure.code, failure.cause
	}
	if errors.Is(err, context.Canceled) {
		return codes.Canceled, err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codes.DeadlineExceeded, err
	}
	if rpcStatus, ok := status.FromError(err); ok {
		return rpcStatus.Code(), err
	}
	return codes.Unknown, err
}

func workerStatusError(
	logger Logger,
	code codes.Code,
	cause error,
	errorType string,
	details ...string,
) error {
	detail := cause.Error()
	if len(details) > 0 {
		detail = details[0]
	}
	rpcStatus := status.New(code, detail)
	withDetails, err := rpcStatus.WithDetails(&dexpb.WorkerErrorResponse{
		Detail:    detail,
		ErrorType: errorType,
	})
	if err != nil {
		logger.Error("attach Worker error details", "error", err)
		return rpcStatus.Err()
	}
	return withDetails.Err()
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxWorkerErrorDetailBytes = 1024
	maxWorkerErrorTypeBytes   = 256
	maxWorkerStackTraceBytes  = 4 * 1024
)

var (
	errorDetailTruncationMarker = []byte("\n... error detail truncated by Dex Go SDK ...")
	errorTypeTruncationMarker   = []byte("\n... error type truncated by Dex Go SDK ...")
	stackTraceTruncationMarker  = []byte("\n... stack trace truncated by Dex Go SDK ...")
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
		panicErr := fmt.Errorf("panic: %v", recovered)
		return workerStatusError(
			logger,
			codes.Internal,
			panicErr,
			fmt.Sprintf("%T", recovered),
			stackTraceFromPanic(recovered),
			nil,
			false,
			panicErr.Error(),
		)
	}
	if err == nil {
		return nil
	}

	reported, retryAfter, isNonRetryable := reportedWorkerFailure(err)
	code, classified := classifyWorkerError(reported)
	detail := classified.Error()
	if rpcStatus, ok := status.FromError(classified); ok {
		detail = rpcStatus.Message()
	}
	return workerStatusError(
		logger,
		code,
		classified,
		workerErrorType(reported),
		stackTraceFromError(classified),
		retryAfter,
		isNonRetryable,
		detail,
	)
}

// sdkGeneratedWorkerError marks SDK-written failures, such as blob hydration and decryption
// errors, whose detail stays plaintext. The server matches hydration error text.
type sdkGeneratedWorkerError struct {
	cause error
}

func markSDKGeneratedWorkerError(err error) error {
	if err == nil {
		return nil
	}
	return &sdkGeneratedWorkerError{cause: err}
}

func (generated *sdkGeneratedWorkerError) Error() string {
	return generated.cause.Error()
}

func (generated *sdkGeneratedWorkerError) Unwrap() error {
	return generated.cause
}

// workerErrorTextEncryptionFailure prefixes the category that replaces error text that could not be encrypted.
const workerErrorTextEncryptionFailure = "dex: the Worker could not encrypt this error text: "

// encodeWorkerErrorText encrypts detail and stack_trace unless the SDK wrote them.
// When encryption fails, it sends a generic text instead of the original.
func encodeWorkerErrorText(
	ctx context.Context,
	logger Logger,
	encryption *payloadEncryption,
	payloadContext PayloadContext,
	cause error,
	workerError *dexpb.WorkerErrorResponse,
) {
	var generated *sdkGeneratedWorkerError
	if errors.As(cause, &generated) {
		return
	}
	detail, detailErr := encryption.encodeErrorText(ctx, payloadContext, workerError.Detail)
	stackTrace, stackTraceErr := encryption.encodeErrorText(ctx, payloadContext, workerError.StackTrace)
	if encodeErr := errors.Join(detailErr, stackTraceErr); encodeErr != nil {
		logger.Error("encrypt Worker error text", "flowID", payloadContext.FlowID, "error", encodeErr)
		workerError.Detail = workerErrorTextEncryptionFailure + payloadFailureSummary(encodeErr, "encrypt_failed")
		workerError.StackTrace = ""
		return
	}
	workerError.Detail = detail
	workerError.StackTrace = stackTrace
}

// workerTaskPayloadContext returns the Flow that a polled task belongs to.
func workerTaskPayloadContext(task *dexpb.PollWorkerTask) PayloadContext {
	switch request := task.GetRequest().(type) {
	case *dexpb.PollWorkerTask_WaitFor:
		return PayloadContext{FlowType: request.WaitFor.GetFlowType(), FlowID: request.WaitFor.GetContext().GetFlowId()}
	case *dexpb.PollWorkerTask_Execute:
		return PayloadContext{FlowType: request.Execute.GetFlowType(), FlowID: request.Execute.GetContext().GetFlowId()}
	case *dexpb.PollWorkerTask_Rpc:
		return PayloadContext{FlowType: request.Rpc.GetFlowType(), FlowID: request.Rpc.GetContext().GetFlowId()}
	default:
		return PayloadContext{}
	}
}

func workerErrorType(reported error) string {
	var generated *sdkGeneratedWorkerError
	if errors.As(reported, &generated) {
		reported = generated.cause
	}
	var failure *workerFailure
	if errors.As(reported, &failure) {
		reported = failure.cause
	}
	for {
		withStack, ok := reported.(*errorWithStack)
		if !ok {
			return fmt.Sprintf("%T", reported)
		}
		reported = withStack.cause
	}
}

// reportedWorkerFailure returns the failure to report without the RetryAfter and NonRetryable
// wrappers, the RetryAfter request or nil, and whether any wrapper in err marks it non-retryable.
func reportedWorkerFailure(err error) (error, *RetryAfterError, bool) {
	reported := err
	var retryAfter *RetryAfterError
	if errors.As(err, &retryAfter) {
		reported = retryAfter.Cause
	}
	var nonRetryable *NonRetryableError
	if errors.As(reported, &nonRetryable) {
		return nonRetryable.Cause, retryAfter, true
	}
	return reported, retryAfter, errors.As(err, &nonRetryable)
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
	reported error,
	errorType string,
	stackTrace string,
	retryAfter *RetryAfterError,
	isNonRetryable bool,
	detail string,
) error {
	workerError := &dexpb.WorkerErrorResponse{
		Detail:       truncateWorkerFailureField(detail, maxWorkerErrorDetailBytes, errorDetailTruncationMarker),
		ErrorType:    truncateWorkerFailureField(errorType, maxWorkerErrorTypeBytes, errorTypeTruncationMarker),
		StackTrace:   truncateWorkerFailureField(stackTrace, maxWorkerStackTraceBytes, stackTraceTruncationMarker),
		GrpcStatus:   int32(code),
		NonRetryable: isNonRetryable,
	}
	if retryAfter != nil {
		workerError.RetryAfterSeconds = int32(retryAfter.After / time.Second)
	}
	rpcStatus := status.New(code, workerError.GetDetail())
	withDetails, err := rpcStatus.WithDetails(workerError)
	if err != nil {
		logger.Error("attach Worker error details", "error", err)
		return rpcStatus.Err()
	}
	return withDetails.Err()
}

func stackTraceFromError(cause error) string {
	var withStack *errorWithStack
	if errors.As(cause, &withStack) {
		return withStack.stackTrace()
	}
	// Plain errors have no origin stack. Do not capture the Worker wrap site;
	// that frame is not useful for diagnosing application failures.
	return "no application stack captured; wrap with dex.ErrorWithStack(err) at the failure site"
}

func stackTraceFromPanic(recovered any) string {
	return fmt.Sprintf("panic: %v\n%s", recovered, debug.Stack())
}

func truncateWorkerFailureField(value string, maximumBytes int, truncationMarker []byte) string {
	encoded := []byte(value)
	if len(encoded) <= maximumBytes {
		return value
	}
	prefixLength := maximumBytes - len(truncationMarker)
	for prefixLength > 0 && encoded[prefixLength]&0xc0 == 0x80 {
		prefixLength--
	}
	return string(encoded[:prefixLength]) + string(truncationMarker)
}

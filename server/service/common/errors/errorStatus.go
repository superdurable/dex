// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package errors

import (
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorAndStatus is an API-layer failure carrying ErrorSubStatus and a gRPC code.
type ErrorAndStatus struct {
	Code  codes.Code
	Error *dexpb.ErrorResponse
}

// NewErrorAndStatus builds an ErrorAndStatus without worker-origin details.
func NewErrorAndStatus(code codes.Code, subStatus dexpb.ErrorSubStatus, details string) *ErrorAndStatus {
	return &ErrorAndStatus{
		Code: code,
		Error: &dexpb.ErrorResponse{
			SubStatus: subStatus,
			Detail:    details,
		},
	}
}

// NewErrorAndStatusWithWorkerError attaches original WorkerService failure fields.
func NewErrorAndStatusWithWorkerError(
	code codes.Code, subStatus dexpb.ErrorSubStatus, details string,
	originalWorkerDetails string, originalWorkerErrType string, originalWorkerStatus int32,
) *ErrorAndStatus {
	return &ErrorAndStatus{
		Code: code,
		Error: &dexpb.ErrorResponse{
			SubStatus:                 subStatus,
			Detail:                    details,
			OriginalWorkerErrorDetail: originalWorkerDetails,
			OriginalWorkerErrorType:   originalWorkerErrType,
			OriginalWorkerErrorStatus: originalWorkerStatus,
		},
	}
}

// ToGRPCError converts ErrorAndStatus into a gRPC status with ErrorResponse details.
func (e *ErrorAndStatus) ToGRPCError() error {
	if e == nil {
		return nil
	}
	st := status.New(e.Code, e.Error.GetDetail())
	if e.Error != nil {
		withDetails, err := st.WithDetails(e.Error)
		if err == nil {
			return withDetails.Err()
		}
	}
	return st.Err()
}

// InvalidArgument is a convenience for bad client/worker input.
func InvalidArgument(subStatus dexpb.ErrorSubStatus, details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.InvalidArgument, subStatus, details)
}

// NotFound is a convenience for missing flows/runs.
func NotFound(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.NotFound, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS, details)
}

// AlreadyExists is a convenience for duplicate flow starts.
func AlreadyExists(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.AlreadyExists, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED, details)
}

// AbortedLockFailure is returned when RPC attribute lock acquisition fails.
func AbortedLockFailure(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.Aborted, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, details)
}

// WorkerAPIFailure maps a WorkerService gRPC failure to FailedPrecondition.
// OriginalWorker* preserves the worker's code, detail, and type.
func WorkerAPIFailure(err error) (*ErrorAndStatus, bool) {
	grpcStatus, ok := status.FromError(err)
	if !ok {
		return nil, false
	}
	workerDetail := ""
	workerType := ""
	for _, detail := range grpcStatus.Details() {
		workerError, ok := detail.(*dexpb.WorkerErrorResponse)
		if !ok {
			continue
		}
		workerDetail = workerError.GetDetail()
		workerType = workerError.GetErrorType()
	}
	return NewErrorAndStatusWithWorkerError(
		codes.FailedPrecondition,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		grpcStatus.Message(),
		workerDetail,
		workerType,
		int32(grpcStatus.Code()),
	), true
}

// DeadlineExceededLongPoll is returned when a wait RPC hits its effective deadline.
func DeadlineExceededLongPoll(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.DeadlineExceeded, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT, details)
}

// Internal is a convenience for unexpected failures.
func Internal(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.Internal, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED, details)
}

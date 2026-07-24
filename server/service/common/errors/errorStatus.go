// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package errors

import (
	"github.com/superdurable/iwf/gen/iwfpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorAndStatus is an API-layer failure carrying ErrorSubStatus and a gRPC code.
type ErrorAndStatus struct {
	Code  codes.Code
	Error *iwfpb.ErrorResponse
}

// NewErrorAndStatus builds an ErrorAndStatus without worker-origin details.
func NewErrorAndStatus(code codes.Code, subStatus iwfpb.ErrorSubStatus, details string) *ErrorAndStatus {
	return &ErrorAndStatus{
		Code: code,
		Error: &iwfpb.ErrorResponse{
			SubStatus: subStatus,
			Detail:    details,
		},
	}
}

// NewErrorAndStatusWithWorkerError attaches original WorkerService failure fields.
func NewErrorAndStatusWithWorkerError(
	code codes.Code, subStatus iwfpb.ErrorSubStatus, details string,
	originalWorkerDetails string, originalWorkerErrType string, originalWorkerStatus int32,
) *ErrorAndStatus {
	return &ErrorAndStatus{
		Code: code,
		Error: &iwfpb.ErrorResponse{
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
func InvalidArgument(subStatus iwfpb.ErrorSubStatus, details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.InvalidArgument, subStatus, details)
}

// NotFound is a convenience for missing flows/runs.
func NotFound(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.NotFound, iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS, details)
}

// AlreadyExists is a convenience for duplicate flow starts.
func AlreadyExists(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.AlreadyExists, iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED, details)
}

// AbortedLockFailure is returned when RPC attribute lock acquisition fails.
func AbortedLockFailure(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.Aborted, iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, details)
}

// DeadlineExceededLongPoll is returned when a wait RPC hits its effective deadline.
func DeadlineExceededLongPoll(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.DeadlineExceeded, iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT, details)
}

// Internal is a convenience for unexpected failures.
func Internal(details string) *ErrorAndStatus {
	return NewErrorAndStatus(codes.Internal, iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED, details)
}

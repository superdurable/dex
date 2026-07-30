// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errInvalidInvocationContext = errors.New("dex: invalid invocation context")
	errPhaseNotImplemented      = errors.New("dex: operation is not implemented")
)

type Error struct {
	Code                codes.Code
	SubStatus           ErrorSubStatus
	Detail              string
	OriginalWorkerError *WorkerError
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Detail == "" {
		return e.Code.String()
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

type WorkerError struct {
	Code   codes.Code
	Type   string
	Detail string
}

type ErrorSubStatus uint8

const (
	ErrorUncategorized ErrorSubStatus = iota + 1
	ErrorFlowAlreadyStarted
	ErrorFlowNotFound
	ErrorWorkerAPI
	ErrorLongPollTimeout
)

func convertRPCError(err error) error {
	if err == nil {
		return nil
	}
	rpcStatus, ok := status.FromError(err)
	if !ok {
		return err
	}
	dexError := &Error{
		Code:      rpcStatus.Code(),
		SubStatus: ErrorUncategorized,
		Detail:    rpcStatus.Message(),
	}
	for _, detail := range rpcStatus.Details() {
		response, isDexError := detail.(*dexpb.ErrorResponse)
		if !isDexError {
			continue
		}
		dexError.SubStatus = mapErrorSubStatus(response.SubStatus)
		if response.Detail != "" {
			dexError.Detail = response.Detail
		}
		if response.OriginalWorkerErrorDetail != "" ||
			response.OriginalWorkerErrorType != "" ||
			response.OriginalWorkerErrorStatus != 0 {
			dexError.OriginalWorkerError = &WorkerError{
				Code:   codes.Code(response.OriginalWorkerErrorStatus),
				Type:   response.OriginalWorkerErrorType,
				Detail: response.OriginalWorkerErrorDetail,
			}
		}
		break
	}
	return dexError
}

func mapErrorSubStatus(subStatus dexpb.ErrorSubStatus) ErrorSubStatus {
	switch subStatus {
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED:
		return ErrorFlowAlreadyStarted
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS:
		return ErrorFlowNotFound
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR:
		return ErrorWorkerAPI
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT:
		return ErrorLongPollTimeout
	default:
		return ErrorUncategorized
	}
}

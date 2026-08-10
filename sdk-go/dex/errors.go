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
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errInvalidInvocationContext = errors.New("dex: invalid invocation context")
)

// AttributeNotFoundError reports a missing invocation attribute.
type AttributeNotFoundError struct {
	AttributeName string
	Instance      string
}

// FlowDefinitionError reports an invalid or unregistered Flow definition.
type FlowDefinitionError struct {
	FlowType   string
	Definition string
	Err        error
}

func (e *FlowDefinitionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := "dex: flow definition"
	if e.FlowType != "" {
		prefix = fmt.Sprintf("dex: flow %q definition", e.FlowType)
	}
	if e.Definition != "" {
		prefix += " " + e.Definition
	}
	if e.Err == nil {
		return prefix
	}
	return prefix + ": " + e.Err.Error()
}

func (e *FlowDefinitionError) Unwrap() error {
	return e.Err
}

// InvalidStepResultError reports an invalid Worker handler result.
type InvalidStepResultError struct {
	FlowType string
	StepType string
	Method   string
	Err      error
}

func (e *InvalidStepResultError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := fmt.Sprintf("dex: flow %q", e.FlowType)
	if e.StepType != "" {
		prefix += fmt.Sprintf(" step %q", e.StepType)
	}
	if e.Method != "" {
		prefix += " " + e.Method
	}
	if e.Err == nil {
		return prefix + " returned an invalid result"
	}
	return prefix + ": " + e.Err.Error()
}

func (e *InvalidStepResultError) Unwrap() error {
	return e.Err
}

// ValueMappingError reports a Dex value conversion failure.
type ValueMappingError struct {
	Operation string
	Err       error
}

func (e *ValueMappingError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := "dex: map value"
	if e.Operation != "" {
		prefix = "dex: " + e.Operation + " value"
	}
	if e.Err == nil {
		return prefix
	}
	return prefix + ": " + e.Err.Error()
}

func (e *ValueMappingError) Unwrap() error {
	return e.Err
}

func (e *AttributeNotFoundError) Error() string {
	if e.Instance == "" {
		return fmt.Sprintf("dex: attribute %q not found", e.AttributeName)
	}
	return fmt.Sprintf(
		"dex: attribute map %q instance %q not found",
		e.AttributeName,
		e.Instance,
	)
}

// ServiceError reports a FlowService failure.
type ServiceError struct {
	Op        string
	FlowID    string
	Code      codes.Code
	SubStatus ErrorSubStatus
	Detail    string
	cause     error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := "dex"
	if e.Op != "" {
		prefix += ": " + e.Op
	}
	if e.FlowID != "" {
		prefix += fmt.Sprintf(" flow %q", e.FlowID)
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", prefix, e.Code)
	}
	return fmt.Sprintf("%s: %s: %s", prefix, e.Code, e.Detail)
}

func (e *ServiceError) Unwrap() error {
	return e.cause
}

// FlowAlreadyStartedError reports a duplicate Flow start.
type FlowAlreadyStartedError struct {
	*ServiceError
}

func (e *FlowAlreadyStartedError) Unwrap() error {
	return e.ServiceError
}

// FlowNotFoundError reports a missing Flow for an existing-Flow operation.
type FlowNotFoundError struct {
	*ServiceError
}

func (e *FlowNotFoundError) Unwrap() error {
	return e.ServiceError
}

// FlowNotActiveError reports a missing active Flow.
type FlowNotActiveError struct {
	*ServiceError
}

func (e *FlowNotActiveError) Unwrap() error {
	return e.ServiceError
}

// WorkerInvocationError reports a WorkerService or user-handler failure.
type WorkerInvocationError struct {
	*ServiceError
	Worker *WorkerError
}

func (e *WorkerInvocationError) Unwrap() error {
	return e.ServiceError
}

// RPCLockConflictError reports concurrent RPC lock contention.
type RPCLockConflictError struct {
	*ServiceError
}

func (e *RPCLockConflictError) Unwrap() error {
	return e.ServiceError
}

// LongPollTimeoutError reports an expired server long poll.
type LongPollTimeoutError struct {
	*ServiceError
}

func (e *LongPollTimeoutError) Unwrap() error {
	return e.ServiceError
}

// FlowUncompletedError reports a Flow that closed without completing.
type FlowUncompletedError struct {
	FlowID       string
	RunID        string
	Status       FlowStatus
	ErrorType    FlowErrorType
	ErrorMessage string
	Completions  []StepCompletion
}

func (e *FlowUncompletedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := fmt.Sprintf("dex: flow %q did not complete: %s", e.FlowID, e.Status)
	if e.ErrorMessage == "" {
		return prefix
	}
	return prefix + ": " + e.ErrorMessage
}

// WorkerError preserves the original WorkerService failure.
type WorkerError struct {
	Code   codes.Code
	Type   string
	Detail string
}

// ErrorSubStatus is Dex's diagnostic service error classification.
type ErrorSubStatus uint8

const (
	ErrorSubStatusUncategorized ErrorSubStatus = iota + 1
	ErrorSubStatusFlowAlreadyStarted
	ErrorSubStatusFlowNotFound
	ErrorSubStatusWorkerAPI
	ErrorSubStatusLongPollTimeout
)

type flowTargetRequirement uint8

const (
	flowTargetNone flowTargetRequirement = iota
	flowTargetExisting
	flowTargetActive
)

func translateRPCError(
	err error,
	op string,
	flowID string,
	requirement flowTargetRequirement,
) error {
	if err == nil {
		return nil
	}
	rpcStatus, ok := status.FromError(err)
	if !ok {
		return err
	}
	serviceError := &ServiceError{
		Op:        op,
		FlowID:    flowID,
		Code:      rpcStatus.Code(),
		SubStatus: ErrorSubStatusUncategorized,
		Detail:    rpcStatus.Message(),
		cause:     err,
	}
	var response *dexpb.ErrorResponse
	for _, detail := range rpcStatus.Details() {
		if malformed, isMalformed := detail.(error); isMalformed {
			serviceError.Detail = fmt.Sprintf(
				"Dex returned malformed error details: %v",
				malformed,
			)
			return serviceError
		}
		var isDexError bool
		response, isDexError = detail.(*dexpb.ErrorResponse)
		if isDexError {
			break
		}
	}
	if response == nil {
		return serviceError
	}
	serviceError.SubStatus = mapErrorSubStatus(response.SubStatus)
	if response.Detail != "" {
		serviceError.Detail = response.Detail
	}
	switch serviceError.SubStatus {
	case ErrorSubStatusFlowAlreadyStarted:
		return &FlowAlreadyStartedError{ServiceError: serviceError}
	case ErrorSubStatusFlowNotFound:
		switch requirement {
		case flowTargetExisting:
			return &FlowNotFoundError{ServiceError: serviceError}
		case flowTargetActive:
			return &FlowNotActiveError{ServiceError: serviceError}
		default:
			return serviceError
		}
	case ErrorSubStatusWorkerAPI:
		if serviceError.Code == codes.Aborted {
			return &RPCLockConflictError{ServiceError: serviceError}
		}
		return &WorkerInvocationError{
			ServiceError: serviceError,
			Worker:       mapWorkerError(response),
		}
	case ErrorSubStatusLongPollTimeout:
		return &LongPollTimeoutError{ServiceError: serviceError}
	default:
		return serviceError
	}
}

func mapWorkerError(response *dexpb.ErrorResponse) *WorkerError {
	if response.OriginalWorkerErrorDetail == "" &&
		response.OriginalWorkerErrorType == "" &&
		response.OriginalWorkerErrorStatus == 0 {
		return nil
	}
	return &WorkerError{
		Code:   codes.Code(response.OriginalWorkerErrorStatus),
		Type:   response.OriginalWorkerErrorType,
		Detail: response.OriginalWorkerErrorDetail,
	}
}

func mapErrorSubStatus(subStatus dexpb.ErrorSubStatus) ErrorSubStatus {
	switch subStatus {
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED:
		return ErrorSubStatusFlowAlreadyStarted
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS:
		return ErrorSubStatusFlowNotFound
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR:
		return ErrorSubStatusWorkerAPI
	case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT:
		return ErrorSubStatusLongPollTimeout
	default:
		return ErrorSubStatusUncategorized
	}
}

func newFlowDefinitionError(
	flowType string,
	definition string,
	err error,
) error {
	var existing *FlowDefinitionError
	if errors.As(err, &existing) {
		return err
	}
	return &FlowDefinitionError{
		FlowType:   flowType,
		Definition: definition,
		Err:        err,
	}
}

func wrapValueMappingError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var existing *ValueMappingError
	if errors.As(err, &existing) {
		return err
	}
	return &ValueMappingError{Operation: operation, Err: err}
}

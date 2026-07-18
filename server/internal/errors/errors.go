// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package errors

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CategorizedError interface {
	error
	// GetFullError will include the from error message, usually for logging
	GetFullError() string
	GetFromError() error
	GetCategory() ErrorCategory
	GetCallAtPath() string

	IsInternalError() bool
	IsTimeoutError() bool
	IsRetriable() bool
	IsNotFoundError() bool
	IsConflictError() bool
	IsInvalidInputError() bool
	IsResourceExhaustedError() bool
	IsCASError() bool
	// IsRetriableExcludingCASError is true when IsRetriable() except CAS errors
	IsRetriableExcludingCASError() bool
}

type ErrorCategory string

const (
	// ErrorCategoryConflict is for DB unique key constraint violation, or alreadyExistError
	ErrorCategoryConflict ErrorCategory = "conflict"
	// ErrorCategoryCAS is for checking CAS error (.e.g version or rangId mismatch)
	ErrorCategoryCAS ErrorCategory = "cas_mismatch"

	ErrorCategoryInternal          ErrorCategory = "internal"
	ErrorCategoryNotFound          ErrorCategory = "not_found"
	ErrorCategoryInvalidInput      ErrorCategory = "invalid_input"
	ErrorCategoryUnauthenticated   ErrorCategory = "unauthenticated"
	ErrorCategoryPermissionDenied  ErrorCategory = "permission_denied"
	ErrorCategoryRateLimited       ErrorCategory = "rate_limited"
	ErrorCategoryUnavailable       ErrorCategory = "unavailable"
	ErrorCategoryTimeout           ErrorCategory = "timeout"
	ErrorCategoryUnimplemented     ErrorCategory = "unimplemented"
	ErrorCategoryCanceled          ErrorCategory = "cancel"
	ErrorCategoryResourceExhausted ErrorCategory = "resource_exhausted"
)

// ToProtoError maps the internal errors to their corresponding Protobuf codes.
func ToProtoError(err error) error {
	if catErr, ok := errors.AsType[CategorizedError](err); ok {
		msg := catErr.Error()

		switch catErr.GetCategory() {
		case ErrorCategoryCAS:
			return status.Error(codes.Aborted, msg)
		case ErrorCategoryConflict:
			return status.Error(codes.AlreadyExists, msg)
		case ErrorCategoryUnauthenticated:
			return status.Error(codes.Unauthenticated, msg)
		case ErrorCategoryPermissionDenied:
			return status.Error(codes.PermissionDenied, msg)
		case ErrorCategoryNotFound:
			return status.Error(codes.NotFound, msg)
		case ErrorCategoryInvalidInput:
			return status.Error(codes.InvalidArgument, string(catErr.GetFullError()))
		case ErrorCategoryRateLimited:
			return status.Error(codes.ResourceExhausted, msg)
		case ErrorCategoryTimeout:
			return status.Error(codes.DeadlineExceeded, msg)
		case ErrorCategoryCanceled:
			return status.Error(codes.Canceled, msg)
		case ErrorCategoryUnavailable:
			return status.Error(codes.Unavailable, msg)
		case ErrorCategoryUnimplemented:
			return status.Error(codes.Unimplemented, msg)
		case ErrorCategoryResourceExhausted:
			return status.Error(codes.ResourceExhausted, msg)

		default:
			return status.Error(codes.Internal, msg)
		}
	}
	return status.Errorf(codes.Internal, "unknown server error")
}

type categorizedErrorImpl struct {
	from     error
	message  string
	category ErrorCategory
	errorAt  string
}

const skipForCallAt = 2

var _ CategorizedError = (*categorizedErrorImpl)(nil)

func NewConflictError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryConflict,
		errorAt:  PathToCaller(skipForCallAt + 1),
	}
}

func NewInternalError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryInternal,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewCASError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryCAS,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewResourceExhaustedError(msg string) CategorizedError {
	return &categorizedErrorImpl{
		message:  msg,
		category: ErrorCategoryResourceExhausted,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewNotFoundError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryNotFound,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewInvalidInputError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryInvalidInput,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewTimeoutError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryTimeout,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewUnavailableError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryUnavailable,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewUnimplementedError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryUnimplemented,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func NewCancelError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryCanceled,
		errorAt:  PathToCaller(skipForCallAt),
	}
}

func (c *categorizedErrorImpl) Error() string {
	return c.message
}

func (c *categorizedErrorImpl) GetFullError() string {
	parts := []string{string(c.category) + ": " + c.message}

	if c.from != nil {
		if catErr, ok := errors.AsType[CategorizedError](c.from); ok {
			parts = append(parts, fmt.Sprintf("from: %s", catErr.GetFullError()))
		}
	}
	return strings.Join(parts, "; ")
}

func (c *categorizedErrorImpl) GetCallAtPath() string {
	return c.errorAt
}

func (c *categorizedErrorImpl) GetFromError() error {
	return c.from
}

func (c *categorizedErrorImpl) GetCategory() ErrorCategory {
	return c.category
}

func (c *categorizedErrorImpl) IsRetriable() bool {
	switch c.category {
	case ErrorCategoryTimeout, ErrorCategoryUnavailable, ErrorCategoryInternal, ErrorCategoryCAS:
		return true
	default:
		return false
	}
}

func (c *categorizedErrorImpl) IsInternalError() bool {
	return c.category == ErrorCategoryInternal
}

func (c *categorizedErrorImpl) IsTimeoutError() bool {
	return c.category == ErrorCategoryTimeout
}

func (c *categorizedErrorImpl) IsNotFoundError() bool {
	return c.category == ErrorCategoryNotFound
}

func (c *categorizedErrorImpl) IsConflictError() bool {
	return c.category == ErrorCategoryConflict
}

func (c *categorizedErrorImpl) IsInvalidInputError() bool {
	return c.category == ErrorCategoryInvalidInput
}

func (c *categorizedErrorImpl) IsResourceExhaustedError() bool {
	return c.category == ErrorCategoryResourceExhausted
}

func (c *categorizedErrorImpl) IsCASError() bool {
	return c.category == ErrorCategoryCAS
}

func (c *categorizedErrorImpl) IsRetriableExcludingCASError() bool {
	if c.IsCASError() {
		return false
	}
	return c.IsRetriable()
}

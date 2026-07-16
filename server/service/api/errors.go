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

package api

import "github.com/xcherryio/apis/goapi/xcapi"

type ErrorWithStatus struct {
	StatusCode int
	Error      xcapi.ApiErrorResponse
}

func NewErrorWithStatus(code int, details string) *ErrorWithStatus {
	return &ErrorWithStatus{
		StatusCode: code,
		Error: xcapi.ApiErrorResponse{
			Details: &details,
		},
	}
}

func NewErrorResponseWithStatus(code int, error xcapi.ApiErrorResponse) *ErrorWithStatus {
	return &ErrorWithStatus{
		StatusCode: code,
		Error:      error,
	}
}

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

import (
	"context"

	"github.com/xcherryio/apis/goapi/xcapi"
)

type Server interface {
	// Start will start running on the background
	Start() error
	Stop(ctx context.Context) error
}

// Service is the interface of API service, which decoupled from REST server framework like Gin
// So that users can choose to use other REST frameworks to serve requests
type Service interface {
	StartProcess(ctx context.Context, request xcapi.ProcessExecutionStartRequest) (
		resp *xcapi.ProcessExecutionStartResponse, err *ErrorWithStatus)
	StopProcess(ctx context.Context, request xcapi.ProcessExecutionStopRequest) *ErrorWithStatus
	DescribeLatestProcess(ctx context.Context, request xcapi.ProcessExecutionDescribeRequest) (
		resp *xcapi.ProcessExecutionDescribeResponse, err *ErrorWithStatus)
	PublishToLocalQueue(ctx context.Context, request xcapi.PublishToLocalQueueRequest) *ErrorWithStatus
	Rpc(
		ctx context.Context, request xcapi.ProcessExecutionRpcRequest,
	) (resp *xcapi.ProcessExecutionRpcResponse, err *ErrorWithStatus)
	ListProcessExecutions(ctx context.Context, request xcapi.ListProcessExecutionsRequest,
	) (response *xcapi.ListProcessExecutionsResponse, retErr *ErrorWithStatus)
	WaitForProcessCompletion(ctx context.Context, request xcapi.ProcessExecutionWaitForCompletionRequest) (
		resp *xcapi.ProcessExecutionWaitForCompletionResponse, err *ErrorWithStatus)
}

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

package persistence

import (
	"context"

	"github.com/xcherryio/apis/goapi/xcapi"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

// ProcessStore is for operating on the database for process execution
type (
	ProcessStore interface {
		Close() error

		StartProcess(
			ctx context.Context, request data_models2.StartProcessRequest,
		) (*data_models2.StartProcessResponse, error)
		StopProcess(
			ctx context.Context, request data_models2.StopProcessRequest,
		) (*data_models2.StopProcessResponse, error)
		DescribeLatestProcess(
			ctx context.Context, request data_models2.DescribeLatestProcessRequest,
		) (*data_models2.DescribeLatestProcessResponse, error)
		RecoverFromStateExecutionFailure(
			ctx context.Context, request data_models2.RecoverFromStateExecutionFailureRequest,
		) error
		GetLatestProcessExecution(
			ctx context.Context, request data_models2.GetLatestProcessExecutionRequest,
		) (*data_models2.GetLatestProcessExecutionResponse, error)

		GetImmediateTasks(
			ctx context.Context, request data_models2.GetImmediateTasksRequest,
		) (*data_models2.GetImmediateTasksResponse, error)
		DeleteImmediateTasks(ctx context.Context, request data_models2.DeleteImmediateTasksRequest) error
		BackoffImmediateTask(ctx context.Context, request data_models2.BackoffImmediateTaskRequest) error
		CleanUpTasksForTest(ctx context.Context, shardId int32) error

		GetTimerTasksUpToTimestamp(
			ctx context.Context, request data_models2.GetTimerTasksRequest,
		) (*data_models2.GetTimerTasksResponse, error)

		GetTimerTasksForTimestamps(
			ctx context.Context, request data_models2.GetTimerTasksForTimestampsRequest,
		) (*data_models2.GetTimerTasksResponse, error)
		ConvertTimerTaskToImmediateTask(
			ctx context.Context, request data_models2.ProcessTimerTaskRequest,
		) (*data_models2.ProcessTimerTaskResponse, error)
		ProcessTimerTaskForTimerCommand(
			ctx context.Context, request data_models2.ProcessTimerTaskRequest,
		) (*data_models2.ProcessTimerTaskResponse, error)
		ProcessTimerTaskForProcessTimeout(
			ctx context.Context, request data_models2.ProcessTimerTaskRequest,
		) (*data_models2.ProcessTimerTaskResponse, error)

		PrepareStateExecution(
			ctx context.Context, request data_models2.PrepareStateExecutionRequest,
		) (*data_models2.PrepareStateExecutionResponse, error)
		ProcessWaitUntilExecution(
			ctx context.Context, request data_models2.ProcessWaitUntilExecutionRequest,
		) (*data_models2.ProcessWaitUntilExecutionResponse, error)
		CompleteExecuteExecution(
			ctx context.Context, request data_models2.CompleteExecuteExecutionRequest,
		) (*data_models2.CompleteExecuteExecutionResponse, error)

		PublishToLocalQueue(
			ctx context.Context, request data_models2.PublishToLocalQueueRequest,
		) (*data_models2.PublishToLocalQueueResponse, error)
		ProcessLocalQueueMessages(
			ctx context.Context, request data_models2.ProcessLocalQueueMessagesRequest,
		) (*data_models2.ProcessLocalQueueMessagesResponse, error)

		ReadAppDatabase(
			ctx context.Context, request data_models2.AppDatabaseReadRequest,
		) (*data_models2.AppDatabaseReadResponse, error)

		LoadLocalAttributes(
			ctx context.Context, request data_models2.LoadLocalAttributesRequest,
		) (*data_models2.LoadLocalAttributesResponse, error)

		UpdateProcessExecutionForRpc(ctx context.Context, request data_models2.UpdateProcessExecutionForRpcRequest) (
			*data_models2.UpdateProcessExecutionForRpcResponse, error)
	}

	VisibilityStore interface {
		Close() error
		RecordProcessExecutionStatus(ctx context.Context, req data_models2.RecordProcessExecutionStatusRequest) error
		ListProcessExecutions(
			ctx context.Context, request xcapi.ListProcessExecutionsRequest,
		) (*xcapi.ListProcessExecutionsResponse, error)
		// TODO: add count process executions api
	}
)

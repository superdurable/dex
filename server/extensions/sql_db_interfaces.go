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

package extensions

import (
	"context"
	"database/sql"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/uuid"
	"github.com/xcherryio/xcherry/server/config"
	"github.com/xcherryio/xcherry/server/persistence/data_models"
)

type SQLDBExtension interface {
	// StartDBSession starts the session for regular business logic
	StartDBSession(cfg *config.SQL) (SQLDBSession, error)
	// StartAdminDBSession starts the session for admin operation like DDL
	StartAdminDBSession(cfg *config.SQL) (SQLAdminDBSession, error)
}

type SQLDBSession interface {
	nonTransactionalCRUD

	ErrorChecker
	StartTransaction(ctx context.Context, opts *sql.TxOptions) (SQLTransaction, error)
	Close() error
}

type SQLTransaction interface {
	transactionalCRUD
	Commit() error
	Rollback() error
}

type SQLAdminDBSession interface {
	CreateDatabase(ctx context.Context, database string) error
	DropDatabase(ctx context.Context, database string) error
	ExecuteSchemaDDL(ctx context.Context, ddlQuery string) error
	Close() error
}

type transactionalCRUD interface {
	InsertLatestProcessExecution(ctx context.Context, row LatestProcessExecutionRow) error
	SelectLatestProcessExecutionForUpdate(
		ctx context.Context, namespace string, processId string,
	) (*LatestProcessExecutionRow, bool, error)
	UpdateLatestProcessExecution(ctx context.Context, row LatestProcessExecutionRow) error

	InsertProcessExecution(ctx context.Context, row ProcessExecutionRow) error
	SelectProcessExecutionForUpdate(
		ctx context.Context, processExecutionId uuid.UUID,
	) (*ProcessExecutionRowForUpdate, error)
	SelectProcessExecution(ctx context.Context, processExecutionId uuid.UUID) (*ProcessExecutionRow, error)
	UpdateProcessExecution(ctx context.Context, row ProcessExecutionRowForUpdate) error

	InsertAsyncStateExecution(ctx context.Context, row AsyncStateExecutionRow) error
	SelectAsyncStateExecutionForUpdate(
		ctx context.Context, filter AsyncStateExecutionSelectFilter,
	) (*AsyncStateExecutionRowForUpdate, error)
	UpdateAsyncStateExecution(ctx context.Context, row AsyncStateExecutionRowForUpdate) error
	UpdateAsyncStateExecutionWithoutCommands(
		ctx context.Context, row AsyncStateExecutionRowForUpdateWithoutCommands,
	) error
	BatchUpdateAsyncStateExecutionsToAbortRunning(ctx context.Context, processExecutionId uuid.UUID) error
	InsertImmediateTask(ctx context.Context, row ImmediateTaskRowForInsert) error
	InsertTimerTask(ctx context.Context, row TimerTaskRowForInsert) error

	DeleteImmediateTask(ctx context.Context, filter ImmediateTaskRowDeleteFilter) error
	DeleteTimerTask(ctx context.Context, filter TimerTaskRowDeleteFilter) error

	InsertLocalQueueMessage(ctx context.Context, row LocalQueueMessageRow) (bool, error)

	InsertAppDatabaseTable(ctx context.Context, row AppDatabaseTableRow, writeConfigMode xcapi.WriteConflictMode) error
	UpsertAppDatabaseTableByPK(ctx context.Context, row AppDatabaseTableRow) error

	InsertLocalAttribute(ctx context.Context, insert LocalAttributeRow) error
	UpsertLocalAttribute(ctx context.Context, row LocalAttributeRow) error
}

type nonTransactionalCRUD interface {
	SelectLatestProcessExecution(ctx context.Context, namespace string, processId string) (*ProcessExecutionRow, error)

	SelectAsyncStateExecution(
		ctx context.Context, filter AsyncStateExecutionSelectFilter,
	) (*AsyncStateExecutionRow, error)

	BatchSelectImmediateTasks(
		ctx context.Context, shardId int32, startSequenceInclusive int64, pageSize int32,
	) ([]ImmediateTaskRow, error)
	BatchDeleteImmediateTask(ctx context.Context, filter ImmediateTaskRangeDeleteFilter) error

	BatchSelectTimerTasks(ctx context.Context, filter TimerTaskRangeSelectFilter) ([]TimerTaskRow, error)
	SelectTimerTasksForTimestamps(ctx context.Context, filter TimerTaskSelectByTimestampsFilter) ([]TimerTaskRow, error)

	CleanUpTasksForTest(ctx context.Context, shardId int32) error

	SelectLocalQueueMessages(
		ctx context.Context, processExecutionId uuid.UUID, dedupIdStrings []string,
	) ([]LocalQueueMessageRow, error)

	SelectAppDatabaseTableByPK(
		ctx context.Context, tableName string, primaryKeys [][]xcapi.AppDatabaseColumnValue, columns []string,
	) ([]AppDatabaseTableRowSelect, error)

	SelectLocalAttributes(
		ctx context.Context, processExecutionId uuid.UUID, keys []string,
	) ([]LocalAttributeRow, error)

	InsertProcessExecutionStartForVisibility(
		ctx context.Context, row ExecutionVisibilityRow,
	) error

	UpdateProcessExecutionStatusForVisibility(
		ctx context.Context, row ExecutionVisibilityRow,
	) error

	SelectProcessExecutions(
		ctx context.Context,
		namespace string,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)

	SelectProcessExecutionsByStatus(
		ctx context.Context,
		namespace string,
		status data_models.ProcessExecutionStatus,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)

	SelectProcessExecutionsByTypeQuery(
		ctx context.Context,
		namespace string,
		processTypeName string,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)

	SelectProcessExecutionsById(
		ctx context.Context,
		namespace string,
		processId string,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)

	SelectProcessExecutionsByStatusAndType(
		ctx context.Context,
		namespace string,
		status data_models.ProcessExecutionStatus,
		processTypeName string,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)

	SelectProcessExecutionsByStatusAndId(
		ctx context.Context,
		namespace string,
		status data_models.ProcessExecutionStatus,
		processId string,
		startTimeMinInclusive, startTimeMaxInclusive int64,
		lastProcessExecutionIdString string,
		lastStartTime int64,
		pageSize int32,
	) ([]ExecutionVisibilityRow, error)
}

type ErrorChecker interface {
	IsDupEntryError(err error) bool
	IsNotFoundError(err error) bool
	IsTimeoutError(err error) bool
	IsThrottlingError(err error) bool
	IsConditionalUpdateFailure(err error) bool
}

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
	"time"

	"github.com/xcherryio/xcherry/server/common/uuid"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"

	"github.com/jmoiron/sqlx/types"
)

/**
* Why we need ProcessExecutionIdString field, in addition to ProcessExecutionId?
* Because different database driver has different way to deal with UUID.
* In some database like MySQL/MariaDB/Oracle, UUID is stored as binary(16) type, which is not human readable directly.
* Therefore, they provide some helper functions to convert UUID to/from string. But the queries to read/write UUID are still byte array.
* Some databases like Postgres, provide UUID type out of the box, the queries to read/write UUID are string.
* For the first type of database, the extension implementation can use the UUID form of ProcessExecutionId, which has implemented the Scan/Value interface.
* For the second type of database, the extension implementation can use the string form of ProcessExecutionId, which is the ProcessExecutionIdString field.
* Having this two fields available so that the extension implementation doesn't need to create a new struct and copy/convert the fields.
*
* Note that this field is a "helper" field, meaning that the caller of the interface(the persistence/ layer of this repo) will not read or write this field.
* The extension implementation is responsible to read/write this field. For example, before writing into database, Postgres extension will write the field
* by converting UUID to string. After reading from database, Postgres extension will read into the string field, then converting it to the UUID field.
 */

type (
	LatestProcessExecutionRow struct {
		Namespace          string
		ProcessId          string
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
	}

	ProcessExecutionRowForUpdate struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		ShardId int32

		Status                 data_models2.ProcessExecutionStatus
		HistoryEventIdSequence int32

		StateExecutionSequenceMaps types.JSONText
		StateExecutionLocalQueues  types.JSONText

		GracefulCompleteRequested bool
	}

	ProcessExecutionRow struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		ShardId int32

		Status                 data_models2.ProcessExecutionStatus
		HistoryEventIdSequence int32

		StateExecutionSequenceMaps types.JSONText
		StateExecutionLocalQueues  types.JSONText

		Namespace string

		ProcessId                 string
		StartTime                 time.Time
		TimeoutSeconds            int32
		Info                      types.JSONText
		GracefulCompleteRequested bool
	}

	AsyncStateExecutionSelectFilter struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		StateId         string
		StateIdSequence int32
	}

	AsyncStateExecutionRowForUpdate struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		StateId         string
		StateIdSequence int32

		Status data_models2.StateExecutionStatus

		WaitUntilCommands       types.JSONText
		WaitUntilCommandResults types.JSONText

		LastFailure types.JSONText

		PreviousVersion int32 // for conditional check
	}

	AsyncStateExecutionRowForUpdateWithoutCommands struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		StateId         string
		StateIdSequence int32

		Status data_models2.StateExecutionStatus

		LastFailure types.JSONText

		PreviousVersion int32 // for conditional check
	}

	AsyncStateExecutionRow struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
		StateId                  string
		StateIdSequence          int32

		Status data_models2.StateExecutionStatus

		WaitUntilCommands       types.JSONText
		WaitUntilCommandResults types.JSONText

		PreviousVersion int32 // for conditional check

		LastFailure types.JSONText

		Input types.JSONText
		Info  types.JSONText
	}

	ImmediateTaskRowForInsert struct {
		ShardId  int32
		TaskType data_models2.ImmediateTaskType

		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
		// StateId and StateIdSequence will be "" and 0 when TaskType is persistence.ImmediateTaskTypeNewLocalQueueMessages
		StateId         string
		StateIdSequence int32

		Info types.JSONText
	}

	ImmediateTaskRow struct {
		ShardId      int32
		TaskSequence int64

		TaskType data_models2.ImmediateTaskType

		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
		StateId                  string
		StateIdSequence          int32

		Info types.JSONText
	}

	ImmediateTaskRowDeleteFilter struct {
		ShardId      int32
		TaskSequence int64

		OptionalPartitionKey *data_models2.PartitionKey
	}

	ImmediateTaskRangeDeleteFilter struct {
		ShardId int32

		MinTaskSequenceInclusive int64
		MaxTaskSequenceInclusive int64
	}

	TimerTaskRowForInsert struct {
		ShardId             int32
		FireTimeUnixSeconds int64
		TaskType            data_models2.TimerTaskType

		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
		StateId                  string
		StateIdSequence          int32

		Info types.JSONText
	}

	TimerTaskRow struct {
		ShardId             int32
		FireTimeUnixSeconds int64
		TaskSequence        int64

		TaskType data_models2.TimerTaskType

		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string
		StateId                  string
		StateIdSequence          int32

		Info types.JSONText
	}

	TimerTaskRowDeleteFilter struct {
		ShardId             int32
		FireTimeUnixSeconds int64
		TaskSequence        int64

		OptionalPartitionKey *data_models2.PartitionKey
	}

	TimerTaskRangeSelectFilter struct {
		ShardId int32

		MaxFireTimeUnixSecondsInclusive int64
		PageSize                        int32
	}

	TimerTaskSelectByTimestampsFilter struct {
		ShardId int32

		FireTimeUnixSeconds      []int64
		MinTaskSequenceInclusive int64
	}

	LocalQueueMessageRow struct {
		ProcessExecutionId uuid.UUID
		// See the top of the file for why we need this field
		ProcessExecutionIdString string

		QueueName string

		DedupId uuid.UUID
		// See the top of the file for why we need this field
		DedupIdString string

		Payload types.JSONText
	}

	AppDatabaseTableRow struct {
		TableName               string
		PrimaryKeyColumnToValue map[string]string
		OtherColumnToValue      map[string]string
	}

	AppDatabaseTableRowSelect struct {
		ColumnToValue map[string]string
	}

	LocalAttributeRow struct {
		ProcessExecutionId       uuid.UUID
		ProcessExecutionIdString string
		Key                      string
		Value                    types.JSONText
	}

	ExecutionVisibilityRow struct {
		Namespace                string
		ProcessId                string
		ProcessExecutionId       uuid.UUID
		ProcessExecutionIdString string
		ProcessTypeName          string
		Status                   data_models2.ProcessExecutionStatus
		StartTime                time.Time
		CloseTime                time.Time
	}
)

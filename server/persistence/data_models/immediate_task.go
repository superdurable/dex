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

package data_models

import (
	"fmt"

	"github.com/xcherryio/xcherry/server/common/uuid"
)

type ImmediateTask struct {
	ShardId int32
	// TaskSequence represents the increasing order in the queue of the shard
	// It should be empty when inserting, because the persistence/database will
	// generate the value automatically
	TaskSequence *int64

	TaskType ImmediateTaskType

	ProcessExecutionId uuid.UUID
	StateExecutionId
	ImmediateTaskInfo ImmediateTaskInfoJson

	// only needed for distributed database that doesn't support global secondary index
	OptionalPartitionKey *PartitionKey
}

func (t ImmediateTask) GetTaskSequence() int64 {
	if t.TaskSequence == nil {
		// this shouldn't happen!
		return -1
	}
	return *t.TaskSequence
}

func (t ImmediateTask) GetTaskId() string {
	if t.TaskSequence == nil {
		return "<WRONG ID, TaskSequence IS EMPTY>"
	}
	return fmt.Sprintf("%v-%v", t.ShardId, *t.TaskSequence)
}

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

package process

import (
	"context"

	"github.com/superdurable/dex/server/common/uuid"
	extensions2 "github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) AddVisibilityTaskRecordProcessExecutionStatus(
	ctx context.Context,
	tx extensions2.SQLTransaction,
	shardId int32,
	namespace string,
	processId string,
	processType string,
	processExecutionId uuid.UUID,
	status data_models2.ProcessExecutionStatus,
	startTime *int64,
	endTime *int64) error {

	visibilityTaskInfo := data_models2.ImmediateTaskInfoJson{
		VisibilityInfo: &data_models2.VisibilityInfoJson{
			Namespace:          namespace,
			ProcessId:          processId,
			ProcessType:        processType,
			ProcessExecutionId: processExecutionId,
			Status:             status,
			StartTime:          startTime,
			CloseTime:          endTime,
		},
	}

	visibilityTaskInfoBytes, err := data_models2.FromImmediateTaskInfoIntoBytes(visibilityTaskInfo)
	if err != nil {
		return err
	}
	visibilityTask := extensions2.ImmediateTaskRowForInsert{

		ShardId:            shardId,
		TaskType:           data_models2.ImmediateTaskTypeVisibility,
		ProcessExecutionId: processExecutionId,
		Info:               visibilityTaskInfoBytes,
	}
	// TODO: upsert for starting process, update for closing process
	err = tx.InsertImmediateTask(ctx, visibilityTask)
	return err
}

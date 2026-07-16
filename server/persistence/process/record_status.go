// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	"github.com/xcherryio/xcherry/server/common/uuid"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
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

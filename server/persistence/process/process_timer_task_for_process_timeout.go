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
	"fmt"

	"github.com/superdurable/dex/server/common/log/tag"
	extensions2 "github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) ProcessTimerTaskForProcessTimeout(
	ctx context.Context, request data_models2.ProcessTimerTaskRequest,
) (*data_models2.ProcessTimerTaskResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doProcessTimerTaskForProcessTimeoutTx(ctx, tx, request)
	if err != nil {
		err2 := tx.Rollback()
		if err2 != nil {
			p.logger.Error("error on rollback transaction", tag.Error(err2))
		}
	} else {
		err = tx.Commit()
		if err != nil {
			p.logger.Error("error on committing transaction", tag.Error(err))
			return nil, err
		}
	}

	return resp, err
}

func (p sqlProcessStoreImpl) doProcessTimerTaskForProcessTimeoutTx(
	ctx context.Context, tx extensions2.SQLTransaction, request data_models2.ProcessTimerTaskRequest,
) (*data_models2.ProcessTimerTaskResponse, error) {
	p.logger.Debug("doProcessTimerTaskForProcessTimeoutTx", tag.Value(request.Task))
	processExecution, err := tx.SelectProcessExecution(ctx, request.Task.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	if processExecution.Status == data_models2.ProcessExecutionStatusRunning {
		resp, err := p.doStopProcessTx(
			ctx,
			tx,
			processExecution.Namespace,
			processExecution.ProcessId,
			request.Task.ShardId,
			data_models2.ProcessExecutionStatusTimeout)
		if err != nil {
			return nil, err
		}

		if resp.NotExists {
			return nil, fmt.Errorf("process execution not exists")
		}
	}

	task := request.Task
	err = tx.DeleteTimerTask(ctx, extensions2.TimerTaskRowDeleteFilter{
		ShardId:              task.ShardId,
		FireTimeUnixSeconds:  task.FireTimestampSeconds,
		TaskSequence:         *task.TaskSequence,
		OptionalPartitionKey: task.OptionalPartitionKey,
	})
	if err != nil {
		return nil, err
	}

	return &data_models2.ProcessTimerTaskResponse{HasNewImmediateTask: false}, nil
}

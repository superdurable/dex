// Copyright 2023 xCherryIO organization
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package process

import (
	"context"
	"fmt"

	"github.com/xcherryio/xcherry/server/common/log/tag"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
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

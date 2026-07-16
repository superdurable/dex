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

	"github.com/xcherryio/xcherry/server/common/log/tag"
	"github.com/xcherryio/xcherry/server/extensions"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) PublishToLocalQueue(ctx context.Context, request data_models2.PublishToLocalQueueRequest) (
	*data_models2.PublishToLocalQueueResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doPublishToLocalQueueTx(ctx, tx, request)

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

func (p sqlProcessStoreImpl) doPublishToLocalQueueTx(
	ctx context.Context, tx extensions.SQLTransaction, request data_models2.PublishToLocalQueueRequest,
) (*data_models2.PublishToLocalQueueResponse, error) {
	curProcExecRow, err := p.session.SelectLatestProcessExecution(ctx, request.Namespace, request.ProcessId)
	if err != nil {
		if p.session.IsNotFoundError(err) {
			// early stop when there is no such process running
			return &data_models2.PublishToLocalQueueResponse{
				ProcessNotExists: true,
			}, nil
		}
		return nil, err
	}

	// check if the process is running

	procExecRow, err := tx.SelectProcessExecutionForUpdate(ctx, curProcExecRow.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	if procExecRow.Status != data_models2.ProcessExecutionStatusRunning {
		return &data_models2.PublishToLocalQueueResponse{
			ProcessNotRunning: true,
		}, nil
	}

	hasNewImmediateTask, err := p.publishToLocalQueue(ctx, tx, curProcExecRow.ProcessExecutionId, procExecRow.ShardId, request.Messages)
	if err != nil {
		return nil, err
	}

	return &data_models2.PublishToLocalQueueResponse{
		ProcessExecutionId:  curProcExecRow.ProcessExecutionId,
		ShardId:             procExecRow.ShardId,
		HasNewImmediateTask: hasNewImmediateTask,
	}, nil
}

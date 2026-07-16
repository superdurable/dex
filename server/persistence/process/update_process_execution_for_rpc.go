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

func (p sqlProcessStoreImpl) UpdateProcessExecutionForRpc(
	ctx context.Context, request data_models2.UpdateProcessExecutionForRpcRequest,
) (
	*data_models2.UpdateProcessExecutionForRpcResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	resp, err := p.doUpdateProcessExecutionForRpcTx(ctx, tx, request)

	if err != nil || resp.FailAtWritingAppDatabase {
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

func (p sqlProcessStoreImpl) doUpdateProcessExecutionForRpcTx(
	ctx context.Context, tx extensions.SQLTransaction, request data_models2.UpdateProcessExecutionForRpcRequest,
) (*data_models2.UpdateProcessExecutionForRpcResponse, error) {
	hasNewImmediateTask := false

	// lock process execution row first
	prcRow, err := tx.SelectProcessExecutionForUpdate(ctx, request.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	// skip the writing operations on a closed process
	if prcRow.Status != data_models2.ProcessExecutionStatusRunning {
		return &data_models2.UpdateProcessExecutionForRpcResponse{
			ProcessNotExists: true,
		}, nil
	}

	// Step 1: update persistence

	err = p.writeToAppDatabaseIfNeeded(ctx, tx, request.AppDatabaseConfig, request.AppDatabaseWrite)
	if err != nil {
		//lint:ignore nilerr reason
		return &data_models2.UpdateProcessExecutionForRpcResponse{
			FailAtWritingAppDatabase: true,
			WritingAppDatabaseError:  err,
		}, nil
	}

	// Step 2: handle state decision

	sequenceMaps, err := data_models2.NewStateExecutionSequenceMapsFromBytes(prcRow.StateExecutionSequenceMaps)
	if err != nil {
		return nil, err
	}

	resp, err := p.handleStateDecision(ctx, tx, HandleStateDecisionRequest{
		Namespace:          request.Namespace,
		ProcessId:          request.ProcessId,
		ProcessType:        request.ProcessType,
		ProcessExecutionId: request.ProcessExecutionId,
		StateDecision:      request.StateDecision,
		AppDatabaseConfig:  request.AppDatabaseConfig,
		WorkerUrl:          request.WorkerUrl,

		ProcessExecutionRowStateExecutionSequenceMaps: &sequenceMaps,
		ProcessExecutionRowGracefulCompleteRequested:  prcRow.GracefulCompleteRequested,
		ProcessExecutionRowStatus:                     prcRow.Status,

		TaskShardId: request.TaskShardId,
	})
	if err != nil {
		return nil, err
	}
	if resp.HasNewImmediateTask {
		hasNewImmediateTask = true
	}

	prcRow.GracefulCompleteRequested = resp.ProcessExecutionRowNewGracefulCompleteRequested
	prcRow.Status = resp.ProcessExecutionRowNewStatus
	prcRow.StateExecutionSequenceMaps, err = resp.ProcessExecutionRowNewStateExecutionSequenceMaps.ToBytes()
	if err != nil {
		return nil, err
	}

	err = tx.UpdateProcessExecution(ctx, *prcRow)
	if err != nil {
		return nil, err
	}

	// Step 3: publish to local queue

	hasNewImmediateTask2, err := p.publishToLocalQueue(ctx, tx, request.ProcessExecutionId, prcRow.ShardId, request.PublishToLocalQueue)
	if err != nil {
		return nil, err
	}
	if hasNewImmediateTask2 {
		hasNewImmediateTask = true
	}

	return &data_models2.UpdateProcessExecutionForRpcResponse{
		HasNewImmediateTask: hasNewImmediateTask,
	}, nil
}

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

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/ptr"
	"github.com/superdurable/dex/server/extensions"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"

	"time"
)

func (p sqlProcessStoreImpl) StopProcess(
	ctx context.Context, request data_models2.StopProcessRequest,
) (*data_models2.StopProcessResponse, error) {
	tx, err := p.session.StartTransaction(ctx, defaultTxOpts)
	if err != nil {
		return nil, err
	}

	namespace := request.Namespace
	processId := request.ProcessId
	status := data_models2.ProcessExecutionStatusTerminated
	if request.ProcessStopType == xcapi.FAIL {
		status = data_models2.ProcessExecutionStatusFailed
	}

	resp, err := p.doStopProcessTx(ctx, tx, namespace, processId, request.NewTaskShardId, status)
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

func (p sqlProcessStoreImpl) doStopProcessTx(
	ctx context.Context, tx extensions.SQLTransaction, namespace string, processId string, newTaskShardId int32,
	status data_models2.ProcessExecutionStatus,
) (*data_models2.StopProcessResponse, error) {
	curProcExecRow, err := p.session.SelectLatestProcessExecution(ctx, namespace, processId)
	if err != nil {
		if p.session.IsNotFoundError(err) {
			// early stop when there is no such process running
			return &data_models2.StopProcessResponse{
				NotExists: true,
			}, nil
		}
		return nil, err
	}

	// handle dex_sys_process_executions
	procExecRow, err := tx.SelectProcessExecutionForUpdate(ctx, curProcExecRow.ProcessExecutionId)
	if err != nil {
		return nil, err
	}

	if procExecRow.Status != data_models2.ProcessExecutionStatusRunning {
		return &data_models2.StopProcessResponse{
			NotExists: false,
		}, nil
	}

	sequenceMaps, err := data_models2.NewStateExecutionSequenceMapsFromBytes(procExecRow.StateExecutionSequenceMaps)
	if err != nil {
		return nil, err
	}

	pendingExecutionMap := sequenceMaps.PendingExecutionMap

	sequenceMaps.PendingExecutionMap = map[string]map[int]bool{}
	procExecRow.StateExecutionSequenceMaps, err = sequenceMaps.ToBytes()
	if err != nil {
		return nil, err
	}

	procExecRow.Status = status

	err = tx.UpdateProcessExecution(ctx, *procExecRow)
	if err != nil {
		return nil, err
	}

	if len(pendingExecutionMap) > 0 {
		// handle dex_sys_async_state_executions
		// find all related rows with the processExecutionId, and
		// modify the wait_until/execute status from running to aborted
		err = tx.BatchUpdateAsyncStateExecutionsToAbortRunning(ctx, curProcExecRow.ProcessExecutionId)
		if err != nil {
			return nil, err
		}
	}

	procExecInfoJson, err := data_models2.BytesToProcessExecutionInfo(curProcExecRow.Info)
	if err != nil {
		return nil, err
	}

	err = p.AddVisibilityTaskRecordProcessExecutionStatus(
		ctx,
		tx,
		newTaskShardId,
		namespace,
		processId,
		procExecInfoJson.ProcessType,
		curProcExecRow.ProcessExecutionId,
		status,
		nil,
		ptr.Any(time.Now().Unix()),
	)
	if err != nil {
		return nil, err
	}

	return &data_models2.StopProcessResponse{
		NotExists: false,
	}, nil
}

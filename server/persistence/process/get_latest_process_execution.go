// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) GetLatestProcessExecution(
	ctx context.Context, request data_models2.GetLatestProcessExecutionRequest,
) (*data_models2.GetLatestProcessExecutionResponse, error) {
	row, err := p.session.SelectLatestProcessExecution(ctx, request.Namespace, request.ProcessId)
	if err != nil {
		if p.session.IsNotFoundError(err) {
			return &data_models2.GetLatestProcessExecutionResponse{
				NotExists: true,
			}, nil
		}
		return nil, err
	}

	info, err := data_models2.BytesToProcessExecutionInfo(row.Info)
	if err != nil {
		return nil, err
	}

	return &data_models2.GetLatestProcessExecutionResponse{
		ProcessExecutionId: row.ProcessExecutionId,
		ShardId:            row.ShardId,
		Status:             row.Status,
		StartTimestamp:     row.StartTime.Unix(),
		AppDatabaseConfig:  info.AppDatabaseConfig,

		ProcessType: info.ProcessType,
		WorkerUrl:   info.WorkerURL,
	}, nil
}

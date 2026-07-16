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

	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
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

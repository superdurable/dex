// Copyright (c) 2023 xCherryIO Organization
// SPDX-License-Identifier: Apache-2.0

package process

import (
	"context"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/ptr"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) DescribeLatestProcess(
	ctx context.Context, request data_models2.DescribeLatestProcessRequest,
) (*data_models2.DescribeLatestProcessResponse, error) {
	row, err := p.session.SelectLatestProcessExecution(ctx, request.Namespace, request.ProcessId)
	if err != nil {
		if p.session.IsNotFoundError(err) {
			return &data_models2.DescribeLatestProcessResponse{
				NotExists: true,
			}, nil
		}
		return nil, err
	}

	info, err := data_models2.BytesToProcessExecutionInfo(row.Info)
	if err != nil {
		return nil, err
	}

	return &data_models2.DescribeLatestProcessResponse{
		Response: &xcapi.ProcessExecutionDescribeResponse{
			ProcessExecutionId: ptr.Any(row.ProcessExecutionId.String()),
			ProcessType:        &info.ProcessType,
			WorkerUrl:          &info.WorkerURL,
			StartTimestamp:     ptr.Any(int32(row.StartTime.Unix())),
			Status:             xcapi.ProcessStatus(row.Status.String()).Ptr(),
		},
	}, nil
}

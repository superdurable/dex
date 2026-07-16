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

package visibility

import (
	"context"
	"fmt"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/log"
	"github.com/xcherryio/xcherry/server/common/ptr"
	"github.com/xcherryio/xcherry/server/config"
	extensions2 "github.com/xcherryio/xcherry/server/extensions"
	"github.com/xcherryio/xcherry/server/persistence"
	data_models2 "github.com/xcherryio/xcherry/server/persistence/data_models"

	"time"
)

type sqlVisibilityStoreImpl struct {
	session extensions2.SQLDBSession
	logger  log.Logger
}

func NewSqlVisibilityStore(sqlConfig config.SQL, logger log.Logger) (persistence.VisibilityStore, error) {
	session, err := extensions2.NewSQLSession(&sqlConfig)
	return &sqlVisibilityStoreImpl{
		session: session,
		logger:  logger,
	}, err
}

func (p sqlVisibilityStoreImpl) Close() error {
	return p.session.Close()
}

func (p sqlVisibilityStoreImpl) RecordProcessExecutionStatus(
	ctx context.Context, req data_models2.RecordProcessExecutionStatusRequest) error {
	if req.Status == data_models2.ProcessExecutionStatusUndefined {
		return fmt.Errorf("process status is undefined")
	}

	if req.Status == data_models2.ProcessExecutionStatusRunning {
		if req.StartTime == nil {
			return fmt.Errorf("start time is required for recording visibility for running process")
		}
		return p.session.InsertProcessExecutionStartForVisibility(ctx, extensions2.ExecutionVisibilityRow{
			Namespace:          req.Namespace,
			ProcessId:          req.ProcessId,
			ProcessExecutionId: req.ProcessExecutionId,
			ProcessTypeName:    req.ProcessType,
			Status:             req.Status,
			StartTime:          time.Unix(*req.StartTime, 0),
		})
	}
	return p.session.UpdateProcessExecutionStatusForVisibility(ctx, extensions2.ExecutionVisibilityRow{
		Namespace:          req.Namespace,
		ProcessId:          req.ProcessId,
		ProcessExecutionId: req.ProcessExecutionId,
		ProcessTypeName:    req.ProcessType,
		Status:             req.Status,
		CloseTime:          time.Unix(*req.CloseTime, 0),
	})
}

func (p sqlVisibilityStoreImpl) ListProcessExecutions(
	ctx context.Context, request xcapi.ListProcessExecutionsRequest) (*xcapi.ListProcessExecutionsResponse, error) {
	if request.Namespace == "" {
		return nil, fmt.Errorf("namespace is required for listing process executions")
	}
	if !request.HasStartTimeFilter() {
		return nil, fmt.Errorf("start time filter is required for listing process executions")
	}
	if !request.StartTimeFilter.HasEarliestTime() || !request.StartTimeFilter.HasLatestTime() {
		return nil, fmt.Errorf("both earliest and latest time are required for start time filter")
	}
	if request.HasProcessIdFilter() && request.HasProcessTypeFilter() {
		return nil, fmt.Errorf("process id and process type filter cannot be used together")
	}

	var paginationToken *data_models2.PaginationToken
	var err error
	if request.HasNextPageToken() {
		paginationToken, err = data_models2.ParsePaginationTokenFromString(*request.NextPageToken)
		if err != nil {
			return nil, err
		}
	}

	var lastProcessExecutionId string
	var lastStartTime int64
	lastProcessExecutionId = "00000000-0000-0000-0000-000000000000"
	if paginationToken != nil {
		lastProcessExecutionId = paginationToken.LastProcessExecutionId
		lastStartTime = paginationToken.LastStartTime
	}

	var processExecutionRows []extensions2.ExecutionVisibilityRow
	var processExecutionListInfo []xcapi.ProcessExecutionListInfo

	if !request.HasStatusFilter() && !request.HasProcessIdFilter() && !request.HasProcessTypeFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutions(
			ctx,
			request.Namespace,
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	} else if request.HasStatusFilter() && !request.HasProcessIdFilter() && !request.HasProcessTypeFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutionsByStatus(
			ctx,
			request.Namespace,
			data_models2.ParseProcessExecutionStatus(string(request.GetStatusFilter())),
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	} else if !request.HasStatusFilter() && request.HasProcessIdFilter() && !request.HasProcessTypeFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutionsById(
			ctx,
			request.Namespace,
			*request.ProcessIdFilter.ProcessId,
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	} else if !request.HasStatusFilter() && !request.HasProcessIdFilter() && request.HasProcessTypeFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutionsByTypeQuery(
			ctx,
			request.Namespace,
			request.ProcessTypeFilter.ProcessType,
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	} else if request.HasStatusFilter() && request.HasProcessIdFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutionsByStatusAndId(
			ctx,
			request.Namespace,
			data_models2.ParseProcessExecutionStatus(string(request.GetStatusFilter())),
			*request.ProcessIdFilter.ProcessId,
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	} else if request.HasStatusFilter() && request.HasProcessTypeFilter() {
		processExecutionRows, err = p.session.SelectProcessExecutionsByStatusAndType(
			ctx,
			request.Namespace,
			data_models2.ParseProcessExecutionStatus(string(request.GetStatusFilter())),
			request.ProcessTypeFilter.ProcessType,
			*request.StartTimeFilter.EarliestTime,
			*request.StartTimeFilter.LatestTime,
			lastProcessExecutionId,
			lastStartTime,
			request.PageSize)
	}

	if err != nil {
		return nil, err
	}

	processExecutionListInfo = make([]xcapi.ProcessExecutionListInfo, len(processExecutionRows))
	for i, row := range processExecutionRows {
		processExecutionListInfo[i] = xcapi.ProcessExecutionListInfo{
			Namespace:          ptr.Any(row.Namespace),
			ProcessId:          ptr.Any(row.ProcessId),
			ProcessExecutionId: ptr.Any(row.ProcessExecutionId.String()),
			ProcessType:        ptr.Any(row.ProcessTypeName),
			Status:             ptr.Any(xcapi.ProcessStatus(row.Status.String())),
			StartTimestamp:     ptr.Any(row.StartTime.Unix()),
			CloseTimestamp:     ptr.Any(row.CloseTime.Unix()),
		}
	}

	nextPaginationToken := data_models2.NewPaginationToken(
		processExecutionRows[len(processExecutionRows)-1].ProcessExecutionId.String(),
		processExecutionRows[len(processExecutionRows)-1].StartTime.Unix(),
	)
	nextPaginationTokenString, err := nextPaginationToken.String()
	if err != nil {
		return nil, err
	}

	return &xcapi.ListProcessExecutionsResponse{
		ProcessExecutions: processExecutionListInfo,
		NextPageToken:     ptr.Any(nextPaginationTokenString),
	}, nil
}

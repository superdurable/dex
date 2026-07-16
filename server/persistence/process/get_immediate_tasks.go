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

	"github.com/superdurable/dex/server/common/ptr"
	data_models2 "github.com/superdurable/dex/server/persistence/data_models"
)

func (p sqlProcessStoreImpl) GetImmediateTasks(
	ctx context.Context, request data_models2.GetImmediateTasksRequest,
) (*data_models2.GetImmediateTasksResponse, error) {
	immediateTasks, err := p.session.BatchSelectImmediateTasks(
		ctx, request.ShardId, request.StartSequenceInclusive, request.PageSize)
	if err != nil {
		return nil, err
	}
	var tasks []data_models2.ImmediateTask
	for _, t := range immediateTasks {
		info, err := data_models2.BytesToImmediateTaskInfo(t.Info)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, data_models2.ImmediateTask{
			ShardId:            request.ShardId,
			TaskSequence:       ptr.Any(t.TaskSequence),
			TaskType:           t.TaskType,
			ProcessExecutionId: t.ProcessExecutionId,
			StateExecutionId: data_models2.StateExecutionId{
				StateId:         t.StateId,
				StateIdSequence: t.StateIdSequence,
			},
			ImmediateTaskInfo: info,
		})
	}
	resp := &data_models2.GetImmediateTasksResponse{
		Tasks: tasks,
	}
	if len(immediateTasks) > 0 {
		firstTask := immediateTasks[0]
		lastTask := immediateTasks[len(immediateTasks)-1]
		resp.MinSequenceInclusive = firstTask.TaskSequence
		resp.MaxSequenceInclusive = lastTask.TaskSequence
	}
	return resp, nil
}

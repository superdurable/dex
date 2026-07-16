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

package data_models

import "github.com/xcherryio/apis/goapi/xcapi"

type GetTimerTasksForTimestampsRequest struct {
	// ShardId is the shardId in all DetailedRequests
	// just for convenience using xcapi.NotifyTimerTasksRequest which also has
	// the ShardId field, but the caller will ensure the ShardId is the same in all
	ShardId int32
	// MinSequenceInclusive is the minimum sequence required for the timer tasks to load
	// because the tasks with smaller sequence are already loaded
	MinSequenceInclusive int64
	// DetailedRequests is the list of NotifyTimerTasksRequest
	// which contains the fire timestamps and other info of all timer tasks to pull
	DetailedRequests []xcapi.NotifyTimerTasksRequest
}

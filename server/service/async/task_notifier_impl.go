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

package async

import (
	"fmt"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/engine"
)

type taskNotifierImpl struct {
	shardIdToImmediateTaskQueue map[int32]engine.ImmediateTaskQueue
	shardIdToTimerTaskQueue     map[int32]engine.TimerTaskQueue
}

func newTaskNotifierImpl() engine.TaskNotifier {
	return &taskNotifierImpl{
		shardIdToImmediateTaskQueue: make(map[int32]engine.ImmediateTaskQueue),
		shardIdToTimerTaskQueue:     make(map[int32]engine.TimerTaskQueue),
	}
}

func (t *taskNotifierImpl) NotifyNewImmediateTasks(request xcapi.NotifyImmediateTasksRequest) {
	queue, ok := t.shardIdToImmediateTaskQueue[request.ShardId]
	if !ok {
		panic(fmt.Sprintf("the shard %d is not registered", request.ShardId))
	}
	queue.TriggerPollingTasks(request)
}

func (t *taskNotifierImpl) NotifyNewTimerTasks(request xcapi.NotifyTimerTasksRequest) {
	queue, ok := t.shardIdToTimerTaskQueue[request.ShardId]
	if !ok {
		panic(fmt.Sprintf("the shard %d is not registered", request.ShardId))
	}
	queue.TriggerPollingTasks(request)
}

func (t *taskNotifierImpl) AddImmediateTaskQueue(shardId int32, queue engine.ImmediateTaskQueue) {
	_, ok := t.shardIdToImmediateTaskQueue[shardId]
	if ok {
		panic(fmt.Sprintf("the shard %d is already registered", shardId))
	}
	t.shardIdToImmediateTaskQueue[shardId] = queue
}

func (t *taskNotifierImpl) RemoveImmediateTaskQueue(shardId int32) {
	delete(t.shardIdToImmediateTaskQueue, shardId)
}

func (t *taskNotifierImpl) AddTimerTaskQueue(shardId int32, queue engine.TimerTaskQueue) {
	_, ok := t.shardIdToTimerTaskQueue[shardId]
	if ok {
		panic(fmt.Sprintf("the shard %d is already registered", shardId))
	}
	t.shardIdToTimerTaskQueue[shardId] = queue
}

func (t *taskNotifierImpl) RemoveTimerTaskQueue(shardId int32) {
	delete(t.shardIdToTimerTaskQueue, shardId)
}

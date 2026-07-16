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
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	engine2 "github.com/superdurable/dex/server/engine"
	"github.com/superdurable/dex/server/persistence"
	"go.uber.org/multierr"
)

type asyncService struct {
	rootCtx context.Context

	taskNotifier engine2.TaskNotifier

	// shardId: queue
	immediateTaskQueueMap map[int32]engine2.ImmediateTaskQueue
	// shardId: channelsPerShard
	waitForProcessCompletionChannelMap map[int32]engine2.WaitForProcessCompletionChannels

	immediateTaskProcessor engine2.ImmediateTaskProcessor

	// shardId: queue
	timerTaskQueueMap  map[int32]engine2.TimerTaskQueue
	timerTaskProcessor engine2.TimerTaskProcessor

	processStore persistence.ProcessStore

	cfg    config.Config
	logger log.Logger

	lock sync.RWMutex
}

func NewAsyncServiceImpl(
	rootCtx context.Context, processStore persistence.ProcessStore,
	visibilityStore persistence.VisibilityStore,
	cfg config.Config, logger log.Logger,
) Service {
	notifier := newTaskNotifierImpl()

	immediateTaskProcessor := engine2.NewImmediateTaskConcurrentProcessor(
		rootCtx, cfg, notifier, processStore, visibilityStore, logger)
	timerTaskProcessor := engine2.NewTimerTaskConcurrentProcessor(rootCtx, cfg, notifier, processStore, logger)

	return &asyncService{
		// to be dynamically initialized later
		immediateTaskQueueMap:              map[int32]engine2.ImmediateTaskQueue{},
		timerTaskQueueMap:                  map[int32]engine2.TimerTaskQueue{},
		waitForProcessCompletionChannelMap: map[int32]engine2.WaitForProcessCompletionChannels{},

		immediateTaskProcessor: immediateTaskProcessor,
		timerTaskProcessor:     timerTaskProcessor,

		taskNotifier: notifier,

		processStore: processStore,

		rootCtx: rootCtx,
		cfg:     cfg,
		logger:  logger,

		lock: sync.RWMutex{},
	}
}

func (a *asyncService) Start() error {
	err := a.immediateTaskProcessor.Start()
	if err != nil {
		a.logger.Error("fail to start immediate task processor", tag.Error(err))
		return err
	}
	err = a.timerTaskProcessor.Start()
	if err != nil {
		a.logger.Error("fail to start timer task processor", tag.Error(err))
		return err
	}

	// When in the standalone mode, need to manually re-balance once to create queues
	if a.cfg.AsyncService.Mode == config.AsyncServiceModeStandalone {
		a.ReBalance([]int32{0})
	}

	return nil
}

func (a *asyncService) NotifyPollingImmediateTask(req xcapi.NotifyImmediateTasksRequest) error {
	queue, ok := a.immediateTaskQueueMap[req.ShardId]
	if !ok {
		return fmt.Errorf("the shardId %v is not owned by this instance", req.ShardId)
	}

	queue.TriggerPollingTasks(req)
	return nil
}

func (a *asyncService) NotifyPollingTimerTask(req xcapi.NotifyTimerTasksRequest) error {
	queue, ok := a.timerTaskQueueMap[req.ShardId]
	if !ok {
		return fmt.Errorf("the shardId %v is not owned by this instance", req.ShardId)
	}

	queue.TriggerPollingTasks(req)
	return nil
}

func (a *asyncService) Stop(ctx context.Context) error {
	var errs []error

	errs = append(errs, a.immediateTaskProcessor.Stop(ctx))
	errs = append(errs, a.timerTaskProcessor.Stop(ctx))

	for _, immediateTaskQueue := range a.immediateTaskQueueMap {
		errs = append(errs, immediateTaskQueue.Stop(ctx))
	}

	for _, timerTaskQueue := range a.timerTaskQueueMap {
		errs = append(errs, timerTaskQueue.Stop(ctx))
	}

	return multierr.Combine(errs...)
}

func (a *asyncService) ReBalance(assignedShardIds []int32) {
	a.lock.Lock()
	defer a.lock.Unlock()

	// logging
	var oldShardIds []int
	for shardId := range a.immediateTaskQueueMap {
		oldShardIds = append(oldShardIds, int(shardId))
	}
	sort.Ints(oldShardIds)

	newShardsStr := ""
	oldShardStr := ""

	for _, shardId := range assignedShardIds {
		newShardsStr += " " + strconv.Itoa(int(shardId))
	}

	for _, shardId := range oldShardIds {
		oldShardStr += " " + strconv.Itoa(shardId)
	}

	a.logger.Info(fmt.Sprintf("ReBalance: %s -> %s", oldShardStr, newShardsStr))

	// execute
	assignedShardMap := map[int32]bool{}
	var currentShardsToRemove []int32

	for _, shardId := range assignedShardIds {
		assignedShardMap[shardId] = true
	}

	for shardId := range a.immediateTaskQueueMap {
		_, ok := assignedShardMap[shardId]
		if !ok {
			currentShardsToRemove = append(currentShardsToRemove, shardId)
		} else {
			delete(assignedShardMap, shardId)
		}
	}

	for _, shardToRemove := range currentShardsToRemove {
		a.stopQueuesAndRemove(shardToRemove)
		a.stopWaitingChannelsAndRemove(shardToRemove)
	}

	for shardId := range assignedShardMap {
		a.createQueuesAndStart(shardId)
		a.createWaitingChannelsAndStart(shardId)
	}

}

func (a *asyncService) createQueuesAndStart(shardId int32) {
	a.logger.Info(fmt.Sprintf("createQueuesAndStart: %d", shardId))

	// immediateTaskQueue
	immediateTaskQueue := engine2.NewImmediateTaskQueueImpl(
		a.rootCtx, shardId, a.cfg, a.processStore, a.immediateTaskProcessor, a.logger)

	a.taskNotifier.AddImmediateTaskQueue(shardId, immediateTaskQueue)
	a.immediateTaskQueueMap[shardId] = immediateTaskQueue

	err := immediateTaskQueue.Start()
	if err != nil {
		a.logger.Error(fmt.Sprintf("fail to start immediate task queue with shard %d", shardId), tag.Error(err))
	}

	// timerTaskQueue
	timerTaskQueue := engine2.NewTimerTaskQueueImpl(
		a.rootCtx, shardId, a.cfg, a.processStore, a.timerTaskProcessor, a.logger)

	a.taskNotifier.AddTimerTaskQueue(shardId, timerTaskQueue)
	a.timerTaskQueueMap[shardId] = timerTaskQueue

	err = timerTaskQueue.Start()
	if err != nil {
		a.logger.Error(fmt.Sprintf("fail to start timer task queue with shard %d", shardId), tag.Error(err))
	}
}

func (a *asyncService) stopQueuesAndRemove(shardId int32) {
	a.logger.Info(fmt.Sprintf("stopQueuesAndRemove: %d", shardId))

	// immediateTaskQueue
	immediateTaskQueue, ok := a.immediateTaskQueueMap[shardId]
	if !ok {
		a.logger.Error(fmt.Sprintf("fail to get immediate task queue with shard %d", shardId))
	} else {
		err := immediateTaskQueue.Stop(a.rootCtx)
		if err != nil {
			a.logger.Error(fmt.Sprintf("fail to stop immediate task queue with shard %d", shardId), tag.Error(err))
		}

		a.taskNotifier.RemoveImmediateTaskQueue(shardId)
		delete(a.immediateTaskQueueMap, shardId)
	}

	// timerTaskQueue
	timerTaskQueue, ok := a.timerTaskQueueMap[shardId]
	if !ok {
		a.logger.Error(fmt.Sprintf("fail to get timer task queue with shard %d", shardId))
	} else {
		err := timerTaskQueue.Stop(a.rootCtx)
		if err != nil {
			a.logger.Error(fmt.Sprintf("fail to stop timer task queue with shard %d", shardId), tag.Error(err))
		}

		a.taskNotifier.RemoveTimerTaskQueue(shardId)
		delete(a.timerTaskQueueMap, shardId)
	}
}

func (a *asyncService) createWaitingChannelsAndStart(shardId int32) {
	a.logger.Info(fmt.Sprintf("createWaitingChannelsAndStart: %d", shardId))

	a.waitForProcessCompletionChannelMap[shardId] = engine2.NewWaitForProcessCompletionChannelsPerShardImplImpl(
		shardId, a.logger, a.immediateTaskProcessor)

	a.waitForProcessCompletionChannelMap[shardId].Start()
}

func (a *asyncService) stopWaitingChannelsAndRemove(shardId int32) {
	a.logger.Info(fmt.Sprintf("stopWaitingChannelsAndRemove: %d", shardId))

	waitForProcessCompletionChannelsPerShard, ok := a.waitForProcessCompletionChannelMap[shardId]
	if !ok {
		a.logger.Error(fmt.Sprintf("fail to get process completion waiting channels with shard %d", shardId))
		return
	}

	waitForProcessCompletionChannelsPerShard.Stop()

	delete(a.waitForProcessCompletionChannelMap, shardId)
}

func (a *asyncService) NotifyRemoteImmediateTaskAsyncInCluster(req xcapi.NotifyImmediateTasksRequest, serverAddress string) {
	go func() {

		ctx, canf := context.WithTimeout(context.Background(), time.Second*10)
		defer canf()

		apiClient := xcapi.NewAPIClient(&xcapi.Configuration{
			Servers: []xcapi.ServerConfiguration{
				{
					URL: serverAddress,
				},
			},
		})

		request := apiClient.DefaultAPI.InternalApiV1XcherryNotifyImmediateTasksPost(ctx)
		httpResp, err := request.NotifyImmediateTasksRequest(req).Execute()
		if httpResp != nil {
			defer httpResp.Body.Close()
		}
		if err != nil {
			a.logger.Error("failed to notify remote immediate task in cluster", tag.Error(err))
			// TODO add backoff and retry
			return
		}
	}()
}

func (a *asyncService) NotifyRemoteTimerTaskAsyncInCluster(req xcapi.NotifyTimerTasksRequest, serverAddress string) {
	// execute in the background as best effort
	go func() {

		ctx, canf := context.WithTimeout(context.Background(), time.Second*10)
		defer canf()

		apiClient := xcapi.NewAPIClient(&xcapi.Configuration{
			Servers: []xcapi.ServerConfiguration{
				{
					URL: serverAddress,
				},
			},
		})

		request := apiClient.DefaultAPI.InternalApiV1XcherryNotifyTimerTasksPost(ctx)
		httpResp, err := request.NotifyTimerTasksRequest(req).Execute()
		if httpResp != nil {
			defer httpResp.Body.Close()
		}
		if err != nil {
			a.logger.Error("failed to notify remote timer task in cluster", tag.Error(err))
			// TODO add backoff and retry
			return
		}
	}()
}

func (a *asyncService) AskRemoteToWaitForProcessCompletionInCluster(
	ctx context.Context, req xcapi.WaitForProcessCompletionRequest, serverAddress string,
) (*xcapi.WaitForProcessCompletionResponse, error) {
	apiClient := xcapi.NewAPIClient(&xcapi.Configuration{
		Servers: []xcapi.ServerConfiguration{
			{
				URL: serverAddress,
			},
		},
	})

	request := apiClient.DefaultAPI.InternalApiV1XcherryWaitForProcessCompletionPost(ctx)
	resp, httpResp, err := request.WaitForProcessCompletionRequest(req).Execute()
	if httpResp != nil {
		defer httpResp.Body.Close()
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &xcapi.WaitForProcessCompletionResponse{
				Timeout: xcapi.PtrBool(true),
			}, nil
		}

		a.logger.Error("failed to ask to wait for process completion in cluster", tag.Error(err))
		// TODO add backoff and retry
		return nil, err
	}

	return resp, nil
}

func (a *asyncService) WaitForProcessCompletion(ctx context.Context, req xcapi.WaitForProcessCompletionRequest,
) (*xcapi.WaitForProcessCompletionResponse, error) {
	waitForProcessCompletionChannelsPerShard, ok := a.waitForProcessCompletionChannelMap[req.ShardId]
	if !ok {
		return nil, fmt.Errorf("the shardId %v is not owned by this instance", req.ShardId)
	}

	waitingChannel := waitForProcessCompletionChannelsPerShard.Add(req.ProcessExecutionId)

	select {
	case <-ctx.Done():
		waitForProcessCompletionChannelsPerShard, ok = a.waitForProcessCompletionChannelMap[req.ShardId]
		if !ok {
			return nil, fmt.Errorf("the shardId %v is not owned by this instance", req.ShardId)
		}

		waitForProcessCompletionChannelsPerShard.TerminateWaiting(req.ProcessExecutionId)

		return &xcapi.WaitForProcessCompletionResponse{
			Timeout: xcapi.PtrBool(true),
		}, nil
	case res := <-waitingChannel:
		if res == engine2.WaitForProcessCompletionResultStop {
			return &xcapi.WaitForProcessCompletionResponse{
				StopBySystem: xcapi.PtrBool(true),
			}, nil
		}

		return &xcapi.WaitForProcessCompletionResponse{
			Status: xcapi.ProcessStatus(res).Ptr(),
		}, nil
	}
}

// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package interpreter

import (
	"fmt"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

func SetQueryHandlers(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	timerProcessor interfaces.TimerProcessor,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	continueAsNewer *ContinueAsNewer,
	flowConfiger *config.FlowConfiger,
	basicInfo service.BasicInfo,
) error {
	err := provider.SetQueryHandler(ctx, service.GetAttributesWorkflowQueryType, func(req *iwfpb.GetAttributesQueryRequest) (*iwfpb.GetAttributesQueryResponse, error) {
		if req == nil {
			return nil, fmt.Errorf("GetAttributes query requires a request")
		}
		return persistenceManager.GetAttributes(req), nil
	})
	if err != nil {
		return err
	}
	err = continueAsNewer.SetQueryHandlersForContinueAsNew(ctx)
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(ctx, service.DebugDumpQueryType, func() (*iwfpb.DebugDumpResponse, error) {
		return &iwfpb.DebugDumpResponse{
			Config:                     flowConfiger.Get(),
			Snapshot:                   continueAsNewer.GetSnapshot(),
			FiringTimersUnixTimestamps: timerProcessor.GetTimerStartedUnixTimestamps(),
		}, nil
	})
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(ctx, service.PrepareRpcQueryType, func(req *iwfpb.PrepareRpcQueryRequest) (*iwfpb.PrepareRpcQueryResponse, error) {
		if req == nil {
			return nil, fmt.Errorf("PrepareRpc query requires a request")
		}
		if _, err := normalizeLockKeys(req.GetLockAttributeKeys()); err != nil {
			return nil, err
		}
		info := provider.GetWorkflowInfo(ctx)
		return &iwfpb.PrepareRpcQueryResponse{
			Attributes:           persistenceManager.GetAllAttributes(),
			RunId:                info.WorkflowExecution.RunID,
			FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
			FlowType:             basicInfo.FlowType,
			WorkerTarget:         basicInfo.WorkerTarget,
			ChannelInfos:         channelStore.GetInfos(),
		}, nil
	})
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(ctx, service.GetCurrentTimerInfosQueryType, func() (*iwfpb.GetCurrentTimerInfosQueryResponse, error) {
		timerInfos := make(map[string]*iwfpb.TimerInfoList, len(timerProcessor.GetTimerInfos()))
		for stepExecutionId, infos := range timerProcessor.GetTimerInfos() {
			timerInfos[stepExecutionId] = &iwfpb.TimerInfoList{Timers: infos}
		}
		return &iwfpb.GetCurrentTimerInfosQueryResponse{
			StepExecutionCurrentTimerInfos: timerInfos,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

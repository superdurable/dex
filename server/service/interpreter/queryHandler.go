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

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

func SetQueryHandlers(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	timerProcessor interfaces.TimerProcessor,
	persistenceManager *PersistenceManager,
	channelStore *ChannelStore,
	continueAsNewer *ContinueAsNewer,
	stepExecutionCounter *StepExecutionCounter,
	flowConfiger *config.FlowConfiger,
	basicInfo service.BasicInfo,
) error {
	err := provider.SetQueryHandler(ctx, service.GetAttributesWorkflowQueryType, func(req *dexpb.GetAttributesQueryRequest) (*dexpb.GetAttributesQueryResponse, error) {
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
	err = provider.SetQueryHandler(ctx, service.DebugDumpQueryType, func() (*dexpb.DebugDumpResponse, error) {
		return &dexpb.DebugDumpResponse{
			Config:                     flowConfiger.Get(),
			Snapshot:                   continueAsNewer.GetSnapshot(),
			FiringTimersUnixTimestamps: timerProcessor.GetTimerStartedUnixTimestamps(),
			ActiveStepExecutions:       continueAsNewer.GetActiveStepExecutionStates(),
		}, nil
	})
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(
		ctx,
		service.IsStepExecutionCompletedQueryType,
		func(stepType string, stepExecutionNumber int32) (bool, error) {
			if stepType == "" || stepExecutionNumber <= 0 {
				return false, fmt.Errorf("step type and positive execution number are required")
			}
			return stepExecutionCounter.IsStepExecutionCompleted(
				stepType,
				stepExecutionNumber,
			), nil
		},
	)
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(ctx, service.PrepareRpcQueryType, func(req *dexpb.PrepareRpcQueryRequest) (*dexpb.PrepareRpcQueryResponse, error) {
		if req == nil {
			return nil, fmt.Errorf("PrepareRpc query requires a request")
		}
		if _, err := normalizeLockKeys(req.GetLockAttributeKeys()); err != nil {
			return nil, err
		}
		info := provider.GetWorkflowInfo(ctx)
		return &dexpb.PrepareRpcQueryResponse{
			Attributes:           persistenceManager.GetAllAttributes(),
			RunId:                info.WorkflowExecution.RunID,
			FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
			FlowType:             basicInfo.FlowType,
			WorkerTarget:         flowConfiger.GetWorkerTarget(),
			ChannelInfos:         channelStore.GetInfos(),
		}, nil
	})
	if err != nil {
		return err
	}
	err = provider.SetQueryHandler(ctx, service.GetCurrentTimerInfosQueryType, func() (*dexpb.GetCurrentTimerInfosQueryResponse, error) {
		timerInfos := make(map[string]*dexpb.TimerInfoList, len(timerProcessor.GetTimerInfos()))
		for stepExecutionId, infos := range timerProcessor.GetTimerInfos() {
			timerInfos[stepExecutionId] = &dexpb.TimerInfoList{Timers: infos}
		}
		return &dexpb.GetCurrentTimerInfosQueryResponse{
			StepExecutionCurrentTimerInfos: timerInfos,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

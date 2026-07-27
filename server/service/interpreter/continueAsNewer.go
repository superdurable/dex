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
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"google.golang.org/protobuf/proto"
)

type ContinueAsNewer struct {
	provider interfaces.WorkflowProvider
	apiCfg   *config.ApiConfig

	StepExecutionToResumeMap map[string]*iwfpb.StepExecutionResumeInfo // stepExeId to StepExecutionResumeInfo
	inflightUpdateOperations int

	stepRequestQueue     *StepRequestQueue
	channelStore         *ChannelStore
	stepExecutionCounter *StepExecutionCounter
	persistenceManager   *PersistenceManager
	outputCollector      *OutputCollector
	timerProcessor       interfaces.TimerProcessor
}

func NewContinueAsNewer(
	apiCfg *config.ApiConfig,
	provider interfaces.WorkflowProvider,
	channelStore *ChannelStore, stepExecutionCounter *StepExecutionCounter,
	persistenceManager *PersistenceManager, stepRequestQueue *StepRequestQueue, collector *OutputCollector,
	timerProcessor interfaces.TimerProcessor,
) *ContinueAsNewer {
	if apiCfg == nil || provider == nil || stepRequestQueue == nil || channelStore == nil ||
		stepExecutionCounter == nil || persistenceManager == nil || collector == nil ||
		timerProcessor == nil {
		panic("ContinueAsNewer requires non-nil dependencies")
	}
	return &ContinueAsNewer{
		provider: provider,
		apiCfg:   apiCfg,

		StepExecutionToResumeMap: map[string]*iwfpb.StepExecutionResumeInfo{},

		stepRequestQueue:     stepRequestQueue,
		channelStore:         channelStore,
		stepExecutionCounter: stepExecutionCounter,
		persistenceManager:   persistenceManager,
		outputCollector:      collector,
		timerProcessor:       timerProcessor,
	}
}

func (i *Interpreter) LoadInternalsFromPreviousRun(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	previousRunId string,
	continueAsNewPageSizeInBytes int32,
) (*iwfpb.ContinueAsNewDump, error) {
	activityOptions := interfaces.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &iwfpb.RetryPolicy{MaximumIntervalSeconds: 5},
	}
	activityCfg := i.sharedConfig.Interpreter.InterpreterActivityConfig
	if activityConfig := activityCfg.DumpWorkflowInternalActivityConfig; activityConfig != nil {
		activityOptions.StartToCloseTimeout = activityConfig.StartToCloseTimeout
		if activityConfig.RetryPolicy != nil {
			activityOptions.RetryPolicy = activityConfig.RetryPolicy
		}
	}
	ctx = provider.WithActivityOptions(ctx, activityOptions)
	workflowId := provider.GetWorkflowInfo(ctx).WorkflowExecution.ID
	pageSize := continueAsNewPageSizeInBytes
	if pageSize == 0 {
		pageSize = service.DefaultContinueAsNewPageSizeInBytes
	}
	var wholeData []byte
	lastChecksum := ""
	pageNum := int32(0)
	for {
		var activityOutput iwfpb.DumpFlowForContinueAsNewActivityOutput
		err := provider.ExecuteLocalActivity(
			&activityOutput,
			ctx,
			i.activities.DumpFlowForContinueAsNew,
			&iwfpb.DumpFlowForContinueAsNewActivityInput{
				Request: &iwfpb.ContinueAsNewDumpRequest{
					FlowId:          workflowId,
					RunId:           previousRunId,
					PageNum:         pageNum,
					PageSizeInBytes: pageSize,
				},
			},
		)
		if err != nil {
			return nil, err
		}
		if lastChecksum != "" && lastChecksum != activityOutput.Response.GetChecksum() {
			// reset to start from beginning
			pageNum = 0
			wholeData = nil
			provider.GetLogger(ctx).Error(
				"checksum has changed during the loading",
				lastChecksum,
				activityOutput.Response.GetChecksum(),
			)
			lastChecksum = ""
			continue
		}
		lastChecksum = activityOutput.Response.GetChecksum()
		wholeData = append(wholeData, activityOutput.Response.GetPageContent()...)
		pageNum++
		if pageNum >= activityOutput.Response.GetTotalPages() {
			break
		}
	}

	var resp iwfpb.ContinueAsNewDump
	if err := proto.Unmarshal(wholeData, &resp); err != nil {
		return nil, provider.NewWorkflowError(
			iwfpb.FlowErrorType_FLOW_ERROR_TYPE_SERVER_INTERNAL,
			fmt.Errorf("unmarshal continue-as-new dump: %w", err))
	}
	return &resp, nil
}

func (c *ContinueAsNewer) GetSnapshot() *iwfpb.ContinueAsNewDump {
	localStepExecutionToResumeMap := map[string]*iwfpb.StepExecutionResumeInfo{}
	for _, key := range DeterministicKeys(c.StepExecutionToResumeMap) {
		localStepExecutionToResumeMap[key] = c.StepExecutionToResumeMap[key]
	}
	// NOTE: there could be more resume from even more previous run that hasn't started yet
	for key, value := range c.stepRequestQueue.GetAllStepResumeRequests() {
		localStepExecutionToResumeMap[key] = value
	}
	var stepExecutionsToResume []*iwfpb.StepExecutionResumeInfo
	for _, key := range DeterministicKeys(localStepExecutionToResumeMap) {
		stepExecutionsToResume = append(
			stepExecutionsToResume,
			localStepExecutionToResumeMap[key],
		)
	}
	return &iwfpb.ContinueAsNewDump{
		ChannelReceived:           c.channelStore.GetAllReceived(),
		CounterInfo:               c.stepExecutionCounter.Dump(),
		Attributes:                c.persistenceManager.GetAllAttributes(),
		StepsToStartFromBeginning: c.stepRequestQueue.GetAllStepStartRequests(),
		StepExecutionsToResume:    stepExecutionsToResume,
		StepOutputs:               c.outputCollector.GetAll(),
		StaleSkipTimers:           c.timerProcessor.Dump(),
	}
}

func (c *ContinueAsNewer) SetQueryHandlersForContinueAsNew(
	ctx interfaces.UnifiedContext,
) error {
	return c.provider.SetQueryHandler(
		ctx,
		service.ContinueAsNewDumpByPageQueryType,
		// return the current page of the whole snapshot
		func(request *iwfpb.ContinueAsNewDumpRequest) (*iwfpb.ContinueAsNewDumpResponse, error) {
			if request == nil {
				return nil, fmt.Errorf("continue-as-new dump request is nil")
			}
			pageSize := int32(service.DefaultContinueAsNewPageSizeInBytes)
			if request.GetPageSizeInBytes() > 0 {
				pageSize = request.GetPageSizeInBytes()
			}
			maxPageSize := c.apiCfg.EffectiveGrpcMaxMessageBytes()
			if int(pageSize) > maxPageSize {
				return nil, fmt.Errorf("page size must be at most %d bytes", maxPageSize)
			}
			if request.GetPageNum() < 0 {
				return nil, fmt.Errorf("page number must be non-negative")
			}

			wholeSnapshot := c.GetSnapshot()
			wholeData, err := proto.MarshalOptions{Deterministic: true}.Marshal(wholeSnapshot)
			if err != nil {
				return nil, fmt.Errorf("marshal continue-as-new dump: %w", err)
			}
			checksum := md5.Sum(wholeData)
			totalPages := int32((len(wholeData) + int(pageSize) - 1) / int(pageSize))
			if totalPages == 0 {
				totalPages = 1
			}
			if request.GetPageNum() >= totalPages {
				return nil, fmt.Errorf("page number %d is out of range", request.GetPageNum())
			}
			start := int(request.GetPageNum() * pageSize)
			end := start + int(pageSize)
			if end > len(wholeData) {
				end = len(wholeData)
			}
			return &iwfpb.ContinueAsNewDumpResponse{
				PageContent: wholeData[start:end],
				PageNum:     request.GetPageNum(),
				TotalPages:  totalPages,
				Checksum:    hex.EncodeToString(checksum[:]),
			}, nil
		},
	)
}

func (c *ContinueAsNewer) AddPotentialStepExecutionToResume(
	resumeInfo *iwfpb.StepExecutionResumeInfo,
) {
	if resumeInfo == nil || resumeInfo.GetStepExecutionId() == "" {
		panic("step resume info requires an execution ID")
	}
	c.StepExecutionToResumeMap[resumeInfo.GetStepExecutionId()] = resumeInfo
}

func (c *ContinueAsNewer) HasAnyStepExecutionToResume() bool {
	return len(c.StepExecutionToResumeMap) > 0
}

func (c *ContinueAsNewer) RemoveStepExecutionToResume(executionId string) {
	delete(c.StepExecutionToResumeMap, executionId)
}

func (c *ContinueAsNewer) DrainThreads(ctx interfaces.UnifiedContext) error {
	// TODO: add metric for before and after Await to monitor stuck
	// NOTE: consider using AwaitWithTimeout to get an alert when workflow stuck due to a bug in the draining logic for continueAsNew

	errWait := c.provider.Await(ctx, func() bool {
		return c.allThreadsDrained(ctx)
	})
	c.provider.GetLogger(ctx).Info("done draining threads for continueAsNew", errWait)

	return errWait
}

func (c *ContinueAsNewer) IncreaseInflightOperation() {
	c.inflightUpdateOperations++
}

func (c *ContinueAsNewer) DecreaseInflightOperation() {
	c.inflightUpdateOperations--
}

// if the DrainThreads await is being called more than a few times and cannot get through,
// there is likely something wrong in the continueAsNew logic (unless worker API is stuck)
// the key is runId, the value is how many times it has been called in this worker
// Using this in memory counter sot hat we don't have to use AwaitWithTimeout which will consume a timer
// TODO add TTL support because we don't have to keep the value in memory forever(likely a few hours or a day is enough)
var inMemoryContinueAsNewMonitor = make(map[string]time.Time)

const warnThreshold = time.Second * 5
const errThreshold = time.Second * 15

func (c *ContinueAsNewer) allThreadsDrained(ctx interfaces.UnifiedContext) bool {
	runId := c.provider.GetWorkflowInfo(ctx).WorkflowExecution.RunID

	remainingThreadCount := c.provider.GetThreadCount()
	if remainingThreadCount == 0 && c.inflightUpdateOperations == 0 {
		delete(inMemoryContinueAsNewMonitor, runId)
		return true
	}

	c.provider.GetLogger(ctx).Debug("continueAsNew is in draining remainingThreadCount, attempt, threadNames, inflightUpdateOperations",
		remainingThreadCount, inMemoryContinueAsNewMonitor[runId], c.provider.GetPendingThreadNames(), c.inflightUpdateOperations)

	// TODO using a flag to control this debugging info
	initTime, ok := inMemoryContinueAsNewMonitor[runId]
	if !ok {
		inMemoryContinueAsNewMonitor[runId] = time.Now()
		return false
	}

	elapsed := time.Since(initTime)

	if elapsed >= errThreshold {
		c.provider.GetLogger(ctx).Warn(
			"continueAsNew is likely stuck (unless worker API is stuck) in draining remainingThreadCount, attempt, threadNames, inflightUpdateOperations",
			remainingThreadCount, inMemoryContinueAsNewMonitor[runId], c.provider.GetPendingThreadNames(), c.inflightUpdateOperations)
		return false
	}
	if elapsed >= warnThreshold {
		c.provider.GetLogger(ctx).Warn(
			"continueAsNew may be stuck (unless worker API is stuck) in draining remainingThreadCount, attempt, threadNames, inflightUpdateOperations",
			remainingThreadCount, inMemoryContinueAsNewMonitor[runId], c.provider.GetPendingThreadNames(), c.inflightUpdateOperations)
	}
	return false
}

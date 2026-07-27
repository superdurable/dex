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
	"strconv"
	"time"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/event"
	"github.com/superdurable/iwf/service/common/utils"
	"github.com/superdurable/iwf/service/interpreter/cont"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type WorkflowUpdater struct {
	activities           *Activities
	apiCfg               *config.ApiConfig
	ctx                  interfaces.UnifiedContext
	persistenceManager   *PersistenceManager
	provider             interfaces.WorkflowProvider
	continueAsNewer      *ContinueAsNewer
	continueAsNewCounter *cont.ContinueAsNewCounter
	channelStore         *ChannelStore
	signalReceiver       *SignalReceiver
	stepRequestQueue     *StepRequestQueue
	stepExecutionCounter *StepExecutionCounter
	basicInfo            service.BasicInfo
}

type stepCompletionWait struct {
	updater             *WorkflowUpdater
	request             *iwfpb.WaitForStepCompletionRequest
	deadline            time.Time
	stepExecutionNumber int32
	matched             bool
}

type attributeWait struct {
	updater  *WorkflowUpdater
	request  *iwfpb.WaitForAttributeRequest
	deadline time.Time
	matched  bool
	matchErr error
}

func NewWorkflowUpdater(
	apiCfg *config.ApiConfig,
	activities *Activities,
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	persistenceManager *PersistenceManager,
	stepRequestQueue *StepRequestQueue,
	continueAsNewer *ContinueAsNewer,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	channelStore *ChannelStore,
	signalReceiver *SignalReceiver,
	stepExecutionCounter *StepExecutionCounter,
	basicInfo service.BasicInfo,
) error {
	if apiCfg == nil || activities == nil || provider == nil ||
		persistenceManager == nil || stepRequestQueue == nil ||
		continueAsNewer == nil ||
		continueAsNewCounter == nil || channelStore == nil ||
		signalReceiver == nil || stepExecutionCounter == nil {
		panic("WorkflowUpdater requires non-nil dependencies")
	}
	updater := &WorkflowUpdater{
		activities:           activities,
		apiCfg:               apiCfg,
		ctx:                  ctx,
		persistenceManager:   persistenceManager,
		provider:             provider,
		continueAsNewer:      continueAsNewer,
		continueAsNewCounter: continueAsNewCounter,
		channelStore:         channelStore,
		signalReceiver:       signalReceiver,
		stepRequestQueue:     stepRequestQueue,
		stepExecutionCounter: stepExecutionCounter,
		basicInfo:            basicInfo,
	}
	if err := provider.SetInvokeRPCUpdateHandler(
		ctx,
		updater.workerRpcValidator,
		updater.workerRpcHandler,
	); err != nil {
		return err
	}
	if err := provider.SetWaitForStepCompletionUpdateHandler(
		ctx,
		updater.validateWaitForStepCompletion,
		updater.waitForStepCompletion,
	); err != nil {
		return err
	}
	if err := provider.SetWaitForAttributeUpdateHandler(
		ctx,
		updater.validateWaitForAttribute,
		updater.waitForAttribute,
	); err != nil {
		return err
	}
	return nil
}

func (u *WorkflowUpdater) workerRpcHandler(
	ctx interfaces.UnifiedContext,
	input *iwfpb.InvokeRPCRequest,
) (output *iwfpb.InvokeRpcUpdateResult, err error) {
	u.continueAsNewer.IncreaseInflightOperation()
	defer u.continueAsNewer.DecreaseInflightOperation()

	info := u.provider.GetWorkflowInfo(ctx)
	rpcExecutionStartTime := u.provider.Now(ctx).UnixMilli()
	defer func() {
		if !u.provider.IsReplaying(ctx) {
			event.Handle(event.Event{
				FlowId:             info.WorkflowExecution.ID,
				RunId:              info.WorkflowExecution.RunID,
				FlowType:           u.basicInfo.FlowType,
				RpcName:            input.GetRpcName(),
				EventType:          "RPC_EXECUTION",
				StartTimestampInMs: rpcExecutionStartTime,
				Attributes:         u.persistenceManager.GetAllAttributes(),
			})
		}
	}()

	keysToLock, err := normalizeLockKeys(input.GetLockAttributeKeys())
	if err != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	attributes, err := u.persistenceManager.LoadAttributes(ctx, keysToLock)
	if err != nil {
		return nil, err
	}

	rpcPrep := &iwfpb.PrepareRpcQueryResponse{
		Attributes:           attributes,
		RunId:                info.WorkflowExecution.RunID,
		FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
		FlowType:             u.basicInfo.FlowType,
		WorkerTarget:         u.basicInfo.WorkerTarget,
		ChannelInfos:         u.channelStore.GetInfos(),
	}
	budget := u.effectiveRPCBudget(input.GetTimeoutSeconds())
	activityOptions := interfaces.ActivityOptions{
		StartToCloseTimeout:                 budget,
		LocalActivityScheduleToCloseTimeout: budget,
		RetryPolicy: &iwfpb.RetryPolicy{
			MaximumAttempts: maxWorkerRpcActivityAttempts,
		},
	}
	ctx = u.provider.WithActivityOptions(ctx, activityOptions)
	var activityOutput iwfpb.InvokeWorkerRPCActivityOutput
	err = u.provider.ExecuteLocalActivity(
		&activityOutput,
		ctx,
		u.activities.InvokeWorkerRPC,
		&iwfpb.InvokeWorkerRPCActivityInput{
			RpcPrep: rpcPrep,
			Request: input,
		},
	)
	u.persistenceManager.UnlockKeys(keysToLock)
	if err != nil {
		// logging only -- intentionally do not return an error -- error will fail the workflow
		u.provider.GetLogger(ctx).Error("activity invocation failure", "error", err)
	}
	response, interpreterErr, err := rpcActivityResponse(&activityOutput)
	if err != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_SERVER_INTERNAL,
			err.Error(),
		)
	}
	if interpreterErr != nil {
		return &iwfpb.InvokeRpcUpdateResult{Error: interpreterErr}, nil
	}
	if err := validateLockedRPCWrites(response.GetUpsertAttributes(), keysToLock); err != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
			err.Error(),
		)
	}

	decision := response.GetStepDecision()
	err = u.persistenceManager.ApplyAttributeWrites(
		ctx,
		response.GetUpsertAttributes(),
	)
	if err != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_SERVER_INTERNAL,
			err.Error(),
		)
	}
	u.channelStore.ProcessPublishing(response.GetPublishToChannel())
	u.stepRequestQueue.AddStepStartRequests(decision.GetNextSteps())
	u.continueAsNewCounter.IncSyncUpdateReceived()
	return &iwfpb.InvokeRpcUpdateResult{
		Response: &iwfpb.InvokeRPCResponse{Output: response.GetOutput()},
	}, nil
}

func (u *WorkflowUpdater) workerRpcValidator(
	_ interfaces.UnifiedContext,
	input *iwfpb.InvokeRPCRequest,
) error {
	if input == nil || input.GetRpcName() == "" {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"RPC name is required",
		)
	}
	if input.GetTimeoutSeconds() < 0 {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"RPC timeout must be non-negative",
		)
	}
	keys, err := normalizeLockKeys(input.GetLockAttributeKeys())
	if err != nil {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	if len(keys) == 0 {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"locking RPC requires attribute keys",
		)
	}
	if !u.persistenceManager.CanLockKeys(keys) {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE,
			"one or more attribute keys are locked",
		)
	}
	return nil
}

func (u *WorkflowUpdater) effectiveRPCBudget(requestedSeconds int32) time.Duration {
	maximumSeconds := u.apiCfg.EffectiveMaxWaitSeconds()
	if requestedSeconds > 0 && int64(requestedSeconds) < maximumSeconds {
		maximumSeconds = int64(requestedSeconds)
	}
	return time.Duration(maximumSeconds) * time.Second
}

func rpcActivityResponse(
	output *iwfpb.InvokeWorkerRPCActivityOutput,
) (*iwfpb.InvokeWorkerRPCResponse, *iwfpb.InterpreterError, error) {
	if (output.GetResponse() == nil) == (output.GetError() == nil) {
		return nil, nil, fmt.Errorf("RPC activity returned an invalid result envelope")
	}
	if output.GetError() != nil {
		return nil, output.GetError(), nil
	}
	return output.GetResponse(), nil, nil
}

func validateLockedRPCWrites(
	writes []*iwfpb.AttributeWrite,
	sortedLockKeys []string,
) error {
	allowed := make(map[string]struct{}, len(sortedLockKeys))
	for _, key := range sortedLockKeys {
		allowed[key] = struct{}{}
	}
	for _, write := range writes {
		if write == nil {
			return fmt.Errorf("RPC returned a nil attribute write")
		}
		if _, ok := allowed[write.GetKey()]; !ok {
			return fmt.Errorf("RPC wrote unlocked attribute key %q", write.GetKey())
		}
	}
	return nil
}

func normalizeLockKeys(keys []string) ([]string, error) {
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("lock attribute key is empty")
		}
	}
	return sortedUniqueStrings(keys), nil
}

func (u *WorkflowUpdater) validateWaitForStepCompletion(
	_ interfaces.UnifiedContext,
	request *iwfpb.WaitForStepCompletionRequest,
) error {
	if request == nil {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"request is nil",
		)
	}
	if request.GetWaitTimeSeconds() < 0 {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"wait time must be non-negative",
		)
	}
	if request.GetStepType() == "" || request.GetStepExecutionNumber() == "" {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"step type and step execution number are required",
		)
	}
	_, err := parseWaitForStepExecutionNumber(
		request.GetStepExecutionNumber(),
	)
	if err != nil {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	return nil
}

func (u *WorkflowUpdater) waitForStepCompletion(
	ctx interfaces.UnifiedContext,
	request *iwfpb.WaitForStepCompletionRequest,
) (*iwfpb.WaitForStepCompletionResponse, error) {
	u.continueAsNewer.IncreaseInflightOperation()
	defer u.continueAsNewer.DecreaseInflightOperation()
	stepExecutionNumber, err := parseWaitForStepExecutionNumber(
		request.GetStepExecutionNumber(),
	)
	if err != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	wait := &stepCompletionWait{
		updater:             u,
		request:             request,
		deadline:            workflowDeadline(u.provider.Now(ctx), request.GetWaitTimeSeconds()),
		stepExecutionNumber: stepExecutionNumber,
	}
	if !wait.ready() && request.GetWaitTimeSeconds() > 0 {
		if err := u.provider.Await(ctx, wait.ready); err != nil {
			return nil, err
		}
	}
	if wait.matched {
		return &iwfpb.WaitForStepCompletionResponse{}, nil
	}
	if deadlinePassed(u.provider.Now(ctx), wait.deadline) ||
		request.GetWaitTimeSeconds() == 0 {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED,
			"step completion wait timed out",
		)
	}
	return nil, u.provider.NewUpdateError(
		iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED,
		"continue-as-new preempted wait",
	)
}

func (w *stepCompletionWait) ready() bool {
	w.matched = w.updater.stepExecutionCounter.IsStepExecutionCompleted(
		w.request.GetStepType(),
		w.stepExecutionNumber,
	)
	return w.matched ||
		w.updater.continueAsNewCounter.IsThresholdMet() ||
		deadlinePassed(w.updater.provider.Now(w.updater.ctx), w.deadline)
}

func parseWaitForStepExecutionNumber(value string) (int32, error) {
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("step execution number must be a positive integer")
	}
	return int32(number), nil
}

func (u *WorkflowUpdater) validateWaitForAttribute(
	_ interfaces.UnifiedContext,
	request *iwfpb.WaitForAttributeRequest,
) error {
	if request == nil || request.GetCondition() == nil {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"attribute condition is required",
		)
	}
	if request.GetWaitTimeSeconds() < 0 {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"wait time must be non-negative",
		)
	}
	equal, ok := request.GetCondition().GetKind().(*iwfpb.WaitForAttributeCondition_Equal)
	if !ok || equal.Equal == nil || equal.Equal.GetKey() == "" ||
		equal.Equal.GetValue() == nil || equal.Equal.GetValue().GetKind() == nil {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"valid attribute equality is required",
		)
	}
	// TODO: hydrate blob-backed attributes deterministically without losing concurrent writes.
	if isBlobValue(equal.Equal.GetValue()) {
		return u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
			"blob-backed WaitForAttribute values are not supported",
		)
	}
	return nil
}

func (u *WorkflowUpdater) waitForAttribute(
	ctx interfaces.UnifiedContext,
	request *iwfpb.WaitForAttributeRequest,
) (*emptypb.Empty, error) {
	u.continueAsNewer.IncreaseInflightOperation()
	defer u.continueAsNewer.DecreaseInflightOperation()
	wait := &attributeWait{
		updater:  u,
		request:  request,
		deadline: workflowDeadline(u.provider.Now(ctx), request.GetWaitTimeSeconds()),
	}
	if !wait.ready() && request.GetWaitTimeSeconds() > 0 {
		if err := u.provider.Await(ctx, wait.ready); err != nil {
			return nil, err
		}
	}
	if wait.matchErr != nil {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
			wait.matchErr.Error(),
		)
	}
	if wait.matched {
		return &emptypb.Empty{}, nil
	}
	if deadlinePassed(u.provider.Now(ctx), wait.deadline) ||
		request.GetWaitTimeSeconds() == 0 {
		return nil, u.provider.NewUpdateError(
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED,
			"attribute wait timed out",
		)
	}
	return nil, u.provider.NewUpdateError(
		iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED,
		"continue-as-new preempted wait",
	)
}

func (w *attributeWait) ready() bool {
	w.matched, w.matchErr = w.updater.attributeMatches(w.request)
	return w.matched ||
		w.matchErr != nil ||
		w.updater.continueAsNewCounter.IsThresholdMet() ||
		deadlinePassed(w.updater.provider.Now(w.updater.ctx), w.deadline)
}

func (u *WorkflowUpdater) attributeMatches(
	request *iwfpb.WaitForAttributeRequest,
) (bool, error) {
	equal := request.GetCondition().GetEqual()
	current, exists := u.persistenceManager.GetAttribute(equal.GetKey())
	if utils.IsNullValue(equal.GetValue()) {
		return !exists || utils.IsNullValue(current), nil
	}
	if !exists {
		return false, nil
	}
	if isBlobValue(current) {
		return false, fmt.Errorf("stored attribute %q is blob-backed", equal.GetKey())
	}
	return attributeValuesEqual(current, equal.GetValue()), nil
}

func isBlobValue(value *iwfpb.Value) bool {
	if value == nil {
		return false
	}
	switch value.GetKind().(type) {
	case *iwfpb.Value_InternalBlobIdForStringValue,
		*iwfpb.Value_InternalBlobIdForObjValue:
		return true
	default:
		return false
	}
}

func workflowDeadline(start time.Time, timeoutSeconds int32) time.Time {
	if timeoutSeconds <= 0 {
		return start
	}
	return start.Add(time.Duration(timeoutSeconds) * time.Second)
}

func deadlinePassed(now, deadline time.Time) bool {
	return now.After(deadline)
}

func attributeValuesEqual(left, right *iwfpb.Value) bool {
	return proto.Equal(left, right)
}

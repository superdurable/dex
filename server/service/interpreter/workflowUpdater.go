// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"fmt"
	"strconv"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/utils"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
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
	terminalCoordinator  *TerminalCoordinator
	stepRequestQueue     *StepRequestQueue
	stepExecutionCounter *StepExecutionCounter
	flowConfiger         *interpreterconfig.FlowConfiger
	basicInfo            service.BasicInfo
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
	terminalCoordinator *TerminalCoordinator,
	stepExecutionCounter *StepExecutionCounter,
	flowConfiger *interpreterconfig.FlowConfiger,
	basicInfo service.BasicInfo,
) error {
	if apiCfg == nil || activities == nil || provider == nil ||
		persistenceManager == nil || stepRequestQueue == nil ||
		continueAsNewer == nil ||
		continueAsNewCounter == nil || channelStore == nil ||
		signalReceiver == nil || terminalCoordinator == nil ||
		stepExecutionCounter == nil || flowConfiger == nil {
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
		terminalCoordinator:  terminalCoordinator,
		stepRequestQueue:     stepRequestQueue,
		stepExecutionCounter: stepExecutionCounter,
		flowConfiger:         flowConfiger,
		basicInfo:            basicInfo,
	}
	if err := provider.SetInvokeRPCUpdateHandler(
		ctx,
		updater.validateWorkerRpc,
		updater.handleWorkerRpc,
	); err != nil {
		return err
	}
	if err := provider.SetWaitForStepCompletionUpdateHandler(
		ctx,
		updater.validateWaitForStepCompletion,
		updater.handleWaitForStepCompletion,
	); err != nil {
		return err
	}
	if err := provider.SetWaitForAttributeUpdateHandler(
		ctx,
		updater.validateWaitForAttribute,
		updater.handleWaitForAttribute,
	); err != nil {
		return err
	}
	return nil
}

type stepCompletionWait struct {
	updater             *WorkflowUpdater
	request             *dexpb.WaitForStepCompletionRequest
	deadline            time.Time
	stepExecutionNumber int32
	matched             bool
}

type attributeWait struct {
	updater  *WorkflowUpdater
	request  *dexpb.WaitForAttributeRequest
	deadline time.Time
	matched  bool
	matchErr error
}

func (u *WorkflowUpdater) handleWorkerRpc(
	ctx interfaces.UnifiedContext,
	input *dexpb.InvokeRPCRequest,
) (output *dexpb.InvokeRpcUpdateResult, err error) {
	u.continueAsNewer.IncreaseInflightOperation()
	defer u.continueAsNewer.DecreaseInflightOperation()
	u.signalReceiver.drainReceivedRpcSignals(ctx)

	info := u.provider.GetWorkflowInfo(ctx)
	rpcExecutionStartTime := u.provider.Now(ctx).UnixMilli()
	defer func() {
		if !u.provider.IsReplaying(ctx) {
			event.Handle(event.Event{
				FlowId:             info.WorkflowExecution.ID,
				RunId:              info.WorkflowExecution.RunID,
				FlowType:           u.basicInfo.FlowType,
				RpcName:            input.GetRpcName(),
				EventType:          event.EventTypeRPCExecution,
				StartTimestampInMs: rpcExecutionStartTime,
				Attributes:         u.persistenceManager.GetAllAttributes(),
			})
		}
	}()

	keysToLock, err := normalizeLockKeys(input.GetLockAttributeKeys())
	if err != nil {
		return nil, u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	attributes, err := u.persistenceManager.LoadAttributes(ctx, keysToLock)
	if err != nil {
		return nil, err
	}

	rpcPrep := &dexpb.PrepareRpcQueryResponse{
		Attributes:           attributes,
		RunId:                info.WorkflowExecution.RunID,
		FlowStartedTimestamp: info.WorkflowStartTime.Unix(),
		FlowType:             u.basicInfo.FlowType,
		WorkerTarget:         u.flowConfiger.GetWorkerTarget(),
		ChannelInfos:         u.channelStore.GetInfos(),
	}
	budget := u.effectiveRPCBudget(input.GetTimeoutSeconds())
	activityOptions := interfaces.ActivityOptions{
		StartToCloseTimeout:                 budget,
		LocalActivityScheduleToCloseTimeout: budget,
		RetryPolicy: &dexpb.RetryPolicy{
			MaximumAttempts: maxWorkerRpcActivityAttempts,
		},
	}
	ctx = u.provider.WithActivityOptions(ctx, activityOptions)
	var activityOutput dexpb.InvokeWorkerRPCActivityOutput
	err = u.provider.ExecuteLocalActivity(
		&activityOutput,
		ctx,
		u.activities.InvokeWorkerRPC,
		&dexpb.InvokeWorkerRPCActivityInput{
			RpcPrep: rpcPrep,
			Request: input,
		},
	)
	u.persistenceManager.UnlockKeys(keysToLock)
	if err != nil {
		return nil, err
	}
	response := activityOutput.GetResponse()
	decision := response.GetStepDecision()
	err = u.persistenceManager.ApplyAttributeWrites(
		ctx,
		response.GetUpsertAttributes(),
	)
	if err != nil {
		return nil, err
	}
	u.channelStore.ProcessPublishing(response.GetPublishToChannel())
	u.stepRequestQueue.AddStepStartRequests(decision.GetNextSteps())
	u.continueAsNewCounter.IncSyncUpdateReceived()
	return &dexpb.InvokeRpcUpdateResult{
		Response:         &dexpb.InvokeRPCResponse{Output: response.GetOutput()},
		StepDecision:     response.GetStepDecision(),
		UpsertAttributes: response.GetUpsertAttributes(),
		RecordEvents:     response.GetRecordEvents(),
		PublishToChannel: response.GetPublishToChannel(),
	}, nil
}

func (u *WorkflowUpdater) validateWorkerRpc(
	_ interfaces.UnifiedContext,
	input *dexpb.InvokeRPCRequest,
) error {
	if err := u.rejectTerminalUpdate(); err != nil {
		return err
	}
	if input == nil || input.GetRpcName() == "" {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"RPC name is required",
		)
	}
	if input.GetTimeoutSeconds() < 0 {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"RPC timeout must be non-negative",
		)
	}
	keys, err := normalizeLockKeys(input.GetLockAttributeKeys())
	if err != nil {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	if !u.persistenceManager.CanLockKeys(keys) {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE,
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
	request *dexpb.WaitForStepCompletionRequest,
) error {
	if err := u.rejectTerminalUpdate(); err != nil {
		return err
	}
	if request == nil {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"request is nil",
		)
	}
	if request.GetWaitTimeSeconds() < 0 {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"wait time must be non-negative",
		)
	}
	if request.GetStepType() == "" || request.GetStepExecutionNumber() == "" {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"step type and step execution number are required",
		)
	}
	_, err := parseWaitForStepExecutionNumber(
		request.GetStepExecutionNumber(),
	)
	if err != nil {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			err.Error(),
		)
	}
	return nil
}

func (u *WorkflowUpdater) handleWaitForStepCompletion(
	ctx interfaces.UnifiedContext,
	request *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
	u.continueAsNewer.IncreaseInflightOperation()
	defer u.continueAsNewer.DecreaseInflightOperation()
	stepExecutionNumber, err := parseWaitForStepExecutionNumber(
		request.GetStepExecutionNumber(),
	)
	if err != nil {
		return nil, u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
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
		return &dexpb.WaitForStepCompletionResponse{}, nil
	}
	if deadlinePassed(u.provider.Now(ctx), wait.deadline) ||
		request.GetWaitTimeSeconds() == 0 {
		return nil, u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED,
			"step completion wait timed out",
		)
	}
	return nil, u.provider.NewUpdateError(
		dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED,
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
	request *dexpb.WaitForAttributeRequest,
) error {
	if err := u.rejectTerminalUpdate(); err != nil {
		return err
	}
	if request == nil || request.GetCondition() == nil {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"attribute condition is required",
		)
	}
	if request.GetWaitTimeSeconds() < 0 {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"wait time must be non-negative",
		)
	}
	equal, ok := request.GetCondition().GetKind().(*dexpb.WaitForAttributeCondition_Equal)
	if !ok || equal.Equal == nil || equal.Equal.GetKey() == "" ||
		equal.Equal.GetValue() == nil || equal.Equal.GetValue().GetKind() == nil {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT,
			"valid attribute equality is required",
		)
	}
	// TODO: hydrate blob-backed attributes deterministically without losing concurrent writes.
	if isBlobValue(equal.Equal.GetValue()) {
		return u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
			"blob-backed WaitForAttribute values are not supported",
		)
	}
	return nil
}

func (u *WorkflowUpdater) rejectTerminalUpdate() error {
	if !u.terminalCoordinator.hasStartedFinalizing() {
		return nil
	}
	return u.provider.NewUpdateError(
		dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
		"flow terminal cleanup is in progress",
	)
}

func (u *WorkflowUpdater) handleWaitForAttribute(
	ctx interfaces.UnifiedContext,
	request *dexpb.WaitForAttributeRequest,
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
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
			wait.matchErr.Error(),
		)
	}
	if wait.matched {
		return &emptypb.Empty{}, nil
	}
	if deadlinePassed(u.provider.Now(ctx), wait.deadline) ||
		request.GetWaitTimeSeconds() == 0 {
		return nil, u.provider.NewUpdateError(
			dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED,
			"attribute wait timed out",
		)
	}
	return nil, u.provider.NewUpdateError(
		dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED,
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
	request *dexpb.WaitForAttributeRequest,
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

func isBlobValue(value *dexpb.Value) bool {
	if value == nil {
		return false
	}
	switch value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue,
		*dexpb.Value_InternalBlobIdForObjValue:
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

func attributeValuesEqual(left, right *dexpb.Value) bool {
	return proto.Equal(left, right)
}

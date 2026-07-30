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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/rpc"
	"github.com/superdurable/dex/service/common/workerclient"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"google.golang.org/grpc/status"
)

type Activities struct {
	activityProvider interfaces.ActivityProvider
	workerPool       *workerclient.WorkerClientPool
	internalClient   *workerclient.InternalServiceClient
	unifiedClient    uclient.UnifiedClient
	blobStore        blobstore.BlobStore
	eventHandler     event.HandleEventFunc
	cfg              *config.Config
}

func NewActivities(
	activityProvider interfaces.ActivityProvider,
	workerPool *workerclient.WorkerClientPool,
	internalClient *workerclient.InternalServiceClient,
	unifiedClient uclient.UnifiedClient,
	blobStore blobstore.BlobStore,
	eventHandler event.HandleEventFunc,
	cfg *config.Config,
) *Activities {
	if activityProvider == nil || workerPool == nil || internalClient == nil ||
		unifiedClient == nil || eventHandler == nil {
		panic("Activities requires non-nil dependencies")
	}
	if cfg == nil {
		panic("Activities requires non-nil config sections")
	}
	return &Activities{
		activityProvider: activityProvider,
		workerPool:       workerPool,
		internalClient:   internalClient,
		unifiedClient:    unifiedClient,
		blobStore:        blobStore,
		eventHandler:     eventHandler,
		cfg:              cfg,
	}
}

// InvokeWaitForMethod calls WorkerService.InvokeWaitForMethod.
func (a *Activities) InvokeWaitForMethod(
	ctx context.Context, input *dexpb.InvokeWaitForMethodActivityInput,
) (*dexpb.InvokeWaitForMethodActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("InvokeWaitForMethodActivity", "input", log.ToJsonAndTruncateForLogging(input))

	activityInfo := provider.GetActivityInfo(ctx)
	req := input.GetRequest()
	req.Context.Attempt = activityInfo.Attempt
	req.Context.FirstAttemptTimestamp = activityInfo.ScheduledTime.Unix()

	if !a.cfg.ExternalStorage.EffectiveLazyLoading() {
		if err := a.hydrateWorkerRequestValues(ctx, req.GetStepInput(), req.GetAttributes()); err != nil {
			a.logLocalActivityWarn(logger, activityInfo, "InvokeWaitForMethod", req.GetContext().GetStepExecutionId(), req, err)
			return nil, composeInternalActivityError(provider, err)
		}
	}

	client, callCtx, release, err := a.workerPool.Acquire(
		ctx,
		input.GetWorkerTarget(),
		activityInfo.WorkflowExecution.ID,
	)
	if err != nil {
		return nil, composeInternalActivityError(provider, err)
	}
	defer release()

	resp, err := client.InvokeWaitForMethod(callCtx, req)
	printDebugMsg(logger, err, workerAddressForLogging(callCtx, input.GetWorkerTarget()))
	if err != nil {
		a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptFail)
		a.logLocalActivityWarn(logger, activityInfo, "InvokeWaitForMethod", req.GetContext().GetStepExecutionId(), req, err)
		return nil, composeActivityError(provider, err)
	}
	if err := validateWaitingCondition(resp.GetWaitingCondition()); err != nil {
		a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptFail)
		return nil, composeActivityError(provider, err)
	}
	if err := validateWorkerWaitForResponse(resp); err != nil {
		return nil, composeActivityError(provider, err)
	}

	resp.LocalActivityInput = nil
	if activityInfo.IsLocalActivity {
		resp.LocalActivityInput = composeLocalActivityInput(req.GetContext())
	}
	if err := a.offloadWorkerAttributeWrites(ctx, resp.GetUpsertAttributes(), activityInfo.WorkflowExecution.ID); err != nil {
		return nil, composeInternalActivityError(provider, err)
	}

	a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptSucc)
	return &dexpb.InvokeWaitForMethodActivityOutput{Response: resp}, nil
}

// InvokeExecuteMethod calls WorkerService.InvokeExecuteMethod.
func (a *Activities) InvokeExecuteMethod(
	ctx context.Context, input *dexpb.InvokeExecuteMethodActivityInput,
) (*dexpb.InvokeExecuteMethodActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("InvokeExecuteMethodActivity", "input", log.ToJsonAndTruncateForLogging(input))

	activityInfo := provider.GetActivityInfo(ctx)
	req := input.GetRequest()
	if req.Context == nil {
		req.Context = &dexpb.Context{}
	}
	req.Context.Attempt = activityInfo.Attempt
	req.Context.FirstAttemptTimestamp = activityInfo.ScheduledTime.Unix()

	lazyLoading := a.cfg.ExternalStorage.EffectiveLazyLoading()
	originalStepInputBlob := stepInputBlobRef(req.GetStepInput())
	if !lazyLoading {
		if err := a.hydrateWorkerRequestValues(ctx, req.GetStepInput(), req.GetAttributes()); err != nil {
			a.logLocalActivityWarn(logger, activityInfo, "InvokeExecuteMethod", req.GetContext().GetStepExecutionId(), req, err)
			return nil, composeInternalActivityError(provider, err)
		}
		if err := blobstore.HydrateKVs(ctx, req.GetStepExeLocals(), a.blobStore); err != nil {
			return nil, composeInternalActivityError(provider, err)
		}
	}

	client, callCtx, release, err := a.workerPool.Acquire(
		ctx,
		input.GetWorkerTarget(),
		activityInfo.WorkflowExecution.ID,
	)
	if err != nil {
		return nil, composeInternalActivityError(provider, err)
	}
	defer release()

	resp, err := client.InvokeExecuteMethod(callCtx, req)
	printDebugMsg(logger, err, workerAddressForLogging(callCtx, input.GetWorkerTarget()))
	if err != nil {
		a.emitStepExecuteMethodEvent(req, activityInfo, event.EventTypeExecuteAttemptFail)
		a.logLocalActivityWarn(logger, activityInfo, "InvokeExecuteMethod", req.GetContext().GetStepExecutionId(), req, err)
		return nil, composeActivityError(provider, err)
	}
	if err := validateStepDecision(resp.GetStepDecision()); err != nil {
		return nil, composeActivityError(provider, err)
	}
	if err := validateWorkerExecuteResponse(resp); err != nil {
		return nil, composeActivityError(provider, err)
	}

	service.SetFromStepExecutionID(
		resp.GetStepDecision(),
		req.GetContext().GetStepExecutionId(),
	)
	resp.LocalActivityInput = nil
	if activityInfo.IsLocalActivity {
		resp.LocalActivityInput = composeLocalActivityInput(req.GetContext())
	}
	if err := a.reuseOrOffloadDecisionInputs(
		ctx,
		resp.GetStepDecision(),
		originalStepInputBlob,
		req.GetStepInput(),
		activityInfo.WorkflowExecution.ID,
	); err != nil {
		return nil, composeInternalActivityError(provider, err)
	}
	if err := a.offloadWorkerAttributeWrites(ctx, resp.GetUpsertAttributes(), activityInfo.WorkflowExecution.ID); err != nil {
		return nil, composeInternalActivityError(provider, err)
	}

	a.emitStepExecuteMethodEvent(req, activityInfo, event.EventTypeExecuteAttemptSucc)
	return &dexpb.InvokeExecuteMethodActivityOutput{Response: resp}, nil
}

// DumpFlowForContinueAsNew pages ContinueAsNewDump via InternalService.
func (a *Activities) DumpFlowForContinueAsNew(
	ctx context.Context, input *dexpb.DumpFlowForContinueAsNewActivityInput,
) (*dexpb.DumpFlowForContinueAsNewActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("DumpFlowForContinueAsNewActivity", "input", log.ToJsonAndTruncateForLogging(input))

	client, callCtx, err := a.internalClient.Client(ctx)
	if err != nil {
		return nil, composeInternalActivityError(provider, err)
	}
	resp, err := client.DumpFlowForContinueAsNew(callCtx, input.GetRequest())
	if err != nil {
		return nil, composeInternalActivityError(provider, err)
	}
	return &dexpb.DumpFlowForContinueAsNewActivityOutput{Response: resp}, nil
}

const maxWorkerRpcActivityAttempts = 3

// InvokeWorkerRPC wraps rpc.InvokeWorkerRpc for the activity worker.
func (a *Activities) InvokeWorkerRPC(
	ctx context.Context, input *dexpb.InvokeWorkerRPCActivityInput,
) (*dexpb.InvokeWorkerRPCActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("InvokeWorkerRpcActivity", "input", log.ToJsonAndTruncateForLogging(input))
	activityInfo := provider.GetActivityInfo(ctx)

	resp, err := rpc.InvokeWorkerRpc(
		ctx,
		a.workerPool,
		input.GetRpcPrep(),
		input.GetRequest(),
		a.cfg.Api.EffectiveMaxWaitSeconds(),
		a.blobStore,
		&a.cfg.ExternalStorage,
	)
	if err != nil {
		return nil, composeActivityError(provider, err)
	}
	out := &dexpb.InvokeWorkerRPCActivityOutput{Response: resp}
	if activityInfo.IsLocalActivity {
		payloadSize, sized := jsonPayloadSize(logger, out)
		if threshold := a.cfg.Interpreter.InterpreterActivityConfig.LogLocalActivityThresholdBytes; sized && threshold > 0 && payloadSize >= threshold {
			logger.Warn("InvokeWorkerRpc local activity return",
				"workflowId", activityInfo.WorkflowExecution.ID,
				"payloadSize", payloadSize)
		}
	}
	return out, nil
}

// CleanupBlobStore deletes blob objects for flows that no longer exist in the backend.
func (a *Activities) CleanupBlobStore(
	ctx context.Context, input *dexpb.CleanupBlobStoreActivityInput,
) (*dexpb.CleanupBlobStoreActivityOutput, error) {
	store := a.blobStore
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("CleanupBlobStore started")
	client := a.unifiedClient

	var continueToken *string
	var totalDeleted int32
	for {
		listOutput, err := store.ListWorkflowPaths(ctx, blobstore.ListObjectPathsInput{
			StoreId:           input.GetStoreId(),
			ContinuationToken: continueToken,
		})
		if err != nil {
			return nil, err
		}
		continueToken = listOutput.ContinuationToken
		for _, workflowPath := range listOutput.WorkflowPaths {
			_, valid := blobstore.ExtractYyyymmddToUnixSeconds(workflowPath)
			if !valid {
				logger.Info("CleanupBlobStore skipped workflow path", "path", workflowPath)
				continue
			}
			flowId := blobstore.MustExtractWorkflowId(workflowPath)
			_, err := client.DescribeWorkflowExecution(ctx, flowId, "", nil)
			if client.IsNotFoundError(err) {
				if err := store.DeleteWorkflowObjects(ctx, input.GetStoreId(), workflowPath); err != nil {
					logger.Error("CleanupBlobStore failed to delete workflow objects", "workflowPath", workflowPath, "error", err)
					return nil, err
				}
				totalDeleted++
				logger.Info("CleanupBlobStore deleted workflow objects", "workflowPath", workflowPath)
			} else if err != nil {
				logger.Error("CleanupBlobStore failed to describe workflow", "workflowPath", workflowPath, "error", err)
				return nil, err
			}
			provider.RecordHeartbeat(ctx)
		}
		if continueToken == nil {
			break
		}
	}
	logger.Info("CleanupBlobStore completed", "totalDeleted", totalDeleted)
	return &dexpb.CleanupBlobStoreActivityOutput{TotalDeleted: totalDeleted}, nil
}

func (a *Activities) hydrateWorkerRequestValues(
	ctx context.Context, stepInput *dexpb.Value, attributes []*dexpb.KV,
) error {
	if err := blobstore.HydrateValue(ctx, stepInput, a.blobStore); err != nil {
		return err
	}
	return blobstore.HydrateKVs(ctx, attributes, a.blobStore)
}

func (a *Activities) offloadWorkerAttributeWrites(
	ctx context.Context, writes []*dexpb.AttributeWrite, flowId string,
) error {
	if !a.cfg.ExternalStorage.Enabled || a.blobStore == nil {
		return nil
	}
	return blobstore.OffloadLargeAttributeWrites(
		ctx, writes, flowId, a.cfg.ExternalStorage.ThresholdInBytes, a.blobStore, true,
	)
}

// reuseOrOffloadDecisionInputs accepts worker-echoed blob ids as-is, reuses the
// original blob id when the concrete payload matches, otherwise offloads.
func (a *Activities) reuseOrOffloadDecisionInputs(
	ctx context.Context,
	decision *dexpb.StepDecision,
	originalStepInputBlob stepInputBlob,
	hydratedStepInput *dexpb.Value,
	flowId string,
) error {
	if decision == nil || !a.cfg.ExternalStorage.Enabled || a.blobStore == nil {
		return nil
	}
	for _, step := range decision.GetNextSteps() {
		if step == nil || step.GetStepInput() == nil {
			continue
		}
		if stepInputBlobRef(step.GetStepInput()).id != "" {
			continue
		}
		if shouldReuseStepInputBlob(originalStepInputBlob, hydratedStepInput, step.GetStepInput()) {
			step.StepInput = originalStepInputBlob.toValue()
			continue
		}
		if err := a.offloadStepInput(ctx, step.StepInput, flowId); err != nil {
			return err
		}
	}
	closeDecision := decision.GetCloseDecision()
	if closeDecision.GetCloseDecisionType() ==
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL {
		return nil
	}
	if closeInput := closeDecision.GetCloseInput(); closeInput != nil {
		if stepInputBlobRef(closeInput).id != "" {
			return nil
		}
		if shouldReuseStepInputBlob(originalStepInputBlob, hydratedStepInput, closeInput) {
			closeDecision.CloseInput = originalStepInputBlob.toValue()
			return nil
		}
		if err := a.offloadStepInput(ctx, closeInput, flowId); err != nil {
			return err
		}
	}
	return nil
}

func (a *Activities) offloadStepInput(
	ctx context.Context, stepInput *dexpb.Value, flowId string,
) error {
	return blobstore.OffloadLargeValue(
		ctx,
		stepInput,
		flowId,
		a.cfg.ExternalStorage.ThresholdInBytes,
		a.blobStore,
		true,
	)
}

type stepInputBlob struct {
	id    string
	isObj bool
}

func stepInputBlobRef(value *dexpb.Value) stepInputBlob {
	if value == nil {
		return stepInputBlob{}
	}
	if blobId := value.GetInternalBlobIdForObjValue(); blobId != "" {
		return stepInputBlob{id: blobId, isObj: true}
	}
	return stepInputBlob{id: value.GetInternalBlobIdForStringValue()}
}

func (blob stepInputBlob) toValue() *dexpb.Value {
	if blob.id == "" {
		return nil
	}
	if blob.isObj {
		return &dexpb.Value{
			Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: blob.id},
		}
	}
	return &dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: blob.id},
	}
}

func shouldReuseStepInputBlob(original stepInputBlob, hydrated, next *dexpb.Value) bool {
	if original.id == "" || next == nil {
		return false
	}
	return valuePayloadEqual(hydrated, next)
}

func valuePayloadEqual(left, right *dexpb.Value) bool {
	if left == nil || right == nil {
		return false
	}
	if leftString := left.GetStringValue(); leftString != "" || right.GetStringValue() != "" {
		return leftString == right.GetStringValue()
	}
	leftObj := left.GetObjValue()
	rightObj := right.GetObjValue()
	if leftObj == nil || rightObj == nil {
		return false
	}
	return leftObj.GetEncoding() == rightObj.GetEncoding() &&
		bytes.Equal(leftObj.GetPayload(), rightObj.GetPayload())
}

func validateWorkerWaitForResponse(resp *dexpb.InvokeWaitForMethodResponse) error {
	if resp == nil {
		return fmt.Errorf("nil InvokeWaitForMethodResponse")
	}
	if err := workerclient.RejectWorkerAttributeWriteBlobIDs(resp.GetUpsertAttributes()); err != nil {
		return err
	}
	if err := workerclient.RejectWorkerKVBlobIDs(resp.GetUpsertStepExeLocals()); err != nil {
		return err
	}
	return workerclient.RejectWorkerKVBlobIDs(resp.GetRecordEvents())
}

func validateWorkerExecuteResponse(resp *dexpb.InvokeExecuteMethodResponse) error {
	if resp == nil {
		return fmt.Errorf("nil InvokeExecuteMethodResponse")
	}
	if err := workerclient.RejectWorkerAttributeWriteBlobIDs(resp.GetUpsertAttributes()); err != nil {
		return err
	}
	if err := workerclient.RejectWorkerKVBlobIDs(resp.GetUpsertStepExeLocals()); err != nil {
		return err
	}
	if err := workerclient.RejectWorkerKVBlobIDs(resp.GetRecordEvents()); err != nil {
		return err
	}
	return nil
}

func validateStepDecision(decision *dexpb.StepDecision) error {
	if decision == nil {
		return fmt.Errorf("step decision is nil")
	}
	if len(decision.GetNextSteps()) == 0 && decision.GetCloseDecision() == nil {
		return fmt.Errorf("empty step decision is not supported")
	}
	for index, movement := range decision.GetNextSteps() {
		if movement == nil || movement.GetStepType() == "" {
			return fmt.Errorf("next step at index %d is invalid", index)
		}
	}
	if closeDecision := decision.GetCloseDecision(); closeDecision != nil {
		return validateCloseDecision(closeDecision, len(decision.GetNextSteps()))
	}
	return nil
}

func validateCloseDecision(closeDecision *dexpb.CloseDecision, nextStepCount int) error {
	closeType := closeDecision.GetCloseDecisionType()
	channelNames := closeDecision.GetConditionalChannelNames()
	if closeType != dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY {
		if nextStepCount > 0 {
			return fmt.Errorf("close decision cannot be combined with next steps")
		}
		if len(channelNames) > 0 {
			return fmt.Errorf("conditional channel names require a conditional close decision")
		}
	}
	switch closeType {
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY:
		if nextStepCount == 0 {
			return fmt.Errorf("conditional close decision requires at least one next step")
		}
		if len(channelNames) == 0 {
			return fmt.Errorf("conditional close decision requires at least one channel")
		}
		seenChannelNames := map[string]struct{}{}
		for _, channelName := range channelNames {
			if channelName == "" {
				return fmt.Errorf("conditional close channel name is empty")
			}
			if _, duplicated := seenChannelNames[channelName]; duplicated {
				return fmt.Errorf("duplicate conditional close channel %q", channelName)
			}
			seenChannelNames[channelName] = struct{}{}
		}
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE:
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL:
		if closeInput := closeDecision.GetCloseInput(); closeInput != nil {
			if _, isString := closeInput.GetKind().(*dexpb.Value_StringValue); !isString {
				return fmt.Errorf("force fail close input must be a string")
			}
		}
	case dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END:
		if closeDecision.GetCloseInput() != nil {
			return fmt.Errorf("dead end close decision cannot have close input")
		}
	default:
		return fmt.Errorf("close decision type is unspecified")
	}
	return nil
}

func validateWaitingCondition(waiting *dexpb.WaitingCondition) error {
	if waiting == nil {
		return nil
	}

	declaredIds := map[string]bool{}
	conditionIdsRequired := waiting.GetWaitingConditionType() ==
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED
	for i, timerCondition := range waiting.GetTimerConditions() {
		if timerCondition == nil {
			return fmt.Errorf("timer condition at index %d is nil", i)
		}
		if err := registerWaitingConditionId(
			declaredIds,
			timerCondition.GetConditionId(),
			"timer",
			conditionIdsRequired,
		); err != nil {
			return err
		}
		if timerCondition.GetDurationSeconds() < 0 {
			return fmt.Errorf(
				"timer condition %q has negative duration_seconds %d",
				timerCondition.GetConditionId(),
				timerCondition.GetDurationSeconds(),
			)
		}
		if timerCondition.GetFiringUnixTimestampSeconds() != 0 {
			return fmt.Errorf(
				"timer condition %q sets server-owned firing_unix_timestamp_seconds",
				timerCondition.GetConditionId(),
			)
		}
	}

	for i, channelCondition := range waiting.GetChannelConditions() {
		if channelCondition == nil {
			return fmt.Errorf("channel condition at index %d is nil", i)
		}
		if err := registerWaitingConditionId(
			declaredIds,
			channelCondition.GetConditionId(),
			"channel",
			conditionIdsRequired,
		); err != nil {
			return err
		}
		if channelCondition.GetChannelName() == "" {
			return fmt.Errorf(
				"channel condition %q has an empty channel_name",
				channelCondition.GetConditionId(),
			)
		}
		if channelCondition.AtLeast != nil && channelCondition.GetAtLeast() < 0 {
			return fmt.Errorf(
				"channel condition %q has negative at_least %d",
				channelCondition.GetConditionId(),
				channelCondition.GetAtLeast(),
			)
		}
		if channelCondition.AtMost != nil && channelCondition.GetAtMost() < 0 {
			return fmt.Errorf(
				"channel condition %q has negative at_most %d",
				channelCondition.GetConditionId(),
				channelCondition.GetAtMost(),
			)
		}
		if channelCondition.AtMost != nil &&
			channelCondition.GetAtMost() < channelCondition.GetAtLeast() {
			return fmt.Errorf(
				"channel condition %q has at_most %d < at_least %d",
				channelCondition.GetConditionId(),
				channelCondition.GetAtMost(),
				channelCondition.GetAtLeast(),
			)
		}
	}

	switch waiting.GetWaitingConditionType() {
	case dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED:
		if len(waiting.GetConditionCombinations()) > 0 {
			return fmt.Errorf("condition_combinations are only valid for ANY_COMBINATION_COMPLETED")
		}
	case dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED:
		if err := validateWaitingConditionCombinations(waiting, declaredIds); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown waiting_condition_type %d", waiting.GetWaitingConditionType())
	}
	return nil
}

func registerWaitingConditionId(
	declaredIds map[string]bool,
	conditionId string,
	kind string,
	required bool,
) error {
	if conditionId == "" {
		if required {
			return fmt.Errorf("%s condition has an empty condition_id", kind)
		}
		return nil
	}
	if declaredIds[conditionId] {
		return fmt.Errorf("duplicate condition_id %q", conditionId)
	}
	declaredIds[conditionId] = true
	return nil
}

func validateWaitingConditionCombinations(
	waiting *dexpb.WaitingCondition,
	declaredIds map[string]bool,
) error {
	combinations := waiting.GetConditionCombinations()
	if len(combinations) == 0 {
		return fmt.Errorf("ANY_COMBINATION_COMPLETED requires at least one condition_combination")
	}
	for i, combination := range combinations {
		if combination == nil || len(combination.GetConditionIds()) == 0 {
			return fmt.Errorf("condition_combination at index %d is empty", i)
		}
		seen := map[string]bool{}
		for _, conditionId := range combination.GetConditionIds() {
			if !declaredIds[conditionId] {
				return fmt.Errorf(
					"condition_combination at index %d references undeclared condition_id %q",
					i,
					conditionId,
				)
			}
			if seen[conditionId] {
				return fmt.Errorf(
					"condition_combination at index %d has duplicate condition_id %q",
					i,
					conditionId,
				)
			}
			seen[conditionId] = true
		}
	}
	return nil
}

func composeLocalActivityInput(ctx *dexpb.Context) *dexpb.LocalActivityInput {
	return &dexpb.LocalActivityInput{
		CurrentStepExecutionId: ctx.GetStepExecutionId(),
		FromStepExecutionId:    ctx.GetFromStepExecutionId(),
	}
}

func workerAddressForLogging(ctx context.Context, workerTarget *dexpb.WorkerTarget) string {
	if !workerTarget.GetIsHeadlessAddress() {
		return workerTarget.GetAddress()
	}
	resolvedAddress := workerclient.ResolvedWorkerAddressFromContext(ctx)
	if resolvedAddress == "" {
		return workerTarget.GetAddress()
	}
	return resolvedAddress
}

func printDebugMsg(logger interfaces.UnifiedLogger, err error, target string) {
	if os.Getenv(service.EnvNameDebugMode) != "" {
		logger.Info("check error at worker gRPC request", err, target)
	}
}

func composeActivityError(provider interfaces.ActivityProvider, err error) error {
	grpcStatus, ok := status.FromError(err)
	if !ok {
		return composeInternalActivityError(provider, err)
	}

	// Error() is "rpc error: code=… desc=<Message>"; do not concat both.
	detail := grpcStatus.Message()
	if detail == "" {
		detail = err.Error()
	}
	errorResponse := &dexpb.ErrorResponse{
		Detail:                    detail,
		SubStatus:                 dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		OriginalWorkerErrorStatus: int32(grpcStatus.Code()),
	}
	for _, detail := range grpcStatus.Details() {
		workerError, ok := detail.(*dexpb.WorkerErrorResponse)
		if !ok {
			continue
		}
		errorResponse.OriginalWorkerErrorDetail = workerError.GetDetail()
		errorResponse.OriginalWorkerErrorType = workerError.GetErrorType()
	}
	return provider.NewFlowError(dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL, errorResponse)
}

func composeInternalActivityError(
	provider interfaces.ActivityProvider,
	err error,
) error {
	return provider.NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
		&dexpb.ErrorResponse{
			Detail:    err.Error(),
			SubStatus: dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		},
	)
}

func (a *Activities) logLocalActivityWarn(
	logger interfaces.UnifiedLogger,
	activityInfo interfaces.ActivityInfo, name, stepExeId string, payload any, err error,
) {
	threshold := a.cfg.Interpreter.InterpreterActivityConfig.LogLocalActivityThresholdBytes
	if !activityInfo.IsLocalActivity || threshold <= 0 {
		return
	}
	payloadSize, sized := jsonPayloadSize(logger, payload)
	if !sized || payloadSize < threshold {
		return
	}
	logger.Warn(name+" local activity return on error",
		"workflowId", activityInfo.WorkflowExecution.ID,
		"stepExecutionId", stepExeId,
		"payloadSize", payloadSize,
		"error", err)
}

func jsonPayloadSize(logger interfaces.UnifiedLogger, payload any) (int, bool) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to serialize local activity payload", "error", err)
		return 0, false
	}
	return len(payloadBytes), true
}

func (a *Activities) emitStepWaitForMethodEvent(
	req *dexpb.InvokeWaitForMethodRequest, activityInfo interfaces.ActivityInfo, eventType event.EventType,
) {
	a.eventHandler(event.Event{
		FlowId:          activityInfo.WorkflowExecution.ID,
		RunId:           activityInfo.WorkflowExecution.RunID,
		FlowType:        req.GetFlowType(),
		StepType:        req.GetStepType(),
		StepExecutionId: req.GetContext().GetStepExecutionId(),
		EventType:       eventType,
	})
}

func (a *Activities) emitStepExecuteMethodEvent(
	req *dexpb.InvokeExecuteMethodRequest, activityInfo interfaces.ActivityInfo, eventType event.EventType,
) {
	a.eventHandler(event.Event{
		FlowId:          activityInfo.WorkflowExecution.ID,
		RunId:           activityInfo.WorkflowExecution.RunID,
		FlowType:        req.GetFlowType(),
		StepType:        req.GetStepType(),
		StepExecutionId: req.GetContext().GetStepExecutionId(),
		EventType:       eventType,
	})
}

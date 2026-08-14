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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/attributestore"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/grpctarget"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/common/retry"
	"github.com/superdurable/dex/service/common/rpc"
	"github.com/superdurable/dex/service/common/workerclient"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Activities struct {
	activityProvider interfaces.ActivityProvider
	workerPool       *workerclient.WorkerClientPool
	internalClient   *workerclient.InternalServiceClient
	unifiedClient    uclient.UnifiedClient
	blobStore        blobstore.BlobStore
	attributeStore   *attributestore.Manager
	eventHandler     event.HandleEventFunc
	cfg              *config.Config
	subFlowResolver  *SubFlowReuseResolver
}

func NewActivities(
	activityProvider interfaces.ActivityProvider,
	workerPool *workerclient.WorkerClientPool,
	internalClient *workerclient.InternalServiceClient,
	unifiedClient uclient.UnifiedClient,
	blobStore blobstore.BlobStore,
	attributeStore *attributestore.Manager,
	eventHandler event.HandleEventFunc,
	cfg *config.Config,
) *Activities {
	if activityProvider == nil || workerPool == nil || internalClient == nil ||
		unifiedClient == nil || attributeStore == nil || eventHandler == nil {
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
		attributeStore:   attributeStore,
		eventHandler:     eventHandler,
		cfg:              cfg,
		subFlowResolver:  NewSubFlowReuseResolver(unifiedClient),
	}
}

func (a *Activities) SyncAttributeBatch(
	ctx context.Context,
	input *dexpb.SyncAttributeBatchActivityInput,
) error {
	for _, item := range input.GetItems() {
		if item == nil {
			continue
		}
		if err := blobstore.HydrateValue(ctx, item.GetValue(), a.blobStore); err != nil {
			return fmt.Errorf("hydrate Attribute Store item: %w", err)
		}
	}
	return a.attributeStore.WriteBatch(ctx, input)
}

// InvokeWaitForMethod calls WorkerService.InvokeWaitForMethod.
func (a *Activities) InvokeWaitForMethod(
	ctx context.Context,
	input *dexpb.InvokeWaitForMethodActivityInput,
	localInput *dexpb.InternalLocalActivityInput,
) (*dexpb.InvokeWaitForMethodActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("InvokeWaitForMethodActivity", "input", log.ToJsonAndTruncateForLogging(input))

	activityInfo := provider.GetActivityInfo(ctx)
	req := waitForMethodRequestForAttempt(input.GetRequest())
	activityAttempt := retry.NewStepActivityAttempt(
		req.GetContext(),
		activityInfo.ScheduledTime,
		activityInfo.Attempt,
	)
	activityAttempt.ApplyToWorkerContext(req.GetContext())
	var localActivityFailure *dexpb.InternalLocalStepActivityFailure
	if activityInfo.IsLocalActivity {
		localActivityFailure = activityAttempt.LocalFailureDetails(
			req.GetContext(),
			localInput.GetMethodOptions(),
		)
	}

	lazyLoading := a.cfg.BlobStore.EffectiveLazyLoading()
	originalStepInputBlob := stepInputBlobRef(req.GetStepInput())
	if !lazyLoading {
		if err := a.hydrateWorkerRequestValues(ctx, req.GetStepInput(), req.GetAttributes()); err != nil {
			a.logLocalActivityWarn(logger, activityInfo, "InvokeWaitForMethod", req.GetContext().GetStepExecutionId(), req, err)
			return nil, newServerActivityFailure(provider, err, localActivityFailure)
		}
	}
	client, callCtx, release, err := a.workerPool.Acquire(
		ctx,
		input.GetWorkerTarget(),
		activityInfo.WorkflowExecution.ID,
	)
	if err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}
	defer release()

	resp, err := client.InvokeWaitForMethod(callCtx, req)
	printDebugMsg(logger, err, workerAddressForLogging(callCtx, input.GetWorkerTarget()))
	if err != nil {
		a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptFail)
		a.logLocalActivityWarn(logger, activityInfo, "InvokeWaitForMethod", req.GetContext().GetStepExecutionId(), req, err)
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := validateWaitingCondition(resp.GetWaitingCondition()); err != nil {
		a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptFail)
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := validateTransientStepMovement(resp.GetTransientStepMovement()); err != nil {
		a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptFail)
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := validateWorkerWaitForResponse(resp); err != nil {
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := a.persistStepEventInput(
		ctx,
		localInput.GetCurrentRunStartedTimestamp(),
		activityInfo,
		req.GetContext().GetStepExecutionId(),
		blobstore.StepEventInputMethodWaitFor,
		&dexpb.InternalAsyncStepInputSnapshot{
			MethodOptions: localInput.GetMethodOptions(),
			Request: &dexpb.InternalAsyncStepInputSnapshot_WaitForRequest{
				WaitForRequest: req,
			},
		},
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}

	transientStep := resp.GetTransientStepMovement()
	if transientStep != nil {
		transientStep.FromStepExecutionIdInternalOnly = req.GetContext().GetStepExecutionId()
		transientStep.RecoveryErrorInternalOnly = nil
		if err := a.reuseOrOffloadStepInput(
			ctx,
			transientStep,
			originalStepInputBlob,
			req.GetStepInput(),
			activityInfo.WorkflowExecution.ID,
			activityInvocationId(activityInfo),
		); err != nil {
			return nil, newServerActivityFailure(provider, err, localActivityFailure)
		}
	}
	resp.LocalActivityMetadata = nil
	if activityInfo.IsLocalActivity {
		resp.LocalActivityMetadata = composeLocalActivityMetadata(req.GetContext())
	}
	if err := a.offloadWorkerAttributeWrites(
		ctx,
		resp.GetUpsertAttributes(),
		activityInfo.WorkflowExecution.ID,
		activityInvocationId(activityInfo),
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}
	if err := a.offloadWorkerSideEffects(
		ctx,
		resp.GetUpsertStepExeLocals(),
		resp.GetRecordEvents(),
		resp.GetPublishToChannel(),
		activityInfo.WorkflowExecution.ID,
		activityInvocationId(activityInfo),
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}

	a.emitStepWaitForMethodEvent(req, activityInfo, event.EventTypeWaitForAttemptSucc)
	return &dexpb.InvokeWaitForMethodActivityOutput{Response: resp}, nil
}

// InvokeExecuteMethod calls WorkerService.InvokeExecuteMethod.
func (a *Activities) InvokeExecuteMethod(
	ctx context.Context,
	input *dexpb.InvokeExecuteMethodActivityInput,
	localInput *dexpb.InternalLocalActivityInput,
) (*dexpb.InvokeExecuteMethodActivityOutput, error) {
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("InvokeExecuteMethodActivity", "input", log.ToJsonAndTruncateForLogging(input))

	activityInfo := provider.GetActivityInfo(ctx)
	req := executeMethodRequestForAttempt(input.GetRequest())
	activityAttempt := retry.NewStepActivityAttempt(
		req.GetContext(),
		activityInfo.ScheduledTime,
		activityInfo.Attempt,
	)
	activityAttempt.ApplyToWorkerContext(req.GetContext())
	var localActivityFailure *dexpb.InternalLocalStepActivityFailure
	if activityInfo.IsLocalActivity {
		localActivityFailure = activityAttempt.LocalFailureDetails(
			req.GetContext(),
			localInput.GetMethodOptions(),
		)
	}

	lazyLoading := a.cfg.BlobStore.EffectiveLazyLoading()
	originalStepInputBlob := stepInputBlobRef(req.GetStepInput())
	if !lazyLoading {
		if err := a.hydrateWorkerRequestValues(ctx, req.GetStepInput(), req.GetAttributes()); err != nil {
			a.logLocalActivityWarn(logger, activityInfo, "InvokeExecuteMethod", req.GetContext().GetStepExecutionId(), req, err)
			return nil, newServerActivityFailure(provider, err, localActivityFailure)
		}
		if err := blobstore.HydrateKVs(ctx, req.GetStepExeLocals(), a.blobStore); err != nil {
			return nil, newServerActivityFailure(provider, err, localActivityFailure)
		}
		if err := blobstore.HydrateConditionResults(ctx, req.GetConditionResults(), a.blobStore); err != nil {
			return nil, newServerActivityFailure(provider, err, localActivityFailure)
		}
	}
	client, callCtx, release, err := a.workerPool.Acquire(
		ctx,
		input.GetWorkerTarget(),
		activityInfo.WorkflowExecution.ID,
	)
	if err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}
	defer release()

	resp, err := client.InvokeExecuteMethod(callCtx, req)
	printDebugMsg(logger, err, workerAddressForLogging(callCtx, input.GetWorkerTarget()))
	if err != nil {
		a.emitStepExecuteMethodEvent(req, activityInfo, event.EventTypeExecuteAttemptFail)
		a.logLocalActivityWarn(logger, activityInfo, "InvokeExecuteMethod", req.GetContext().GetStepExecutionId(), req, err)
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := validateExecuteResponse(resp, input.GetIsTransientStep()); err != nil {
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, localActivityFailure)
	}
	if err := a.persistStepEventInput(
		ctx,
		localInput.GetCurrentRunStartedTimestamp(),
		activityInfo,
		req.GetContext().GetStepExecutionId(),
		blobstore.StepEventInputMethodExecute,
		&dexpb.InternalAsyncStepInputSnapshot{
			MethodOptions: localInput.GetMethodOptions(),
			Request: &dexpb.InternalAsyncStepInputSnapshot_ExecuteRequest{
				ExecuteRequest: req,
			},
		},
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}

	service.SetFromStepExecutionIDForStepDecision(
		resp.GetStepDecision(),
		req.GetContext().GetStepExecutionId(),
	)
	resp.LocalActivityMetadata = nil
	if activityInfo.IsLocalActivity {
		resp.LocalActivityMetadata = composeLocalActivityMetadata(req.GetContext())
	}
	if err := a.reuseOrOffloadDecisionInputs(
		ctx,
		resp.GetStepDecision(),
		originalStepInputBlob,
		req.GetStepInput(),
		activityInfo.WorkflowExecution.ID,
		activityInvocationId(activityInfo),
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}
	if err := a.offloadWorkerAttributeWrites(
		ctx,
		resp.GetUpsertAttributes(),
		activityInfo.WorkflowExecution.ID,
		activityInvocationId(activityInfo),
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}
	if err := a.offloadWorkerSideEffects(
		ctx,
		resp.GetUpsertStepExeLocals(),
		resp.GetRecordEvents(),
		resp.GetPublishToChannel(),
		activityInfo.WorkflowExecution.ID,
		activityInvocationId(activityInfo),
	); err != nil {
		return nil, newServerActivityFailure(provider, err, localActivityFailure)
	}

	a.emitStepExecuteMethodEvent(req, activityInfo, event.EventTypeExecuteAttemptSucc)
	return &dexpb.InvokeExecuteMethodActivityOutput{Response: resp}, nil
}

func waitForMethodRequestForAttempt(
	request *dexpb.InvokeWaitForMethodRequest,
) *dexpb.InvokeWaitForMethodRequest {
	if request == nil {
		panic("InvokeWaitForMethod request required")
	}
	return &dexpb.InvokeWaitForMethodRequest{
		Context:    workerContextForAttempt(request.GetContext()),
		FlowType:   request.GetFlowType(),
		StepType:   request.GetStepType(),
		StepInput:  request.GetStepInput(),
		Attributes: request.GetAttributes(),
	}
}

func executeMethodRequestForAttempt(
	request *dexpb.InvokeExecuteMethodRequest,
) *dexpb.InvokeExecuteMethodRequest {
	if request == nil {
		panic("InvokeExecuteMethod request required")
	}
	return &dexpb.InvokeExecuteMethodRequest{
		Context:          workerContextForAttempt(request.GetContext()),
		FlowType:         request.GetFlowType(),
		StepType:         request.GetStepType(),
		StepInput:        request.GetStepInput(),
		Attributes:       request.GetAttributes(),
		StepExeLocals:    request.GetStepExeLocals(),
		ConditionResults: request.GetConditionResults(),
	}
}

func workerContextForAttempt(workerContext *dexpb.Context) *dexpb.Context {
	if workerContext == nil {
		return &dexpb.Context{}
	}
	return &dexpb.Context{
		FlowId:                workerContext.GetFlowId(),
		RunId:                 workerContext.GetRunId(),
		FlowStartedTimestamp:  workerContext.GetFlowStartedTimestamp(),
		StepExecutionId:       workerContext.GetStepExecutionId(),
		FirstAttemptTimestamp: workerContext.GetFirstAttemptTimestamp(),
		Attempt:               workerContext.GetAttempt(),
		FromStepExecutionId:   workerContext.GetFromStepExecutionId(),
		RecoveryError:         workerContext.GetRecoveryError(),
	}
}

func (a *Activities) persistStepEventInput(
	ctx context.Context,
	runStartedTimestamp int64,
	activityInfo interfaces.ActivityInfo,
	stepExecutionID string,
	method string,
	input *dexpb.InternalAsyncStepInputSnapshot,
) error {
	if !activityInfo.IsLocalActivity || !a.cfg.BlobStore.EffectiveEnabled() {
		return nil
	}
	if stepExecutionID == "" {
		return fmt.Errorf("step event input requires a step execution ID")
	}
	runStarted := time.Unix(runStartedTimestamp, 0)
	if runStartedTimestamp <= 0 {
		description, err := a.unifiedClient.DescribeWorkflowExecution(
			ctx,
			activityInfo.WorkflowExecution.ID,
			activityInfo.WorkflowExecution.RunID,
			nil,
		)
		if err != nil {
			return fmt.Errorf("describe current run for step event input: %w", err)
		}
		runStarted = description.StartTime
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal step event input: %w", err)
	}
	return a.blobStore.WriteStepEventInput(
		ctx,
		runStarted,
		activityInfo.WorkflowExecution.ID,
		activityInfo.WorkflowExecution.RunID,
		stepExecutionID,
		method,
		data,
	)
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
		return nil, newServerActivityFailure(provider, err, nil)
	}
	resp, err := client.DumpFlowForContinueAsNew(callCtx, input.GetRequest())
	if err != nil {
		return nil, newServerActivityFailure(provider, err, nil)
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
		input.GetRequest().GetRequestId(),
		&a.cfg.BlobStore,
	)
	if err != nil {
		return nil, newWorkerActivityFailure(provider, a.unifiedClient.GetBackendType(), err, nil)
	}
	out := &dexpb.InvokeWorkerRPCActivityOutput{
		Response:  resp,
		RequestId: input.GetRequest().GetRequestId(),
	}
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

func (a *Activities) StartSubFlow(
	ctx context.Context, input *dexpb.StartSubFlowActivityInput,
) (*dexpb.StartSubFlowActivityOutput, error) {
	condition := input.GetCondition()
	if condition == nil {
		return nil, fmt.Errorf("SubFlow start requires a condition")
	}
	parentFlowID, subFlowID, requestID, err := a.subFlowStartIdentity(
		ctx, input.GetParentStepExecutionId(), condition.GetSubFlowIndex(),
	)
	if err != nil {
		return nil, err
	}
	if err := blobstore.ValidateWorkflowId(subFlowID); err != nil {
		return nil, err
	}
	options := condition.GetOptions()
	if options.GetFlowTimeoutSeconds() < 0 || options.GetFlowStartDelaySeconds() < 0 {
		return nil, fmt.Errorf("SubFlow timeout and start delay must be non-negative")
	}
	if err := workerclient.RejectWorkerBlobIDs(condition.GetStepInput()); err != nil {
		return nil, err
	}
	if err := workerclient.ValidateAttributeWrites(options.GetAttributes()); err != nil {
		return nil, err
	}

	parentFlowConfig := input.GetParentFlowConfig()
	if parentFlowConfig == nil {
		return nil, fmt.Errorf("SubFlow start requires the parent FlowConfig")
	}
	flowConfig, err := buildSubFlowConfig(parentFlowConfig, options.GetFlowConfigOverride())
	if err != nil {
		return nil, err
	}
	if configName := flowConfig.GetAttributeSyncConfigName(); configName != "" && !a.attributeStore.HasStore(configName) {
		return nil, fmt.Errorf("Attribute Store %q is unavailable", configName)
	}
	if err := blobstore.OffloadLargeValue(
		ctx,
		condition.GetStepInput(),
		subFlowID,
		requestID,
		a.cfg.BlobStore.EffectiveThresholdInBytes(),
		a.blobStore,
		a.cfg.BlobStore.EffectiveEnabled(),
	); err != nil {
		return nil, err
	}
	if err := blobstore.OffloadLargeAttributeWrites(
		ctx,
		options.GetAttributes(),
		subFlowID,
		requestID,
		a.cfg.BlobStore.EffectiveThresholdInBytes(),
		a.blobStore,
		a.cfg.BlobStore.EffectiveEnabled(),
	); err != nil {
		return nil, err
	}

	workflowOptions := buildSubFlowStartOptions(
		condition, flowConfig, parentFlowID, subFlowID, requestID,
	)
	workflowInput := &dexpb.InterpreterWorkflowInput{
		FlowType:       condition.GetSubFlowType(),
		StartStepType:  condition.GetStartStepType(),
		StepInput:      condition.GetStepInput(),
		StepOptions:    condition.GetStepOptions(),
		InitAttributes: options.GetAttributes(),
		Config:         flowConfig,
	}
	return a.subFlowResolver.Resolve(
		ctx, condition, subFlowID, requestID, workflowOptions, workflowInput,
	)
}

func (a *Activities) subFlowStartIdentity(
	ctx context.Context,
	stepExecutionID string,
	subFlowIndex int32,
) (string, string, string, error) {
	if stepExecutionID == "" {
		return "", "", "", fmt.Errorf("SubFlow start requires a Step execution ID")
	}
	activityInfo := a.activityProvider.GetActivityInfo(ctx)
	parentExecution := activityInfo.WorkflowExecution
	if parentExecution.ID == "" || parentExecution.RunID == "" {
		return "", "", "", fmt.Errorf("SubFlow start requires a parent Workflow execution")
	}
	requestID := parentExecution.RunID + stepExecutionID
	return parentExecution.ID,
		service.SubFlowID(parentExecution.ID, stepExecutionID, subFlowIndex),
		requestID,
		nil
}

func (a *Activities) ReportSubFlowCompletion(
	ctx context.Context, input *dexpb.ReportSubFlowCompletionActivityInput,
) (*dexpb.ReportSubFlowCompletionActivityOutput, error) {
	request := input.GetRequest()
	result := request.GetFlowResult()
	activityInfo := a.activityProvider.GetActivityInfo(ctx)
	if result == nil || request.GetSubFlowId() != activityInfo.WorkflowExecution.ID {
		return nil, fmt.Errorf("SubFlow completion result does not match the reporting workflow")
	}
	description, describeErr := a.unifiedClient.DescribeWorkflowExecution(
		ctx,
		activityInfo.WorkflowExecution.ID,
		activityInfo.WorkflowExecution.RunID,
		map[string]dexpb.IndexType{
			service.SearchAttributeDexParentFlowID: dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	)
	if describeErr != nil {
		return nil, fmt.Errorf("describe reporting SubFlow: %w", describeErr)
	}
	parentFlowID := description.IndexedAttributes[service.SearchAttributeDexParentFlowID].GetStringValue()
	if parentFlowID == "" {
		return nil, fmt.Errorf("reporting SubFlow is missing %s", service.SearchAttributeDexParentFlowID)
	}
	err := a.unifiedClient.SignalWorkflow(
		ctx, parentFlowID, "", service.SubFlowCompletionSignalChannelName, request,
	)
	if err == nil {
		return &dexpb.ReportSubFlowCompletionActivityOutput{
			Status: dexpb.SubFlowCompletionDeliveryStatus_SUB_FLOW_COMPLETION_DELIVERY_STATUS_DELIVERED,
		}, nil
	}
	if a.unifiedClient.IsWorkflowClosedOrNotFoundError(err) {
		return &dexpb.ReportSubFlowCompletionActivityOutput{
			Status: dexpb.SubFlowCompletionDeliveryStatus_SUB_FLOW_COMPLETION_DELIVERY_STATUS_PARENT_CLOSED_OR_NOT_FOUND,
		}, nil
	}
	return nil, fmt.Errorf("report SubFlow completion: %w", err)
}

func buildSubFlowConfig(parent, override *dexpb.FlowConfig) (*dexpb.FlowConfig, error) {
	if parent == nil {
		parent = config.DefaultWorkflowConfig
	}
	flowConfig := &dexpb.FlowConfig{
		ActiveStepSearchMode:         parent.ActiveStepSearchMode,
		ContinueAsNewThreshold:       parent.ContinueAsNewThreshold,
		ContinueAsNewPageSizeInBytes: parent.ContinueAsNewPageSizeInBytes,
		StepDurability:               parent.StepDurability,
		WorkerTarget:                 parent.WorkerTarget,
		AttributeSyncConfigName:      parent.AttributeSyncConfigName,
	}
	if override != nil {
		if override.ActiveStepSearchMode != nil {
			flowConfig.ActiveStepSearchMode = override.ActiveStepSearchMode
		}
		if override.ContinueAsNewThreshold != nil {
			flowConfig.ContinueAsNewThreshold = override.ContinueAsNewThreshold
		}
		if override.ContinueAsNewPageSizeInBytes != nil {
			flowConfig.ContinueAsNewPageSizeInBytes = override.ContinueAsNewPageSizeInBytes
		}
		if override.StepDurability != nil {
			flowConfig.StepDurability = override.StepDurability
		}
		if override.WorkerTarget != nil {
			flowConfig.WorkerTarget = override.WorkerTarget
		}
		if override.AttributeSyncConfigName != nil {
			flowConfig.AttributeSyncConfigName = override.AttributeSyncConfigName
		}
	}
	if err := interpreterconfig.ValidateFlowConfig(flowConfig); err != nil {
		return nil, err
	}
	workerTarget, err := grpctarget.NormalizeWorkerTarget(flowConfig.GetWorkerTarget())
	if err != nil {
		return nil, err
	}
	flowConfig.WorkerTarget = workerTarget
	return flowConfig, nil
}

func buildSubFlowStartOptions(
	condition *dexpb.SubFlowCondition,
	flowConfig *dexpb.FlowConfig,
	parentFlowID string,
	subFlowID string,
	requestID string,
) uclient.StartWorkflowOptions {
	options := condition.GetOptions()
	workflowOptions := uclient.StartWorkflowOptions{
		ID:                       subFlowID,
		TaskQueue:                service.TaskQueue,
		WorkflowExecutionTimeout: time.Duration(options.GetFlowTimeoutSeconds()) * time.Second,
		IdReusePolicy:            ptr.Any(subFlowIDReusePolicy(options.GetReusePolicy())),
		RetryPolicy:              options.GetRetryPolicy(),
		SearchAttributes:         index.ConvertAttributeWritesToSearchAttributeUpsertMap(options.GetAttributes()),
		Memo: map[string]interface{}{
			service.WorkerAddressMemoKey: &dexpb.EncodedObject{Payload: []byte(flowConfig.GetWorkerTarget().GetAddress())},
			service.WorkflowRequestId:    &dexpb.EncodedObject{Payload: []byte(requestID)},
		},
	}
	workflowOptions.SearchAttributes[service.SearchAttributeDexWorkflowType] = condition.GetSubFlowType()
	workflowOptions.SearchAttributes[service.SearchAttributeDexParentFlowID] = parentFlowID
	if options.GetFlowStartDelaySeconds() > 0 {
		workflowOptions.WorkflowStartDelay = ptr.Any(time.Duration(options.GetFlowStartDelaySeconds()) * time.Second)
	}
	return workflowOptions
}

func subFlowIDReusePolicy(policy dexpb.SubFlowReusePolicy) dexpb.IdReusePolicy {
	switch effectiveSubFlowReusePolicy(policy) {
	case dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ATTACH:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE
	case dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
	default:
		return dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY
	}
}

// CleanupBlobsAfterAllRunsDeleted deletes blobs after the backend removes every run.
func (a *Activities) CleanupBlobsAfterAllRunsDeleted(
	ctx context.Context, input *dexpb.CleanupBlobStoreActivityInput,
) (*dexpb.CleanupBlobStoreActivityOutput, error) {
	store := a.blobStore
	provider := a.activityProvider
	logger := provider.GetLogger(ctx)
	logger.Info("CleanupBlobsAfterAllRunsDeleted started")
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
			parsedPath, parseErr := blobstore.ParseWorkflowPath(workflowPath)
			if parseErr != nil {
				logger.Info("CleanupBlobsAfterAllRunsDeleted skipped workflow path", "path", workflowPath)
				continue
			}
			_, err := client.DescribeWorkflowExecution(ctx, parsedPath.FlowID, parsedPath.RunID, nil)
			if client.IsNotFoundError(err) {
				if err := store.DeleteWorkflowObjects(ctx, input.GetStoreId(), workflowPath); err != nil {
					logger.Error("CleanupBlobsAfterAllRunsDeleted failed to delete workflow objects", "workflowPath", workflowPath, "error", err)
					return nil, err
				}
				totalDeleted++
				logger.Info("CleanupBlobsAfterAllRunsDeleted deleted workflow objects", "workflowPath", workflowPath)
			} else if err != nil {
				logger.Error("CleanupBlobsAfterAllRunsDeleted failed to describe workflow", "workflowPath", workflowPath, "error", err)
				return nil, err
			}
			provider.RecordHeartbeat(ctx)
		}
		if continueToken == nil {
			break
		}
	}
	logger.Info("CleanupBlobsAfterAllRunsDeleted completed", "totalDeleted", totalDeleted)
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
	ctx context.Context,
	writes []*dexpb.AttributeWrite,
	flowId string,
	invocationId string,
) error {
	if !a.cfg.BlobStore.EffectiveEnabled() || a.blobStore == nil {
		return nil
	}
	return blobstore.OffloadLargeAttributeWrites(
		ctx, writes, flowId, invocationId, a.cfg.BlobStore.EffectiveThresholdInBytes(), a.blobStore, true,
	)
}

func (a *Activities) offloadWorkerSideEffects(
	ctx context.Context,
	stepLocals []*dexpb.KV,
	recordEvents []*dexpb.KV,
	channelMessages []*dexpb.ChannelMessage,
	flowId string,
	invocationId string,
) error {
	threshold := a.cfg.BlobStore.EffectiveThresholdInBytes()
	if err := blobstore.OffloadLargeKVs(
		ctx, stepLocals, flowId, invocationId, threshold, a.blobStore, a.cfg.BlobStore.EffectiveEnabled(),
	); err != nil {
		return err
	}
	if err := blobstore.OffloadLargeKVs(
		ctx, recordEvents, flowId, invocationId, threshold, a.blobStore, a.cfg.BlobStore.EffectiveEnabled(),
	); err != nil {
		return err
	}
	return blobstore.OffloadLargeChannelMessages(
		ctx, channelMessages, flowId, invocationId, threshold, a.blobStore, a.cfg.BlobStore.EffectiveEnabled(),
	)
}

func activityInvocationId(activityInfo interfaces.ActivityInfo) string {
	return activityInfo.WorkflowExecution.RunID + activityInfo.ActivityID
}

// reuseOrOffloadDecisionInputs accepts worker-echoed blob ids as-is, reuses the
// original blob id when the concrete payload matches, otherwise offloads.
func (a *Activities) reuseOrOffloadDecisionInputs(
	ctx context.Context,
	decision *dexpb.StepDecision,
	originalStepInputBlob stepInputBlob,
	hydratedStepInput *dexpb.Value,
	flowId string,
	invocationId string,
) error {
	if decision == nil || !a.cfg.BlobStore.EffectiveEnabled() || a.blobStore == nil {
		return nil
	}
	for _, step := range decision.GetNextSteps() {
		if err := a.reuseOrOffloadStepInput(
			ctx,
			step,
			originalStepInputBlob,
			hydratedStepInput,
			flowId,
			invocationId,
		); err != nil {
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
		if err := a.offloadStepInput(
			ctx,
			closeInput,
			flowId,
			invocationId,
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *Activities) reuseOrOffloadStepInput(
	ctx context.Context,
	step *dexpb.StepMovement,
	originalStepInputBlob stepInputBlob,
	hydratedStepInput *dexpb.Value,
	flowId string,
	invocationId string,
) error {
	if step == nil || step.GetStepInput() == nil ||
		!a.cfg.BlobStore.EffectiveEnabled() || a.blobStore == nil {
		return nil
	}
	if stepInputBlobRef(step.GetStepInput()).id != "" {
		return nil
	}
	if shouldReuseStepInputBlob(originalStepInputBlob, hydratedStepInput, step.GetStepInput()) {
		step.StepInput = originalStepInputBlob.toValue()
		return nil
	}
	return a.offloadStepInput(ctx, step.StepInput, flowId, invocationId)
}

func (a *Activities) offloadStepInput(
	ctx context.Context,
	stepInput *dexpb.Value,
	flowId string,
	invocationId string,
) error {
	return blobstore.OffloadLargeValue(
		ctx,
		stepInput,
		flowId,
		invocationId,
		a.cfg.BlobStore.EffectiveThresholdInBytes(),
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

func validateExecuteResponse(
	resp *dexpb.InvokeExecuteMethodResponse,
	isTransientStep bool,
) error {
	if err := validateStepDecision(resp.GetStepDecision()); err != nil {
		return err
	}
	if isTransientStep {
		if err := validateTransientDeadEndDecision(resp.GetStepDecision()); err != nil {
			return err
		}
	}
	return validateWorkerExecuteResponse(resp)
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

func validateTransientStepMovement(movement *dexpb.StepMovement) error {
	if movement == nil {
		return nil
	}
	if movement.GetStepType() == "" {
		return fmt.Errorf("transient step type is empty")
	}
	options := movement.GetStepOptions()
	if !options.GetSkipWaitFor() {
		return fmt.Errorf("transient step must skip WaitFor")
	}
	if options.GetWaitForFailurePolicy() ==
		dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE {
		return fmt.Errorf("transient step cannot proceed on WaitFor failure")
	}
	if options.GetExecuteFailurePolicy() ==
		dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP {
		return fmt.Errorf("transient step cannot proceed on Execute failure")
	}
	if options.GetExecuteFailureProceedStepType() != "" {
		return fmt.Errorf("transient step cannot configure an Execute failure step")
	}
	if options.GetExecuteFailureProceedStepOptions() != nil {
		return fmt.Errorf("transient step cannot configure Execute failure step options")
	}
	return nil
}

func validateTransientDeadEndDecision(decision *dexpb.StepDecision) error {
	if decision == nil || len(decision.GetNextSteps()) != 0 ||
		decision.GetCloseDecision().GetCloseDecisionType() !=
			dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END {
		return fmt.Errorf("transient step requires a DeadEnd close decision")
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

	for i, subFlowCondition := range waiting.GetSubFlowConditions() {
		if subFlowCondition == nil {
			return fmt.Errorf("SubFlow condition at index %d is nil", i)
		}
		if err := registerWaitingConditionId(
			declaredIds,
			subFlowCondition.GetConditionId(),
			"SubFlow",
			conditionIdsRequired,
		); err != nil {
			return err
		}
		if subFlowCondition.GetSubFlowType() == "" || subFlowCondition.GetStartStepType() == "" {
			return fmt.Errorf("SubFlow condition at index %d requires Flow and starting Step types", i)
		}
		if subFlowCondition.GetSubFlowIndex() != int32(i) {
			return fmt.Errorf("SubFlow condition at index %d has unstable index %d", i, subFlowCondition.GetSubFlowIndex())
		}
		if subFlowCondition.GetOptions().GetFlowTimeoutSeconds() < 0 ||
			subFlowCondition.GetOptions().GetFlowStartDelaySeconds() < 0 {
			return fmt.Errorf("SubFlow condition at index %d has a negative duration", i)
		}
		reusePolicy := subFlowCondition.GetOptions().GetReusePolicy()
		if _, known := dexpb.SubFlowReusePolicy_name[int32(reusePolicy)]; !known {
			return fmt.Errorf("SubFlow condition at index %d has an unknown reuse policy", i)
		}
		if err := workerclient.RejectWorkerBlobIDs(subFlowCondition.GetStepInput()); err != nil {
			return err
		}
		if err := workerclient.RejectWorkerAttributeWriteBlobIDs(
			subFlowCondition.GetOptions().GetAttributes(),
		); err != nil {
			return err
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

func composeLocalActivityMetadata(ctx *dexpb.Context) *dexpb.LocalActivityMetadata {
	return &dexpb.LocalActivityMetadata{
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

// Worker and server errors become InternalActivityError before backend-specific encoding.
func newWorkerActivityFailure(
	provider interfaces.ActivityProvider,
	backendType service.BackendType,
	err error,
	localActivityFailure *dexpb.InternalLocalStepActivityFailure,
) error {
	grpcStatus, ok := status.FromError(err)
	if !ok {
		return newServerActivityFailure(provider, err, localActivityFailure)
	}

	// Error() is "rpc error: code=… desc=<Message>"; do not concat both.
	detail := grpcStatus.Message()
	if detail == "" {
		detail = err.Error()
	}
	activityError := &dexpb.InternalActivityError{
		WorkerGrpcStatus: int32(grpcStatus.Code()),
	}
	for _, statusDetail := range grpcStatus.Details() {
		workerError, ok := statusDetail.(*dexpb.WorkerErrorResponse)
		if !ok {
			continue
		}
		if backendType == service.BackendTypeCadence && workerError.GetRetryAfterSeconds() != 0 {
			invalidActivityError := &dexpb.InternalActivityError{
				ServerDetail: "WorkerErrorResponse.retry_after_seconds requires the Temporal backend",
			}
			return newBackendActivityFailure(
				provider,
				dexpb.FlowErrorType_FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE,
				invalidActivityError,
				localActivityFailure,
				0,
			)
		}
		activityError.WorkerError = &dexpb.InternalWorkerError{
			Detail:     workerError.GetDetail(),
			ErrorType:  workerError.GetErrorType(),
			StackTrace: workerError.GetStackTrace(),
		}
		if activityError.GetWorkerError().GetDetail() == "" {
			activityError.WorkerError.Detail = detail
		}
		retryAfterSeconds := workerError.GetRetryAfterSeconds()
		return newBackendActivityFailure(
			provider,
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			activityError,
			localActivityFailure,
			retryAfterSeconds,
		)
	}
	activityError.ServerDetail = detail
	return newBackendActivityFailure(
		provider,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		activityError,
		localActivityFailure,
		0,
	)
}

func newServerActivityFailure(
	provider interfaces.ActivityProvider,
	err error,
	localActivityFailure *dexpb.InternalLocalStepActivityFailure,
) error {
	return newBackendActivityFailure(
		provider,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
		&dexpb.InternalActivityError{ServerDetail: err.Error()},
		localActivityFailure,
		0,
	)
}

func newBackendActivityFailure(
	provider interfaces.ActivityProvider,
	errorType dexpb.FlowErrorType,
	activityError *dexpb.InternalActivityError,
	failure *dexpb.InternalLocalStepActivityFailure,
	retryAfterSeconds int32,
) error {
	if activityError == nil {
		panic("activity error required")
	}
	if failure != nil {
		// Nesting keeps the local marker at one details payload.
		failure.ActivityError = activityError
		return provider.NewLocalActivityError(errorType, failure, retryAfterSeconds)
	}
	return provider.NewActivityError(errorType, activityError, retryAfterSeconds)
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

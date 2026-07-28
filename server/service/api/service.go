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

package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	uclient "github.com/superdurable/iwf/service/client"
	"github.com/superdurable/iwf/service/common/blobstore"
	serviceerrors "github.com/superdurable/iwf/service/common/errors"
	"github.com/superdurable/iwf/service/common/grpctarget"
	"github.com/superdurable/iwf/service/common/index"
	"github.com/superdurable/iwf/service/common/log"
	"github.com/superdurable/iwf/service/common/log/tag"
	"github.com/superdurable/iwf/service/common/ptr"
	"github.com/superdurable/iwf/service/common/rpc"
	"github.com/superdurable/iwf/service/common/utils"
	"github.com/superdurable/iwf/service/common/workerclient"
	interpreterconfig "github.com/superdurable/iwf/service/interpreter/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type serviceImpl struct {
	client         uclient.UnifiedClient
	store          blobstore.BlobStore
	taskQueue      string
	logger         log.Logger
	apiCfg         *config.ApiConfig
	extStore       *config.ExternalStorageConfig
	interpreterCfg *config.Interpreter
	workerPool     *workerclient.Pool
}

func NewApiService(
	apiCfg *config.ApiConfig,
	extStore *config.ExternalStorageConfig,
	interpreterCfg *config.Interpreter,
	client uclient.UnifiedClient,
	taskQueue string,
	logger log.Logger,
	store blobstore.BlobStore,
) (ApiService, error) {
	if apiCfg == nil || extStore == nil || interpreterCfg == nil {
		panic("API service requires non-nil config sections")
	}
	if client == nil || logger == nil || taskQueue == "" {
		panic("API service requires non-nil dependencies and a task queue")
	}
	if extStore.Enabled && store == nil {
		panic("API service requires a blob store when external storage is enabled")
	}
	activityCfg := &interpreterCfg.InterpreterActivityConfig
	workerPool, err := workerclient.NewPool(workerclient.Config{
		IdleTimeout:     activityCfg.EffectiveWorkerConnectionIdleTimeout(),
		MaxConnections:  activityCfg.EffectiveMaxWorkerConnections(),
		MaxMessageBytes: apiCfg.EffectiveGrpcMaxMessageBytes(),
		DefaultHeaders:  activityCfg.DefaultHeaders,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("create API worker client pool: %w", err)
	}
	return &serviceImpl{
		apiCfg:         apiCfg,
		extStore:       extStore,
		client:         client,
		store:          store,
		taskQueue:      taskQueue,
		logger:         logger,
		interpreterCfg: interpreterCfg,
		workerPool:     workerPool,
	}, nil
}

func (s *serviceImpl) Close() {
	s.workerPool.Close()
	s.client.Close()
}

func (s *serviceImpl) StartFlow(
	ctx context.Context,
	req *iwfpb.StartFlowRequest,
) (*iwfpb.StartFlowResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetFlowType() == "" {
		return nil, makeInvalidRequestError("flow ID and flow type are required")
	}
	if req.GetFlowTimeoutSeconds() <= 0 {
		return nil, makeInvalidRequestError("flow timeout must be positive")
	}
	workerTarget, err := grpctarget.NormalizeWorkerTarget(req.GetWorkerTarget())
	if err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if err := workerclient.RejectWorkerBlobIDs(req.GetStepInput()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}

	startOptions := req.GetFlowStartOptions()
	attributes := startOptions.GetAttributes()
	if err := validateAttributeWrites(attributes); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}

	searchAttributes := index.ConvertAttributeWritesToSearchAttributeUpsertMap(attributes)
	searchAttributes[service.SearchAttributeIwfWorkflowType] = req.GetFlowType()

	if err := blobstore.ValidateWorkflowId(req.GetFlowId()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if err := blobstore.OffloadLargeValue(
		ctx,
		req.GetStepInput(),
		req.GetFlowId(),
		s.extStore.ThresholdInBytes,
		s.store,
		s.extStore.Enabled,
	); err != nil {
		return nil, s.handleError(err)
	}
	if err := blobstore.OffloadLargeAttributeWrites(
		ctx,
		attributes,
		req.GetFlowId(),
		s.extStore.ThresholdInBytes,
		s.store,
		s.extStore.Enabled,
	); err != nil {
		return nil, s.handleError(err)
	}

	initAttributes := make([]*iwfpb.KV, 0, len(attributes))
	for _, attribute := range attributes {
		if _, isNull := attribute.GetValue().GetKind().(*iwfpb.Value_NullValue); isNull {
			continue
		}
		initAttributes = append(initAttributes, &iwfpb.KV{
			Key:   attribute.GetKey(),
			Value: attribute.GetValue(),
		})
	}

	var workflowConfig iwfpb.FlowConfig
	if s.interpreterCfg.DefaultWorkflowConfig == nil {
		workflowConfig = *config.DefaultWorkflowConfig
	} else {
		workflowConfig = *s.interpreterCfg.DefaultWorkflowConfig
	}
	if startOptions.GetFlowConfigOverride() != nil {
		overrideWorkflowConfig(*startOptions.GetFlowConfigOverride(), &workflowConfig)
	}
	if err := interpreterconfig.ValidateFlowConfig(&workflowConfig); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}

	workflowOptions := uclient.StartWorkflowOptions{
		ID:                       req.GetFlowId(),
		TaskQueue:                s.taskQueue,
		WorkflowExecutionTimeout: time.Duration(req.GetFlowTimeoutSeconds()) * time.Second,
		SearchAttributes:         searchAttributes,
		Memo: map[string]interface{}{
			service.WorkerTargetMemoKey: &iwfpb.EncodedObject{
				Payload: []byte(workerTarget),
			},
		},
	}
	ignoreAlreadyStartedError := false
	requestId := ""
	if startOptions != nil {
		if startOptions.GetIdReusePolicy() != iwfpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED {
			if _, known := iwfpb.IdReusePolicy_name[int32(startOptions.GetIdReusePolicy())]; !known {
				return nil, makeInvalidRequestError("unknown ID reuse policy")
			}
			workflowOptions.IdReusePolicy = ptr.Any(startOptions.GetIdReusePolicy())
		}
		if startOptions.GetCronSchedule() != "" {
			workflowOptions.CronSchedule = ptr.Any(startOptions.GetCronSchedule())
		}
		workflowOptions.RetryPolicy = startOptions.GetRetryPolicy()
		if startOptions.GetFlowStartDelaySeconds() < 0 {
			return nil, makeInvalidRequestError("flow start delay must be non-negative")
		}
		if startOptions.GetFlowStartDelaySeconds() > 0 {
			workflowOptions.WorkflowStartDelay = ptr.Any(
				time.Duration(startOptions.GetFlowStartDelaySeconds()) * time.Second,
			)
		}
		if alreadyStartedOptions := startOptions.GetFlowAlreadyStartedOptions(); alreadyStartedOptions != nil {
			ignoreAlreadyStartedError = alreadyStartedOptions.GetIgnoreAlreadyStartedError()
			requestId = alreadyStartedOptions.GetRequestId()
			if requestId != "" {
				workflowOptions.Memo[service.WorkflowRequestId] = &iwfpb.EncodedObject{
					Payload: []byte(requestId),
				}
			}
		}
	}

	input := &iwfpb.InterpreterWorkflowInput{
		FlowType:       req.GetFlowType(),
		WorkerTarget:   workerTarget,
		StartStepType:  req.GetStartStepType(),
		StepInput:      req.GetStepInput(),
		StepOptions:    req.GetStepOptions(),
		InitAttributes: initAttributes,
		Config:         &workflowConfig,
	}

	runId, err := s.client.StartInterpreterWorkflow(ctx, workflowOptions, input)
	if err != nil {
		shouldReturnError := true
		if s.client.IsWorkflowAlreadyStartedError(err) && ignoreAlreadyStartedError {
			alreadyRunningRunId, hasRunId := s.client.GetRunIdFromWorkflowAlreadyStartedError(err)
			runId = alreadyRunningRunId
			if requestId == "" {
				shouldReturnError = false
			} else {
				if !hasRunId {
					runId = ""
				}
				response, descErr := s.client.DescribeWorkflowExecution(ctx, req.GetFlowId(), runId, nil)
				if descErr != nil {
					return nil, s.handleError(descErr)
				}
				requestMemo := response.Memos[service.WorkflowRequestId]
				if requestMemo.GetObjValue() != nil &&
					string(requestMemo.GetObjValue().GetPayload()) == requestId {
					shouldReturnError = false
				}
			}
		}
		if shouldReturnError {
			return nil, s.handleError(err)
		}
	} else {
		s.logger.Info("Started flow", tag.WorkflowID(req.GetFlowId()), tag.WorkflowRunID(runId))
	}
	return &iwfpb.StartFlowResponse{RunId: runId}, nil
}

func overrideWorkflowConfig(configOverride iwfpb.FlowConfig, workflowConfig *iwfpb.FlowConfig) {
	if configOverride.ActiveStepSearchMode != nil {
		workflowConfig.ActiveStepSearchMode = configOverride.ActiveStepSearchMode
	}
	if configOverride.ContinueAsNewThreshold != nil {
		workflowConfig.ContinueAsNewThreshold = configOverride.ContinueAsNewThreshold
	}
	if configOverride.ContinueAsNewPageSizeInBytes != nil {
		workflowConfig.ContinueAsNewPageSizeInBytes = configOverride.ContinueAsNewPageSizeInBytes
	}
	if configOverride.StepDurability != nil {
		workflowConfig.StepDurability = configOverride.StepDurability
	}
}

func (s *serviceImpl) WaitForStepCompletion(ctx context.Context, req *iwfpb.WaitForStepCompletionRequest) (*iwfpb.WaitForStepCompletionResponse, error) {
	if s.client.GetBackendType() == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "WaitForStepCompletion requires Temporal synchronous update")
	}
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	if req.GetStepType() == "" || req.GetStepExecutionNumber() == "" {
		return nil, makeInvalidRequestError("step type and step execution number are required")
	}
	stepExecutionNumber, err := strconv.ParseInt(
		req.GetStepExecutionNumber(),
		10,
		32,
	)
	if err != nil || stepExecutionNumber <= 0 {
		return nil, makeInvalidRequestError("step execution number must be a positive integer")
	}
	waitCtx, cancel, deadline := s.waitContext(ctx, req.GetWaitTimeSeconds())
	defer cancel()
	var response iwfpb.WaitForStepCompletionResponse
	backoff := 25 * time.Millisecond
	originalWaitSeconds := req.GetWaitTimeSeconds()
	for {
		req.WaitTimeSeconds = remainingWaitSeconds(deadline, req.GetWaitTimeSeconds())
		err := s.client.SynchronousUpdateWorkflow(
			waitCtx,
			&response,
			req.GetFlowId(),
			"",
			service.WaitForStepCompletionUpdateType,
			req,
		)
		if err == nil {
			return &response, nil
		}
		if s.client.GetApplicationErrorTypeIfIsApplicationError(err) !=
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED.String() {
			return nil, s.handleError(err)
		}
		if originalWaitSeconds == 0 {
			return nil, serviceerrors.DeadlineExceededLongPoll(
				"continue-as-new exhausted the immediate-check budget",
			).ToGRPCError()
		}
		if err := waitForCANRetry(waitCtx, deadline, backoff); err != nil {
			return nil, waitContextStatus(err)
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
}

func (s *serviceImpl) WaitForAttribute(ctx context.Context, req *iwfpb.WaitForAttributeRequest) (*emptypb.Empty, error) {
	if s.client.GetBackendType() == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "WaitForAttribute requires Temporal synchronous update")
	}
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	equal := req.GetCondition().GetEqual()
	if equal == nil || equal.GetKey() == "" || equal.GetValue() == nil ||
		equal.GetValue().GetKind() == nil {
		return nil, makeInvalidRequestError("attribute equality key and value are required")
	}
	if err := workerclient.RejectWorkerBlobIDs(equal.GetValue()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	waitCtx, cancel, deadline := s.waitContext(ctx, req.GetWaitTimeSeconds())
	defer cancel()
	var response emptypb.Empty
	backoff := 25 * time.Millisecond
	originalWaitSeconds := req.GetWaitTimeSeconds()
	for {
		req.WaitTimeSeconds = remainingWaitSeconds(deadline, req.GetWaitTimeSeconds())
		err := s.client.SynchronousUpdateWorkflow(
			waitCtx,
			&response,
			req.GetFlowId(),
			"",
			service.WaitForAttributeUpdateType,
			req,
		)
		if err == nil {
			return &response, nil
		}
		if s.client.GetApplicationErrorTypeIfIsApplicationError(err) !=
			iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED.String() {
			return nil, s.handleError(err)
		}
		if originalWaitSeconds == 0 {
			return nil, serviceerrors.DeadlineExceededLongPoll(
				"continue-as-new exhausted the immediate-check budget",
			).ToGRPCError()
		}
		if err := waitForCANRetry(waitCtx, deadline, backoff); err != nil {
			return nil, waitContextStatus(err)
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
}

func (s *serviceImpl) PublishToChannel(
	ctx context.Context,
	req *iwfpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	for _, message := range req.GetMessages() {
		if message == nil || message.GetChannelName() == "" || message.GetValue() == nil ||
			message.GetValue().GetKind() == nil {
			return nil, makeInvalidRequestError("channel name and value are required")
		}
		if err := workerclient.RejectWorkerBlobIDs(message.GetValue()); err != nil {
			return nil, makeInvalidRequestError(err.Error())
		}
	}
	if len(req.GetMessages()) == 0 {
		return &emptypb.Empty{}, nil
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.ExecuteRpcSignalChannelName,
		&iwfpb.ExecuteRpcSignalRequest{PublishToChannel: req.GetMessages()},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) UpdateFlowConfig(
	ctx context.Context,
	req *iwfpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" || req.GetFlowConfig() == nil {
		return nil, makeInvalidRequestError("flow ID and flow config are required")
	}
	if err := interpreterconfig.ValidateFlowConfig(req.GetFlowConfig()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.UpdateConfigSignalChannelName,
		req,
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) TriggerContinueAsNew(
	ctx context.Context,
	req *iwfpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.TriggerContinueAsNewSignalChannelName,
		&emptypb.Empty{},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) StopFlow(ctx context.Context, req *iwfpb.StopFlowRequest) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	var err error
	switch req.GetStopType() {
	case iwfpb.StopType_STOP_TYPE_CANCEL:
		err = s.client.CancelWorkflow(ctx, req.GetFlowId(), req.GetRunId())
	case iwfpb.StopType_STOP_TYPE_TERMINATE:
		err = s.client.TerminateWorkflow(ctx, req.GetFlowId(), req.GetRunId(), req.GetReason())
	case iwfpb.StopType_STOP_TYPE_FAIL:
		err = s.client.SignalWorkflow(
			ctx,
			req.GetFlowId(),
			req.GetRunId(),
			service.FailWorkflowSignalChannelName,
			&iwfpb.FailFlowSignalRequest{Reason: req.GetReason()},
		)
	default:
		return nil, makeInvalidRequestError("stop type is required")
	}
	if err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) GetAttributes(
	ctx context.Context,
	req *iwfpb.GetAttributesRequest,
) (*iwfpb.GetAttributesResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	var queryResponse iwfpb.GetAttributesQueryResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&queryResponse,
		req.GetFlowId(),
		req.GetRunId(),
		service.GetAttributesWorkflowQueryType,
		&iwfpb.GetAttributesQueryRequest{
			Keys:    req.GetKeys(),
			AllKeys: req.GetAllKeys(),
		},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &iwfpb.GetAttributesResponse{Attributes: queryResponse.GetAttributes()}, nil
}

func (s *serviceImpl) SetAttributes(
	ctx context.Context,
	req *iwfpb.SetAttributesRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	if err := validateAttributeWrites(req.GetAttributes()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	attributes := req.GetAttributes()
	if err := blobstore.OffloadLargeAttributeWrites(
		ctx,
		attributes,
		req.GetFlowId(),
		s.extStore.ThresholdInBytes,
		s.store,
		s.extStore.Enabled,
	); err != nil {
		return nil, s.handleError(err)
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.ExecuteRpcSignalChannelName,
		&iwfpb.ExecuteRpcSignalRequest{UpsertAttributes: attributes},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) LoadBlobs(ctx context.Context, req *iwfpb.LoadBlobsRequest) (*iwfpb.LoadBlobsResponse, error) {
	if req == nil || len(req.GetBlobIds()) == 0 {
		return &iwfpb.LoadBlobsResponse{Values: map[string]*iwfpb.Value{}}, nil
	}
	values := make(map[string]*iwfpb.Value, len(req.GetBlobIds()))
	for _, blobId := range req.GetBlobIds() {
		if blobId == "" {
			return nil, makeInvalidRequestError("blob ID is required")
		}
		value := &iwfpb.Value{
			Kind: &iwfpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: blobId,
			},
		}
		if err := blobstore.HydrateValue(ctx, value, s.store); err != nil {
			return nil, s.handleError(err)
		}
		values[blobId] = value
	}
	return &iwfpb.LoadBlobsResponse{Values: values}, nil
}

func (s *serviceImpl) WaitForFlow(
	ctx context.Context,
	req *iwfpb.WaitForFlowRequest,
) (*iwfpb.WaitForFlowResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	getCtx, cancel := utils.TrimContextByTimeoutWithCappedDDL(
		ctx,
		ptr.Any(req.GetWaitTimeSeconds()),
		s.apiCfg.EffectiveMaxWaitSeconds(),
	)
	defer cancel()
	var output iwfpb.InterpreterWorkflowOutput
	runId, flowStatus, getErr := s.client.GetWorkflowResult(
		getCtx,
		&output,
		req.GetFlowId(),
		req.GetRunId(),
	)
	response := &iwfpb.WaitForFlowResponse{
		RunId:      runId,
		FlowStatus: flowStatus,
	}
	if getErr == nil {
		if req.GetNeedsResults() {
			response.Results = output.GetStepCompletionOutputs()
		}
		return response, nil
	}
	if getCtx.Err() != nil {
		return nil, waitContextStatus(getCtx.Err())
	}
	if s.client.IsRequestTimeoutError(getErr) {
		return nil, serviceerrors.DeadlineExceededLongPoll(
			"flow is still running and waiting exceeded the timeout",
		).ToGRPCError()
	}

	errorType := s.client.GetApplicationErrorTypeIfIsApplicationError(getErr)
	if errorType != "" {
		errorTypeValue, known := iwfpb.FlowErrorType_value[errorType]
		if !known {
			return nil, s.handleError(getErr)
		}
		_, errorMessage := s.client.GetApplicationErrorTypeAndDetails(getErr)
		response.FlowStatus = iwfpb.FlowStatus_FLOW_STATUS_FAILED
		response.ErrorType = iwfpb.FlowErrorType(errorTypeValue)
		response.ErrorMessage = errorMessage
		return response, nil
	}

	switch flowStatus {
	case iwfpb.FlowStatus_FLOW_STATUS_CANCELED,
		iwfpb.FlowStatus_FLOW_STATUS_TERMINATED,
		iwfpb.FlowStatus_FLOW_STATUS_TIMEOUT:
		return response, nil
	case iwfpb.FlowStatus_FLOW_STATUS_FAILED:
		response.ErrorMessage = "unknown flow failure from interpreter implementation"
		return response, nil
	default:
		return nil, s.handleError(getErr)
	}
}

func (s *serviceImpl) SearchFlows(
	ctx context.Context,
	req *iwfpb.SearchFlowsRequest,
) (*iwfpb.SearchFlowsResponse, error) {
	if req == nil || req.GetPageSize() < 0 {
		return nil, makeInvalidRequestError("page size must be non-negative")
	}
	pageSize := int32(1000)
	if req.GetPageSize() > 0 {
		pageSize = req.GetPageSize()
	}
	response, err := s.client.ListWorkflow(ctx, &uclient.ListWorkflowExecutionsRequest{
		PageSize:      pageSize,
		Query:         req.GetQuery(),
		NextPageToken: []byte(req.GetNextPageToken()),
	})
	if err != nil {
		return nil, s.handleError(err)
	}
	return &iwfpb.SearchFlowsResponse{
		FlowRuns:      response.Executions,
		NextPageToken: string(response.NextPageToken),
	}, nil
}

func (s *serviceImpl) InvokeRPC(
	ctx context.Context,
	req *iwfpb.InvokeRPCRequest,
) (*iwfpb.InvokeRPCResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRpcName() == "" {
		return nil, makeInvalidRequestError("flow ID and RPC name are required")
	}
	if req.GetTimeoutSeconds() < 0 {
		return nil, makeInvalidRequestError("RPC timeout must be non-negative")
	}
	if err := workerclient.RejectWorkerBlobIDs(req.GetInput()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if len(req.GetLockAttributeKeys()) > 0 {
		return s.handleRpcBySynchronousUpdate(ctx, req)
	}

	var preparation iwfpb.PrepareRpcQueryResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&preparation,
		req.GetFlowId(),
		req.GetRunId(),
		service.PrepareRpcQueryType,
		&iwfpb.PrepareRpcQueryRequest{},
	); err != nil {
		return nil, s.handleError(err)
	}
	workerResponse, err := rpc.InvokeWorkerRpc(
		ctx,
		s.workerPool,
		&preparation,
		req,
		s.apiCfg.EffectiveMaxWaitSeconds(),
		s.store,
		s.extStore,
	)
	if err != nil {
		return nil, err
	}
	decision := workerResponse.GetStepDecision()
	if len(workerResponse.GetUpsertAttributes()) > 0 ||
		len(workerResponse.GetRecordEvents()) > 0 ||
		len(workerResponse.GetPublishToChannel()) > 0 ||
		len(decision.GetNextSteps()) > 0 ||
		decision.GetConditionalClose() != nil {
		signalRequest := &iwfpb.ExecuteRpcSignalRequest{
			RpcInput:         req.GetInput(),
			RpcOutput:        workerResponse.GetOutput(),
			UpsertAttributes: workerResponse.GetUpsertAttributes(),
			StepDecision:     workerResponse.GetStepDecision(),
			RecordEvents:     workerResponse.GetRecordEvents(),
			PublishToChannel: workerResponse.GetPublishToChannel(),
		}
		if s.apiCfg.OmitRpcInputOutputInHistory != nil && *s.apiCfg.OmitRpcInputOutputInHistory {
			signalRequest.RpcInput = nil
			signalRequest.RpcOutput = nil
		}
		if err := s.client.SignalWorkflow(
			ctx,
			req.GetFlowId(),
			req.GetRunId(),
			service.ExecuteRpcSignalChannelName,
			signalRequest,
		); err != nil {
			return nil, s.handleError(err)
		}
	}
	return &iwfpb.InvokeRPCResponse{Output: workerResponse.GetOutput()}, nil
}

func (s *serviceImpl) handleRpcBySynchronousUpdate(
	ctx context.Context,
	req *iwfpb.InvokeRPCRequest,
) (*iwfpb.InvokeRPCResponse, error) {
	if s.client.GetBackendType() == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "locking RPC requires Temporal synchronous update")
	}
	var result iwfpb.InvokeRpcUpdateResult
	if err := s.client.SynchronousUpdateWorkflow(
		ctx,
		&result,
		req.GetFlowId(),
		req.GetRunId(),
		service.ExecuteOptimisticLockingRpcUpdateType,
		req,
	); err != nil {
		return nil, s.handleError(err)
	}
	if result.GetResponse() == nil {
		return nil, serviceerrors.Internal("locking RPC returned no response").ToGRPCError()
	}
	return result.GetResponse(), nil
}

func (s *serviceImpl) ResetFlow(
	ctx context.Context,
	req *iwfpb.ResetFlowRequest,
) (*iwfpb.ResetFlowResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	runId, err := s.client.ResetWorkflow(ctx, req)
	if err != nil {
		return nil, s.handleError(err)
	}
	return &iwfpb.ResetFlowResponse{RunId: runId}, nil
}

func (s *serviceImpl) SkipTimer(
	ctx context.Context,
	req *iwfpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" || req.GetStepExecutionId() == "" {
		return nil, makeInvalidRequestError("flow ID and step execution ID are required")
	}
	if req.GetTimerConditionId() == "" && req.TimerConditionIndex == nil {
		return nil, makeInvalidRequestError("timer condition ID or index is required")
	}
	var timerInfos iwfpb.GetCurrentTimerInfosQueryResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&timerInfos,
		req.GetFlowId(),
		req.GetRunId(),
		service.GetCurrentTimerInfosQueryType,
	); err != nil {
		return nil, s.handleError(err)
	}
	stepTimerInfos := timerInfos.GetStepExecutionCurrentTimerInfos()[req.GetStepExecutionId()]
	if _, valid := service.ValidateTimerSkipRequest(
		stepTimerInfos.GetTimers(),
		req.GetTimerConditionId(),
		int(req.GetTimerConditionIndex()),
	); !valid {
		return nil, makeInvalidRequestError("requested timer condition does not exist or is not pending")
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.SkipTimerSignalChannelName,
		&iwfpb.SkipTimerSignalRequest{
			StepExecutionId:     req.GetStepExecutionId(),
			TimerConditionId:    req.GetTimerConditionId(),
			TimerConditionIndex: req.GetTimerConditionIndex(),
		},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) DumpFlowForContinueAsNew(
	ctx context.Context,
	req *iwfpb.ContinueAsNewDumpRequest,
) (*iwfpb.ContinueAsNewDumpResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRunId() == "" {
		return nil, makeInvalidRequestError("flow ID and run ID are required")
	}
	var response iwfpb.ContinueAsNewDumpResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&response,
		req.GetFlowId(),
		req.GetRunId(),
		service.ContinueAsNewDumpByPageQueryType,
		req,
	); err != nil {
		return nil, s.handleError(err)
	}
	return &response, nil
}

func (s *serviceImpl) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*iwfpb.HealthInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Hostname Not Available"
	}
	return &iwfpb.HealthInfo{
		Condition: "OK",
		Hostname:  hostname,
		Duration:  0,
	}, nil
}

func (s *serviceImpl) waitContext(
	parent context.Context,
	requestedSeconds int32,
) (context.Context, context.CancelFunc, time.Time) {
	if requestedSeconds == 0 {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, time.Time{}
	}
	effectiveSeconds := int64(requestedSeconds)
	if maximum := s.apiCfg.EffectiveMaxWaitSeconds(); effectiveSeconds > maximum {
		effectiveSeconds = maximum
	}
	deadline := time.Now().Add(time.Duration(effectiveSeconds) * time.Second)
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	return ctx, cancel, deadline
}

func remainingWaitSeconds(deadline time.Time, originalSeconds int32) int32 {
	if deadline.IsZero() {
		return originalSeconds
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	seconds := (remaining + time.Second - 1) / time.Second
	return int32(seconds)
}

func waitForCANRetry(
	ctx context.Context,
	deadline time.Time,
	backoff time.Duration,
) error {
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		if backoff > remaining {
			backoff = remaining
		}
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *serviceImpl) handleError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return waitContextStatus(err)
	}
	if s.client.IsNotFoundError(err) {
		return serviceerrors.NotFound(err.Error()).ToGRPCError()
	}
	if s.client.IsRequestTimeoutError(err) {
		return serviceerrors.DeadlineExceededLongPoll(err.Error()).ToGRPCError()
	}
	if s.client.IsWorkflowAlreadyStartedError(err) {
		return serviceerrors.AlreadyExists(err.Error()).ToGRPCError()
	}
	errorTypeName := s.client.GetApplicationErrorTypeIfIsApplicationError(err)
	if errorTypeName == "" {
		s.logger.Error("encountered API server error", tag.Error(err))
		return serviceerrors.Internal(err.Error()).ToGRPCError()
	}
	errorTypeValue, known := iwfpb.UpdateErrorType_value[errorTypeName]
	if known {
		var details string
		if detailsErr := s.client.GetApplicationErrorDetails(err, &details); detailsErr != nil {
			s.logger.Error("failed to decode update error details", tag.Error(detailsErr))
			return serviceerrors.Internal(err.Error()).ToGRPCError()
		}
		switch iwfpb.UpdateErrorType(errorTypeValue) {
		case iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT:
			return serviceerrors.InvalidArgument(
				iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
				details,
			).ToGRPCError()
		case iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION:
			return serviceerrors.NewErrorAndStatus(
				codes.FailedPrecondition,
				iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
				details,
			).ToGRPCError()
		case iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED:
			return serviceerrors.DeadlineExceededLongPoll(details).ToGRPCError()
		case iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE:
			return serviceerrors.AbortedLockFailure(details).ToGRPCError()
		case iwfpb.UpdateErrorType_UPDATE_ERROR_TYPE_SERVER_INTERNAL:
			return serviceerrors.Internal(details).ToGRPCError()
		default:
			return serviceerrors.Internal(details).ToGRPCError()
		}
	}

	flowErrorTypeValue, known := iwfpb.FlowErrorType_value[errorTypeName]
	if known {
		flowErrorType := iwfpb.FlowErrorType(flowErrorTypeValue)
		switch flowErrorType {
		case iwfpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			iwfpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL:
			var errorResponse iwfpb.ErrorResponse
			if detailsErr := s.client.GetApplicationErrorDetails(
				err,
				&errorResponse,
			); detailsErr != nil {
				s.logger.Error("failed to decode flow error details", tag.Error(detailsErr))
				return serviceerrors.Internal(err.Error()).ToGRPCError()
			}
			if errorResponse.GetDetail() == "" &&
				errorResponse.GetSubStatus() == iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNSPECIFIED &&
				errorResponse.GetOriginalWorkerErrorDetail() == "" &&
				errorResponse.GetOriginalWorkerErrorType() == "" &&
				errorResponse.GetOriginalWorkerErrorStatus() == 0 {
				return serviceerrors.Internal(err.Error()).ToGRPCError()
			}
			grpcCode := codes.Internal
			if flowErrorType == iwfpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL {
				grpcCode = codes.Code(errorResponse.GetOriginalWorkerErrorStatus())
			}
			return serviceerrors.NewErrorAndStatusWithWorkerError(
				grpcCode,
				errorResponse.GetSubStatus(),
				errorResponse.GetDetail(),
				errorResponse.GetOriginalWorkerErrorDetail(),
				errorResponse.GetOriginalWorkerErrorType(),
				errorResponse.GetOriginalWorkerErrorStatus(),
			).ToGRPCError()
		}
	}

	s.logger.Error("encountered unknown application error", tag.Error(err))
	return serviceerrors.Internal(err.Error()).ToGRPCError()
}

func waitContextStatus(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	return serviceerrors.DeadlineExceededLongPoll(err.Error()).ToGRPCError()
}

func makeInvalidRequestError(details string) error {
	return serviceerrors.InvalidArgument(
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		details,
	).ToGRPCError()
}

// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/client/history"
	"github.com/superdurable/dex/service/common/attributestore"
	"github.com/superdurable/dex/service/common/blobstore"
	serviceerrors "github.com/superdurable/dex/service/common/errors"
	"github.com/superdurable/dex/service/common/grpctarget"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/common/retry"
	"github.com/superdurable/dex/service/common/rpc"
	"github.com/superdurable/dex/service/common/utils"
	"github.com/superdurable/dex/service/common/workerclient"
	"github.com/superdurable/dex/service/indexsync"
	interpreterconfig "github.com/superdurable/dex/service/interpreter/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultHistoryPageSize = 100

type serviceImpl struct {
	client             uclient.UnifiedClient
	store              blobstore.BlobStore
	taskQueue          string
	logger             log.Logger
	apiCfg             *config.ApiConfig
	blobStoreCfg       *config.BlobStoreConfig
	interpreterCfg     *config.Interpreter
	workerPool         *workerclient.WorkerClientPool
	stepInputPopulator *history.AsyncStepInputSnapshotPopulator
	attributeStore     *attributestore.Manager
	indexSynchronizer  *indexsync.Synchronizer
}

func NewApiService(
	apiCfg *config.ApiConfig,
	blobStoreCfg *config.BlobStoreConfig,
	interpreterCfg *config.Interpreter,
	client uclient.UnifiedClient,
	taskQueue string,
	logger log.Logger,
	store blobstore.BlobStore,
	attributeStore *attributestore.Manager,
	workerPool *workerclient.WorkerClientPool,
) (ApiService, error) {
	if apiCfg == nil || blobStoreCfg == nil || interpreterCfg == nil {
		panic("API service requires non-nil config sections")
	}
	if client == nil || logger == nil || workerPool == nil || attributeStore == nil || taskQueue == "" {
		panic("API service requires non-nil dependencies and a task queue")
	}
	if blobStoreCfg.EffectiveEnabled() && store == nil {
		panic("API service requires a blob store when blob storage is enabled")
	}
	return &serviceImpl{
		apiCfg:             apiCfg,
		blobStoreCfg:       blobStoreCfg,
		client:             client,
		store:              store,
		taskQueue:          taskQueue,
		logger:             logger,
		interpreterCfg:     interpreterCfg,
		workerPool:         workerPool,
		stepInputPopulator: history.NewAsyncStepInputSnapshotPopulator(blobStoreCfg, client, store),
		attributeStore:     attributeStore,
		indexSynchronizer:  indexsync.New(interpreterCfg, client, logger),
	}, nil
}

func (s *serviceImpl) Close() {
	s.client.Close()
}

func (s *serviceImpl) StartFlow(
	ctx context.Context,
	req *dexpb.StartFlowRequest,
) (*dexpb.StartFlowResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetFlowType() == "" {
		return nil, makeInvalidRequestError("flow ID and flow type are required")
	}
	if req.GetRequestId() == "" {
		return nil, makeInvalidRequestError("request ID is required")
	}
	if req.GetFlowTimeoutSeconds() < 0 {
		return nil, makeInvalidRequestError("flow timeout must be non-negative")
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
	searchAttributes[service.SearchAttributeDexWorkflowType] = req.GetFlowType()

	if err := blobstore.ValidateWorkflowId(req.GetFlowId()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if err := blobstore.OffloadLargeValue(
		ctx,
		req.GetStepInput(),
		req.GetFlowId(),
		req.GetRequestId(),
		s.blobStoreCfg.EffectiveThresholdInBytes(),
		s.store,
		s.blobStoreCfg.EffectiveEnabled(),
	); err != nil {
		return nil, s.handleError(err)
	}
	if err := blobstore.OffloadLargeAttributeWrites(
		ctx,
		attributes,
		req.GetFlowId(),
		req.GetRequestId(),
		s.blobStoreCfg.EffectiveThresholdInBytes(),
		s.store,
		s.blobStoreCfg.EffectiveEnabled(),
	); err != nil {
		return nil, s.handleError(err)
	}

	var workflowConfig dexpb.FlowConfig
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
	if err := s.validateAttributeStoreName(workflowConfig.GetAttributeSyncConfigName()); err != nil {
		return nil, err
	}
	workerTarget, err := grpctarget.NormalizeWorkerTarget(workflowConfig.GetWorkerTarget())
	if err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	workflowConfig.WorkerTarget = workerTarget

	workflowOptions := uclient.StartWorkflowOptions{
		ID:                       req.GetFlowId(),
		TaskQueue:                s.taskQueue,
		WorkflowExecutionTimeout: time.Duration(req.GetFlowTimeoutSeconds()) * time.Second,
		SearchAttributes:         searchAttributes,
		Memo: map[string]interface{}{
			service.WorkerAddressMemoKey: &dexpb.EncodedObject{
				Payload: []byte(workerTarget.GetAddress()),
			},
			service.WorkflowRequestId: &dexpb.EncodedObject{
				Payload: []byte(req.GetRequestId()),
			},
		},
		IdReusePolicy: ptr.Any(dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING),
	}
	ignoreAlreadyStartedError := false
	if startOptions != nil {
		if startOptions.GetIdReusePolicy() != dexpb.IdReusePolicy_ID_REUSE_POLICY_UNSPECIFIED {
			if _, known := dexpb.IdReusePolicy_name[int32(startOptions.GetIdReusePolicy())]; !known {
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
		}
	}

	input := &dexpb.InterpreterWorkflowInput{
		FlowType:       req.GetFlowType(),
		StartStepType:  req.GetStartStepType(),
		StepInput:      req.GetStepInput(),
		StepOptions:    req.GetStepOptions(),
		InitAttributes: attributes,
		Config:         &workflowConfig,
	}

	runId, err := s.client.StartInterpreterWorkflow(ctx, workflowOptions, input)
	if err != nil {
		shouldReturnError := true
		if s.client.IsWorkflowAlreadyStartedError(err) && ignoreAlreadyStartedError {
			alreadyRunningRunId, _ := s.client.GetRunIdFromWorkflowAlreadyStartedError(err)
			runId = alreadyRunningRunId
			response, descErr := s.client.DescribeWorkflowExecution(ctx, req.GetFlowId(), runId, nil)
			if descErr != nil {
				return nil, s.handleError(descErr)
			}
			requestMemo := response.Memos[service.WorkflowRequestId]
			if requestMemo.GetObjValue() != nil &&
				string(requestMemo.GetObjValue().GetPayload()) == req.GetRequestId() {
				shouldReturnError = false
			}
		}
		if shouldReturnError {
			return nil, s.handleError(err)
		}
	} else {
		s.logger.Info("Started flow", tag.WorkflowID(req.GetFlowId()), tag.WorkflowRunID(runId))
	}
	return &dexpb.StartFlowResponse{RunId: runId}, nil
}

func overrideWorkflowConfig(configOverride dexpb.FlowConfig, workflowConfig *dexpb.FlowConfig) {
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
	if configOverride.WorkerTarget != nil {
		workflowConfig.WorkerTarget = configOverride.WorkerTarget
	}
	if configOverride.AttributeSyncConfigName != nil {
		workflowConfig.AttributeSyncConfigName = configOverride.AttributeSyncConfigName
	}
}

func (s *serviceImpl) WaitForStepCompletion(ctx context.Context, req *dexpb.WaitForStepCompletionRequest) (*dexpb.WaitForStepCompletionResponse, error) {
	if s.client.GetBackendType() == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "WaitForStepCompletion requires Temporal synchronous update")
	}
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	if req.GetRequestId() == "" {
		return nil, makeInvalidRequestError("request ID is required")
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
	var response dexpb.WaitForStepCompletionResponse
	backoff := 25 * time.Millisecond
	originalWaitSeconds := req.GetWaitTimeSeconds()
	for {
		req.WaitTimeSeconds = remainingWaitSeconds(deadline, req.GetWaitTimeSeconds())
		err := s.client.SynchronousUpdateWorkflow(
			waitCtx,
			&response,
			req.GetFlowId(),
			"",
			req.GetRequestId(),
			service.WaitForStepCompletionUpdateType,
			req,
		)
		if err == nil {
			return &response, nil
		}
		if s.client.IsNotFoundError(err) {
			completed, queryErr := s.isStepExecutionCompleted(
				waitCtx,
				req.GetFlowId(),
				req.GetStepType(),
				int32(stepExecutionNumber),
			)
			if queryErr == nil && completed {
				return &response, nil
			}
			if queryErr != nil && !s.client.IsNotFoundError(queryErr) {
				s.logger.Warn(
					"failed to recover step completion after flow closed",
					tag.WorkflowID(req.GetFlowId()),
					tag.Error(queryErr),
				)
			}
			return nil, s.handleError(err)
		}
		if updateType, ok := s.client.GetIfUpdateError(err, nil); !ok ||
			updateType != dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED {
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

func (s *serviceImpl) isStepExecutionCompleted(
	ctx context.Context,
	flowID string,
	stepType string,
	stepExecutionNumber int32,
) (bool, error) {
	var completed bool
	if err := s.client.QueryWorkflow(
		ctx,
		&completed,
		flowID,
		"",
		service.IsStepExecutionCompletedQueryType,
		stepType,
		stepExecutionNumber,
	); err != nil {
		return false, err
	}
	return completed, nil
}

func (s *serviceImpl) WaitForAttribute(ctx context.Context, req *dexpb.WaitForAttributeRequest) (*emptypb.Empty, error) {
	if s.client.GetBackendType() == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "WaitForAttribute requires Temporal synchronous update")
	}
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	if req.GetRequestId() == "" {
		return nil, makeInvalidRequestError("request ID is required")
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
			req.GetRequestId(),
			service.WaitForAttributeUpdateType,
			req,
		)
		if err == nil {
			return &response, nil
		}
		if updateType, ok := s.client.GetIfUpdateError(err, nil); !ok ||
			updateType != dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED {
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
	req *dexpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	for _, message := range req.GetMessages() {
		if message == nil || message.GetChannelName() == "" {
			return nil, makeInvalidRequestError("channel name is required")
		}
		if message.GetValue() == nil {
			message.Value = &dexpb.Value{}
		}
		if err := workerclient.RejectWorkerBlobIDs(message.GetValue()); err != nil {
			return nil, makeInvalidRequestError(err.Error())
		}
	}
	if len(req.GetMessages()) == 0 {
		return &emptypb.Empty{}, nil
	}
	if err := blobstore.OffloadLargeChannelMessages(
		ctx,
		req.GetMessages(),
		req.GetFlowId(),
		uuid.NewString(),
		s.blobStoreCfg.EffectiveThresholdInBytes(),
		s.store,
		s.blobStoreCfg.EffectiveEnabled(),
	); err != nil {
		return nil, s.handleError(err)
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.ExecuteRpcSignalChannelName,
		&dexpb.ExecuteRpcSignalRequest{PublishToChannel: req.GetMessages()},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) UpdateFlowConfig(
	ctx context.Context,
	req *dexpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" || req.GetFlowConfig() == nil {
		return nil, makeInvalidRequestError("flow ID and flow config are required")
	}
	if err := interpreterconfig.ValidateFlowConfig(req.GetFlowConfig()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if req.GetFlowConfig().AttributeSyncConfigName != nil {
		if err := s.validateAttributeStoreName(req.GetFlowConfig().GetAttributeSyncConfigName()); err != nil {
			return nil, err
		}
	}
	if req.GetFlowConfig().GetWorkerTarget() != nil {
		workerTarget, err := grpctarget.NormalizeWorkerTarget(req.GetFlowConfig().GetWorkerTarget())
		if err != nil {
			return nil, makeInvalidRequestError(err.Error())
		}
		req.GetFlowConfig().WorkerTarget = workerTarget
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
	req *dexpb.TriggerContinueAsNewRequest,
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

func (s *serviceImpl) StopFlow(ctx context.Context, req *dexpb.StopFlowRequest) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	stopType := req.GetStopType()
	if stopType == dexpb.StopType_STOP_TYPE_UNSPECIFIED {
		stopType = dexpb.StopType_STOP_TYPE_CANCEL
	}
	var err error
	switch stopType {
	case dexpb.StopType_STOP_TYPE_CANCEL, dexpb.StopType_STOP_TYPE_FAIL:
		err = s.client.SignalWorkflow(
			ctx,
			req.GetFlowId(),
			req.GetRunId(),
			service.StopWorkflowSignalChannelName,
			&dexpb.StopFlowSignalRequest{StopType: stopType, Reason: req.GetReason()},
		)
	case dexpb.StopType_STOP_TYPE_TERMINATE:
		err = s.client.TerminateWorkflow(ctx, req.GetFlowId(), req.GetRunId(), req.GetReason())
	default:
		return nil, makeInvalidRequestError("stop type is required")
	}
	if err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) validateAttributeStoreName(name string) error {
	if name == "" || s.attributeStore.HasStore(name) {
		return nil
	}
	return makeInvalidRequestError(fmt.Sprintf("unknown Attribute Store %q", name))
}

func (s *serviceImpl) GetAttributes(
	ctx context.Context,
	req *dexpb.GetAttributesRequest,
) (*dexpb.GetAttributesResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	var queryResponse dexpb.GetAttributesQueryResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&queryResponse,
		req.GetFlowId(),
		req.GetRunId(),
		service.GetAttributesWorkflowQueryType,
		&dexpb.GetAttributesQueryRequest{
			Keys:    req.GetKeys(),
			AllKeys: req.GetAllKeys(),
		},
	); err != nil {
		return nil, s.handleError(err)
	}
	attributes := queryResponse.GetAttributes()
	if !s.blobStoreCfg.EffectiveLazyLoading() {
		if err := blobstore.HydrateKVs(ctx, attributes, s.store); err != nil {
			return nil, s.handleError(err)
		}
	}
	return &dexpb.GetAttributesResponse{Attributes: attributes}, nil
}

func (s *serviceImpl) SetAttributes(
	ctx context.Context,
	req *dexpb.SetAttributesRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	if req.GetRequestId() == "" {
		return nil, makeInvalidRequestError("request ID is required")
	}
	if err := validateAttributeWrites(req.GetAttributes()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	attributes := req.GetAttributes()
	if err := blobstore.OffloadLargeAttributeWrites(
		ctx,
		attributes,
		req.GetFlowId(),
		req.GetRequestId(),
		s.blobStoreCfg.EffectiveThresholdInBytes(),
		s.store,
		s.blobStoreCfg.EffectiveEnabled(),
	); err != nil {
		return nil, s.handleError(err)
	}
	if err := s.client.SignalWorkflow(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		service.ExecuteRpcSignalChannelName,
		&dexpb.ExecuteRpcSignalRequest{
			IsSetAttributeApi: true,
			UpsertAttributes:  attributes,
		},
	); err != nil {
		return nil, s.handleError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *serviceImpl) LoadBlobs(ctx context.Context, req *dexpb.LoadBlobsRequest) (*dexpb.LoadBlobsResponse, error) {
	if req == nil || len(req.GetValues()) == 0 {
		return &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{}}, nil
	}
	values := make(map[string]*dexpb.Value, len(req.GetValues()))
	for _, value := range req.GetValues() {
		blobId, hydrateValue, err := blobArmForLoad(value)
		if err != nil {
			return nil, makeInvalidRequestError(err.Error())
		}
		if err := blobstore.HydrateValue(ctx, hydrateValue, s.store); err != nil {
			if blobstore.IsObjectUnavailable(err) {
				continue
			}
			return nil, s.handleError(err)
		}
		values[blobId] = hydrateValue
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

// blobArmForLoad requires a blob-id arm and returns a fresh Value for hydrate.
func blobArmForLoad(value *dexpb.Value) (blobId string, hydrateValue *dexpb.Value, err error) {
	if value == nil {
		return "", nil, fmt.Errorf("value is required")
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		if kind.InternalBlobIdForStringValue == "" {
			return "", nil, fmt.Errorf("blob ID is required")
		}
		return kind.InternalBlobIdForStringValue, &dexpb.Value{
			Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: kind.InternalBlobIdForStringValue,
			},
		}, nil
	case *dexpb.Value_InternalBlobIdForObjValue:
		if kind.InternalBlobIdForObjValue == "" {
			return "", nil, fmt.Errorf("blob ID is required")
		}
		return kind.InternalBlobIdForObjValue, &dexpb.Value{
			Kind: &dexpb.Value_InternalBlobIdForObjValue{
				InternalBlobIdForObjValue: kind.InternalBlobIdForObjValue,
			},
		}, nil
	default:
		return "", nil, fmt.Errorf("LoadBlobs accepts only blob-id arms, not payloads")
	}
}

func (s *serviceImpl) WaitForFlow(
	ctx context.Context,
	req *dexpb.WaitForFlowRequest,
) (*dexpb.FlowResult, error) {
	if req == nil || req.GetFlowId() == "" || req.GetWaitTimeSeconds() < 0 {
		return nil, makeInvalidRequestError("valid flow ID and non-negative wait time are required")
	}
	getCtx, cancel := utils.TrimContextByTimeoutWithCappedDDL(
		ctx,
		ptr.Any(req.GetWaitTimeSeconds()),
		s.apiCfg.EffectiveMaxWaitSeconds(),
	)
	defer cancel()
	var output dexpb.InterpreterWorkflowOutput
	resolvedRunID, flowStatus, getErr := s.client.GetWorkflowResult(
		getCtx,
		&output,
		req.GetFlowId(),
		req.GetRunId(),
	)
	response := &dexpb.FlowResult{
		FlowStatus: flowStatus,
		FlowId:     req.GetFlowId(),
		RunId:      resolvedRunID,
	}
	if getErr == nil {
		if req.GetNeedsResults() {
			response.Results = output.GetStepCompletionOutputs()
		}
		return response, nil
	}
	if errors.Is(getCtx.Err(), context.DeadlineExceeded) || s.client.IsRequestTimeoutError(getErr) {
		return nil, serviceerrors.DeadlineExceededLongPoll(
			"flow is still running and waiting exceeded the timeout",
		).ToGRPCError()
	}
	if getCtx.Err() != nil {
		return nil, waitContextStatus(getCtx.Err())
	}

	var errorResponse dexpb.ServiceErrorResponse
	if errorType, ok := s.client.GetIfFlowError(getErr, &errorResponse); ok {
		response.FlowStatus = dexpb.FlowStatus_FLOW_STATUS_FAILED
		response.ErrorType = errorType
		response.ErrorMessage = serviceerrors.ServiceErrorResponseDetail(&errorResponse)
		return response, nil
	}

	switch flowStatus {
	case dexpb.FlowStatus_FLOW_STATUS_CANCELED,
		dexpb.FlowStatus_FLOW_STATUS_TERMINATED,
		dexpb.FlowStatus_FLOW_STATUS_TIMEOUT:
		return response, nil
	case dexpb.FlowStatus_FLOW_STATUS_FAILED:
		response.ErrorMessage = "unknown flow failure from interpreter implementation"
		return response, nil
	default:
		return nil, s.handleError(getErr)
	}
}

func (s *serviceImpl) SearchFlows(
	ctx context.Context,
	req *dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
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
	return &dexpb.SearchFlowsResponse{
		FlowRuns:      response.Executions,
		NextPageToken: string(response.NextPageToken),
	}, nil
}

func (s *serviceImpl) SyncAttributeIndexes(
	ctx context.Context,
	req *dexpb.SyncAttributeIndexRequest,
) (*dexpb.SyncAttributeIndexResponse, error) {
	if req == nil {
		return nil, makeInvalidRequestError("request is required")
	}
	if err := s.indexSynchronizer.Sync(ctx, req.GetAttributeIndexes()); err != nil {
		return nil, err
	}
	return &dexpb.SyncAttributeIndexResponse{}, nil
}

func (s *serviceImpl) GetFlowSummary(
	ctx context.Context,
	req *dexpb.GetFlowSummaryRequest,
) (*dexpb.GetFlowSummaryResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	description, err := s.client.DescribeWorkflowExecution(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		map[string]dexpb.IndexType{
			service.SearchAttributeDexWorkflowType: dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	)
	if err != nil {
		return nil, s.handleError(err)
	}
	response := &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{
			FlowId: req.GetFlowId(),
			RunId:  description.RunId,
		},
		FirstRunId: description.FirstRunId,
		RequestId:  decodeStringMemo(description.Memos[service.WorkflowRequestId]),
		FlowType:   description.IndexedAttributes[service.SearchAttributeDexWorkflowType].GetStringValue(),
		FlowStatus: description.Status,
		StartTime:  timestamppb.New(description.StartTime),
	}
	if description.CloseTime != nil {
		response.CloseTime = timestamppb.New(*description.CloseTime)
	}
	return response, nil
}

func decodeStringMemo(value *dexpb.Value) string {
	if value == nil {
		return ""
	}
	return string(value.GetObjValue().GetPayload())
}

func (s *serviceImpl) GetHistoryEvents(
	ctx context.Context,
	req *dexpb.GetHistoryEventsRequest,
) (*dexpb.GetHistoryEventsResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRunId() == "" {
		return nil, makeInvalidRequestError("flow ID and run ID are required")
	}
	if req.GetStartInternalEventId() < 0 || req.GetEstimatePageSize() < 0 {
		return nil, makeInvalidRequestError("event ID and page size must be non-negative")
	}
	pageSize := req.GetEstimatePageSize()
	if pageSize == 0 {
		pageSize = defaultHistoryPageSize
	}
	history, err := s.client.GetWorkflowHistory(ctx, &uclient.GetWorkflowHistoryRequest{
		WorkflowID:           req.GetFlowId(),
		RunID:                req.GetRunId(),
		StartInternalEventID: req.GetStartInternalEventId(),
		EstimatePageSize:     pageSize,
		NextPageToken:        req.GetNextPageToken(),
	})
	if err != nil {
		return nil, s.handleError(err)
	}
	if err := s.stepInputPopulator.Populate(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		history.Events,
	); err != nil {
		return nil, s.handleError(err)
	}
	return &dexpb.GetHistoryEventsResponse{
		Events:              history.Events,
		NextPageToken:       history.NextPageToken,
		NextInternalEventId: history.NextInternalEventID,
	}, nil
}

func (s *serviceImpl) WaitForHistoryEvent(
	ctx context.Context,
	req *dexpb.WaitForHistoryEventRequest,
) (*dexpb.WaitForHistoryEventResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRunId() == "" || req.GetNextInternalEventId() <= 0 {
		return nil, makeInvalidRequestError("flow ID, run ID, and positive next event ID are required")
	}
	history, err := s.client.WaitForWorkflowHistoryEvent(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		req.GetNextInternalEventId(),
	)
	if err != nil {
		if s.client.IsRequestTimeoutError(err) {
			return nil, serviceerrors.DeadlineExceededLongPoll(
				"history long poll exceeded the deadline",
			).ToGRPCError()
		}
		return nil, s.handleError(err)
	}
	description, err := s.client.DescribeWorkflowExecution(
		ctx,
		req.GetFlowId(),
		req.GetRunId(),
		nil,
	)
	if err != nil {
		return nil, s.handleError(err)
	}
	return &dexpb.WaitForHistoryEventResponse{
		EventAvailable:           history.EventAvailable,
		AvailableInternalEventId: history.AvailableInternalEventID,
		FlowStatus:               description.Status,
	}, nil
}

func (s *serviceImpl) GetFlowState(
	ctx context.Context,
	req *dexpb.GetFlowStateRequest,
) (*dexpb.GetFlowStateResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	description, err := s.client.DescribeWorkflowExecution(ctx, req.GetFlowId(), req.GetRunId(), nil)
	if err != nil {
		return nil, s.handleError(err)
	}
	var dump dexpb.DebugDumpResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&dump,
		req.GetFlowId(),
		description.RunId,
		service.DebugDumpQueryType,
	); err != nil {
		return nil, s.handleError(err)
	}
	snapshot := dump.GetSnapshot()
	activeStepExecutions := dump.GetActiveStepExecutions()
	attachPendingStepFailures(activeStepExecutions, description.PendingStepFailures)
	return &dexpb.GetFlowStateResponse{
		FlowConfig:             dump.GetConfig(),
		Attributes:             snapshot.GetAttributes(),
		ActiveStepExecutions:   activeStepExecutions,
		QueuedSteps:            snapshot.GetStepsToStartFromBeginning(),
		PendingChannelMessages: snapshot.GetChannelReceived(),
		CompletedSteps:         snapshot.GetStepOutputs(),
	}, nil
}

func attachPendingStepFailures(
	activeStepExecutions []*dexpb.ActiveStepExecutionState,
	pendingStepFailures map[string]*dexpb.StepMethodFailure,
) {
	for _, activeStepExecution := range activeStepExecutions {
		failure := pendingStepFailures[activeStepExecution.GetStepExecutionId()]
		if failure != nil {
			activeStepExecution.LastFailureInfo = failure
		}
	}
}

func (s *serviceImpl) InvokeRPC(
	ctx context.Context,
	req *dexpb.InvokeRPCRequest,
) (*dexpb.InvokeRPCResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRpcName() == "" {
		return nil, makeInvalidRequestError("flow ID and RPC name are required")
	}
	if req.GetRequestId() == "" {
		return nil, makeInvalidRequestError("request ID is required")
	}
	if req.GetTimeoutSeconds() < 0 {
		return nil, makeInvalidRequestError("RPC timeout must be non-negative")
	}
	if err := workerclient.RejectWorkerBlobIDs(req.GetInput()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	backendType := s.client.GetBackendType()
	if len(req.GetLockAttributeKeys()) > 0 && backendType == service.BackendTypeCadence {
		return nil, status.Errorf(codes.Unimplemented, "locking RPC requires Temporal synchronous update")
	}
	useSynchronousUpdate := s.shouldInvokeRPCWithSynchronousUpdate(req)
	if err := blobstore.ValidateWorkflowId(req.GetFlowId()); err != nil {
		return nil, makeInvalidRequestError(err.Error())
	}
	if err := blobstore.OffloadLargeValue(
		ctx,
		req.GetInput(),
		req.GetFlowId(),
		req.GetRequestId(),
		s.blobStoreCfg.EffectiveThresholdInBytes(),
		s.store,
		s.blobStoreCfg.EffectiveEnabled(),
	); err != nil {
		return nil, s.handleError(err)
	}

	// Pin each attempt to one run so its query, signal, or Update cannot cross Continue-as-New.
	runID := req.GetRunId()
	if runID == "" {
		description, err := s.client.DescribeWorkflowExecution(ctx, req.GetFlowId(), "", nil)
		if err != nil {
			return nil, s.handleError(err)
		}
		runID = description.RunId
	}

	retryBackoff := retry.NewInvokeRPCBackoff(
		s.apiCfg.InvokeRPCContinuedAsNewErrorRetryPolicy,
	)
	for {
		response, err := s.doInvokeRPC(ctx, req, runID, useSynchronousUpdate)
		if err == nil {
			if !s.blobStoreCfg.EffectiveLazyLoading() {
				if err := blobstore.HydrateValue(ctx, response.GetOutput(), s.store); err != nil {
					return nil, s.handleError(err)
				}
			}
			return response, nil
		}
		updateTransitionError := useSynchronousUpdate && s.isInvokeRPCUpdateTransitionError(err)
		if req.GetRunId() != "" ||
			(!s.client.IsNotFoundError(err) && !updateTransitionError) {
			return nil, s.handleInvokeRPCError(err)
		}
		description, describeErr := s.client.DescribeWorkflowExecution(
			ctx,
			req.GetFlowId(),
			"",
			nil,
		)
		if describeErr != nil {
			s.logger.Warn(
				"failed to resolve current run after RPC error",
				tag.WorkflowID(req.GetFlowId()),
				tag.Error(describeErr),
			)
			return nil, s.handleInvokeRPCError(err)
		}
		if description.RunId == runID && !updateTransitionError {
			return nil, s.handleInvokeRPCError(err)
		}
		shouldRetry, retryErr := retryBackoff.WaitForNextAttempt(ctx)
		if retryErr != nil {
			return nil, s.handleError(retryErr)
		}
		if !shouldRetry {
			return nil, s.handleInvokeRPCError(err)
		}
		runID = description.RunId
	}
}

func (s *serviceImpl) isInvokeRPCUpdateTransitionError(err error) bool {
	if s.client.IsUnknownUpdateError(err, service.InvokeRpcUpdateType) {
		return true
	}
	updateType, updateError := s.client.GetIfUpdateError(err, nil)
	return updateError &&
		updateType == dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_CONTINUE_AS_NEW_PREEMPTED
}

func (s *serviceImpl) doInvokeRPC(
	ctx context.Context,
	req *dexpb.InvokeRPCRequest,
	runID string,
	useSynchronousUpdate bool,
) (*dexpb.InvokeRPCResponse, error) {
	if useSynchronousUpdate {
		return s.doInvokeRpcUpdate(ctx, req, runID)
	}

	var preparation dexpb.PrepareRpcQueryResponse
	if err := s.client.QueryWorkflow(
		ctx,
		&preparation,
		req.GetFlowId(),
		runID,
		service.PrepareRpcQueryType,
		&dexpb.PrepareRpcQueryRequest{},
	); err != nil {
		return nil, err
	}
	workerResponse, err := rpc.InvokeWorkerRpc(
		ctx,
		s.workerPool,
		&preparation,
		req,
		s.apiCfg.EffectiveMaxWaitSeconds(),
		s.store,
		req.GetRequestId(),
		s.blobStoreCfg,
	)
	if err != nil {
		return nil, err
	}
	decision := workerResponse.GetStepDecision()
	if len(workerResponse.GetUpsertAttributes()) > 0 ||
		len(workerResponse.GetRecordEvents()) > 0 ||
		len(workerResponse.GetPublishToChannel()) > 0 ||
		len(decision.GetNextSteps()) > 0 ||
		decision.GetCloseDecision() != nil {
		signalRequest := &dexpb.ExecuteRpcSignalRequest{
			UpsertAttributes: workerResponse.GetUpsertAttributes(),
			StepDecision:     workerResponse.GetStepDecision(),
			RecordEvents:     workerResponse.GetRecordEvents(),
			PublishToChannel: workerResponse.GetPublishToChannel(),
		}
		if s.apiCfg.IncludeRPCInputOutputIntoHistory {
			signalRequest.RpcInput = req.GetInput()
			signalRequest.RpcOutput = workerResponse.GetOutput()
		}
		if err := s.client.SignalWorkflow(
			ctx,
			req.GetFlowId(),
			runID,
			service.ExecuteRpcSignalChannelName,
			signalRequest,
		); err != nil {
			return nil, err
		}
	}
	return &dexpb.InvokeRPCResponse{Output: workerResponse.GetOutput()}, nil
}

func (s *serviceImpl) shouldInvokeRPCWithSynchronousUpdate(req *dexpb.InvokeRPCRequest) bool {
	return s.client.GetBackendType() == service.BackendTypeTemporal &&
		(len(req.GetLockAttributeKeys()) > 0 ||
			s.apiCfg.UseTemporalSynchronousUpdateForAllRPCs)
}

func (s *serviceImpl) handleInvokeRPCError(err error) error {
	if mapped, ok := serviceerrors.WorkerAPIFailure(err); ok {
		return mapped.ToGRPCError()
	}
	return s.handleError(err)
}

func (s *serviceImpl) doInvokeRpcUpdate(
	ctx context.Context,
	req *dexpb.InvokeRPCRequest,
	runID string,
) (*dexpb.InvokeRPCResponse, error) {
	var result dexpb.InvokeRpcUpdateResult
	if err := s.client.SynchronousUpdateWorkflow(
		ctx,
		&result,
		req.GetFlowId(),
		runID,
		req.GetRequestId(),
		service.InvokeRpcUpdateType,
		req,
	); err != nil {
		return nil, err
	}
	if result.GetResponse() == nil {
		return nil, fmt.Errorf("InvokeRpc Update returned no response")
	}
	return result.GetResponse(), nil
}

func (s *serviceImpl) ResetFlow(
	ctx context.Context,
	req *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	if req == nil || req.GetFlowId() == "" {
		return nil, makeInvalidRequestError("flow ID is required")
	}
	runId, err := s.client.ResetWorkflow(ctx, req)
	if err != nil {
		return nil, s.handleError(err)
	}
	return &dexpb.ResetFlowResponse{RunId: runId}, nil
}

func (s *serviceImpl) SkipTimer(
	ctx context.Context,
	req *dexpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	if req == nil || req.GetFlowId() == "" || req.GetStepExecutionId() == "" {
		return nil, makeInvalidRequestError("flow ID and step execution ID are required")
	}
	if req.GetTimerConditionId() == "" && req.TimerConditionIndex == nil {
		return nil, makeInvalidRequestError("timer condition ID or index is required")
	}
	var timerInfos dexpb.GetCurrentTimerInfosQueryResponse
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
		&dexpb.SkipTimerSignalRequest{
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
	req *dexpb.ContinueAsNewDumpRequest,
) (*dexpb.ContinueAsNewDumpResponse, error) {
	if req == nil || req.GetFlowId() == "" || req.GetRunId() == "" {
		return nil, makeInvalidRequestError("flow ID and run ID are required")
	}
	var response dexpb.ContinueAsNewDumpResponse
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

func (s *serviceImpl) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*dexpb.HealthInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Hostname Not Available"
	}
	return &dexpb.HealthInfo{
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
	var details string
	if updateType, ok := s.client.GetIfUpdateError(err, &details); ok {
		switch updateType {
		case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_INVALID_ARGUMENT:
			return serviceerrors.InvalidArgument(
				dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
				details,
			).ToGRPCError()
		case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION:
			return serviceerrors.NewErrorAndStatus(
				codes.FailedPrecondition,
				dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
				details,
			).ToGRPCError()
		case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_DEADLINE_EXCEEDED:
			return serviceerrors.DeadlineExceededLongPoll(details).ToGRPCError()
		case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_RPC_ACQUIRE_LOCK_FAILURE:
			return serviceerrors.AbortedLockFailure(details).ToGRPCError()
		case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_SERVER_INTERNAL:
			return serviceerrors.Internal(details).ToGRPCError()
		default:
			return serviceerrors.Internal(details).ToGRPCError()
		}
	}

	var errorResponse dexpb.ServiceErrorResponse
	if flowErrorType, ok := s.client.GetIfFlowError(err, &errorResponse); ok {
		switch flowErrorType {
		case dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL:
			if errorResponse.GetDetail() == "" &&
				errorResponse.GetSubStatus() == dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNSPECIFIED &&
				errorResponse.GetOriginalWorkerErrorDetail() == "" &&
				errorResponse.GetOriginalWorkerErrorType() == "" &&
				errorResponse.GetOriginalWorkerErrorStatus() == 0 &&
				errorResponse.GetOriginalWorkerErrorStackTrace() == "" {
				return serviceerrors.Internal(err.Error()).ToGRPCError()
			}
			grpcCode := codes.Internal
			if flowErrorType == dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL {
				grpcCode = codes.FailedPrecondition
			}
			return serviceerrors.NewErrorAndStatusWithWorkerError(
				grpcCode,
				errorResponse.GetSubStatus(),
				errorResponse.GetDetail(),
				errorResponse.GetOriginalWorkerErrorDetail(),
				errorResponse.GetOriginalWorkerErrorType(),
				errorResponse.GetOriginalWorkerErrorStatus(),
				errorResponse.GetOriginalWorkerErrorStackTrace(),
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
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		details,
	).ToGRPCError()
}

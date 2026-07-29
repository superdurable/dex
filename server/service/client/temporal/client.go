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

package temporal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/utils"
	"github.com/superdurable/dex/service/interpreter/temporal"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	realtemporal "go.temporal.io/sdk/temporal"
)

type temporalClient struct {
	tClient                        client.Client
	namespace                      string
	dataConverter                  converter.DataConverter
	memoEncryption                 bool // this is a workaround for https://github.com/temporalio/sdk-go/issues/1045
	queryWorkflowFailedRetryPolicy config.QueryWorkflowFailedRetryPolicy
	it                             *temporal.InterpreterWorker
}

func NewTemporalClient(
	tClient client.Client, namespace string, dataConverter converter.DataConverter, memoEncryption bool, retryPolicy *config.QueryWorkflowFailedRetryPolicy,
) uclient.UnifiedClient {
	return &temporalClient{
		tClient:                        tClient,
		namespace:                      namespace,
		dataConverter:                  dataConverter,
		memoEncryption:                 memoEncryption,
		queryWorkflowFailedRetryPolicy: config.QueryWorkflowFailedRetryPolicyWithDefaults(retryPolicy),
	}
}

func (t *temporalClient) Close() {
	t.tClient.Close()
}

func (t *temporalClient) IsWorkflowAlreadyStartedError(err error) bool {
	if err.Error() == "schedule with this ID is already registered" {
		// there is no type to check, just a string
		// https://github.com/temporalio/sdk-go/blob/d10e87118a07b44fd09bf88d39a628f0e6e70c34/internal/error.go#L336
		return true
	}
	return realtemporal.IsWorkflowExecutionAlreadyStartedError(err)
}

func (t *temporalClient) GetRunIdFromWorkflowAlreadyStartedError(err error) (string, bool) {
	var workflowExecutionAlreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	ok := errors.As(err, &workflowExecutionAlreadyStarted)

	runId := ""
	if ok {
		runId = workflowExecutionAlreadyStarted.RunId
	}

	return runId, ok
}

func (t *temporalClient) IsNotFoundError(err error) bool {
	var notFound *serviceerror.NotFound
	ok := errors.As(err, &notFound)
	return ok
}

func (t *temporalClient) isQueryFailedError(err error) bool {
	var serviceError *serviceerror.QueryFailed
	ok := errors.As(err, &serviceError)
	return ok
}

func (t *temporalClient) IsRequestTimeoutError(err error) bool {
	var deadlineExceeded *serviceerror.DeadlineExceeded
	ok := errors.As(err, &deadlineExceeded)
	if ok {
		return ok
	}
	var canceled *serviceerror.Canceled
	ok = errors.As(err, &canceled)
	return ok
}

func (t *temporalClient) IsWorkflowTimeoutError(err error) bool {
	return realtemporal.IsTimeoutError(err)
}

func (t *temporalClient) GetIfUpdateError(err error, detail *string) (dexpb.UpdateErrorType, bool) {
	typeName := t.extractAppErrType(err)
	value, ok := dexpb.UpdateErrorType_value[typeName]
	if !ok {
		return 0, false
	}
	var decoded string
	if decodeErr := t.decodeAppErrDetails(err, &decoded); decodeErr == nil && detail != nil {
		*detail = decoded
	}
	return dexpb.UpdateErrorType(value), true
}

func (t *temporalClient) GetIfFlowError(err error, resp *dexpb.ErrorResponse) (dexpb.FlowErrorType, bool) {
	typeName := t.extractAppErrType(err)
	value, ok := dexpb.FlowErrorType_value[typeName]
	if !ok {
		return 0, false
	}
	if resp == nil {
		panic("resp required")
	}
	if decodeErr := t.decodeAppErrDetails(err, resp); decodeErr != nil {
		if resp.Detail == "" {
			resp.Detail = err.Error()
		}
	}
	return dexpb.FlowErrorType(value), true
}

func (t *temporalClient) extractAppErrType(err error) string {
	var applicationError *realtemporal.ApplicationError
	if errors.As(err, &applicationError) {
		return applicationError.Type()
	}
	return ""
}

func (t *temporalClient) decodeAppErrDetails(err error, detailsPtr interface{}) error {
	var applicationError *realtemporal.ApplicationError
	if !errors.As(err, &applicationError) {
		return fmt.Errorf("not an application error")
	}
	if !applicationError.HasDetails() {
		return fmt.Errorf("application error doesn't have details")
	}
	return applicationError.Details(detailsPtr)
}

func (t *temporalClient) StartInterpreterWorkflow(
	ctx context.Context, options uclient.StartWorkflowOptions, args ...interface{},
) (runId string, err error) {
	memo, err := t.encryptMemoIfNeeded(options.Memo)
	if err != nil {
		return "", err
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                                       options.ID,
		TaskQueue:                                options.TaskQueue,
		WorkflowExecutionTimeout:                 options.WorkflowExecutionTimeout,
		SearchAttributes:                         options.SearchAttributes,
		Memo:                                     memo,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}

	if options.IdReusePolicy != nil {
		workflowIdReusePolicy, err := mapToTemporalWorkflowIdReusePolicy(*options.IdReusePolicy)
		if err != nil {
			return "", err
		}

		workflowOptions.WorkflowIDReusePolicy = *workflowIdReusePolicy
	}

	if options.RetryPolicy != nil {
		workflowOptions.RetryPolicy = mapToTemporalRetryPolicy(options.RetryPolicy)
	}

	if options.CronSchedule != nil && *options.CronSchedule != "" {
		// use temporal schedule instead of cron
		// https://temporal.io/blog/how-do-i-convert-my-cron-into-a-schedule
		// workflowOptions.CronSchedule = *options.CronSchedule

		_, err := t.tClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID: "schedule for workflow: " + options.ID,
			Spec: client.ScheduleSpec{
				CronExpressions: []string{*options.CronSchedule},
			},
			Action: &client.ScheduleWorkflowAction{
				ID:                       workflowOptions.ID,
				TaskQueue:                workflowOptions.TaskQueue,
				Workflow:                 t.it.Engine,
				Args:                     args,
				WorkflowExecutionTimeout: workflowOptions.WorkflowExecutionTimeout,
				RetryPolicy:              workflowOptions.RetryPolicy,
				Memo:                     workflowOptions.Memo,
				TypedSearchAttributes:    workflowOptions.TypedSearchAttributes,
			},
		})

		return "", err
	}

	if options.WorkflowStartDelay != nil {
		workflowOptions.StartDelay = *options.WorkflowStartDelay
	}

	run, err := t.tClient.ExecuteWorkflow(ctx, workflowOptions, t.it.Engine, args...)
	if err != nil {
		return "", err
	}
	return run.GetRunID(), nil
}

func (t *temporalClient) StartBlobStoreCleanupWorkflow(
	ctx context.Context, taskQueue, workflowID, cronSchedule, storeId string,
) error {

	if cronSchedule == "" {
		_, err := t.tClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: taskQueue,
		}, t.it.BlobStoreCleanup, storeId)
		return err
	}

	_, err := t.tClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: workflowID,
		Spec: client.ScheduleSpec{
			CronExpressions: []string{cronSchedule},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:                       workflowID,
			TaskQueue:                taskQueue,
			Workflow:                 t.it.BlobStoreCleanup,
			Args:                     []interface{}{storeId},
			WorkflowExecutionTimeout: 0,
		},
	})
	return err
}

func (t *temporalClient) SignalWorkflow(
	ctx context.Context, workflowID string, runID string, signalName string, arg interface{},
) error {
	return t.tClient.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (t *temporalClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	return t.tClient.CancelWorkflow(ctx, workflowID, runID)
}

func (t *temporalClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string) error {
	var reasonStr string
	if reason == "" {
		reasonStr = "Force termiantion from user"
	} else {
		reasonStr = reason
	}

	return t.tClient.TerminateWorkflow(ctx, workflowID, runID, reasonStr)
}

func (t *temporalClient) ListWorkflow(
	ctx context.Context, request *uclient.ListWorkflowExecutionsRequest,
) (*uclient.ListWorkflowExecutionsResponse, error) {
	listReq := &workflowservice.ListWorkflowExecutionsRequest{
		PageSize:      request.PageSize,
		Query:         request.Query,
		NextPageToken: request.NextPageToken,
	}
	resp, err := t.tClient.ListWorkflow(ctx, listReq)
	if err != nil {
		return nil, err
	}
	var executions []*dexpb.SearchFlowsResponseEntry
	for _, exe := range resp.GetExecutions() {
		executions = append(executions, &dexpb.SearchFlowsResponseEntry{
			FlowId: exe.Execution.WorkflowId,
			RunId:  exe.Execution.RunId,
		})
	}
	return &uclient.ListWorkflowExecutionsResponse{
		Executions:    executions,
		NextPageToken: resp.NextPageToken,
	}, nil
}

func (t *temporalClient) QueryWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string, queryType string, args ...interface{},
) error {
	var qres converter.EncodedValue
	var err error

	attempt := 1
	// Only QueryFailed error causes retry; all other errors make the loop to finish immediately
	for attempt <= t.queryWorkflowFailedRetryPolicy.MaximumAttempts {
		qres, err = t.tClient.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
		if err == nil {
			break
		} else {
			if t.isQueryFailedError(err) {
				time.Sleep(time.Duration(t.queryWorkflowFailedRetryPolicy.InitialIntervalSeconds) * time.Second)
				attempt++
				continue
			}
			return err
		}
	}
	if err != nil {
		return err
	}
	return qres.Get(valuePtr)
}

func (t *temporalClient) DescribeWorkflowExecution(
	ctx context.Context, workflowID, runID string, indexedAttrTypes map[string]dexpb.IndexType,
) (*uclient.DescribeWorkflowExecutionResponse, error) {
	resp, err := t.tClient.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return nil, err
	}
	status, err := mapToDexWorkflowStatus(resp.GetWorkflowExecutionInfo().GetStatus())
	if err != nil {
		return nil, err
	}

	indexedAttributes, err := index.MapTemporalSearchAttributeFieldsToAttrValues(resp.GetWorkflowExecutionInfo().GetSearchAttributes(), indexedAttrTypes)
	if err != nil {
		return nil, err
	}

	memo, err := t.getMemoAndDecryptIfNeeded(resp.GetWorkflowExecutionInfo().GetMemo())
	if err != nil {
		return nil, err
	}

	return &uclient.DescribeWorkflowExecutionResponse{
		RunId:                resp.GetWorkflowExecutionInfo().GetExecution().GetRunId(),
		FirstRunId:           resp.GetWorkflowExecutionInfo().GetFirstRunId(),
		Status:               status,
		IndexedAttributes:    indexedAttributes,
		Memos:                memo,
		FlowStartedTimestamp: utils.ToNanoSeconds(resp.GetWorkflowExecutionInfo().GetStartTime()),
	}, nil
}

func (t *temporalClient) encryptMemoIfNeeded(rawMemo map[string]interface{}) (map[string]interface{}, error) {
	if !t.memoEncryption || rawMemo == nil {
		return rawMemo, nil
	}

	out := map[string]interface{}{}
	for k, v := range rawMemo {

		pl, err := t.dataConverter.ToPayload(v)
		if err != nil {
			return nil, err
		}
		out[k] = pl
	}
	return out, nil
}

func (t *temporalClient) getMemoAndDecryptIfNeeded(memo *common.Memo) (map[string]*dexpb.Value, error) {
	if memo == nil || len(memo.GetFields()) == 0 {
		return nil, nil
	}

	out := map[string]*dexpb.Value{}
	for k, payload := range memo.GetFields() {
		var value dexpb.EncodedObject

		if t.memoEncryption {
			// Newer Temporal SDKs apply the configured DataConverter (including its
			// PayloadCodec) to memos (sdk-go #1045), whereas older SDKs used the default
			// converter. dex also pre-encrypts the memo value, so the stored memo is
			// double-wrapped by the encrypting converter. Decode twice through that same
			// converter: first to recover the inner (pre-encrypted) payload, then the value.
			var encryptedPayload common.Payload
			err := t.dataConverter.FromPayload(payload, &encryptedPayload)
			if err != nil {
				return nil, err
			}

			err = t.dataConverter.FromPayload(&encryptedPayload, &value)
			if err != nil {
				return nil, err
			}
		} else {
			err := converter.GetDefaultDataConverter().FromPayload(payload, &value)
			if err != nil {
				return nil, err
			}
		}
		out[k] = &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &value}}
	}
	return out, nil
}

func mapToTemporalWorkflowIdReusePolicy(idReusePolicy dexpb.IdReusePolicy) (*enums.WorkflowIdReusePolicy, error) {
	var res enums.WorkflowIdReusePolicy
	switch idReusePolicy {
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING:
		res = enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY:
		res = enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE:
		res = enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING:
		res = enums.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING
		return &res, nil
	default:
		return nil, fmt.Errorf("unsupported workflow id reuse policy %s", idReusePolicy)
	}
}

// mapToTemporalRetryPolicy fills unset (zero-value) fields with the same
// defaults dex has always used for flow retries.
func mapToTemporalRetryPolicy(policy *dexpb.FlowRetryPolicy) *realtemporal.RetryPolicy {
	if policy == nil {
		return nil
	}

	initialIntervalSeconds := policy.GetInitialIntervalSeconds()
	if initialIntervalSeconds <= 0 {
		initialIntervalSeconds = 1
	}
	backoffCoefficient := policy.GetBackoffCoefficient()
	if backoffCoefficient <= 0 {
		backoffCoefficient = 2
	}
	maximumIntervalSeconds := policy.GetMaximumIntervalSeconds()
	if maximumIntervalSeconds <= 0 {
		maximumIntervalSeconds = 100
	}

	return &realtemporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initialIntervalSeconds),
		MaximumInterval:    time.Second * time.Duration(maximumIntervalSeconds),
		MaximumAttempts:    policy.GetMaximumAttempts(),
		BackoffCoefficient: float64(backoffCoefficient),
	}
}

func mapToDexWorkflowStatus(status enums.WorkflowExecutionStatus) (dexpb.FlowStatus, error) {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return dexpb.FlowStatus_FLOW_STATUS_CANCELED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return dexpb.FlowStatus_FLOW_STATUS_COMPLETED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW, nil
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED:
		return dexpb.FlowStatus_FLOW_STATUS_FAILED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return dexpb.FlowStatus_FLOW_STATUS_RUNNING, nil
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return dexpb.FlowStatus_FLOW_STATUS_TIMEOUT, nil
	case enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return dexpb.FlowStatus_FLOW_STATUS_TERMINATED, nil
	default:
		return dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED, fmt.Errorf("not supported status %s", status)
	}
}

func (t *temporalClient) GetWorkflowResult(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string,
) (resolvedRunID string, status dexpb.FlowStatus, err error) {
	workflowRun := t.tClient.GetWorkflow(ctx, workflowID, runID)
	err = workflowRun.Get(ctx, valuePtr)
	resolvedRunID = workflowRun.GetRunID()
	switch {
	case err == nil:
		status = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
	case realtemporal.IsCanceledError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_CANCELED
	case realtemporal.IsTimeoutError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_TIMEOUT
	case realtemporal.IsTerminatedError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_TERMINATED
	default:
		var workflowExecutionError *realtemporal.WorkflowExecutionError
		if errors.As(err, &workflowExecutionError) {
			status = dexpb.FlowStatus_FLOW_STATUS_FAILED
		}
	}
	return
}

func (t *temporalClient) SynchronousUpdateWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID, runID, updateType string, input interface{},
) error {
	args := []interface{}{input}
	options := client.UpdateWorkflowOptions{
		WorkflowID: workflowID,
		RunID:      runID,
		UpdateName: updateType,
		Args:       args,
		// TODO: Leaving this as Accepted that was a default value before WaitForStage became required argument, but Completed might be a better choice
		WaitForStage: client.WorkflowUpdateStageAccepted,
	}
	handle, err := t.tClient.UpdateWorkflow(ctx, options)
	if err != nil {
		return err
	}
	return handle.Get(ctx, valuePtr)
}

func (t *temporalClient) ResetWorkflow(
	ctx context.Context, request *dexpb.ResetFlowRequest,
) (runId string, err error) {
	reqRunId := request.GetRunId()
	if reqRunId == "" {
		// set default runId to current
		resp, err := t.tClient.DescribeWorkflowExecution(ctx, request.GetFlowId(), "")
		if err != nil {
			return "", err
		}
		reqRunId = resp.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	}

	resetType := request.GetResetType()
	resetBaseRunID, resetEventId, err := getResetEventIDByType(ctx, resetType,
		t.namespace, request.GetFlowId(), reqRunId,
		t.tClient.WorkflowService(), t.dataConverter,
		request.GetHistoryEventId(), request.GetHistoryEventTime(), request.GetStepType(), request.GetStepExecutionId())

	if err != nil {
		return "", err
	}

	requestId := uuid.New().String()
	var resetReapplyExcludeTypes []enums.ResetReapplyExcludeType
	if request.GetSkipChannelMessagesReapply() {
		resetReapplyExcludeTypes = append(resetReapplyExcludeTypes, enums.RESET_REAPPLY_EXCLUDE_TYPE_SIGNAL)
	}
	if request.GetSkipLockingRpcReapply() {
		resetReapplyExcludeTypes = append(resetReapplyExcludeTypes, enums.RESET_REAPPLY_EXCLUDE_TYPE_UPDATE)
	}

	resp, err := t.tClient.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: t.namespace,
		WorkflowExecution: &common.WorkflowExecution{
			WorkflowId: request.FlowId,
			RunId:      resetBaseRunID,
		},
		Reason:                    request.GetReason(),
		WorkflowTaskFinishEventId: resetEventId,
		RequestId:                 requestId,
		ResetReapplyExcludeTypes:  resetReapplyExcludeTypes,
	})

	if err != nil {
		return "", err
	}
	return resp.GetRunId(), nil
}

func (t *temporalClient) GetBackendType() (backendType service.BackendType) {
	return service.BackendTypeTemporal
}

func (t *temporalClient) GetApiService() interface{} {
	return t.tClient.WorkflowService()
}

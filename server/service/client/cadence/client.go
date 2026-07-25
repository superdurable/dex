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

package cadence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/iwf/config"

	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"

	"github.com/google/uuid"
	"github.com/superdurable/iwf/gen/iwfpb"
	uclient "github.com/superdurable/iwf/service/client"
	"github.com/superdurable/iwf/service/common/index"
	"github.com/superdurable/iwf/service/interpreter/cadence"
	realcadence "go.uber.org/cadence"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/client"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/workflow"
)

type cadenceClient struct {
	domain                         string
	cClient                        client.Client
	closeFunc                      func()
	serviceClient                  workflowserviceclient.Interface
	converter                      encoded.DataConverter
	queryWorkflowFailedRetryPolicy config.QueryWorkflowFailedRetryPolicy
}

func (t *cadenceClient) IsWorkflowAlreadyStartedError(err error) bool {
	var workflowExecutionAlreadyStartedError *shared.WorkflowExecutionAlreadyStartedError
	ok := errors.As(err, &workflowExecutionAlreadyStartedError)
	return ok
}

func (t *cadenceClient) GetRunIdFromWorkflowAlreadyStartedError(err error) (string, bool) {
	var res *shared.WorkflowExecutionAlreadyStartedError
	ok := errors.As(err, &res)
	runId := ""
	if ok {
		runId = *res.RunId
	}
	return runId, ok
}

func (t *cadenceClient) IsNotFoundError(err error) bool {
	var entityNotExistsError *shared.EntityNotExistsError
	ok := errors.As(err, &entityNotExistsError)
	return ok
}

func (t *cadenceClient) isQueryFailedError(err error) bool {
	var serviceError *shared.QueryFailedError
	ok := errors.As(err, &serviceError)
	return ok
}

func (t *cadenceClient) IsWorkflowTimeoutError(err error) bool {
	return realcadence.IsTimeoutError(err)
}

func (t *cadenceClient) IsRequestTimeoutError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func (t *cadenceClient) GetApplicationErrorTypeIfIsApplicationError(err error) string {
	var cErr *realcadence.CustomError
	ok := errors.As(err, &cErr)
	if ok {
		return cErr.Reason()
	}
	return ""
}

func (t *cadenceClient) GetApplicationErrorDetails(err error, detailsPtr interface{}) error {
	var cErr *realcadence.CustomError
	ok := errors.As(err, &cErr)
	if ok {
		if cErr.HasDetails() {
			return cErr.Details(detailsPtr)
		}
		return fmt.Errorf("application error doesn't have details. Critical code bug")
	}
	return fmt.Errorf("not an application error. Critical code bug")
}

func (t *cadenceClient) GetApplicationErrorTypeAndDetails(err error) (string, string) {
	errType := t.GetApplicationErrorTypeIfIsApplicationError(err)

	var errDetailsPtr interface{}
	var errDetails string

	err2 := t.GetApplicationErrorDetails(err, &errDetailsPtr)
	if err2 != nil {
		errDetails = err2.Error()
	} else {
		errDetailsString, ok := errDetailsPtr.(string)
		if ok {
			errDetails = errDetailsString
		} else {
			// All other types, e.g. iwfpb.StepCompletionOutput, try to Marshal the object to JSON
			var err error
			jsonBytes, err := json.Marshal(errDetailsPtr)
			if err == nil {
				errDetails = string(jsonBytes)
			}
		}
	}

	return errType, errDetails
}

func NewCadenceClient(
	domain string, cClient client.Client, serviceClient workflowserviceclient.Interface,
	converter encoded.DataConverter, closeFunc func(), retryPolicy *config.QueryWorkflowFailedRetryPolicy,
) uclient.UnifiedClient {
	return &cadenceClient{
		domain:                         domain,
		cClient:                        cClient,
		closeFunc:                      closeFunc,
		serviceClient:                  serviceClient,
		converter:                      converter,
		queryWorkflowFailedRetryPolicy: config.QueryWorkflowFailedRetryPolicyWithDefaults(retryPolicy),
	}
}

func (t *cadenceClient) Close() {
	t.closeFunc()
}

func (t *cadenceClient) StartInterpreterWorkflow(
	ctx context.Context, options uclient.StartWorkflowOptions, args ...interface{},
) (runId string, err error) {
	workflowOptions := client.StartWorkflowOptions{
		ID:                           options.ID,
		TaskList:                     options.TaskQueue,
		ExecutionStartToCloseTimeout: options.WorkflowExecutionTimeout,
		SearchAttributes:             options.SearchAttributes,
		Memo:                         options.Memo,
	}

	if options.IdReusePolicy != nil {
		workflowIdReusePolicy, err := mapToCadenceWorkflowIdReusePolicy(*options.IdReusePolicy)
		if err != nil {
			return "", err
		}

		workflowOptions.WorkflowIDReusePolicy = *workflowIdReusePolicy
	}

	if options.CronSchedule != nil {
		workflowOptions.CronSchedule = *options.CronSchedule
	}

	if options.RetryPolicy != nil {
		workflowOptions.RetryPolicy = mapToCadenceRetryPolicy(options.RetryPolicy)
	}

	if options.WorkflowStartDelay != nil {
		workflowOptions.DelayStart = *options.WorkflowStartDelay
	}

	run, err := t.cClient.StartWorkflow(ctx, workflowOptions, service.InterpreterWorkflowName, args...)
	if err != nil {
		return "", err
	}
	return run.RunID, nil
}

func (t *cadenceClient) StartBlobStoreCleanupWorkflow(
	ctx context.Context, taskQueue, workflowID, cronSchedule, storeId string,
) error {

	workflowOptions := client.StartWorkflowOptions{
		ID:                           workflowID,
		TaskList:                     taskQueue,
		ExecutionStartToCloseTimeout: time.Hour * 24 * 365,
		CronSchedule:                 cronSchedule,
	}

	_, err := t.cClient.StartWorkflow(ctx, workflowOptions, cadence.BlobStoreCleanup, storeId)

	return err
}

func (t *cadenceClient) SignalWorkflow(
	ctx context.Context, workflowID string, runID string, signalName string, arg interface{},
) error {
	return t.cClient.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (t *cadenceClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	return t.cClient.CancelWorkflow(ctx, workflowID, runID)
}

func (t *cadenceClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string) error {
	var reasonStr string
	if reason == "" {
		reasonStr = "Force termiantion from user"
	} else {
		reasonStr = reason
	}

	return t.cClient.TerminateWorkflow(ctx, workflowID, runID, reasonStr, nil)
}

func (t *cadenceClient) ListWorkflow(
	ctx context.Context, request *uclient.ListWorkflowExecutionsRequest,
) (*uclient.ListWorkflowExecutionsResponse, error) {
	listReq := &shared.ListWorkflowExecutionsRequest{
		PageSize:      &request.PageSize,
		Query:         &request.Query,
		NextPageToken: request.NextPageToken,
	}
	resp, err := t.cClient.ListWorkflow(ctx, listReq)
	if err != nil {
		return nil, err
	}
	var executions []*iwfpb.SearchFlowsResponseEntry
	for _, exe := range resp.GetExecutions() {
		executions = append(executions, &iwfpb.SearchFlowsResponseEntry{
			FlowId: *exe.Execution.WorkflowId,
			RunId:  *exe.Execution.RunId,
		})
	}
	return &uclient.ListWorkflowExecutionsResponse{
		Executions:    executions,
		NextPageToken: resp.NextPageToken,
	}, nil
}

func (t *cadenceClient) QueryWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string, queryType string, args ...interface{},
) error {
	var qres encoded.Value
	var err error

	attempt := 1
	// Only QueryFailed error causes retry; all other errors make the loop to finish immediately
	for attempt <= t.queryWorkflowFailedRetryPolicy.MaximumAttempts {
		qres, err = t.cClient.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
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

func queryWorkflowWithStrongConsistency(
	t *cadenceClient, ctx context.Context, workflowID string, runID string, queryType string, args []interface{},
) (encoded.Value, error) {
	queryWorkflowWithOptionsRequest := &client.QueryWorkflowWithOptionsRequest{
		WorkflowID:            workflowID,
		RunID:                 runID,
		QueryType:             queryType,
		Args:                  args,
		QueryConsistencyLevel: ptr.Any(shared.QueryConsistencyLevelStrong),
	}
	result, err := t.cClient.QueryWorkflowWithOptions(ctx, queryWorkflowWithOptionsRequest)
	if err != nil {
		return nil, err
	}
	return result.QueryResult, nil
}

func (t *cadenceClient) DescribeWorkflowExecution(
	ctx context.Context, workflowID, runID string, indexedAttrTypes map[string]iwfpb.IndexType,
) (*uclient.DescribeWorkflowExecutionResponse, error) {
	resp, err := t.cClient.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return nil, err
	}
	status, err := mapToIwfWorkflowStatus(resp.GetWorkflowExecutionInfo().CloseStatus)
	if err != nil {
		return nil, err
	}
	indexedAttributes, err := index.MapCadenceSearchAttributeFieldsToAttrValues(resp.GetWorkflowExecutionInfo().GetSearchAttributes(), indexedAttrTypes)
	if err != nil {
		return nil, err
	}

	memo, err := t.decodeMemo(resp.GetWorkflowExecutionInfo().GetMemo())
	if err != nil {
		return nil, err
	}

	return &uclient.DescribeWorkflowExecutionResponse{
		RunId:             resp.GetWorkflowExecutionInfo().GetExecution().GetRunId(),
		FirstRunId:        "", // Cadence does not provide FirstRunId
		Status:            status,
		IndexedAttributes: indexedAttributes,
		Memos:             memo,
	}, nil
}

func (t *cadenceClient) decodeMemo(memo *shared.Memo) (map[string]*iwfpb.Value, error) {
	if memo == nil || len(memo.GetFields()) == 0 {
		return nil, nil
	}

	out := map[string]*iwfpb.Value{}
	for k, payload := range memo.GetFields() {
		var value iwfpb.EncodedObject
		err := encoded.GetDefaultDataConverter().FromData(payload, &value)
		if err != nil {
			return nil, err
		}
		out[k] = &iwfpb.Value{Kind: &iwfpb.Value_ObjValue{ObjValue: &value}}
	}
	return out, nil
}

func mapToCadenceWorkflowIdReusePolicy(idReusePolicy iwfpb.IdReusePolicy) (*client.WorkflowIDReusePolicy, error) {
	var res client.WorkflowIDReusePolicy
	switch idReusePolicy {
	case iwfpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING:
		res = client.WorkflowIDReusePolicyAllowDuplicate
		return &res, nil
	case iwfpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY:
		res = client.WorkflowIDReusePolicyAllowDuplicateFailedOnly
		return &res, nil
	case iwfpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE:
		res = client.WorkflowIDReusePolicyRejectDuplicate
		return &res, nil
	case iwfpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING:
		res = client.WorkflowIDReusePolicyTerminateIfRunning
		return &res, nil
	default:
		return nil, fmt.Errorf("unsupported workflow id reuse policy %s", idReusePolicy)
	}
}

// mapToCadenceRetryPolicy fills unset (zero-value) fields with the same
// defaults iwf has always used for flow retries.
func mapToCadenceRetryPolicy(policy *iwfpb.FlowRetryPolicy) *workflow.RetryPolicy {
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

	return &workflow.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initialIntervalSeconds),
		MaximumInterval:    time.Second * time.Duration(maximumIntervalSeconds),
		MaximumAttempts:    policy.GetMaximumAttempts(),
		BackoffCoefficient: float64(backoffCoefficient),
	}
}

func mapToIwfWorkflowStatus(status *shared.WorkflowExecutionCloseStatus) (iwfpb.FlowStatus, error) {
	if status == nil {
		return iwfpb.FlowStatus_FLOW_STATUS_RUNNING, nil
	}

	switch *status {
	case shared.WorkflowExecutionCloseStatusCanceled:
		return iwfpb.FlowStatus_FLOW_STATUS_CANCELED, nil
	case shared.WorkflowExecutionCloseStatusContinuedAsNew:
		return iwfpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW, nil
	case shared.WorkflowExecutionCloseStatusFailed:
		return iwfpb.FlowStatus_FLOW_STATUS_FAILED, nil
	case shared.WorkflowExecutionCloseStatusTimedOut:
		return iwfpb.FlowStatus_FLOW_STATUS_TIMEOUT, nil
	case shared.WorkflowExecutionCloseStatusTerminated:
		return iwfpb.FlowStatus_FLOW_STATUS_TERMINATED, nil
	case shared.WorkflowExecutionCloseStatusCompleted:
		return iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, nil
	default:
		return iwfpb.FlowStatus_FLOW_STATUS_UNSPECIFIED, fmt.Errorf("not supported status %s", status)
	}
}

func (t *cadenceClient) GetWorkflowResult(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string,
) (resolvedRunID string, status iwfpb.FlowStatus, err error) {
	workflowRun := t.cClient.GetWorkflow(ctx, workflowID, runID)
	err = workflowRun.Get(ctx, valuePtr)
	resolvedRunID = workflowRun.GetRunID()
	switch {
	case err == nil:
		status = iwfpb.FlowStatus_FLOW_STATUS_COMPLETED
	case realcadence.IsCanceledError(err):
		status = iwfpb.FlowStatus_FLOW_STATUS_CANCELED
	case realcadence.IsTimeoutError(err):
		status = iwfpb.FlowStatus_FLOW_STATUS_TIMEOUT
	case realcadence.IsTerminatedError(err):
		status = iwfpb.FlowStatus_FLOW_STATUS_TERMINATED
	case client.IsWorkflowError(err):
		status = iwfpb.FlowStatus_FLOW_STATUS_FAILED
	}
	return
}

func (t *cadenceClient) SynchronousUpdateWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID, runID, updateType string, input interface{},
) error {
	return fmt.Errorf("not supported in Cadence")
}

func (t *cadenceClient) ResetWorkflow(
	ctx context.Context, request *iwfpb.ResetFlowRequest,
) (newRunId string, err error) {

	reqRunId := request.GetRunId()
	if reqRunId == "" {
		// set default runId to current
		resp, err := t.cClient.DescribeWorkflowExecution(ctx, request.GetFlowId(), "")
		if err != nil {
			return "", err
		}
		reqRunId = resp.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	}

	// TODO not sure why Cadence reset API requires this for GetWorkflowExecutionHistory API....
	ctx, cancelFn := context.WithTimeout(ctx, time.Second*120)
	defer cancelFn()

	resetType := request.GetResetType()
	resetBaseRunID, decisionFinishID, err := getResetIDsByType(ctx, resetType, t.domain, request.GetFlowId(),
		reqRunId, t.serviceClient, t.converter, request.GetHistoryEventId(), request.GetHistoryEventTime(), request.GetStepType(), request.GetStepExecutionId())

	if err != nil {
		return "", err
	}

	requestId := uuid.New().String()
	resetReq := &shared.ResetWorkflowExecutionRequest{
		Domain: &t.domain,
		WorkflowExecution: &shared.WorkflowExecution{
			WorkflowId: &request.FlowId,
			RunId:      &resetBaseRunID,
		},
		Reason:                &request.Reason,
		DecisionFinishEventId: ptr.Any(decisionFinishID),
		RequestId:             &requestId,
		SkipSignalReapply:     ptr.Any(request.GetSkipChannelMessagesReapply()),
	}

	resp, err := t.serviceClient.ResetWorkflowExecution(ctx, resetReq)
	if err != nil {
		return "", err
	}
	return resp.GetRunId(), nil
}

func (t *cadenceClient) GetBackendType() (backendType service.BackendType) {
	return service.BackendTypeCadence
}

func (t *cadenceClient) GetApiService() interface{} {
	return t.cClient
}

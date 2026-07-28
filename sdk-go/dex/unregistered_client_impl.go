// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"context"
	"fmt"
	"net/http"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type unregisteredClientImpl struct {
	options   *ClientOptions
	apiClient *dexpb.APIClient
}

func (u *unregisteredClientImpl) StartWorkflow(ctx context.Context, workflowType string, startStateId, workflowId string, timeoutSecs int32, input interface{}, options *UnregisteredWorkflowOptions) (string, error) {
	var encodedInput *dexpb.EncodedObject
	var err error
	if input != nil {
		encodedInput, err = u.options.ObjectEncoder.Encode(input)
		if err != nil {
			return "", err
		}
	}

	var startStateIdPtr *string
	if startStateId != "" {
		startStateIdPtr = &startStateId
	}
	var stateOptions *dexpb.WorkflowStateOptions
	var startOptions *dexpb.WorkflowStartOptions
	if options != nil {
		for _, sa := range options.InitialSearchAttributes {
			val, _ := getSearchAttributeValue(sa)
			if val == nil {
				return "", fmt.Errorf("search attribute value is not set correctly for key %s with value type %s", sa.GetKey(), sa.GetValueType())
			}
		}
		stateOptions = options.StartStateOptions
		startOptions = &dexpb.WorkflowStartOptions{
			IdReusePolicy:             options.WorkflowIdReusePolicy,
			CronSchedule:              options.WorkflowCronSchedule,
			WorkflowStartDelaySeconds: options.WorkflowStartDelaySeconds,
			RetryPolicy:               options.WorkflowRetryPolicy,
			SearchAttributes:          options.InitialSearchAttributes,
		}
	}

	req := u.apiClient.DefaultApi.ApiV1WorkflowStartPost(ctx)
	resp, httpResp, err := req.WorkflowStartRequest(dexpb.WorkflowStartRequest{
		WorkflowId:             workflowId,
		DexWorkflowType:        workflowType,
		WorkflowTimeoutSeconds: timeoutSecs,
		DexWorkerUrl:           u.options.WorkerUrl,
		StartStateId:           startStateIdPtr,
		StateInput:             encodedInput,
		StateOptions:           stateOptions,
		WorkflowStartOptions:   startOptions,
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return "", err
	}
	return resp.GetWorkflowRunId(), nil
}

func (u *unregisteredClientImpl) SignalWorkflow(ctx context.Context, workflowId, workflowRunId, signalChannelName string, signalValue interface{}) error {
	value, err := u.options.ObjectEncoder.Encode(signalValue)
	if err != nil {
		return err
	}
	req := u.apiClient.DefaultApi.ApiV1WorkflowSignalPost(ctx)
	httpResp, err := req.WorkflowSignalRequest(dexpb.WorkflowSignalRequest{
		WorkflowId:        workflowId,
		WorkflowRunId:     &workflowRunId,
		SignalChannelName: signalChannelName,
		SignalValue:       value,
	}).Execute()
	return u.processError(err, httpResp)
}

func (u *unregisteredClientImpl) GetWorkflowDataAttributes(ctx context.Context, workflowId, workflowRunId string, keys []string) (map[string]Object, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("must specify keys to return, use GetAllWorkflowDataAttributes if intended to get all keys")
	}
	return u.doGetWorkflowDataObjects(ctx, workflowId, workflowRunId, keys)
}

func (u *unregisteredClientImpl) GetAllWorkflowDataAttributes(ctx context.Context, workflowId, workflowRunId string) (map[string]Object, error) {
	return u.doGetWorkflowDataObjects(ctx, workflowId, workflowRunId, nil)
}

func (u *unregisteredClientImpl) doGetWorkflowDataObjects(ctx context.Context, workflowId, workflowRunId string, keys []string) (map[string]Object, error) {
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowDataobjectsGetPost(ctx)
	resp, httpResp, err := reqPost.WorkflowGetDataObjectsRequest(dexpb.WorkflowGetDataObjectsRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: dexpb.PtrString(workflowRunId),
		Keys:          keys,
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return nil, err
	}
	out := make(map[string]Object, len(resp.Objects))
	for _, kv := range resp.Objects {
		out[kv.GetKey()] = NewObject(ptr.Any(kv.GetValue()), u.options.ObjectEncoder)
	}
	return out, nil
}

func (u *unregisteredClientImpl) GetWorkflowSearchAttributes(ctx context.Context, workflowId, workflowRunId string, keys []dexpb.SearchAttributeKeyAndType) (map[string]dexpb.SearchAttribute, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("must specify keys to return, use GetAllWorkflowSearchAttributes if intended to get all keys")
	}
	return u.doGetWorkflowSearchAttributes(ctx, workflowId, workflowRunId, keys)
}

func (u *unregisteredClientImpl) doGetWorkflowSearchAttributes(ctx context.Context, workflowId, workflowRunId string, keys []dexpb.SearchAttributeKeyAndType) (map[string]dexpb.SearchAttribute, error) {
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowSearchattributesGetPost(ctx)
	resp, httpResp, err := reqPost.WorkflowGetSearchAttributesRequest(dexpb.WorkflowGetSearchAttributesRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: dexpb.PtrString(workflowRunId),
		Keys:          keys,
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return nil, err
	}
	out := make(map[string]dexpb.SearchAttribute, len(resp.SearchAttributes))
	for _, kv := range resp.SearchAttributes {
		out[kv.GetKey()] = kv
	}
	return out, nil
}

func (u *unregisteredClientImpl) StopWorkflow(ctx context.Context, workflowId, workflowRunId string, options *WorkflowStopOptions) error {
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowStopPost(ctx)
	req := &dexpb.WorkflowStopRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: &workflowRunId,
	}
	if options != nil {
		req.StopType = &options.StopType
		req.Reason = &options.Reason
	}
	httpResp, err := reqPost.WorkflowStopRequest(*req).Execute()
	return u.processError(err, httpResp)
}

func (u *unregisteredClientImpl) GetSimpleWorkflowResult(ctx context.Context, workflowId, workflowRunId string, resultPtr interface{}) error {
	req := u.apiClient.DefaultApi.ApiV1WorkflowGetWithWaitPost(ctx)
	resp, httpResp, err := req.WorkflowGetRequest(dexpb.WorkflowGetRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: &workflowRunId,
		NeedsResults:  ptr.Any(true),
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return err
	}
	if resp.WorkflowStatus != dexpb.COMPLETED {
		return u.processUncompletedError(resp)
	}
	count := 0
	var output *dexpb.EncodedObject
	for _, res := range resp.Results {
		if res.HasCompletedStateOutput() && res.CompletedStateOutput.HasData() {
			output = res.CompletedStateOutput
			count++
		}
	}
	if count > 1 {
		return NewWorkflowDefinitionError("this workflow should have one or zero state output for using this API")
	}
	if count == 0 {
		return nil
	}
	return u.options.ObjectEncoder.Decode(output, resultPtr)
}

func (u *unregisteredClientImpl) GetComplexWorkflowResults(ctx context.Context, workflowId, workflowRunId string) ([]dexpb.StateCompletionOutput, error) {
	req := u.apiClient.DefaultApi.ApiV1WorkflowGetWithWaitPost(ctx)
	resp, httpResp, err := req.WorkflowGetRequest(dexpb.WorkflowGetRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: &workflowRunId,
		NeedsResults:  ptr.Any(true),
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return nil, err
	}
	if resp.WorkflowStatus != dexpb.COMPLETED {
		return nil, u.processUncompletedError(resp)
	}
	return resp.Results, nil
}

func (u *unregisteredClientImpl) ResetWorkflow(ctx context.Context, workflowId, workflowRunId string, options *ResetWorkflowOptions) (string, error) {
	resetType := dexpb.BEGINNING
	reason := ptr.Any("")
	if options != nil {
		resetType = options.ResetType
		reason = &options.Reason
	}

	req := dexpb.WorkflowResetRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: dexpb.PtrString(workflowRunId),
		ResetType:     resetType,
		Reason:        reason,
	}
	if options != nil {
		req.HistoryEventId = options.HistoryEventId
		req.HistoryEventTime = ptr.Any(options.HistoryEventTime.Format(DateTimeFormat))
		req.SkipSignalReapply = options.SkipSignalReapply
		req.StateId = options.StateId
		req.StateExecutionId = options.StateExecutionId
	}
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowResetPost(ctx)
	resp, httpResp, err := reqPost.WorkflowResetRequest(req).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return "", err
	}
	return resp.WorkflowRunId, nil
}

func (u *unregisteredClientImpl) DescribeWorkflow(ctx context.Context, workflowId, workflowRunId string) (*WorkflowInfo, error) {
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowGetPost(ctx)
	resp, httpResp, err := reqPost.WorkflowGetRequest(dexpb.WorkflowGetRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: dexpb.PtrString(workflowRunId),
		NeedsResults:  ptr.Any(false),
	}).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return nil, err
	}
	return &WorkflowInfo{
		Status:       resp.WorkflowStatus,
		CurrentRunId: resp.WorkflowRunId,
	}, nil
}

func (u *unregisteredClientImpl) SearchWorkflow(ctx context.Context, request dexpb.WorkflowSearchRequest) (*dexpb.WorkflowSearchResponse, error) {
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowSearchPost(ctx)
	resp, httpResp, err := reqPost.WorkflowSearchRequest(request).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (u *unregisteredClientImpl) SkipTimerByCommandId(ctx context.Context, workflowId, workflowRunId, workflowStateId string, stateExecutionNumber int, timerCommandId string) error {
	if timerCommandId == "" {
		return NewInvalidArgumentErrorFmt("cannot use empty timerCommandId")
	}
	return u.doSkipTimer(ctx, workflowId, workflowRunId, workflowStateId, stateExecutionNumber, timerCommandId, 0)
}

func (u *unregisteredClientImpl) SkipTimerByCommandIndex(ctx context.Context, workflowId, workflowRunId, workflowStateId string, stateExecutionNumber int, timerCommandIndex int) error {
	return u.doSkipTimer(ctx, workflowId, workflowRunId, workflowStateId, stateExecutionNumber, "", timerCommandIndex)
}

func (u *unregisteredClientImpl) UpdateWorkflowConfig(ctx context.Context, workflowId, workflowRunId string, config dexpb.WorkflowConfig) error {
	req := u.apiClient.DefaultApi.ApiV1WorkflowConfigUpdatePost(ctx)
	httpResp, err := req.WorkflowConfigUpdateRequest(dexpb.WorkflowConfigUpdateRequest{
		WorkflowId:     workflowId,
		WorkflowRunId:  &workflowRunId,
		WorkflowConfig: config,
	}).Execute()
	return u.processError(err, httpResp)
}

func (u *unregisteredClientImpl) InvokeRPCByName(ctx context.Context, workflowId, workflowRunId, rpcName string, input interface{}, outputPtr interface{}, rpcOptions *RPCOptions) error {
	req := u.apiClient.DefaultApi.ApiV1WorkflowRpcPost(ctx)
	encodedInput, err := u.options.ObjectEncoder.Encode(input)
	if err != nil {
		return err
	}
	request := dexpb.WorkflowRpcRequest{
		WorkflowId:    workflowId,
		WorkflowRunId: &workflowRunId,
		RpcName:       rpcName,
		Input:         encodedInput,
	}
	if rpcOptions != nil {
		request.SearchAttributesLoadingPolicy = rpcOptions.SearchAttributesLoadingPolicy
		request.DataAttributesLoadingPolicy = rpcOptions.DataAttributesLoadingPolicy
	}

	resp, httpResp, err := req.WorkflowRpcRequest(request).Execute()
	if err := u.processError(err, httpResp); err != nil {
		return err
	}
	if outputPtr != nil {
		return u.options.ObjectEncoder.Decode(resp.Output, outputPtr)
	}
	return nil
}

func (u *unregisteredClientImpl) doSkipTimer(ctx context.Context, workflowId, workflowRunId, workflowStateId string, stateExecutionNumber int, timerCommandId string, timerCommandIndex int) error {
	workflowStateExecutionId := fmt.Sprintf("%v-%v", workflowStateId, stateExecutionNumber)
	reqPost := u.apiClient.DefaultApi.ApiV1WorkflowTimerSkipPost(ctx)
	req := dexpb.WorkflowSkipTimerRequest{
		WorkflowId:               workflowId,
		WorkflowRunId:            dexpb.PtrString(workflowRunId),
		WorkflowStateExecutionId: workflowStateExecutionId,
	}
	if timerCommandId != "" {
		req.TimerCommandId = &timerCommandId
	} else {
		req.TimerCommandIndex = ptr.Any(int32(timerCommandIndex))
	}
	httpResp, err := reqPost.WorkflowSkipTimerRequest(req).Execute()
	return u.processError(err, httpResp)
}

func (u *unregisteredClientImpl) processError(err error, httpResp *http.Response) error {
	if err == nil && httpResp != nil && httpResp.StatusCode == http.StatusOK {
		return nil
	}
	var resp *dexpb.ErrorResponse
	oerr, ok := err.(*dexpb.GenericOpenAPIError)
	if ok {
		rsp, ok := oerr.Model().(dexpb.ErrorResponse)
		if ok {
			resp = &rsp
		}
	}
	return NewApiError(err, oerr, httpResp, resp)
}

func (u *unregisteredClientImpl) processUncompletedError(resp *dexpb.WorkflowGetResponse) error {
	return NewWorkflowUncompletedError(resp.WorkflowRunId, resp.WorkflowStatus, resp.ErrorType, resp.ErrorMessage, resp.Results, u.options.ObjectEncoder)
}

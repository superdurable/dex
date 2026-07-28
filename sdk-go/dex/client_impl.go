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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"strconv"
	"time"
)

type clientImpl struct {
	UnregisteredClient
	registry Registry
	options  *ClientOptions
}

func (c *clientImpl) StartWorkflow(ctx context.Context, workflow ObjectWorkflow, workflowId string, timeoutSecs int32, input interface{}, options *WorkflowOptions) (string, error) {
	wfType := GetFinalWorkflowType(workflow)
	wf := c.registry.GetWorkflow(wfType)
	if wf == nil {
		return "", NewInvalidArgumentError("workflow is not registered")
	}

	state := c.registry.getWorkflowStartingState(wfType)

	unregOpt := &UnregisteredWorkflowOptions{}

	startStateId := ""
	if state != nil {
		startStateId = GetFinalWorkflowStateId(state)
		startStateOpt := state.GetStateOptions()
		unregOpt.StartStateOptions = toIdlStateOptions(ShouldSkipWaitUntilAPI(state), startStateOpt)
	}

	if options != nil {
		unregOpt.WorkflowIdReusePolicy = options.WorkflowIdReusePolicy
		unregOpt.WorkflowRetryPolicy = options.WorkflowRetryPolicy
		unregOpt.WorkflowCronSchedule = options.WorkflowCronSchedule
		unregOpt.WorkflowStartDelaySeconds = options.WorkflowStartDelaySeconds

		saTypes := c.registry.getSearchAttributeTypeStore(wfType)

		convertedSAs, err := convertToSearchAttributeList(saTypes, options.InitialSearchAttributes)
		if err != nil {
			return "", err
		}
		unregOpt.InitialSearchAttributes = convertedSAs
	}
	return c.UnregisteredClient.StartWorkflow(ctx, wfType, startStateId, workflowId, timeoutSecs, input, unregOpt)
}

func convertToSearchAttributeList(types map[string]dexpb.SearchAttributeValueType, attributes map[string]interface{}) ([]dexpb.SearchAttribute, error) {
	var converted []dexpb.SearchAttribute
	for key, rawAtt := range attributes {
		saType, ok := types[key]
		if !ok {
			return nil, NewWorkflowDefinitionErrorFmt("key %v is not defined as search attribute, all keys are: %v ", key, types)
		}
		att := dexpb.SearchAttribute{
			Key:       ptr.Any(key),
			ValueType: ptr.Any(saType),
		}

		sv := fmt.Sprintf("%v", rawAtt)
		var err error
		var intV int64
		var douV float64
		var bV bool
		var arr []string
		switch saType {
		case dexpb.INT:
			intV, err = strconv.ParseInt(sv, 10, 64)
			att.IntegerValue = &intV
		case dexpb.DOUBLE:
			douV, err = strconv.ParseFloat(sv, 64)
			att.DoubleValue = &douV
		case dexpb.BOOL:
			bV, err = strconv.ParseBool(sv)
			att.BoolValue = ptr.Any(bV)
		case dexpb.KEYWORD, dexpb.TEXT:
			att.StringValue = &sv
		case dexpb.DATETIME:
			dtV, ok := rawAtt.(time.Time)
			if ok {
				att.StringValue = ptr.Any(dtV.Format(DateTimeFormat))
			} else {
				att.StringValue = &sv
			}
		case dexpb.KEYWORD_ARRAY:
			arr, ok = rawAtt.([]string)
			if !ok {
				err = fmt.Errorf("not a string array")
			}
			att.StringArrayValue = arr
		default:
			return nil, NewInvalidArgumentErrorFmt("unsupported search attribute type %v", saType)
		}
		if err != nil {
			return nil, NewInvalidArgumentErrorFmt("unable to convert the value %v to registered type %v", rawAtt, saType)
		}
		converted = append(converted, att)
	}
	return converted, nil
}

func (c *clientImpl) SignalWorkflow(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId, signalChannelName string, signalValue interface{}) error {
	wfType := GetFinalWorkflowType(workflow)
	signalNameStore := c.registry.getWorkflowSignalNameStore(wfType)
	if !signalNameStore[signalChannelName] {
		return NewWorkflowDefinitionErrorFmt("signal channel %v is not defined in workflow type %v", signalChannelName, wfType)
	}
	return c.UnregisteredClient.SignalWorkflow(ctx, workflowId, workflowRunId, signalChannelName, signalValue)
}

func (c *clientImpl) GetWorkflowDataAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string, keys []string) (map[string]Object, error) {
	wfType := GetFinalWorkflowType(workflow)
	doTypeMap := c.registry.getWorkflowDataAttributesKeyStore(wfType)
	for _, k := range keys {
		_, ok := doTypeMap[k]
		if !ok {
			return nil, fmt.Errorf("data object type %v is not registered", k)
		}
	}
	return c.UnregisteredClient.GetWorkflowDataAttributes(ctx, workflowId, workflowRunId, keys)
}

func (c *clientImpl) GetWorkflowSearchAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string, keys []string) (map[string]interface{}, error) {
	wfType := GetFinalWorkflowType(workflow)
	allTypes := c.registry.getSearchAttributeTypeStore(wfType)
	var keyAndTypes []dexpb.SearchAttributeKeyAndType
	for _, k := range keys {
		keyAndTypes = append(keyAndTypes, dexpb.SearchAttributeKeyAndType{
			Key:       ptr.Any(k),
			ValueType: ptr.Any(allTypes[k]),
		})
	}
	vals, err := c.UnregisteredClient.GetWorkflowSearchAttributes(ctx, workflowId, workflowRunId, keyAndTypes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]interface{}, len(vals))
	for _, val := range vals {
		v, err := getSearchAttributeValue(val)
		if err != nil {
			return nil, err
		}
		out[val.GetKey()] = v
	}
	return out, nil
}

func (c *clientImpl) GetAllWorkflowSearchAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string) (map[string]interface{}, error) {
	wfType := GetFinalWorkflowType(workflow)
	allTypes := c.registry.getSearchAttributeTypeStore(wfType)
	var keys []string
	for k := range allTypes {
		keys = append(keys, k)
	}
	return c.GetWorkflowSearchAttributes(ctx, workflow, workflowId, workflowRunId, keys)
}

func (c *clientImpl) SkipTimerByCommandId(ctx context.Context, workflowId, workflowRunId string, workflowState WorkflowState, stateExecutionNumber int, timerCommandId string) error {
	stateId := GetFinalWorkflowStateId(workflowState)
	return c.UnregisteredClient.SkipTimerByCommandId(ctx, workflowId, workflowRunId, stateId, stateExecutionNumber, timerCommandId)
}

func (c *clientImpl) SkipTimerByCommandIndex(ctx context.Context, workflowId, workflowRunId string, workflowState WorkflowState, stateExecutionNumber, timerCommandIndex int) error {
	stateId := GetFinalWorkflowStateId(workflowState)
	return c.UnregisteredClient.SkipTimerByCommandIndex(ctx, workflowId, workflowRunId, stateId, stateExecutionNumber, timerCommandIndex)
}

func (c *clientImpl) InvokeRPC(ctx context.Context, workflowId, workflowRunId string, rpc RPC, input interface{}, outputPtr interface{}) error {
	rpcName, wfType := extractRPCNameAndWorkflowType(rpc)
	rpcDef := c.registry.getWorkflowRPC(wfType, rpcName)
	return c.InvokeRPCByName(ctx, workflowId, workflowRunId, rpcName, input, outputPtr, rpcDef.RPCOptions)
}

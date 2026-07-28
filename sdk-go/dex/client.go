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

//go:generate mockgen -source=./client.go -package=dextest -destination=../dextest/client.go

import (
	"context"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// Client is a full-featured client
type Client interface {
	ClientCommon
	// StartWorkflow starts a workflow execution
	// workflowId is the required identifier for the workflow execution(see Cadence/Temporal for more details about WorkflowId uniqueness)
	// timeoutSecs is required as the workflow execution timeout in seconds
	// input can be optional, it's the input for the startState
	// options is optional includes like IdReusePolicy, RetryPolicy, CronSchedule and also WorkflowStateOptions for the startState. Empty by default(when nil).
	// return the workflowRunId
	StartWorkflow(ctx context.Context, workflow ObjectWorkflow, workflowId string, timeoutSecs int32, input interface{}, options *WorkflowOptions) (string, error)
	// SignalWorkflow signals a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// signalChannelName is required, signalValue is optional(for case of empty value)
	SignalWorkflow(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId, signalChannelName string, signalValue interface{}) error
	// GetWorkflowDataAttributes returns the data objects of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// keys is required to be non-empty. If you intend to return all data objects, use GetAllWorkflowDataAttributes API instead
	GetWorkflowDataAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string, keys []string) (map[string]Object, error)
	// GetWorkflowSearchAttributes returns search attributes of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// keys is required to be non-empty. If you intend to return all data objects, use GetAllWorkflowSearchAttributes API instead
	GetWorkflowSearchAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string, keys []string) (map[string]interface{}, error)
	// GetAllWorkflowSearchAttributes returns all search attributes of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	GetAllWorkflowSearchAttributes(ctx context.Context, workflow ObjectWorkflow, workflowId, workflowRunId string) (map[string]interface{}, error)
	// SkipTimerByCommandId skips a timer for the state execution based on the timerCommandId
	SkipTimerByCommandId(ctx context.Context, workflowId, workflowRunId string, workflowState WorkflowState, stateExecutionNumber int, timerCommandId string) error
	// SkipTimerByCommandIndex skips a timer for the state execution based on the timerCommandId
	SkipTimerByCommandIndex(ctx context.Context, workflowId, workflowRunId string, workflowState WorkflowState, stateExecutionNumber, timerCommandIndex int) error
	// InvokeRPC invokes an RPC
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// rpc is required
	// input and outputPtr are optional
	InvokeRPC(ctx context.Context, workflowId, workflowRunId string, rpc RPC, input interface{}, outputPtr interface{}) error
}

// ClientCommon is the common APIs between Client and UnregisteredClient
type ClientCommon interface {
	// StopWorkflow stops a workflow execution.
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// options is optional, default (when nil)to use Cancel as stopType
	StopWorkflow(ctx context.Context, workflowId, workflowRunId string, options *WorkflowStopOptions) error
	// UpdateWorkflowConfig updates the config of a workflow
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	UpdateWorkflowConfig(ctx context.Context, workflowId, workflowRunId string, config dexpb.WorkflowConfig) error
	// GetSimpleWorkflowResult returns the result of a workflow execution, for simple case that only one WorkflowState completes with result
	// If there are more than one WorkflowStates complete with result, GetComplexWorkflowResults must be used instead
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// resultPtr is the pointer to retrieve the result
	GetSimpleWorkflowResult(ctx context.Context, workflowId, workflowRunId string, resultPtr interface{}) error
	// GetComplexWorkflowResults returns the results of a workflow execution
	// It returns a list of dexpb.StateCompletionOutput and user code will have to use ObjectEncoder to deserialize
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	GetComplexWorkflowResults(ctx context.Context, workflowId, workflowRunId string) ([]dexpb.StateCompletionOutput, error)
	// ResetWorkflow resets a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// resetWorkflowTypeAndOptions is optional, it provides combination parameter for reset. Default (when nil) will reset to dexpb.BEGINNING resetType
	// return the workflowRunId
	ResetWorkflow(ctx context.Context, workflowId, workflowRunId string, options *ResetWorkflowOptions) (string, error)
	// DescribeWorkflow describes the basic info of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	DescribeWorkflow(ctx context.Context, workflowId, workflowRunId string) (*WorkflowInfo, error)

	// SearchWorkflow searches for workflow executions given a query (see SearchAttribute query in Cadence/Temporal)
	//  https://cadenceworkflow.io/docs/concepts/search-workflows/
	//  https://docs.temporal.io/concepts/what-is-a-search-attribute/
	SearchWorkflow(ctx context.Context, request dexpb.WorkflowSearchRequest) (*dexpb.WorkflowSearchResponse, error)
	// GetAllWorkflowDataAttributes returns all the data objects of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	GetAllWorkflowDataAttributes(ctx context.Context, workflowId, workflowRunId string) (map[string]Object, error)
}

// UnregisteredClient is a client without workflow registry
type UnregisteredClient interface {
	ClientCommon
	// StartWorkflow starts a workflow execution
	// startStateId is the first stateId to start
	// workflowId is the required identifier for the workflow execution(see Cadence/Temporal for more details about WorkflowId uniqueness)
	// timeoutSecs is required as the workflow execution timeout in seconds
	// input can be optional, it's the input for the startState
	// options is optional includes like IdReusePolicy, RetryPolicy, CronSchedule and also WorkflowStateOptions for the startState. Empty by default(when nil).
	// return the workflowRunId
	StartWorkflow(ctx context.Context, workflowType string, startStateId, workflowId string, timeoutSecs int32, input interface{}, options *UnregisteredWorkflowOptions) (string, error)
	// SignalWorkflow signals a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// signalChannelName is required, signalValue is optional(for case of empty value)
	SignalWorkflow(ctx context.Context, workflowId, workflowRunId, signalChannelName string, signalValue interface{}) error
	// GetWorkflowDataAttributes returns the data objects of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// keys is required to be non-empty. If you intend to return all data objects, use GetAllWorkflowDataAttributes API instead
	GetWorkflowDataAttributes(ctx context.Context, workflowId, workflowRunId string, keys []string) (map[string]Object, error)
	// GetWorkflowSearchAttributes returns search attributes of a workflow execution
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// keys is required to be non-empty. If you intend to return all data objects, use GetAllWorkflowSearchAttributes API instead
	GetWorkflowSearchAttributes(ctx context.Context, workflowId, workflowRunId string, keys []dexpb.SearchAttributeKeyAndType) (map[string]dexpb.SearchAttribute, error)
	// SkipTimerByCommandIndex skips a timer for the state execution based on the timerCommandId
	SkipTimerByCommandIndex(ctx context.Context, workflowId, workflowRunId, workflowStateId string, stateExecutionNumber, timerCommandIndex int) error
	// SkipTimerByCommandId skips a timer for the state execution based on the timerCommandId
	SkipTimerByCommandId(ctx context.Context, workflowId, workflowRunId, workflowStateId string, stateExecutionNumber int, timerCommandId string) error
	// InvokeRPCByName invokes an RPC
	// workflowId is required, workflowRunId is optional and default to current runId of the workflowId
	// rpcName is required
	// input and outputPtr are optional
	InvokeRPCByName(ctx context.Context, workflowId, workflowRunId, rpcName string, input interface{}, outputPtr interface{}, rpcOptions *RPCOptions) error
}

// NewUnregisteredClient returns a UnregisteredClient
// It will let you invoke the APIs to Dex server without much type validation checks(workflow type, channel names, etc).
// It's useful for calling Client APIs without workflow registry(which may require to have all the workflow dependencies)
func NewUnregisteredClient(options *ClientOptions) UnregisteredClient {
	if options == nil {
		options = GetLocalDefaultClientOptions()
	}

	apiClient := dexpb.NewAPIClient(&dexpb.Configuration{
		Servers: []dexpb.ServerConfiguration{
			{
				URL: options.ServerUrl,
			},
		},
	})

	return &unregisteredClientImpl{
		options:   options,
		apiClient: apiClient,
	}
}

// NewClient returns a Client
// It requires a registry in order to perform validation checks (workflow type, channel names, etc)
// Use NewUnregisteredClient if you don't have a registry in your application
func NewClient(registry Registry, options *ClientOptions) Client {
	if registry == nil {
		panic("cannot have nil registry")
	}
	return &clientImpl{
		UnregisteredClient: NewUnregisteredClient(options),
		registry:           registry,
		options:            options,
	}
}

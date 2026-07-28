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

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

type Registry interface {
	// AddWorkflow registers a workflow
	AddWorkflow(workflow ObjectWorkflow) error
	// AddWorkflows registers multiple workflows
	AddWorkflows(workflows ...ObjectWorkflow) error
	// GetAllRegisteredWorkflowTypes returns all the workflow types that have been registered
	GetAllRegisteredWorkflowTypes() []string

	// GetWorkflow returns the workflow with the given type
	GetWorkflow(wfType string) ObjectWorkflow

	// below are all for internal implementation
	getWorkflowStartingState(wfType string) WorkflowState
	getWorkflowStateDef(wfType string, id string) StateDef
	getWorkflowRPC(wfType string, rpcMethod string) CommunicationMethodDef
	getWorkflowSignalNameStore(wfType string) map[string]bool
	getWorkflowInternalChannelNameStore(wfType string) map[string]bool
	getWorkflowDataAttributesKeyStore(wfType string) map[string]bool
	getSearchAttributeTypeStore(wfType string) map[string]dexpb.SearchAttributeValueType
}

func NewRegistry() Registry {
	return &registryImpl{
		workflowStore:            map[string]ObjectWorkflow{},
		workflowStartingState:    map[string]WorkflowState{},
		workflowStateStore:       map[string]map[string]StateDef{},
		signalNameStore:          map[string]map[string]bool{},
		internalChannelNameStore: map[string]map[string]bool{},
		dataAttrsKeyStore:        map[string]map[string]bool{},
		searchAttributeTypeStore: map[string]map[string]dexpb.SearchAttributeValueType{},
		workflowRPCStore:         map[string]map[string]CommunicationMethodDef{},
	}
}

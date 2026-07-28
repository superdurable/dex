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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type registryImpl struct {
	workflowStore            map[string]ObjectWorkflow
	workflowStartingState    map[string]WorkflowState
	workflowStateStore       map[string]map[string]StateDef
	workflowRPCStore         map[string]map[string]CommunicationMethodDef
	signalNameStore          map[string]map[string]bool
	internalChannelNameStore map[string]map[string]bool
	dataAttrsKeyStore        map[string]map[string]bool
	searchAttributeTypeStore map[string]map[string]dexpb.SearchAttributeValueType
}

func (r *registryImpl) AddWorkflows(workflows ...ObjectWorkflow) error {
	for _, wf := range workflows {
		err := r.AddWorkflow(wf)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *registryImpl) AddWorkflow(wf ObjectWorkflow) error {
	if err := r.registerWorkflow(wf); err != nil {
		return err
	}
	if err := r.registerWorkflowState(wf); err != nil {
		return err
	}
	if err := r.registerWorkflowCommunicationSchema(wf); err != nil {
		return err
	}
	return r.registerWorkflowPersistenceSchema(wf)
}

func (r *registryImpl) GetAllRegisteredWorkflowTypes() []string {
	var res []string
	for wfType := range r.workflowStore {
		res = append(res, wfType)
	}
	return res
}

func (r *registryImpl) GetWorkflow(wfType string) ObjectWorkflow {
	return r.workflowStore[wfType]
}

func (r *registryImpl) getWorkflowStartingState(wfType string) WorkflowState {
	return r.workflowStartingState[wfType]
}

func (r *registryImpl) getWorkflowStateDef(wfType string, id string) StateDef {
	return r.workflowStateStore[wfType][id]
}

func (r *registryImpl) getWorkflowSignalNameStore(wfType string) map[string]bool {
	return r.signalNameStore[wfType]
}

func (r *registryImpl) getWorkflowInternalChannelNameStore(wfType string) map[string]bool {
	return r.internalChannelNameStore[wfType]
}

func (r *registryImpl) getWorkflowDataAttributesKeyStore(wfType string) map[string]bool {
	return r.dataAttrsKeyStore[wfType]
}

func (r *registryImpl) getSearchAttributeTypeStore(wfType string) map[string]dexpb.SearchAttributeValueType {
	return r.searchAttributeTypeStore[wfType]
}

func (r *registryImpl) getWorkflowRPC(wfType string, rpcMethod string) CommunicationMethodDef {
	return r.workflowRPCStore[wfType][rpcMethod]
}

func (r *registryImpl) registerWorkflow(wf ObjectWorkflow) error {
	wfType := GetFinalWorkflowType(wf)
	_, ok := r.workflowStore[wfType]
	if ok {
		return NewWorkflowDefinitionError("workflow type conflict: " + wfType)
	}
	r.workflowStore[wfType] = wf
	return nil
}

func (r *registryImpl) registerWorkflowState(wf ObjectWorkflow) error {
	wfType := GetFinalWorkflowType(wf)
	stateMap := map[string]StateDef{}
	var startingState WorkflowState
	for _, state := range wf.GetWorkflowStates() {
		stateId := GetFinalWorkflowStateId(state.State)
		_, ok := stateMap[stateId]
		if ok {
			return NewWorkflowDefinitionErrorFmt("Workflow %v cannot have duplicate stateId %v ", wfType, stateId)
		}
		stateMap[stateId] = state
		if state.CanStartWorkflow {
			if startingState == nil {
				startingState = state.State
			} else {
				return NewWorkflowDefinitionError("Workflow must contain exactly one starting states: " + wfType)
			}
		}
	}
	r.workflowStateStore[wfType] = stateMap
	r.workflowStartingState[wfType] = startingState

	return nil
}

func (r *registryImpl) registerWorkflowCommunicationSchema(wf ObjectWorkflow) error {
	wfType := GetFinalWorkflowType(wf)
	signalMap := map[string]bool{}
	internalChannel := map[string]bool{}
	rpcMap := map[string]CommunicationMethodDef{}
	for _, methodDef := range wf.GetCommunicationSchema() {
		if methodDef.CommunicationMethod == CommunicationMethodSignalChannel {
			signalMap[methodDef.Name] = true
		} else if methodDef.CommunicationMethod == CommunicationMethodInternalChannel {
			internalChannel[methodDef.Name] = true
		} else if methodDef.CommunicationMethod == CommunicationMethodRPCMethod {
			rpcName, wfTypeFromRpc := extractRPCNameAndWorkflowType(methodDef.RPC)
			if wfTypeFromRpc != wfType {
				return NewWorkflowDefinitionError("invalid CommunicationMethod definition for RPC" + string(methodDef.CommunicationMethod) + " :" + rpcName)
			}
			rpcMap[rpcName] = methodDef
		} else {
			return NewWorkflowDefinitionError("invalid CommunicationMethod definition " + string(methodDef.CommunicationMethod))
		}
	}
	r.signalNameStore[wfType] = signalMap
	r.internalChannelNameStore[wfType] = internalChannel
	r.workflowRPCStore[wfType] = rpcMap
	return nil
}

func (r *registryImpl) registerWorkflowPersistenceSchema(wf ObjectWorkflow) error {
	wfType := GetFinalWorkflowType(wf)
	dataAttrsKeys := map[string]bool{}
	searchAttributes := map[string]dexpb.SearchAttributeValueType{}
	for _, pers := range wf.GetPersistenceSchema() {
		if pers.FieldType == PersistenceFieldTypeDataObject {
			dataAttrsKeys[pers.Key] = true
		} else if pers.FieldType == PersistenceFieldTypeSearchAttribute && pers.SearchAttributeType != nil {
			searchAttributes[pers.Key] = *pers.SearchAttributeType
		} else {
			return NewWorkflowDefinitionErrorFmt("invalid PersistenceField definition %s for key %s ", string(pers.FieldType), pers.Key)
		}
	}
	r.dataAttrsKeyStore[wfType] = dataAttrsKeys
	r.searchAttributeTypeStore[wfType] = searchAttributes
	return nil
}

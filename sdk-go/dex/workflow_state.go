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
	"reflect"
)

type WorkflowState interface {
	// GetStateId defines the StateId of this workflow state definition.
	// the StateId is being used for WorkerService to choose the right WorkflowState to execute Start/Execute APIs
	// See GetDefaultWorkflowStateId for default value when return empty string.
	// It's the package + struct name of the workflow instance and ignores the import paths and aliases.
	// e.g. if the workflow is from myStruct{} under mywf package, the simple name is just "mywf.myStruct". Underneath, it's from reflect.TypeOf(wf).String().
	//
	// Usually using default value is enough. Unless cases like:
	// 1. You rename the workflowState struct but there is some in-flight state execution still using the old StateId
	// 2. To avoid type name conflicts because the GetDefaultWorkflowStateId is not long enough
	// 3. In case of dynamic workflow state implementation, return customized values instead of using empty string
	GetStateId() string

	// WaitUntil is the method to set up commands set up to wait for, before `Execute` API is invoked.
	//           It's optional -- use dex.WorkflowStateDefaultsNoWaitUntil or dex.NoWaitUntil to skip this step( Execute will be invoked instead)
	//
	//  ctx              the context info of this API invocation, like workflow start time, workflowId, etc
	//  input            the state input
	//  Persistence      the API for 1) data attributes, 2) search attributes and 3) stateExecutionLocals 4) recordEvent
	//                         DataObjects and SearchAttributes are defined by ObjectWorkflow interface.
	//                         StateExecutionLocals are for passing data within the state execution
	//                         RecordEvent is for storing some tracking info(e.g. RPC call input/output) when executing the API.
	//                         Note that any write API will be recorded to server after the whole WaitUntil API response is accepted
	//  Communication    the API right now only for publishing value to internalChannel
	//                         Note that any write API will be recorded to server after the whole start API response is accepted.
	// @return the requested commands for this state
	///
	WaitUntil(ctx WorkflowContext, input Object, persistence Persistence, communication Communication) (*CommandRequest, error)

	// Execute is the method to execute and decide what to do next. Invoke after commands from WaitUntil are completed, or there is WaitUntil is not implemented for the state.
	//
	//  ctx              the context info of this API invocation, like workflow start time, workflowId, etc
	//  input            the state input
	//  CommandResults   the results of the command that executed by WaitUntil
	//  Persistence      the API for 1) data attributes, 2) search attributes and 3) stateExecutionLocals 4) recordEvent
	//                         DataObjects and SearchAttributes are defined by ObjectWorkflow interface.
	//                         StateExecutionLocals are for passing data within the state execution
	//                         RecordEvent is for storing some tracking info(e.g. RPC call input/output) when executing the API.
	//                         Note that any write API will be recorded to server after the whole WaitUntil API response is accepted
	//  Communication    the API right now only for publishing value to internalChannel
	//                         Note that any write API will be recorded to server after the whole start API response is accepted.
	// @return the decision of what to do next(e.g. transition to next states or closing workflow)
	Execute(ctx WorkflowContext, input Object, commandResults CommandResults, persistence Persistence, communication Communication) (*StateDecision, error)

	// GetStateOptions can just return nil to use the default Options
	// StateOptions is optional configuration to adjust the state behaviors
	GetStateOptions() *StateOptions
}

// GetFinalWorkflowStateId returns the stateId that will be registered and used
// if the workflowState is from myStruct{} under mywf package, the method returns "mywf.myStruct"
func GetFinalWorkflowStateId(workflowState WorkflowState) string {
	sid := workflowState.GetStateId()
	if sid == "" {
		return getSimpleTypeNameFromReflect(workflowState)
	}
	return sid
}

// WorkflowStateDefaults is a convenient struct to put into your state implementation to save the boilerplate code. Eg:
// Example usage:
//
//	type myStateImpl struct{
//	    WorkflowStateDefaults
//	}
type WorkflowStateDefaults struct {
	DefaultStateId
	DefaultStateOptions
}

// WorkflowStateDefaultsNoWaitUntil is a convenient struct to put into your state implementation to save the boilerplate code. Eg:
// Example usage:
//
//	type myStateImpl struct{
//	    WorkflowStateDefaultsNoWaitUntil
//	}
type WorkflowStateDefaultsNoWaitUntil struct {
	DefaultStateId
	DefaultStateOptions
	NoWaitUntil
}

type DefaultStateId struct{}

func (d DefaultStateId) GetStateId() string {
	return ""
}

type DefaultStateOptions struct{}

func (d DefaultStateOptions) GetStateOptions() *StateOptions {
	return nil
}

// NoWaitUntil is a struct to tell that the state doesn't implement a WaitUntil API
// The State will then invoke the Execute API without invoking WaitUntil API and waiting for any commands
type NoWaitUntil struct{}

func (d NoWaitUntil) WaitUntil(ctx WorkflowContext, input Object, persistence Persistence, communication Communication) (*CommandRequest, error) {
	panic("this method is for skipping WaitUntil. It should never be called")
}

func ShouldSkipWaitUntilAPI(state WorkflowState) bool {
	rt := reflect.TypeOf(state)
	var t reflect.Type
	if rt.Kind() == reflect.Pointer {
		t = rt.Elem()
	} else if rt.Kind() == reflect.Struct {
		t = rt
	} else {
		panic("a workflow state must be an pointer or a struct")
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Type.String() == "dex.NoWaitUntil" || field.Type.String() == "dex.WorkflowStateDefaultsNoWaitUntil" {
			return true
		}
	}

	return false
}

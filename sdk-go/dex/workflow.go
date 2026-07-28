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
	"runtime"
	"strings"
)

// ObjectWorkflow is the interface to define a workflow definition.
// ObjectWorkflow is a top level concept in Dex. Any object that is long-lasting(at least a few seconds) can be modeled as an "ObjectWorkflow".
type ObjectWorkflow interface {
	// GetWorkflowStates defines the states of the workflow. A state represents a step of the workflow state machine.
	// A state can execute some commands (signal/timer) and wait for result
	// See more details in the WorkflowState interface.
	// It can return an empty list, meaning no states.
	// There can be at most one startingState in the list.
	// If there is no startingState or with the default empty state list, the workflow
	// will not start any state execution after workflow stated. Application can still
	// use RPC to invoke new state execution in the future.
	GetWorkflowStates() []StateDef

	// GetPersistenceSchema defines all the persistence fields for this workflow, this includes:
	//  1. Data objects
	//  2. Search attributes
	//
	// Data objects can be read/upsert in WorkflowState WaitUntil/Execute API
	// Data objects  can also be read by getDataObjects API by external applications using {@link Client}
	//
	// Search attributes can be read/upsert in WorkflowState WaitUntil/Execute API
	// Search attributes can also be read by GetSearchAttributes Client API by external applications.
	// External applications can also use "SearchWorkflow" API to find workflows by SQL-like query
	GetPersistenceSchema() []PersistenceFieldDef

	// GetCommunicationSchema defines all the communication methods for this workflow, this includes
	// 1. Signal channel
	// 2. Interstate channel
	//
	// Signal channel is for external applications to send signal to workflow execution.
	// ObjectWorkflow execution can listen on the signal in the WorkflowState WaitUntil API and receive in
	// the WorkflowState Execute API
	//
	// InterStateChannel is for synchronization communications between WorkflowStates.
	// E.g. WorkflowStateA will continue after receiving a value from WorkflowStateB
	///
	GetCommunicationSchema() []CommunicationMethodDef

	// GetWorkflowType Define the workflowType of this workflow definition.
	// See GetFinalWorkflowType for default value when return empty string.
	// It's the package + struct name of the workflow instance and ignores the import paths and aliases.
	// e.g. if the workflow is from myStruct{} under mywf package, the simple name is just "mywf.myStruct". Underneath, it's from reflect.TypeOf(wf).String().
	//
	// Usually using default value is enough. Unless cases like:
	// 1. To avoid type name conflicts because the GetFinalWorkflowType is not long enough
	// 2. In case of dynamic workflow implementation, return customized values instead of using empty string
	GetWorkflowType() string
}

// RPC is the signature of an RPC of workflow, which will be defined as a workflow method, and registered as RPCMethod under CommunicationSchema
type RPC func(ctx WorkflowContext, input Object, persistence Persistence, communication Communication) (output interface{}, err error)

func extractRPCNameAndWorkflowType(fn RPC) (rpcName string, wfType string) {
	fullName := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()

	elements := strings.Split(fullName, ".")
	shortName := elements[len(elements)-1]
	wfTypeShort := elements[len(elements)-2]
	prefix := elements[len(elements)-3]
	pkgEles := strings.Split(prefix, "/")
	wfType = pkgEles[len(pkgEles)-1] + "." + wfTypeShort

	return strings.TrimSuffix(shortName, "-fm"), wfType
}

// GetFinalWorkflowType returns the workflow type that will be registered and used as DexWorkflowType
// if the workflow is from &myStruct{} or myStruct{} under mywf package, the method returns "mywf.myStruct"
func GetFinalWorkflowType(wf ObjectWorkflow) string {
	wfType := wf.GetWorkflowType()
	if wfType == "" {
		simpleType := getSimpleTypeNameFromReflect(wf)
		return simpleType
	}
	return wfType
}

func getSimpleTypeNameFromReflect(obj interface{}) string {
	rt := reflect.TypeOf(obj)
	rtStr := strings.TrimLeft(rt.String(), "*")
	return rtStr
}

// WorkflowDefaults is a convenient struct to put into your workflow implementation to save the boilerplate code.
// Example usage :
//
//	type myStateImpl struct{
//	    WorkflowDefaults
//	}
type WorkflowDefaults struct {
	DefaultWorkflowType
	EmptyPersistenceSchema
	EmptyWorkflowStates
	EmptyCommunicationSchema
}

// DefaultWorkflowType is a convenient struct to put into your workflow implementation to save the boilerplate code. Eg:
//
//	type myStateImpl struct{
//	    DefaultWorkflowType
//	}
type DefaultWorkflowType struct{}

func (d DefaultWorkflowType) GetWorkflowType() string {
	return ""
}

// EmptyPersistenceSchema is a convenient struct to put into your workflow implementation to save the boilerplate code. Eg:
//
//	type myStateImpl struct{
//	    EmptyPersistenceSchema
//	}
type EmptyPersistenceSchema struct{}

func (d EmptyPersistenceSchema) GetPersistenceSchema() []PersistenceFieldDef {
	return nil
}

// EmptyCommunicationSchema is a convenient struct to put into your workflow implementation to save the boilerplate code. Eg:
//
//	type myStateImpl struct{
//	    EmptyCommunicationSchema
//	}
type EmptyCommunicationSchema struct{}

func (d EmptyCommunicationSchema) GetCommunicationSchema() []CommunicationMethodDef {
	return nil
}

// EmptyWorkflowStates is a convenient struct to put into your workflow implementation to save the boilerplate code. Eg:
//
//	type myStateImpl struct{
//	    EmptyWorkflowStates
//	}
type EmptyWorkflowStates struct{}

func (d EmptyWorkflowStates) GetWorkflowStates() []StateDef {
	return nil
}

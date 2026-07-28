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

package integ

import (
	"fmt"
	"github.com/superdurable/dex/sdk-go/dex"
)

type rpcWorkflow struct {
	dex.WorkflowDefaults
}

func (b rpcWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.InternalChannelDef("test"),
		dex.RPCMethodDef(b.TestRPC, nil),
		dex.RPCMethodDef(b.TestErrorRPC, nil),
	}
}

func (b rpcWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&rpcWorkflowState1{}),
	}
}

func (b rpcWorkflow) TestRPC(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {
	var i int
	input.Get(&i)
	i++
	communication.PublishInternalChannel("test", i)
	return i, nil
}

func (b rpcWorkflow) TestErrorRPC(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {
	return nil, fmt.Errorf("test error")
}

type rpcWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b rpcWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AllCommandsCompletedRequest(
		dex.NewInternalChannelCommand("", "test"),
	), nil
}

func (b rpcWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var i int
	input.Get(&i)
	var j int
	commandResults.InternalChannelCommands[0].Value.Get(&j)
	return dex.GracefulCompleteWorkflow(i + j), nil
}

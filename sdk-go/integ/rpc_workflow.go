// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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

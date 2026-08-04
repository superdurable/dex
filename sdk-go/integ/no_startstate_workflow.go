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
	"github.com/superdurable/dex/sdk-go/dex"
)

type noStartStateWorkflow struct {
	dex.WorkflowDefaults
}

func (b noStartStateWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.RPCMethodDef(b.TestRPC, nil),
	}
}

func (b noStartStateWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.NonStartingStateDef(&noStartStateWorkflowState1{}),
	}
}

func (b noStartStateWorkflow) TestRPC(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {
	var i int
	input.Get(&i)
	i++
	communication.TriggerStateMovements(dex.NewStateMovement(noStartStateWorkflowState1{}, nil))
	return i, nil
}

type noStartStateWorkflowState1 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
}

func (b noStartStateWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.GracefulCompletingWorkflow, nil
}

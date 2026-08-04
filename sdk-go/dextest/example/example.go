// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package example

import "github.com/superdurable/dex/sdk-go/dex"

func NewInitState() dex.WorkflowState {
	return initState{}
}

type initState struct {
	dex.WorkflowStateDefaults
}

const keyCustomer = "customer"

func (b initState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var customer string
	input.Get(&customer)
	persistence.SetDataAttribute(keyCustomer, customer)
	return dex.EmptyCommandRequest(), nil
}

func (b initState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.GracefulCompletingWorkflow, nil
}

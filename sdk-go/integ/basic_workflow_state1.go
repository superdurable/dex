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

type basicWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b basicWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	if ctx.GetAttempt() <= 0 {
		panic("attempt should be greater than zero")
	}
	if ctx.GetFirstAttemptTimestampSeconds() <= 0 {
		panic("GetFirstAttemptTimestampSeconds should be greater than zero")
	}
	return dex.EmptyCommandRequest(), nil
}

func (b basicWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	if ctx.GetAttempt() <= 0 {
		panic("attempt should be greater than zero")
	}
	if ctx.GetFirstAttemptTimestampSeconds() <= 0 {
		panic("GetFirstAttemptTimestampSeconds should be greater than zero")
	}
	var i int
	input.Get(&i)
	return dex.SingleNextState(basicWorkflowState2{}, i+1), nil
}

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
	"errors"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

type proceedOnStateStartFailWorkflowState1 struct {
	output string
}

func (b *proceedOnStateStartFailWorkflowState1) GetStateId() string {
	return "proceed_on_state_start_fail_workflow_state1"
}

func (b *proceedOnStateStartFailWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var i string
	input.Get(&i)
	b.output = i + "_state1_start"
	return nil, errors.New("")
}

func (b *proceedOnStateStartFailWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	b.output += "_state1_decide"
	return dex.SingleNextState(&proceedOnStateStartFailWorkflowState2{}, b.output), nil
}

func (b *proceedOnStateStartFailWorkflowState1) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		WaitUntilApiRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: dexpb.PtrInt32(1),
			MaximumAttempts:        dexpb.PtrInt32(2),
		},
		WaitUntilApiFailurePolicy: dexpb.PROCEED_ON_FAILURE.Ptr(),
	}
}

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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

type stateApiTimeoutWorkflowState1 struct {
	dex.DefaultStateId
}

func (b stateApiTimeoutWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	time.Sleep(time.Minute)
	return nil, nil
}

func (b stateApiTimeoutWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.ForceFailWorkflow("a failing message"), nil
}

func (b stateApiTimeoutWorkflowState1) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		WaitUntilApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttempts: dexpb.PtrInt32(1),
		},
		WaitUntilApiTimeoutSeconds: dexpb.PtrInt32(1),
	}
}

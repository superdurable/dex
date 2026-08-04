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

type executeApiFailRecoveryWorkflowState1 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
}

func (b executeApiFailRecoveryWorkflowState1) GetStateId() string {
	return "execute_api_fail_recovery_workflow_state1"
}

func (b executeApiFailRecoveryWorkflowState1) GetStateOptions() *dex.StateOptions {
	options := &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: dexpb.PtrInt32(1),
			MaximumAttempts:        dexpb.PtrInt32(1),
		},
		ExecuteApiFailureProceedState: &executeApiFailRecoveryWorkflowState2{},
	}

	return options
}

func (b executeApiFailRecoveryWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return nil, errors.New("error")
}

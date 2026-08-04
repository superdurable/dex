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

type interStateWorkflowState2 struct {
	dex.WorkflowStateDefaults
}

func (b interStateWorkflowState2) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	communication.PublishInternalChannel(interStateChannel2, 2)
	return dex.EmptyCommandRequest(), nil
}

func (b interStateWorkflowState2) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.GracefulCompletingWorkflow, nil
}

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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

type interStateWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b interStateWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
			dex.NewInternalChannelCommand("id1", interStateChannel1),
			dex.NewInternalChannelCommand("id2", interStateChannel2)),
		nil
}

func (b interStateWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var i int
	cmd1 := commandResults.GetInternalChannelCommandResultById("id1")
	cmd2 := commandResults.GetInternalChannelCommandResultById("id2")
	cmd2.Value.Get(&i)

	if cmd1.Status == dexpb.WAITING && i == 2 {
		return dex.GracefulCompletingWorkflow, nil
	}
	return nil, fmt.Errorf("error in executing %s", ctx.GetStateExecutionId())
}

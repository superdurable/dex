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
	"time"
)

type timerWorkflowState1 struct {
	dex.DefaultStateId
	dex.DefaultStateOptions
}

func (b timerWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var i int
	input.Get(&i)
	return dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", time.Duration(i)*time.Second),
	), nil
}

func (b timerWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var i int
	input.Get(&i)
	return dex.GracefulCompleteWorkflow(i + 1), nil
}

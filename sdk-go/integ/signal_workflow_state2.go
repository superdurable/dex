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
	"time"
)

type signalWorkflowState2 struct {
	dex.WorkflowStateDefaults
}

const timerCommandId = "timerId"
const signalCommandId = "s1"

func (b signalWorkflowState2) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var val int
	input.Get(&val)
	if val != 10 {
		panic(fmt.Sprintf("input value should be 10 but is %v", val))
	}

	return dex.AnyCommandCombinationsCompletedRequest(
		[][]string{
			{signalCommandId, timerCommandId},
		},
		dex.NewSignalCommand(signalCommandId, testChannelName1),
		dex.NewSignalCommand(signalCommandId, testChannelName2),
		dex.NewTimerCommandByDuration(timerCommandId, 24*time.Hour),
	), nil
}

func (b signalWorkflowState2) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	signal0 := commandResults.Signals[0]
	signal1 := commandResults.Signals[1]
	timer := commandResults.Timers[0]

	if signal0.CommandId != signalCommandId || signal0.ChannelName != testChannelName1 || signal0.Status != dexpb.RECEIVED {
		panic(testChannelName1 + " should be waiting....")
	}

	if signal1.CommandId != signalCommandId || signal1.ChannelName != testChannelName2 || signal1.Status != dexpb.WAITING {
		panic(testChannelName2 + " should be received....")
	}

	if timer.CommandId != timerCommandId || timer.Status != dexpb.FIRED {
		panic("timer should be fired")
	}

	var val int
	signal0.SignalValue.Get(&val)
	if val != 100 {
		panic("signal value should be 100")
	}

	return dex.GracefulCompleteWorkflow(val), nil
}

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

type signalWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b signalWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
		dex.NewSignalCommand("", testChannelName1),
		dex.NewSignalCommand("", testChannelName2),
	), nil
}

func (b signalWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	signal0 := commandResults.Signals[0]
	signal1 := commandResults.Signals[1]
	if signal0.CommandId != "" || signal0.ChannelName != testChannelName1 || signal0.Status != dexpb.WAITING {
		panic(testChannelName1 + " should be waiting....")
	}
	if signal1.CommandId == "" && signal1.ChannelName == testChannelName2 && signal1.Status == dexpb.RECEIVED {
		var value int
		signal1.SignalValue.Get(&value)
		return dex.SingleNextState(signalWorkflowState2{}, value), nil
	}
	return nil, fmt.Errorf("%s doesn't receive correct value", testChannelName2)
}

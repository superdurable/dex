// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package polling

import (
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

func NewPollingWorkflow(svc service.MyService) dex.ObjectWorkflow {

	return &PollingWorkflow{
		svc: svc,
	}
}

const (
	dataAttrCurrPolls = "currPolls" // tracks how many polls have been done

	SignalChannelTaskACompleted = "taskACompleted"
	SignalChannelTaskBCompleted = "taskBCompleted"

	InternalChannelTaskCCompleted = "taskCCompleted"
)

type PollingWorkflow struct {
	dex.WorkflowDefaults

	svc service.MyService
}

func (e PollingWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&initState{}),
		dex.NonStartingStateDef(&pollState{svc: e.svc}),
		dex.NonStartingStateDef(&checkAndCompleteState{svc: e.svc}),
	}
}

func (e PollingWorkflow) GetPersistenceSchema() []dex.PersistenceFieldDef {
	return []dex.PersistenceFieldDef{
		dex.DataAttributeDef(dataAttrCurrPolls),
	}
}

func (e PollingWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(SignalChannelTaskACompleted),
		dex.SignalChannelDef(SignalChannelTaskBCompleted),
		dex.InternalChannelDef(InternalChannelTaskCCompleted),
	}
}

type initState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
}

func (i initState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var maxPollsRequired int
	input.Get(&maxPollsRequired)

	return dex.MultiNextStatesWithInput(
		dex.NewStateMovement(pollState{}, maxPollsRequired),
		dex.NewStateMovement(checkAndCompleteState{}, nil),
	), nil
}

type checkAndCompleteState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (i checkAndCompleteState) WaitUntil(
	ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication,
) (*dex.CommandRequest, error) {
	return dex.AllCommandsCompletedRequest(
		dex.NewSignalCommand("", SignalChannelTaskACompleted),
		dex.NewSignalCommand("", SignalChannelTaskBCompleted),
		dex.NewInternalChannelCommand("", InternalChannelTaskCCompleted),
	), nil
}

func (i checkAndCompleteState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	return dex.GracefulCompletingWorkflow, nil
}

type pollState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (i pollState) WaitUntil(
	ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication,
) (*dex.CommandRequest, error) {

	return dex.AnyCommandCompletedRequest(
		dex.NewTimerCommand("", time.Now().Add(time.Second*2)),
	), nil
}

func (i pollState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var maxPollsRequired int
	input.Get(&maxPollsRequired)

	i.svc.CallAPI1("calling API1 for polling service C")

	var currPolls int
	persistence.GetDataAttribute(dataAttrCurrPolls, &currPolls)
	if currPolls >= maxPollsRequired {
		communication.PublishInternalChannel(InternalChannelTaskCCompleted, nil)
		return dex.DeadEnd, nil
	}

	persistence.SetDataAttribute(dataAttrCurrPolls, currPolls+1)
	// loop back to check
	return dex.SingleNextState(pollState{}, maxPollsRequired), nil
}
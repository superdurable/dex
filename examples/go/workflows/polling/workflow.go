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
	"github.com/superdurable/iwf/examples/go/workflows/service"
	"github.com/superdurable/iwf/sdk-go/iwf"
	"time"
)

func NewPollingWorkflow(svc service.MyService) iwf.ObjectWorkflow {

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
	iwf.WorkflowDefaults

	svc service.MyService
}

func (e PollingWorkflow) GetWorkflowStates() []iwf.StateDef {
	return []iwf.StateDef{
		iwf.StartingStateDef(&initState{}),
		iwf.NonStartingStateDef(&pollState{svc: e.svc}),
		iwf.NonStartingStateDef(&checkAndCompleteState{svc: e.svc}),
	}
}

func (e PollingWorkflow) GetPersistenceSchema() []iwf.PersistenceFieldDef {
	return []iwf.PersistenceFieldDef{
		iwf.DataAttributeDef(dataAttrCurrPolls),
	}
}

func (e PollingWorkflow) GetCommunicationSchema() []iwf.CommunicationMethodDef {
	return []iwf.CommunicationMethodDef{
		iwf.SignalChannelDef(SignalChannelTaskACompleted),
		iwf.SignalChannelDef(SignalChannelTaskBCompleted),
		iwf.InternalChannelDef(InternalChannelTaskCCompleted),
	}
}

type initState struct {
	iwf.WorkflowStateDefaultsNoWaitUntil
}

func (i initState) Execute(
	ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence,
	communication iwf.Communication,
) (*iwf.StateDecision, error) {
	var maxPollsRequired int
	input.Get(&maxPollsRequired)

	return iwf.MultiNextStatesWithInput(
		iwf.NewStateMovement(pollState{}, maxPollsRequired),
		iwf.NewStateMovement(checkAndCompleteState{}, nil),
	), nil
}

type checkAndCompleteState struct {
	iwf.WorkflowStateDefaults
	svc service.MyService
}

func (i checkAndCompleteState) WaitUntil(
	ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication,
) (*iwf.CommandRequest, error) {
	return iwf.AllCommandsCompletedRequest(
		iwf.NewSignalCommand("", SignalChannelTaskACompleted),
		iwf.NewSignalCommand("", SignalChannelTaskBCompleted),
		iwf.NewInternalChannelCommand("", InternalChannelTaskCCompleted),
	), nil
}

func (i checkAndCompleteState) Execute(
	ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence,
	communication iwf.Communication,
) (*iwf.StateDecision, error) {
	return iwf.GracefulCompletingWorkflow, nil
}

type pollState struct {
	iwf.WorkflowStateDefaults
	svc service.MyService
}

func (i pollState) WaitUntil(
	ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication,
) (*iwf.CommandRequest, error) {

	return iwf.AnyCommandCompletedRequest(
		iwf.NewTimerCommand("", time.Now().Add(time.Second*2)),
	), nil
}

func (i pollState) Execute(
	ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence,
	communication iwf.Communication,
) (*iwf.StateDecision, error) {
	var maxPollsRequired int
	input.Get(&maxPollsRequired)

	i.svc.CallAPI1("calling API1 for polling service C")

	var currPolls int
	persistence.GetDataAttribute(dataAttrCurrPolls, &currPolls)
	if currPolls >= maxPollsRequired {
		communication.PublishInternalChannel(InternalChannelTaskCCompleted, nil)
		return iwf.DeadEnd, nil
	}

	persistence.SetDataAttribute(dataAttrCurrPolls, currPolls+1)
	// loop back to check
	return iwf.SingleNextState(pollState{}, maxPollsRequired), nil
}
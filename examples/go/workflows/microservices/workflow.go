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

package microservices

import (
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

func NewMicroserviceOrchestrationWorkflow(svc service.MyService) dex.ObjectWorkflow {

	return &OrchestrationWorkflow{
		svc: svc,
	}
}

type OrchestrationWorkflow struct {
	dex.DefaultWorkflowType

	svc service.MyService
}

func (e OrchestrationWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(NewState1(e.svc)),
		dex.NonStartingStateDef(NewState2(e.svc)),
		dex.NonStartingStateDef(NewState3(e.svc)),
		dex.NonStartingStateDef(NewState4(e.svc)),
	}
}

func (e OrchestrationWorkflow) GetPersistenceSchema() []dex.PersistenceFieldDef {
	return []dex.PersistenceFieldDef{
		dex.DataAttributeDef(keyData),
	}
}

func (e OrchestrationWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(SignalChannelReady),

		dex.RPCMethodDef(e.Swap, nil),
	}
}

const (
	keyData = "data"

	SignalChannelReady = "Ready"
)

func (e OrchestrationWorkflow) Swap(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {

	var oldData string
	persistence.GetDataAttribute(keyData, &oldData)
	var newData string
	input.Get(&newData)
	persistence.SetDataAttribute(keyData, newData)

	return oldData, nil
}

func NewState1(svc service.MyService) dex.WorkflowState {
	return state1{svc: svc}
}

type state1 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i state1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var inString string
	input.Get(&inString)

	i.svc.CallAPI1(inString)

	persistence.SetDataAttribute(keyData, inString)
	return dex.MultiNextStatesWithInput(
		dex.NewStateMovement(state2{}, nil),
		dex.NewStateMovement(state3{}, nil),
	), nil
}

func NewState2(svc service.MyService) dex.WorkflowState {
	return state2{svc: svc}
}

type state2 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i state2) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var data string
	persistence.GetDataAttribute(keyData, &data)

	i.svc.CallAPI2(data)
	return dex.DeadEnd, nil
}

func NewState3(svc service.MyService) dex.WorkflowState {
	return state3{svc: svc}
}

type state3 struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (i state3) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
		dex.NewTimerCommand("", time.Now().Add(time.Hour*24)),
		dex.NewSignalCommand("", SignalChannelReady),
	), nil
}

func (i state3) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var data string
	persistence.GetDataAttribute(keyData, &data)
	i.svc.CallAPI3(data)

	if commandResults.Timers[0].Status == dexpb.FIRED {
		return dex.SingleNextState(state4{}, nil), nil
	}
	return dex.GracefulCompletingWorkflow, nil
}

func NewState4(svc service.MyService) dex.WorkflowState {
	return state4{svc: svc}
}

type state4 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i state4) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var data string
	persistence.GetDataAttribute(keyData, &data)
	i.svc.CallAPI4(data)
	return dex.GracefulCompletingWorkflow, nil
}

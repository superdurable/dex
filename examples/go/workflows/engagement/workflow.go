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

package engagement

import (
	"fmt"
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

func NewEngagementWorkflow(svc service.MyService) dex.ObjectWorkflow {

	return &EngagementWorkflow{
		svc: svc,
	}
}

type EngagementWorkflow struct {
	dex.DefaultWorkflowType

	svc service.MyService
}

func (e EngagementWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(NewInitState()),
		dex.NonStartingStateDef(NewProcessTimoutState(e.svc)),
		dex.NonStartingStateDef(NewReminderState(e.svc)),
		dex.NonStartingStateDef(NewNotifyExternalSystemState(e.svc)),
	}
}

func (e EngagementWorkflow) GetPersistenceSchema() []dex.PersistenceFieldDef {
	return []dex.PersistenceFieldDef{
		dex.SearchAttributeDef(keyEmployerId, dexpb.KEYWORD),
		dex.SearchAttributeDef(keyJobSeekerId, dexpb.KEYWORD),
		dex.SearchAttributeDef(keyStatus, dexpb.KEYWORD),
		dex.SearchAttributeDef(keyLastUpdateTimestamp, dexpb.INT),

		dex.DataAttributeDef(keyNotes),
	}
}

func (e EngagementWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(SignalChannelOptOutReminder),
		dex.InternalChannelDef(InternalChannelCompleteProcess),

		dex.RPCMethodDef(e.Describe, nil),
		dex.RPCMethodDef(e.Decline, nil),
		dex.RPCMethodDef(e.Accept, nil),
	}
}

const (
	keyEmployerId          = "EmployerId"
	keyJobSeekerId         = "JobSeekerId"
	keyStatus              = "EngagementStatus"
	keyLastUpdateTimestamp = "LastUpdateTimeMillis"
	keyNotes               = "notes"

	SignalChannelOptOutReminder    = "OptOutReminder"
	InternalChannelCompleteProcess = "CompleteProcess"
)

func (e EngagementWorkflow) Describe(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {

	status := persistence.GetSearchAttributeKeyword(keyStatus)
	employerId := persistence.GetSearchAttributeKeyword(keyEmployerId)
	jobSeekerId := persistence.GetSearchAttributeKeyword(keyJobSeekerId)
	var notes string
	persistence.GetDataAttribute(keyNotes, &notes)

	return EngagementDescription{
		EmployerId:    employerId,
		JobSeekerId:   jobSeekerId,
		Notes:         notes,
		CurrentStatus: Status(status),
	}, nil
}

func (e EngagementWorkflow) Decline(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {

	status := Status(persistence.GetSearchAttributeKeyword(keyStatus))
	if status != StatusInitiated {
		return nil, fmt.Errorf("can only decline in INITIATED status, current is %v", status)
	}

	persistence.SetSearchAttributeKeyword(keyStatus, string(StatusDeclined))
	persistence.SetSearchAttributeInt(keyLastUpdateTimestamp, time.Now().Unix())
	communication.TriggerStateMovements(dex.NewStateMovement(notifyExternalSystemState{}, string(StatusDeclined)))

	var notes string
	input.Get(&notes)

	var currentNotes string
	persistence.GetDataAttribute(keyNotes, &currentNotes)
	persistence.SetDataAttribute(keyNotes, currentNotes+";"+notes)
	return nil, nil
}

func (e EngagementWorkflow) Accept(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {

	status := Status(persistence.GetSearchAttributeKeyword(keyStatus))
	if status != StatusInitiated && status != StatusDeclined {
		return nil, fmt.Errorf("can only decline in INITIATED or DECLINED status, current is %v", status)
	}

	persistence.SetSearchAttributeKeyword(keyStatus, string(StatusAccepted))
	persistence.SetSearchAttributeInt(keyLastUpdateTimestamp, time.Now().Unix())
	communication.TriggerStateMovements(dex.NewStateMovement(notifyExternalSystemState{}, string(StatusAccepted)))

	var notes string
	input.Get(&notes)

	var currentNotes string
	persistence.GetDataAttribute(keyNotes, &currentNotes)
	persistence.SetDataAttribute(keyNotes, currentNotes+";"+notes)
	return nil, nil
}

func NewInitState() dex.WorkflowState {
	return initState{}
}

type initState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
}

func (i initState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var engInput EngagementInput
	input.Get(&engInput)

	persistence.SetSearchAttributeKeyword(keyEmployerId, engInput.EmployerId)
	persistence.SetSearchAttributeKeyword(keyJobSeekerId, engInput.JobSeekerId)
	persistence.SetSearchAttributeKeyword(keyStatus, string(StatusInitiated))

	persistence.SetDataAttribute(keyNotes, engInput.Notes)
	return dex.MultiNextStatesWithInput(
		dex.NewStateMovement(processTimoutState{}, nil),
		dex.NewStateMovement(reminderState{}, nil),
		dex.NewStateMovement(notifyExternalSystemState{}, StatusInitiated),
	), nil
}

func NewProcessTimoutState(svc service.MyService) dex.WorkflowState {
	return processTimoutState{
		svc: svc,
	}
}

type processTimoutState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (p processTimoutState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
		dex.NewTimerCommand("", time.Now().Add(time.Hour*24*60)), // ~ 2 months
		dex.NewInternalChannelCommand("", InternalChannelCompleteProcess),
	), nil
}

func (p processTimoutState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	status := persistence.GetSearchAttributeKeyword(keyStatus)
	employerId := persistence.GetSearchAttributeKeyword(keyEmployerId)
	jobSeekerId := persistence.GetSearchAttributeKeyword(keyJobSeekerId)
	updateStatus := "timeout"
	if status == string(StatusAccepted) {
		updateStatus = "done"
	}
	p.svc.UpdateExternalSystem(fmt.Sprintf("notify engagement from employer %v, jobSeeker %v for status %v", employerId, jobSeekerId, status))
	return dex.GracefulCompleteWorkflow(updateStatus), nil
}

func NewReminderState(svc service.MyService) dex.WorkflowState {
	return reminderState{
		svc: svc,
	}
}

type reminderState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (r reminderState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
		dex.NewTimerCommand("", time.Now().Add(time.Second*5)), // use 5 seconds for demo, should be 24 hours in real world
		dex.NewSignalCommand("", SignalChannelOptOutReminder),
	), nil
}

func (r reminderState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	status := persistence.GetSearchAttributeKeyword(keyStatus)
	if status != string(StatusInitiated) {
		return dex.DeadEnd, nil
	}
	optoutSignalCommandResult := commandResults.Signals[0]
	if optoutSignalCommandResult.Status == dexpb.RECEIVED {
		var currentNotes string
		persistence.GetDataAttribute(keyNotes, &currentNotes)
		persistence.SetDataAttribute(keyNotes, currentNotes+";"+"User optout reminder")

		return dex.DeadEnd, nil
	}

	jobSeekerId := persistence.GetSearchAttributeKeyword(keyJobSeekerId)
	r.svc.SendEmail(jobSeekerId, "Reminder:xxx please respond", "Hello xxx, ...")
	return dex.SingleNextState(reminderState{}, nil), nil
}

func NewNotifyExternalSystemState(svc service.MyService) dex.WorkflowState {
	return notifyExternalSystemState{
		svc: svc,
	}
}

type notifyExternalSystemState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (n notifyExternalSystemState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var status Status
	input.Get(&status)

	jobSeekerId := persistence.GetSearchAttributeKeyword(keyJobSeekerId)
	employerId := persistence.GetSearchAttributeKeyword(keyEmployerId)
	n.svc.UpdateExternalSystem(fmt.Sprintf("notify engagement from employerId %v to jobSeekerId %v for status %v ", employerId, jobSeekerId, status))
	return dex.DeadEnd, nil
}

// GetStateOptions customize the state options
// By default, all state execution will retry infinitely (until workflow timeout).
// This may not work for some dependency as we may want to retry for only a certain times
func (n notifyExternalSystemState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			BackoffCoefficient:             dexpb.PtrFloat32(2),
			MaximumAttempts:                dexpb.PtrInt32(100),
			MaximumAttemptsDurationSeconds: dexpb.PtrInt32(3600),
			MaximumIntervalSeconds:         dexpb.PtrInt32(60),
			InitialIntervalSeconds:         dexpb.PtrInt32(3),
		},
	}
}

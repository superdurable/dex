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
	"time"

	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const StatusSearchKey = "CustomKeywordField"

var (
	EmployerID       = dex.DefineAttribute[string]("EmployerId")
	JobSeekerID      = dex.DefineAttribute[string]("JobSeekerId")
	EngagementStatus = dex.DefineAttribute[Status](
		"EngagementStatus",
		dex.Indexed(dex.AttributeIndex{
			Type:     dex.IndexKeyword,
			IndexKey: StatusSearchKey,
		}),
	)
	LastUpdateTimestamp = dex.DefineAttribute[int64]("LastUpdateTimeMillis")
	Notes               = dex.DefineAttribute[string]("notes")
	OptOutReminder      = dex.DefineChannel[dex.None]("OptOutReminder")
	CompleteProcess     = dex.DefineChannel[dex.None]("CompleteProcess")
)

type EngagementFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewEngagementFlow(applicationService service.MyService) *EngagementFlow {
	return &EngagementFlow{service: applicationService}
}

func (flow *EngagementFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initializeStep{}),
		dex.DefineStep(processTimeoutStep{service: flow.service}),
		dex.DefineStep(reminderStep{service: flow.service}),
		dex.DefineStep(notifyExternalSystemStep{service: flow.service}),
	}
}

func (*EngagementFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			EmployerID,
			JobSeekerID,
			EngagementStatus,
			LastUpdateTimestamp,
			Notes,
		},
		Channels: []dex.ChannelDef{OptOutReminder, CompleteProcess},
	}
}

func (*EngagementFlow) Describe(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[EngagementDescription], error) {
	description, err := describe(ctx)
	return dex.RPCResult[EngagementDescription]{Output: description}, err
}

func (*EngagementFlow) Decline(
	ctx dex.Context,
	note string,
) (dex.RPCResult[Status], error) {
	status, err := EngagementStatus.Get(ctx)
	if err != nil {
		return dex.RPCResult[Status]{}, err
	}
	if status != StatusInitiated {
		return dex.RPCResult[Status]{}, fmt.Errorf(
			"can only decline an initiated engagement; current status is %q",
			status,
		)
	}
	if err := updateStatus(ctx, StatusDeclined, note); err != nil {
		return dex.RPCResult[Status]{}, err
	}
	return dex.RPCResult[Status]{
		Output: StatusDeclined,
		NextSteps: []dex.StepMovement{
			dex.MovementOf(notifyExternalSystemStep{}, StatusDeclined),
		},
	}, nil
}

func (*EngagementFlow) Accept(
	ctx dex.Context,
	note string,
) (dex.RPCResult[Status], error) {
	status, err := EngagementStatus.Get(ctx)
	if err != nil {
		return dex.RPCResult[Status]{}, err
	}
	if status != StatusInitiated && status != StatusDeclined {
		return dex.RPCResult[Status]{}, fmt.Errorf(
			"can only accept an initiated or declined engagement; current status is %q",
			status,
		)
	}
	if err := updateStatus(ctx, StatusAccepted, note); err != nil {
		return dex.RPCResult[Status]{}, err
	}
	if err := CompleteProcess.Publish(ctx, nil); err != nil {
		return dex.RPCResult[Status]{}, err
	}
	return dex.RPCResult[Status]{
		Output: StatusAccepted,
		NextSteps: []dex.StepMovement{
			dex.MovementOf(notifyExternalSystemStep{}, StatusAccepted),
		},
	}, nil
}

func describe(ctx dex.Context) (EngagementDescription, error) {
	status, err := EngagementStatus.Get(ctx)
	if err != nil {
		return EngagementDescription{}, err
	}
	employerID, err := EmployerID.Get(ctx)
	if err != nil {
		return EngagementDescription{}, err
	}
	jobSeekerID, err := JobSeekerID.Get(ctx)
	if err != nil {
		return EngagementDescription{}, err
	}
	notes, err := Notes.Get(ctx)
	if err != nil {
		return EngagementDescription{}, err
	}
	return EngagementDescription{
		EmployerID:    employerID,
		JobSeekerID:   jobSeekerID,
		Notes:         notes,
		CurrentStatus: status,
	}, nil
}

func updateStatus(ctx dex.Context, status Status, note string) error {
	if err := EngagementStatus.Set(ctx, status); err != nil {
		return err
	}
	if err := LastUpdateTimestamp.Set(ctx, time.Now().UnixMilli()); err != nil {
		return err
	}
	currentNotes, err := Notes.Get(ctx)
	if err != nil {
		return err
	}
	if note != "" {
		currentNotes += ";" + note
	}
	return Notes.Set(ctx, currentNotes)
}

type initializeStep struct {
	dex.StepDefaultsNoWaitFor[EngagementInput]
}

func (initializeStep) Execute(
	ctx dex.Context,
	input EngagementInput,
) (*dex.StepDecision, error) {
	if err := EmployerID.Set(ctx, input.EmployerID); err != nil {
		return nil, err
	}
	if err := JobSeekerID.Set(ctx, input.JobSeekerID); err != nil {
		return nil, err
	}
	if err := EngagementStatus.Set(ctx, StatusInitiated); err != nil {
		return nil, err
	}
	if err := LastUpdateTimestamp.Set(ctx, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	if err := Notes.Set(ctx, input.Notes); err != nil {
		return nil, err
	}
	return dex.GoToMulti(
		dex.MovementOf(processTimeoutStep{}, nil),
		dex.MovementOf(reminderStep{}, nil),
		dex.MovementOf(notifyExternalSystemStep{}, StatusInitiated),
	), nil
}

type processTimeoutStep struct {
	dex.StepDefaults
	service service.MyService
}

func (processTimeoutStep) WaitFor(
	dex.Context,
	dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(60*24*time.Hour),
		CompleteProcess.ForOne(),
	), nil
}

func (step processTimeoutStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	description, err := describe(ctx)
	if err != nil {
		return nil, err
	}
	result := "timeout"
	if description.CurrentStatus == StatusAccepted {
		result = "done"
	}
	step.service.UpdateExternalSystem(fmt.Sprintf(
		"engagement from employer %s to job seeker %s finished with status %s",
		description.EmployerID,
		description.JobSeekerID,
		description.CurrentStatus,
	))
	return dex.GracefulComplete(result), nil
}

type reminderStep struct {
	dex.StepDefaults
	service service.MyService
}

func (reminderStep) WaitFor(
	dex.Context,
	dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(5*time.Second),
		OptOutReminder.ForOne(),
	), nil
}

func (step reminderStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	status, err := EngagementStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusInitiated {
		return dex.DeadEnd(), nil
	}
	optOuts, err := OptOutReminder.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(optOuts) > 0 {
		if err := updateStatus(ctx, status, "user opted out of reminders"); err != nil {
			return nil, err
		}
		return dex.DeadEnd(), nil
	}
	jobSeekerID, err := JobSeekerID.Get(ctx)
	if err != nil {
		return nil, err
	}
	step.service.SendEmail(jobSeekerID, "Reminder: please respond", "Please respond to the engagement.")
	return dex.GoTo(reminderStep{}, nil), nil
}

type notifyExternalSystemStep struct {
	dex.StepDefaultsNoWaitFor[Status]
	service service.MyService
}

func (step notifyExternalSystemStep) Execute(
	ctx dex.Context,
	status Status,
) (*dex.StepDecision, error) {
	employerID, err := EmployerID.Get(ctx)
	if err != nil {
		return nil, err
	}
	jobSeekerID, err := JobSeekerID.Get(ctx)
	if err != nil {
		return nil, err
	}
	step.service.UpdateExternalSystem(fmt.Sprintf(
		"notify engagement from employer %s to job seeker %s for status %s",
		employerID,
		jobSeekerID,
		status,
	))
	return dex.DeadEnd(), nil
}

var (
	_ dex.Flow                                 = (*EngagementFlow)(nil)
	_ dex.RPC[dex.None, EngagementDescription] = (*EngagementFlow)(nil).Describe
	_ dex.RPC[string, Status]                  = (*EngagementFlow)(nil).Decline
	_ dex.RPC[string, Status]                  = (*EngagementFlow)(nil).Accept
)

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

package reminders

import (
	"fmt"
	"time"

	patternsservice "github.com/superdurable/dex/examples/go/workflows/patterns/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	DAStatus                       = "Status"
	SignalNameOptOutReminder       = "OptOutReminder"
	InternalChannelCompleteProcess = "CompleteProcess"
)

type Status string

const (
	StatusInitiated Status = "INITIATED"
	StatusAccepted  Status = "ACCEPTED"
)

var (
	StatusAttribute = dex.DefineAttribute[string](DAStatus)
	OptOutReminder  = dex.DefineChannel[dex.None](SignalNameOptOutReminder)
	CompleteProcess = dex.DefineChannel[dex.None](InternalChannelCompleteProcess)
)

type ReminderFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewReminderFlow(service patternsservice.ServiceDependency) *ReminderFlow {
	return &ReminderFlow{service: service}
}

func (flow *ReminderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(processTimeoutStep{service: flow.service}),
		dex.DefineStep(reminderStep{service: flow.service}),
	}
}

func (*ReminderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{StatusAttribute},
		Channels:   []dex.ChannelDef{OptOutReminder, CompleteProcess},
	}
}

func (*ReminderFlow) Accept(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	currentStatus, err := StatusAttribute.Get(ctx)
	if err != nil {
		return nil, err
	}
	if currentStatus != string(StatusInitiated) {
		return nil, fmt.Errorf(
			"can only accept in INITIATED status",
		)
	}
	if err := StatusAttribute.Set(ctx, string(StatusAccepted)); err != nil {
		return nil, err
	}
	if err := CompleteProcess.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (initStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if err := StatusAttribute.Set(ctx, string(StatusInitiated)); err != nil {
		return nil, err
	}
	return dex.GoToMulti(
		dex.MovementOf(processTimeoutStep{}, nil),
		dex.MovementOf(reminderStep{}, nil),
	), nil
}

type processTimeoutStep struct {
	dex.StepDefaults
	service patternsservice.ServiceDependency
}

func (processTimeoutStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
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
	currentStatus, err := StatusAttribute.Get(ctx)
	if err != nil {
		return nil, err
	}
	resultStatus := "TIMEOUT"
	if currentStatus == string(StatusAccepted) {
		resultStatus = "ACCEPTED"
	}
	step.service.UpdateExternalSystem("notify for status: " + resultStatus)
	return dex.ForceComplete("done"), nil
}

type reminderStep struct {
	dex.StepDefaults
	service patternsservice.ServiceDependency
}

func (reminderStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
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
	currentStatus, err := StatusAttribute.Get(ctx)
	if err != nil {
		return nil, err
	}
	if currentStatus == string(StatusAccepted) {
		fmt.Println("Reminder state timer expired, but status already ACCEPTED")
		return dex.ForceComplete("done"), nil
	}
	optOuts, err := OptOutReminder.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(optOuts) > 0 {
		step.service.UpdateExternalSystem("user opted out - no more reminders")
		return dex.ForceComplete("done - opt out"), nil
	}
	step.service.SendEmail("Reminder:xxx please respond", "Hello xxx, ...")
	return dex.GoTo(reminderStep{}, nil), nil
}

var (
	_ dex.Flow                    = (*ReminderFlow)(nil)
	_ dex.RPC[dex.None, dex.None] = (*ReminderFlow)(nil).Accept
)

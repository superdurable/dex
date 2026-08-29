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
	"time"

	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const OptOutChannelName = "OptOut"

var OptOut = dex.DefineChannel[dex.None](OptOutChannelName)

type ReminderFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewReminderFlow(service patternsservice.ServiceDependency) *ReminderFlow {
	return &ReminderFlow{service: service}
}

func (flow *ReminderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(reminderStep{service: flow.service}),
	}
}

func (*ReminderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{OptOut}}
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
		OptOut.ForOne(),
	), nil
}

func (step reminderStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if !ctx.HasTimerFired() {
		return dex.GracefulComplete(nil), nil
	}
	step.service.SendEmail("Reminder: please respond", "Hello, ...")
	return dex.GoTo(reminderStep{}, nil), nil
}

var _ dex.Flow = (*ReminderFlow)(nil)

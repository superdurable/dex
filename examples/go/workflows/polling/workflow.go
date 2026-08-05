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
	"time"

	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	CurrentPolls   = dex.DefineAttribute[int]("current-polls")
	TaskACompleted = dex.DefineChannel[dex.None]("task-a-completed")
	TaskBCompleted = dex.DefineChannel[dex.None]("task-b-completed")
	TaskCCompleted = dex.DefineChannel[dex.None]("task-c-completed")
)

type PollingFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewPollingFlow(applicationService service.MyService) *PollingFlow {
	return &PollingFlow{service: applicationService}
}

func (flow *PollingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initializeStep{}),
		dex.DefineStep(pollStep{service: flow.service}),
		dex.DefineStep(waitForTasksStep{}),
	}
}

func (*PollingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{CurrentPolls},
		Channels: []dex.ChannelDef{
			TaskACompleted,
			TaskBCompleted,
			TaskCCompleted,
		},
	}
}

type initializeStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (initializeStep) Execute(
	_ dex.Context,
	maximumPolls int,
) (dex.StepDecision, error) {
	return dex.GoToMulti(
		dex.MovementOf(pollStep{}, maximumPolls),
		dex.MovementOf(waitForTasksStep{}, nil),
	), nil
}

type waitForTasksStep struct {
	dex.StepDefaults
}

func (waitForTasksStep) WaitFor(
	dex.Context,
	dex.None,
) (dex.Wait, error) {
	return dex.AllOf(
		TaskACompleted.ForOne(),
		TaskBCompleted.ForOne(),
		TaskCCompleted.ForOne(),
	), nil
}

func (waitForTasksStep) Execute(
	dex.Context,
	dex.None,
) (dex.StepDecision, error) {
	return dex.GracefulComplete("all tasks completed"), nil
}

type pollStep struct {
	dex.StepDefaults
	service service.MyService
}

func (pollStep) WaitFor(
	dex.Context,
	int,
) (dex.Wait, error) {
	return dex.AnyOf(dex.Timer(time.Second)), nil
}

func (step pollStep) Execute(
	ctx dex.Context,
	maximumPolls int,
) (dex.StepDecision, error) {
	step.service.CallAPI1("calling API1 for polling service C")
	currentPolls, found, err := CurrentPolls.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		currentPolls = 0
	}
	if currentPolls >= maximumPolls {
		if err := TaskCCompleted.Publish(ctx, nil); err != nil {
			return dex.StepDecision{}, err
		}
		return dex.DeadEnd(), nil
	}
	if err := CurrentPolls.Set(ctx, currentPolls+1); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoTo(pollStep{}, maximumPolls), nil
}

var _ dex.Flow = (*PollingFlow)(nil)

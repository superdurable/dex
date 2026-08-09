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

package parentchild

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	ConcurrencyPerParentWorkflow = 3
	TaskQueueChannelName         = "task_queue"
)

var TaskQueue = dex.DefineChannel[int](TaskQueueChannelName)

type WaitForChildInput struct {
	ChildWFID    string
	TimerSeconds int
}

type ParentFlowV2 struct {
	dex.FlowDefaults
	getClient func() *dex.Client
	childFlow *ChildFlow
}

func NewParentFlowV2(
	getClient func() *dex.Client,
	childFlow *ChildFlow,
) *ParentFlowV2 {
	return &ParentFlowV2{getClient: getClient, childFlow: childFlow}
}

func (flow *ParentFlowV2) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(loopForNextTaskStep{}),
		dex.DefineStep(startChildWorkflowStep{
			getClient: flow.getClient,
			childFlow: flow.childFlow,
		}),
		dex.DefineStep(awaitChildWorkflowCompletionStep{getClient: flow.getClient}),
	}
}

func (*ParentFlowV2) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{TaskQueue},
	}
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (initStep) Execute(
	ctx dex.Context,
	numRequests int,
) (*dex.StepDecision, error) {
	for index := 0; index < numRequests; index++ {
		if err := TaskQueue.Publish(ctx, index); err != nil {
			return nil, err
		}
	}
	movements := make([]dex.StepMovement, ConcurrencyPerParentWorkflow)
	for index := 0; index < ConcurrencyPerParentWorkflow; index++ {
		movements[index] = dex.MovementOf(loopForNextTaskStep{}, nil)
	}
	return dex.GoToMulti(movements...), nil
}

type loopForNextTaskStep struct {
	dex.StepDefaults
}

func (loopForNextTaskStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.Until(TaskQueue.ForOne()), nil
}

func (loopForNextTaskStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	results, err := TaskQueue.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	request := results[0]
	return dex.GoTo(startChildWorkflowStep{}, request), nil
}

type startChildWorkflowStep struct {
	dex.StepDefaultsNoWaitFor[int]
	getClient func() *dex.Client
	childFlow *ChildFlow
}

func (step startChildWorkflowStep) Execute(
	_ dex.Context,
	uuid int,
) (*dex.StepDecision, error) {
	childWorkflowID := fmt.Sprintf("child-wf-%d", uuid)
	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	_, err := client.StartFlow(
		context.Background(),
		step.childFlow,
		childWorkflowID,
		fmt.Sprintf("%d", uuid),
		dex.StartFlowOptions{},
	)
	if err != nil {
		var dexErr *dex.Error
		if errors.As(err, &dexErr) && dexErr.SubStatus == dex.ErrorFlowAlreadyStarted {
			fmt.Println("ignore this error because it is already started")
		} else {
			return nil, err
		}
	}
	return dex.GoTo(awaitChildWorkflowCompletionStep{}, WaitForChildInput{
		ChildWFID:    childWorkflowID,
		TimerSeconds: 1,
	}), nil
}

type awaitChildWorkflowCompletionStep struct {
	dex.StepDefaults
	getClient func() *dex.Client
}

func (awaitChildWorkflowCompletionStep) WaitFor(
	_ dex.Context,
	input WaitForChildInput,
) (*dex.Wait, error) {
	return dex.Until(dex.Timer(time.Duration(input.TimerSeconds) * time.Second)), nil
}

func (step awaitChildWorkflowCompletionStep) Execute(
	_ dex.Context,
	input WaitForChildInput,
) (*dex.StepDecision, error) {
	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	waitSeconds := input.TimerSeconds
	if waitSeconds < 1 {
		waitSeconds = 1
	}
	_, err := client.WaitForFlow(
		context.Background(),
		input.ChildWFID,
		dex.WaitForFlowOptions{Timeout: time.Duration(waitSeconds) * time.Second},
	)
	if err != nil {
		var dexErr *dex.Error
		if errors.As(err, &dexErr) && dexErr.SubStatus == dex.ErrorLongPollTimeout {
			nextTimer := input.TimerSeconds * 2
			if nextTimer > 10 {
				nextTimer = 10
			}
			return dex.GoTo(awaitChildWorkflowCompletionStep{}, WaitForChildInput{
				ChildWFID:    input.ChildWFID,
				TimerSeconds: nextTimer,
			}), nil
		}
		return nil, err
	}
	return dex.GoTo(loopForNextTaskStep{}, nil), nil
}

var _ dex.Flow = (*ParentFlowV2)(nil)

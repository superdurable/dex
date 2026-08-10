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

package scalableparallel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

const (
	TaskQueueChannelName         = "TaskQueue"
	ChildCompleteChannelPrefix   = "ChildComplete_"
	CurrentWaitChildWfsAttribute = "CurrentWaitChildWfs"
)

var (
	TaskQueue           = dex.DefineChannel[string](TaskQueueChannelName)
	ChildComplete       = dex.DefineChannelMap[dex.None](ChildCompleteChannelPrefix)
	CurrentWaitChildWfs = dex.DefineAttribute[[]string](CurrentWaitChildWfsAttribute)
)

type ParentFlow struct {
	dex.FlowDefaults
	getClient func() *dex.Client
	childFlow *ChildFlow
}

func NewParentFlow(getClient func() *dex.Client, childFlow *ChildFlow) *ParentFlow {
	return &ParentFlow{getClient: getClient, childFlow: childFlow}
}

func (flow *ParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(loopForNextMessageStep{
			getClient: flow.getClient,
			childFlow: flow.childFlow,
		}),
	}
}

func (*ParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{CurrentWaitChildWfs},
		Channels:   []dex.ChannelDef{TaskQueue, ChildComplete},
	}
}

func (*ParentFlow) Enqueue(
	ctx dex.Context,
	request BatchEnqueueRequest,
) (*dex.RPCResult[bool], error) {
	if TaskQueue.Size(ctx)+len(request.List) > MaxBufferedTasks {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	for _, uuid := range request.List {
		if err := TaskQueue.Publish(ctx, uuid); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*ParentFlow) CompleteChildWorkflow(
	ctx dex.Context,
	childWorkflowID string,
) (*dex.RPCResult[dex.None], error) {
	if err := ChildComplete.Publish(ctx, childWorkflowID, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[BatchEnqueueRequest]
}

func (initStep) Execute(
	ctx dex.Context,
	initRequest BatchEnqueueRequest,
) (*dex.StepDecision, error) {
	for _, uuid := range initRequest.List {
		if err := TaskQueue.Publish(ctx, uuid); err != nil {
			return nil, err
		}
	}
	return dex.GoTo(loopForNextMessageStep{}, nil), nil
}

type loopForNextMessageStep struct {
	dex.StepDefaults
	getClient func() *dex.Client
	childFlow *ChildFlow
}

func (loopForNextMessageStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	waiting, err := CurrentWaitChildWfs.Get(ctx)
	if err != nil {
		var notFound *dex.AttributeNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
		waiting = nil
	}
	conditions := make([]dex.Condition, 0, len(waiting)+1)
	if len(waiting) < ConcurrencyPerParentWorkflow {
		conditions = append(conditions, TaskQueue.ForOne())
	}
	for _, childWfID := range waiting {
		conditions = append(conditions, ChildComplete.ForOne(childWfID))
	}
	return dex.AnyOf(conditions...), nil
}

func (step loopForNextMessageStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	waiting, err := CurrentWaitChildWfs.Get(ctx)
	if err != nil {
		var notFound *dex.AttributeNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
		waiting = nil
	}
	newWaitList := append([]string(nil), waiting...)

	taskResults, err := TaskQueue.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(taskResults) > 0 {
		request := taskResults[0]
		childWorkflowID := "processing-" + request
		client := step.getClient()
		if client == nil {
			return nil, fmt.Errorf("dex client is not available")
		}
		parentAttr, attrErr := dex.InitialAttribute(
			ParentWorkflowID,
			ctx.FlowID(),
		)
		if attrErr != nil {
			return nil, attrErr
		}
		flowTimeout := time.Hour
		_, startErr := client.StartFlow(
			context.Background(),
			step.childFlow,
			childWorkflowID,
			request,
			dex.StartFlowOptions{
				Timeout:       &flowTimeout,
				IDReusePolicy: dex.IDReuseDisallow,
				RequestID:     ptr.Any(ctx.StepExecutionID()),
				AlreadyStarted: &dex.AlreadyStartedOptions{
					IgnoreError: true,
				},
				Attributes: []dex.InitialAttributeDef{parentAttr},
			},
		)
		if startErr != nil {
			var duplicate *dex.FlowAlreadyStartedError
			if errors.As(startErr, &duplicate) {
				fmt.Println(
					"already started by other state/workflow, ignore it " +
						"-- not waiting for it",
				)
			} else {
				return nil, startErr
			}
		} else {
			newWaitList = append(newWaitList, childWorkflowID)
		}
	}

	for _, childWfID := range append([]string(nil), newWaitList...) {
		completions, completeErr := ChildComplete.GetConditionResults(ctx, childWfID)
		if completeErr != nil {
			return nil, completeErr
		}
		if len(completions) > 0 {
			removed := false
			for index, waitingID := range newWaitList {
				if waitingID == childWfID {
					newWaitList = append(newWaitList[:index], newWaitList[index+1:]...)
					removed = true
					break
				}
			}
			if !removed {
				return nil, fmt.Errorf(
					"child workflow %s is not in the waiting list?",
					childWfID,
				)
			}
		}
	}

	if err := CurrentWaitChildWfs.Set(ctx, newWaitList); err != nil {
		return nil, err
	}

	if len(newWaitList) == 0 {
		return dex.ForceCompleteOnChannelsEmpty(
			nil,
			[]dex.ChannelDef{TaskQueue},
			dex.MovementOf(loopForNextMessageStep{}, nil),
		), nil
	}
	return dex.GoTo(loopForNextMessageStep{}, nil), nil
}

var (
	_ dex.Flow                           = (*ParentFlow)(nil)
	_ dex.RPC[BatchEnqueueRequest, bool] = (*ParentFlow)(nil).Enqueue
	_ dex.RPC[string, dex.None]          = (*ParentFlow)(nil).CompleteChildWorkflow
)

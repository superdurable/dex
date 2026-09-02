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

package parallelsubflows

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	SubFlowCompletedCh = dex.DefineChannel[bool]("SubFlowCompletedCh")
	AllDoneCh          = dex.DefineChannel[bool]("AllDoneCh")
)

type WaitForHalfParentFlow struct {
	dex.FlowDefaults
	getClient   func() *dex.Client
	exampleFlow *ExampleSubFlow
}

func NewWaitForHalfParentFlow(
	getClient func() *dex.Client,
	exampleFlow *ExampleSubFlow,
) *WaitForHalfParentFlow {
	return &WaitForHalfParentFlow{getClient: getClient, exampleFlow: exampleFlow}
}

func (flow *WaitForHalfParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(waitForHalfInitStep{}),
		dex.DefineStep(subFlowStep{getClient: flow.getClient, exampleFlow: flow.exampleFlow}),
		dex.DefineStep(waitSubFlowsStep{}),
	}
}

func (*WaitForHalfParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{SubFlowCompletedCh, AllDoneCh}}
}

type waitForHalfInitStep struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (waitForHalfInitStep) GetStepType() string { return "InitStep" }

func (waitForHalfInitStep) Execute(_ dex.Context, requests []string) (*dex.StepDecision, error) {
	if len(requests) == 0 {
		return dex.GracefulComplete(nil), nil
	}
	movements := make([]dex.StepMovement, 0, len(requests)+1)
	movements = append(movements, dex.MovementOf(waitSubFlowsStep{}, len(requests)))
	for _, request := range requests {
		movements = append(movements, dex.MovementOf(subFlowStep{}, request))
	}
	return dex.GoToMany(movements...), nil
}

type subFlowStep struct {
	dex.StepDefaults
	getClient   func() *dex.Client
	exampleFlow *ExampleSubFlow
}

func (subFlowStep) GetStepType() string { return "SubFlowStep" }

func (step subFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.AnyOf(dex.SubFlow(step.exampleFlow, request), AllDoneCh.ForOne()), nil
}

func (step subFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	if result.Status != dex.FlowRunning {
		if err := SubFlowCompletedCh.Publish(ctx, true); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(nil), nil
	}
	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	flowID, err := dex.SubFlowID(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.StopFlow(context.Background(), flowID, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "enough SubFlows completed",
	}); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type waitSubFlowsStep struct{ dex.StepDefaults }

func (waitSubFlowsStep) GetStepType() string { return "WaitSubFlowsStep" }

func (waitSubFlowsStep) WaitFor(_ dex.Context, total int) (*dex.Wait, error) {
	return dex.Until(SubFlowCompletedCh.ForN((total + 1) / 2)), nil
}

func (waitSubFlowsStep) Execute(ctx dex.Context, total int) (*dex.StepDecision, error) {
	remaining := total - (total+1)/2
	for index := 0; index < remaining; index++ {
		if err := AllDoneCh.Publish(ctx, true); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*WaitForHalfParentFlow)(nil)

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
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	SubFlowCompletedCh = dex.DefineChannel[bool]("SubFlowCompletedCh")
	AllDoneCh          = dex.DefineChannel[bool]("AllDoneCh")
	ChildStopOutcomes  = dex.DefineAttributeMap[ChildStopOutcome]("ChildStopOutcomes")
)

type ChildStopOutcome struct {
	Status string  `json:"status"`
	Result *string `json:"result,omitempty"`
}

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
		dex.DefineStartStep(waitForHalfInit{}),
		dex.DefineStep(subFlow{getClient: flow.getClient, exampleFlow: flow.exampleFlow}),
		dex.DefineStep(waitSubFlows{}),
	}
}

func (*WaitForHalfParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{ChildStopOutcomes},
		Channels:   []dex.ChannelDef{SubFlowCompletedCh, AllDoneCh},
	}
}

type waitForHalfInit struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (waitForHalfInit) GetStepType() string { return "Init" }

func (waitForHalfInit) Execute(_ dex.Context, requests []string) (*dex.StepDecision, error) {
	if len(requests) == 0 {
		return dex.GracefulComplete(nil), nil
	}
	movements := make([]dex.StepMovement, 0, len(requests)+1)
	movements = append(movements, dex.MovementOf(waitSubFlows{}, len(requests)))
	for _, request := range requests {
		movements = append(movements, dex.MovementOf(subFlow{}, request))
	}
	return dex.GoToMany(movements...), nil
}

type subFlow struct {
	dex.StepDefaults
	getClient   func() *dex.Client
	exampleFlow *ExampleSubFlow
}

func (subFlow) GetStepType() string { return "SubFlow" }

func (step subFlow) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.AnyOf(dex.SubFlow(step.exampleFlow, request), AllDoneCh.ForOne()), nil
}

func (step subFlow) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
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
	clientContext := context.Background()
	outcome := ChildStopOutcome{Status: "stopped"}
	if err := client.StopFlow(clientContext, flowID, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "enough SubFlows completed",
	}); err != nil {
		var notFound *dex.FlowNotFoundError
		var notActiveOrNotFound *dex.FlowNotActiveOrNotFoundError
		var closing *dex.FlowClosingError
		if !errors.As(err, &notFound) && !errors.As(err, &notActiveOrNotFound) && !errors.As(err, &closing) {
			return nil, err
		}
		outcome, err = readChildStopOutcome(clientContext, client, flowID)
		if err != nil {
			return nil, err
		}
	}
	if err := ChildStopOutcomes.Set(ctx, flowID, outcome); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

func readChildStopOutcome(ctx context.Context, client *dex.Client, flowID string) (ChildStopOutcome, error) {
	info, err := client.DescribeFlow(ctx, flowID)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) || (err == nil && info.Status == dex.FlowRunning) {
		return ChildStopOutcome{Status: "unknown"}, nil
	}
	if err != nil {
		return ChildStopOutcome{}, err
	}
	result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		return ChildStopOutcome{}, err
	}
	outcome := ChildStopOutcome{Status: result.Status.String()}
	if len(result.Completions) > 0 {
		var output string
		if err := result.DecodeSingleOutput(&output); err != nil {
			return ChildStopOutcome{}, err
		}
		outcome.Result = &output
	}
	return outcome, nil
}

type waitSubFlows struct{ dex.StepDefaults }

func (waitSubFlows) GetStepType() string { return "WaitSubFlows" }

func (waitSubFlows) WaitFor(_ dex.Context, total int) (*dex.Wait, error) {
	return dex.Until(SubFlowCompletedCh.ForN((total + 1) / 2)), nil
}

func (waitSubFlows) Execute(ctx dex.Context, total int) (*dex.StepDecision, error) {
	remaining := total - (total+1)/2
	for index := 0; index < remaining; index++ {
		if err := AllDoneCh.Publish(ctx, true); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*WaitForHalfParentFlow)(nil)

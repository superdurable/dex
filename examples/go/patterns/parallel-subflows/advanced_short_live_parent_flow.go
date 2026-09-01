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

import "github.com/superdurable/dex/sdk-go/dex"

var (
	RequestChannel = dex.DefineChannel[string]("RequestChannel")
	CurrSubFlowNum = dex.DefineAttribute[int]("CurrSubFlowNum")
)

type AdvancedShortLiveParentFlow struct {
	dex.FlowDefaults
	exampleFlow *ExampleSubFlow
}

func NewAdvancedShortLiveParentFlow(exampleFlow *ExampleSubFlow) *AdvancedShortLiveParentFlow {
	return &AdvancedShortLiveParentFlow{exampleFlow: exampleFlow}
}

func (flow *AdvancedShortLiveParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(shortLiveInitStep{}),
		dex.DefineStep(shortLiveHandleRequestStep{}),
		dex.DefineStep(shortLiveHandleSubFlowStep{exampleFlow: flow.exampleFlow}),
	}
}

func (*AdvancedShortLiveParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{CurrSubFlowNum},
		Channels:   []dex.ChannelDef{RequestChannel},
	}
}

func (*AdvancedShortLiveParentFlow) SendRequest(ctx dex.Context, request string) (*dex.RPCResult[bool], error) {
	if RequestChannel.Size(ctx) >= MaxBufferedRequests {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := RequestChannel.Publish(ctx, request); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

type shortLiveInitStep struct {
	dex.StepDefaultsNoWaitFor[ParentInput]
}

func (shortLiveInitStep) GetStepType() string { return "InitStep" }

func (shortLiveInitStep) Execute(ctx dex.Context, input ParentInput) (*dex.StepDecision, error) {
	for _, request := range input.Requests {
		if err := RequestChannel.Publish(ctx, request); err != nil {
			return nil, err
		}
	}
	if err := CurrSubFlowNum.Set(ctx, 0); err != nil {
		return nil, err
	}
	count := concurrency(input.Concurrency)
	movements := make([]dex.StepMovement, 0, count)
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(shortLiveHandleRequestStep{}, nil))
	}
	return dex.GoToMany(movements...), nil
}

type shortLiveHandleRequestStep struct{ dex.StepDefaults }

func (shortLiveHandleRequestStep) GetStepType() string { return "HandleRequestStep" }

func (shortLiveHandleRequestStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(CurrSubFlowNum)}}
}

func (shortLiveHandleRequestStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(RequestChannel.ForOne()), nil
}

func (shortLiveHandleRequestStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	requests, err := RequestChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	current, err := CurrSubFlowNum.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := CurrSubFlowNum.Set(ctx, current+1); err != nil {
		return nil, err
	}
	return dex.GoTo(shortLiveHandleSubFlowStep{}, requests[0]), nil
}

type shortLiveHandleSubFlowStep struct {
	dex.StepDefaults
	exampleFlow *ExampleSubFlow
}

func (shortLiveHandleSubFlowStep) GetStepType() string { return "HandleSubFlowStep" }

func (shortLiveHandleSubFlowStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(CurrSubFlowNum)}}
}

func (step shortLiveHandleSubFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.exampleFlow, request)), nil
}

func (shortLiveHandleSubFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	current, err := CurrSubFlowNum.Get(ctx)
	if err != nil {
		return nil, err
	}
	current--
	if err := CurrSubFlowNum.Set(ctx, current); err != nil {
		return nil, err
	}
	if current == 0 {
		return dex.ForceCompleteIfChannelsEmpty(
			nil,
			[]dex.ChannelDef{RequestChannel},
			dex.MovementOf(shortLiveHandleRequestStep{}, nil),
		), nil
	}
	return dex.GoTo(shortLiveHandleRequestStep{}, nil), nil
}

var (
	_ dex.Flow              = (*AdvancedShortLiveParentFlow)(nil)
	_ dex.RPC[string, bool] = (*AdvancedShortLiveParentFlow)(nil).SendRequest
)

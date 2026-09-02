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
	LongLiveRequestChannel = dex.DefineChannel[string]("RequestChannel")
	Stopped                = dex.DefineAttribute[bool]("Stopped")
)

type AdvancedLongLiveParentFlow struct {
	dex.FlowDefaults
	exampleFlow *ExampleSubFlow
}

func NewAdvancedLongLiveParentFlow(exampleFlow *ExampleSubFlow) *AdvancedLongLiveParentFlow {
	return &AdvancedLongLiveParentFlow{exampleFlow: exampleFlow}
}

func (flow *AdvancedLongLiveParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(longLiveInitStep{}),
		dex.DefineStep(longLiveHandleRequestStep{}),
		dex.DefineStep(longLiveHandleSubFlowStep{exampleFlow: flow.exampleFlow}),
	}
}

func (*AdvancedLongLiveParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Stopped},
		Channels:   []dex.ChannelDef{LongLiveRequestChannel},
	}
}

func (*AdvancedLongLiveParentFlow) SendRequest(ctx dex.Context, request string) (*dex.RPCResult[bool], error) {
	if LongLiveRequestChannel.Size(ctx) >= MaxBufferedRequests {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := LongLiveRequestChannel.Publish(ctx, request); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*AdvancedLongLiveParentFlow) Stop(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Stopped.Set(ctx, true); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type longLiveInitStep struct {
	dex.StepDefaultsNoWaitFor[ParentInput]
}

func (longLiveInitStep) GetStepType() string { return "InitStep" }

func (longLiveInitStep) Execute(ctx dex.Context, input ParentInput) (*dex.StepDecision, error) {
	for _, request := range input.Requests {
		if err := LongLiveRequestChannel.Publish(ctx, request); err != nil {
			return nil, err
		}
	}
	if err := Stopped.Set(ctx, false); err != nil {
		return nil, err
	}
	count := concurrency(input.Concurrency)
	movements := make([]dex.StepMovement, 0, count)
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(longLiveHandleRequestStep{}, nil))
	}
	return dex.GoToMany(movements...), nil
}

type longLiveHandleRequestStep struct{ dex.StepDefaults }

func (longLiveHandleRequestStep) GetStepType() string { return "HandleRequestStep" }

func (longLiveHandleRequestStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(LongLiveRequestChannel.ForOne()), nil
}

func (longLiveHandleRequestStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	requests, err := LongLiveRequestChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(longLiveHandleSubFlowStep{}, requests[0]), nil
}

type longLiveHandleSubFlowStep struct {
	dex.StepDefaults
	exampleFlow *ExampleSubFlow
}

func (longLiveHandleSubFlowStep) GetStepType() string { return "HandleSubFlowStep" }

func (step longLiveHandleSubFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.exampleFlow, request)), nil
}

func (longLiveHandleSubFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	stopped, err := Stopped.Get(ctx)
	if err != nil {
		return nil, err
	}
	if stopped {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(longLiveHandleRequestStep{}, nil), nil
}

var (
	_ dex.Flow                    = (*AdvancedLongLiveParentFlow)(nil)
	_ dex.RPC[string, bool]       = (*AdvancedLongLiveParentFlow)(nil).SendRequest
	_ dex.RPC[dex.None, dex.None] = (*AdvancedLongLiveParentFlow)(nil).Stop
)

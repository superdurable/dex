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

package microservices

import (
	"time"

	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	Data  = dex.DefineAttribute[string]("data")
	Ready = dex.DefineChannel[dex.None]("Ready")
)

type OrchestrationFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewOrchestrationFlow(applicationService service.MyService) *OrchestrationFlow {
	return &OrchestrationFlow{service: applicationService}
}

func (flow *OrchestrationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(callAPI1Step{service: flow.service}),
		dex.DefineStep(callAPI2Step{service: flow.service}),
		dex.DefineStep(callAPI3Step{service: flow.service}),
		dex.DefineStep(callAPI4Step{service: flow.service}),
	}
}

func (*OrchestrationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Data},
		Channels:   []dex.ChannelDef{Ready},
	}
}

func (*OrchestrationFlow) Swap(
	ctx dex.Context,
	newData string,
) (dex.RPCResult[string], error) {
	oldData, err := Data.Get(ctx)
	if err != nil {
		return dex.RPCResult[string]{}, err
	}
	if err := Data.Set(ctx, newData); err != nil {
		return dex.RPCResult[string]{}, err
	}
	return dex.RPCResult[string]{Output: oldData}, nil
}

type callAPI1Step struct {
	dex.StepDefaultsNoWaitFor[string]
	service service.MyService
}

func (step callAPI1Step) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	step.service.CallAPI1(input)
	if err := Data.Set(ctx, input); err != nil {
		return nil, err
	}
	return dex.GoToMulti(
		dex.MovementOf(callAPI2Step{}, nil),
		dex.MovementOf(callAPI3Step{}, nil),
	), nil
}

type callAPI2Step struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	service service.MyService
}

func (step callAPI2Step) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	data, err := Data.Get(ctx)
	if err != nil {
		return nil, err
	}
	step.service.CallAPI2(data)
	return dex.DeadEnd(), nil
}

type callAPI3Step struct {
	dex.StepDefaults
	service service.MyService
}

func (callAPI3Step) WaitFor(
	dex.Context,
	dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(24*time.Hour),
		Ready.ForOne(),
	), nil
}

func (step callAPI3Step) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	data, err := Data.Get(ctx)
	if err != nil {
		return nil, err
	}
	step.service.CallAPI3(data)
	if ctx.HasTimerFired() {
		return dex.GoTo(callAPI4Step{}, nil), nil
	}
	return dex.GracefulComplete(data), nil
}

type callAPI4Step struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	service service.MyService
}

func (step callAPI4Step) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	data, err := Data.Get(ctx)
	if err != nil {
		return nil, err
	}
	step.service.CallAPI4(data)
	return dex.GracefulComplete(data), nil
}

var (
	_ dex.Flow                = (*OrchestrationFlow)(nil)
	_ dex.RPC[string, string] = (*OrchestrationFlow)(nil).Swap
)

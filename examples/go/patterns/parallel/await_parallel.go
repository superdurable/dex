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

package parallel

import "github.com/superdurable/dex/sdk-go/dex"

var CompleteCh = dex.DefineChannel[dex.None]("parallel-complete")

type AwaitParallelStepsFlow struct{ dex.FlowDefaults }

func NewAwaitParallelStepsFlow() *AwaitParallelStepsFlow {
	return &AwaitParallelStepsFlow{}
}

func (*AwaitParallelStepsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitInitStep{}),
		dex.DefineStep(awaitWorkStep{}),
		dex.DefineStep(awaitStep{}),
	}
}

func (*AwaitParallelStepsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{CompleteCh}}
}

type awaitInitStep struct{ dex.StepDefaultsNoWaitFor[int] }

func (awaitInitStep) GetStepType() string { return "InitStep" }

func (awaitInitStep) Execute(_ dex.Context, count int) (*dex.StepDecision, error) {
	movements := []dex.StepMovement{dex.MovementOf(awaitStep{}, count)}
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(awaitWorkStep{}, index))
	}
	return dex.GoToMulti(movements...), nil
}

type awaitWorkStep struct{ dex.StepDefaultsNoWaitFor[int] }

func (awaitWorkStep) GetStepType() string { return "DoWorkStep" }

func (awaitWorkStep) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	randomWorkDelay()
	if err := CompleteCh.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type awaitStep struct{ dex.StepDefaults }

func (awaitStep) GetStepType() string { return "AwaitStep" }

func (awaitStep) WaitFor(_ dex.Context, count int) (*dex.Wait, error) {
	return dex.Until(CompleteCh.ForN(count)), nil
}

func (awaitStep) Execute(_ dex.Context, count int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(count), nil
}

var _ dex.Flow = (*AwaitParallelStepsFlow)(nil)

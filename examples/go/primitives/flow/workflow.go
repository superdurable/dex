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

package flow

import "github.com/superdurable/dex/sdk-go/dex"

var (
	Status = dex.DefineAttribute[string]("status")
	Notify = dex.DefineChannel[dex.None]("notify")
)

type ExampleFlow struct {
	dex.FlowDefaults
}

func NewExampleFlow() *ExampleFlow {
	return &ExampleFlow{}
}

func (*ExampleFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(ExampleStep{}),
		dex.DefineStep(FinishStep{}),
	}
}

func (*ExampleFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Status},
		Channels:   []dex.ChannelDef{Notify},
	}
}

func (*ExampleFlow) Describe(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	value, err := Status.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: value}, nil
}

func (*ExampleFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	if err := Status.Set(ctx, "timed out"); err != nil {
		return nil, err
	}
	return dex.ForceFail("processing deadline reached"), nil
}

type ExampleStep struct {
	dex.StepDefaults
}

func (ExampleStep) WaitFor(ctx dex.Context, _ int) (*dex.Wait, error) {
	if err := Status.Set(ctx, "running"); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (ExampleStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GoTo(FinishStep{}, input+1), nil
}

type FinishStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (FinishStep) Execute(ctx dex.Context, input int) (*dex.StepDecision, error) {
	if err := Status.Set(ctx, "done"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(input + 1), nil
}

var _ dex.Flow = (*ExampleFlow)(nil)

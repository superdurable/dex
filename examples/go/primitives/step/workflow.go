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

package step

import "github.com/superdurable/dex/sdk-go/dex"

type StepFlow struct {
	dex.FlowDefaults
}

func NewStepFlow() *StepFlow {
	return &StepFlow{}
}

func (*StepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(stepFirstStep{}),
		dex.DefineStep(stepSecondStep{}),
	}
}

func (*StepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type stepFirstStep struct {
	dex.StepDefaults
}

func (stepFirstStep) WaitFor(ctx dex.Context, input int) (*dex.Wait, error) {
	if err := ctx.SetStepExecutionLocal("input", input); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (stepFirstStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GoTo(stepSecondStep{}, input+1), nil
}

type stepSecondStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (stepSecondStep) Execute(_ dex.Context, input int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input + 1), nil
}

var _ dex.Flow = (*StepFlow)(nil)

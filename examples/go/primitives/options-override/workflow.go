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

package optionsoverride

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type OptionsOverrideFlow struct {
	dex.FlowDefaults
}

func NewOptionsOverrideFlow() *OptionsOverrideFlow {
	return &OptionsOverrideFlow{}
}

func (*OptionsOverrideFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(overrideFirstStep{}),
		dex.DefineStep(overrideSecondStep{}),
	}
}

func (*OptionsOverrideFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type overrideFirstStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (overrideFirstStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	override := &dex.StepOptions{
		WaitForRetry:   &dex.RetryPolicy{MaximumAttempts: 2},
		WaitForFailure: dex.ProceedOnWaitForFailure,
	}
	payload := input + "_state1"
	return dex.GoTo(overrideSecondStep{}, payload, dex.WithStepOptions(override)), nil
}

type overrideSecondStep struct {
	dex.StepDefaults
}

func (overrideSecondStep) WaitFor(_ dex.Context, input string) (*dex.Wait, error) {
	_ = input
	return nil, fmt.Errorf("state 2 wait failure")
}

func (overrideSecondStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	if !ctx.WaitForMethodFailed() {
		return nil, fmt.Errorf("waitFor failure was not reported")
	}
	return dex.GracefulComplete(input + "_state2"), nil
}

func (overrideSecondStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		WaitForFailure: dex.FailFlowOnWaitForFailure,
	}
}

var _ dex.Flow = (*OptionsOverrideFlow)(nil)

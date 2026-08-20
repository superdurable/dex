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

package proceedonwaitfailure

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type ProceedOnWaitFailureFlow struct {
	dex.FlowDefaults
}

func NewProceedOnWaitFailureFlow() *ProceedOnWaitFailureFlow {
	return &ProceedOnWaitFailureFlow{}
}

func (*ProceedOnWaitFailureFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(failingWaitStep{}),
		dex.DefineStep(finishStep{}),
	}
}

func (*ProceedOnWaitFailureFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type failingWaitStep struct {
	dex.StepDefaults
}

func (failingWaitStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry:   &dex.RetryPolicy{MaximumAttempts: 2},
		WaitForFailure: dex.ProceedOnWaitForFailure,
	}
}

func (failingWaitStep) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return nil, fmt.Errorf("planned WaitFor failure")
}

func (failingWaitStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	if !ctx.WaitForMethodFailed() {
		return nil, fmt.Errorf("waitFor failure was not reported")
	}
	return dex.GoTo(finishStep{}, input+"_recovered"), nil
}

type finishStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (finishStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

var _ dex.Flow = (*ProceedOnWaitFailureFlow)(nil)

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

package timeout

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type FlowGracefulTimeout struct {
	dex.FlowDefaults
}

func NewFlowGracefulTimeout() *FlowGracefulTimeout {
	return &FlowGracefulTimeout{}
}

func (*FlowGracefulTimeout) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(longWaitStep{}),
	}
}

func (*FlowGracefulTimeout) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type longWaitStep struct {
	dex.StepDefaults
}

func (longWaitStep) WaitFor(
	_ dex.Context,
	workflowSuccessful bool,
) (*dex.Wait, error) {
	if workflowSuccessful {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(dex.Timer(65 * time.Second)), nil
}

func (longWaitStep) Execute(
	_ dex.Context,
	_ bool,
) (*dex.StepDecision, error) {
	return dex.ForceComplete("Workflow completed successfully"), nil
}

func (*FlowGracefulTimeout) HandleTimeout(
	_ dex.Context,
) (*dex.StepDecision, error) {
	return dex.ForceFail("Workflow did not finish the task in time"), nil
}

var _ dex.Flow = (*FlowGracefulTimeout)(nil)
var _ dex.FlowTimeoutHandler = (*FlowGracefulTimeout)(nil)

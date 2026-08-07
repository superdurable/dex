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

package polling

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type SimplePollingFlow struct {
	dex.FlowDefaults
}

func NewSimplePollingFlow() *SimplePollingFlow {
	return &SimplePollingFlow{}
}

func (*SimplePollingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(simplePollingStep{}),
		dex.DefineStep(simplePollingCompleteStep{}),
	}
}

func (*SimplePollingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type simplePollingStep struct {
	dex.StepDefaults
}

func (simplePollingStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(10 * time.Second)), nil
}

func (simplePollingStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if isSystemReady() {
		return dex.GoTo(simplePollingCompleteStep{}, nil), nil
	}
	return dex.GoTo(simplePollingStep{}, nil), nil
}

func isSystemReady() bool {
	fmt.Println("Executing external system check for readiness...")
	return true
}

type simplePollingCompleteStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (simplePollingCompleteStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	fmt.Println("Executing final state to complete the workflow...")
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*SimplePollingFlow)(nil)

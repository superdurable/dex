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

package stepexecutionlocal

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

var Approval = dex.DefineChannel[string]("Approval")

type StepExecutionLocalFlow struct {
	dex.FlowDefaults
}

func NewStepExecutionLocalFlow() *StepExecutionLocalFlow {
	return &StepExecutionLocalFlow{}
}

func (*StepExecutionLocalFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(noteWaitStep{})}
}

func (*StepExecutionLocalFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Approval}}
}

type noteWaitStep struct {
	dex.StepDefaults
}

func (noteWaitStep) WaitFor(ctx dex.Context, input int) (*dex.Wait, error) {
	if err := ctx.SetStepExecutionLocal("note", fmt.Sprintf("approval:%d", input)); err != nil {
		return nil, err
	}
	return dex.Until(Approval.ForOne()), nil
}

func (noteWaitStep) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	var note string
	found, err := ctx.GetStepExecutionLocal("note", &note)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("note was not set")
	}
	return dex.GracefulComplete(note), nil
}

var _ dex.Flow = (*StepExecutionLocalFlow)(nil)

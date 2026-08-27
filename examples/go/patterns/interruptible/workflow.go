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

package interruptible

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const DAInterruptSignal = "interruptSignal"

var InterruptSignal = dex.DefineAttribute[string](DAInterruptSignal)

type WorkJobParametersInput struct {
	JobUpperBound int
	Progress      int
}

type InterruptibleFlow struct {
	dex.FlowDefaults
}

func NewInterruptibleFlow() *InterruptibleFlow {
	return &InterruptibleFlow{}
}

func (*InterruptibleFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(workAStep{}),
		dex.DefineStep(workBStep{}),
	}
}

func (*InterruptibleFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{InterruptSignal},
	}
}

func (*InterruptibleFlow) Interrupt(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := InterruptSignal.Set(ctx, "cancel"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (initStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	input := WorkJobParametersInput{JobUpperBound: 15, Progress: 1}
	return dex.GoToMulti(
		dex.MovementOf(workAStep{}, input),
		dex.MovementOf(workBStep{}, input),
	), nil
}

type workAStep struct {
	dex.StepDefaults
}

func (workAStep) WaitFor(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.Wait, error) {
	return dex.Until(dex.Timer(1500 * time.Millisecond)), nil
}

func (workAStep) Execute(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.StepDecision, error) {
	signal, err := InterruptSignal.Get(ctx)
	if err == nil && signal == "cancel" {
		fmt.Println("A: Interrupted!")
		return dex.GracefulComplete(nil), nil
	}
	if input.Progress > input.JobUpperBound {
		fmt.Println("WorkAStep completed")
		return dex.GracefulComplete(nil), nil
	}
	fmt.Printf(
		"[%s][%s]: Doing job %d\n",
		ctx.FlowID(),
		ctx.StepExecutionID(),
		input.Progress,
	)
	next := WorkJobParametersInput{
		JobUpperBound: input.JobUpperBound,
		Progress:      input.Progress + 1,
	}
	return dex.GoTo(workAStep{}, next), nil
}

type workBStep struct {
	dex.StepDefaults
}

func (workBStep) WaitFor(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.Wait, error) {
	return dex.Until(dex.Timer(3 * time.Second)), nil
}

func (workBStep) Execute(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.StepDecision, error) {
	signal, err := InterruptSignal.Get(ctx)
	if err == nil && signal == "cancel" {
		fmt.Println("B: Interrupted!")
		return dex.GracefulComplete(nil), nil
	}
	if input.Progress > input.JobUpperBound {
		fmt.Println("WorkBStep completed")
		return dex.GracefulComplete(nil), nil
	}
	fmt.Printf(
		"[%s][%s]: Processing job %d\n",
		ctx.FlowID(),
		ctx.StepExecutionID(),
		input.Progress,
	)
	next := WorkJobParametersInput{
		JobUpperBound: input.JobUpperBound,
		Progress:      input.Progress + 1,
	}
	return dex.GoTo(workBStep{}, next), nil
}

var (
	_ dex.Flow                    = (*InterruptibleFlow)(nil)
	_ dex.RPC[dex.None, dex.None] = (*InterruptibleFlow)(nil).Interrupt
)

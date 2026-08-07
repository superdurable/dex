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

type InterruptibleExecutionFlow struct {
	dex.FlowDefaults
}

func NewInterruptibleExecutionFlow() *InterruptibleExecutionFlow {
	return &InterruptibleExecutionFlow{}
}

func (*InterruptibleExecutionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(workAExecutionStep{}),
		dex.DefineStep(workNExecutionStep{}),
	}
}

func (*InterruptibleExecutionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{InterruptSignal},
	}
}

func (*InterruptibleExecutionFlow) Interrupt(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[dex.None], error) {
	if err := InterruptSignal.Set(ctx, "cancel"); err != nil {
		return dex.RPCResult[dex.None]{}, err
	}
	return dex.RPCResult[dex.None]{}, nil
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
		dex.MovementOf(workAExecutionStep{}, input),
		dex.MovementOf(workNExecutionStep{}, input),
	), nil
}

type workAExecutionStep struct {
	dex.StepDefaults
}

func (workAExecutionStep) WaitFor(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(1500 * time.Millisecond)), nil
}

func (workAExecutionStep) Execute(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.StepDecision, error) {
	signal, err := InterruptSignal.Get(ctx)
	if err == nil && signal == "cancel" {
		fmt.Println("A: Interrupted!")
		return dex.GracefulComplete(nil), nil
	}
	if input.Progress > input.JobUpperBound {
		fmt.Println("Executing WorkAExecution completed")
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
	return dex.GoTo(workAExecutionStep{}, next), nil
}

type workNExecutionStep struct {
	dex.StepDefaults
}

func (workNExecutionStep) WaitFor(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(3 * time.Second)), nil
}

func (workNExecutionStep) Execute(
	ctx dex.Context,
	input WorkJobParametersInput,
) (*dex.StepDecision, error) {
	signal, err := InterruptSignal.Get(ctx)
	if err == nil && signal == "cancel" {
		fmt.Println("N: Interrupted!")
		return dex.GracefulComplete(nil), nil
	}
	if input.Progress > input.JobUpperBound {
		fmt.Println("Executing WorkNExecution completed")
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
	return dex.GoTo(workNExecutionStep{}, next), nil
}

var (
	_ dex.Flow                    = (*InterruptibleExecutionFlow)(nil)
	_ dex.RPC[dex.None, dex.None] = (*InterruptibleExecutionFlow)(nil).Interrupt
)

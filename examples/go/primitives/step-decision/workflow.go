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

package stepdecision

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type StepDecisionFlow struct {
	dex.FlowDefaults
}

func NewStepDecisionFlow() *StepDecisionFlow {
	return &StepDecisionFlow{}
}

func (*StepDecisionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(routeStep{}),
		dex.DefineStep(carrierAStep{}),
		dex.DefineStep(carrierBStep{}),
		dex.DefineStep(winnerStep{}),
		dex.DefineStep(recordQuoteStep{}),
		dex.DefineStep(branchWorkerStep{}),
	}
}

func (*StepDecisionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type StepDecisionInput struct {
	Mode string
}

type routeStep struct {
	dex.StepDefaultsNoWaitFor[StepDecisionInput]
}

func (routeStep) Execute(_ dex.Context, input StepDecisionInput) (*dex.StepDecision, error) {
	switch input.Mode {
	case "graceful":
		return dex.GracefulComplete("done"), nil
	case "dead-end":
		return dex.GoToMulti(
			dex.MovementOf(branchWorkerStep{}, "left"),
			dex.MovementOf(branchWorkerStep{}, "right"),
		), nil
	default:
		quote := Quote{Carrier: "winner", Price: 9}
		return dex.GoToMulti(
			dex.MovementOf(carrierAStep{}, Quote{Carrier: "A", Price: 10}),
			dex.MovementOf(carrierBStep{}, Quote{Carrier: "B", Price: 12}),
			dex.MovementOf(winnerStep{}, quote),
		), nil
	}
}

type branchWorkerStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (branchWorkerStep) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type Quote struct {
	Carrier string
	Price   int
}

type carrierAStep struct {
	dex.StepDefaults
}

func (carrierAStep) WaitFor(_ dex.Context, _ Quote) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (carrierAStep) Execute(_ dex.Context, _ Quote) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type carrierBStep struct {
	dex.StepDefaults
}

func (carrierBStep) WaitFor(_ dex.Context, _ Quote) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (carrierBStep) Execute(_ dex.Context, _ Quote) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type winnerStep struct {
	dex.StepDefaultsNoWaitFor[Quote]
}

func (winnerStep) Execute(_ dex.Context, quote Quote) (*dex.StepDecision, error) {
	return dex.GoTo(recordQuoteStep{}, quote).
		CancelSteps(carrierAStep{}, carrierBStep{}), nil
}

type recordQuoteStep struct {
	dex.StepDefaultsNoWaitFor[Quote]
}

func (recordQuoteStep) Execute(_ dex.Context, quote Quote) (*dex.StepDecision, error) {
	return dex.GracefulComplete(quote), nil
}

var _ dex.Flow = (*StepDecisionFlow)(nil)

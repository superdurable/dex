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
		dex.DefineStartStep(route{}),
		dex.DefineStep(carrierA{}),
		dex.DefineStep(carrierB{}),
		dex.DefineStep(winnerStep{}),
		dex.DefineStep(recordQuote{}),
		dex.DefineStep(branchWorker{}),
	}
}

func (*StepDecisionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type StepDecisionInput struct {
	Mode string
}

type route struct {
	dex.StepDefaultsNoWaitFor[StepDecisionInput]
}

func (route) Execute(_ dex.Context, input StepDecisionInput) (*dex.StepDecision, error) {
	switch input.Mode {
	case "graceful":
		return dex.GracefulComplete("done"), nil
	case "dead-end":
		return dex.GoToMany(
			dex.MovementOf(branchWorker{}, "left"),
			dex.MovementOf(branchWorker{}, "right"),
		), nil
	default:
		quote := Quote{Carrier: "winner", Price: 9}
		return dex.GoToMany(
			dex.MovementOf(carrierA{}, Quote{Carrier: "A", Price: 10}),
			dex.MovementOf(carrierB{}, Quote{Carrier: "B", Price: 12}),
			dex.MovementOf(winnerStep{}, quote),
		), nil
	}
}

type branchWorker struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (branchWorker) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type Quote struct {
	Carrier string
	Price   int
}

type carrierA struct {
	dex.StepDefaults
}

func (carrierA) WaitFor(_ dex.Context, _ Quote) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (carrierA) Execute(_ dex.Context, _ Quote) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type carrierB struct {
	dex.StepDefaults
}

func (carrierB) WaitFor(_ dex.Context, _ Quote) (*dex.Wait, error) {
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (carrierB) Execute(_ dex.Context, _ Quote) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type winnerStep struct {
	dex.StepDefaultsNoWaitFor[Quote]
}

func (winnerStep) Execute(_ dex.Context, quote Quote) (*dex.StepDecision, error) {
	return dex.GoTo(recordQuote{}, quote).
		CancelSteps(carrierA{}, carrierB{}), nil
}

type recordQuote struct {
	dex.StepDefaultsNoWaitFor[Quote]
}

func (recordQuote) Execute(_ dex.Context, quote Quote) (*dex.StepDecision, error) {
	return dex.GracefulComplete(quote), nil
}

var _ dex.Flow = (*StepDecisionFlow)(nil)

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

package durability

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type DurabilityFlow struct {
	dex.FlowDefaults
}

func NewDurabilityFlow() *DurabilityFlow {
	return &DurabilityFlow{}
}

func (*DurabilityFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(routeDurabilityStep{}),
		dex.DefineStep(syncWorkStep{}),
		dex.DefineStep(asyncWorkStep{}),
		dex.DefineStep(finishDurabilityStep{}),
	}
}

func (*DurabilityFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type routeDurabilityStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (routeDurabilityStep) Execute(_ dex.Context, mode string) (*dex.StepDecision, error) {
	if mode == "async" {
		return dex.GoTo(asyncWorkStep{}, mode), nil
	}
	return dex.GoTo(syncWorkStep{}, mode), nil
}

type syncWorkStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (syncWorkStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync}
}

func (syncWorkStep) Execute(_ dex.Context, mode string) (*dex.StepDecision, error) {
	return dex.GoTo(finishDurabilityStep{}, "sync:"+mode), nil
}

type asyncWorkStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (asyncWorkStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilityAsync}
}

func (asyncWorkStep) Execute(_ dex.Context, mode string) (*dex.StepDecision, error) {
	return dex.GoTo(finishDurabilityStep{}, "async:"+mode), nil
}

type finishDurabilityStep struct {
	dex.StepDefaults
}

func (finishDurabilityStep) WaitFor(_ dex.Context, label string) (*dex.Wait, error) {
	_ = label
	return dex.AnyOf(dex.Timer(1 * time.Second)), nil
}

func (finishDurabilityStep) Execute(_ dex.Context, label string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(label), nil
}

var _ dex.Flow = (*DurabilityFlow)(nil)

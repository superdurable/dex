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

package parallel

import (
	"math/rand"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type DynamicParallelStepsFlow struct{ dex.FlowDefaults }

func NewDynamicParallelStepsFlow() *DynamicParallelStepsFlow { return &DynamicParallelStepsFlow{} }

func (*DynamicParallelStepsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(dynamicInitStep{}), dex.DefineStep(dynamicWorkStep{})}
}

func (*DynamicParallelStepsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type dynamicInitStep struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (dynamicInitStep) GetStepType() string { return "InitStep" }

func (dynamicInitStep) Execute(_ dex.Context, items []string) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, len(items))
	for _, item := range items {
		movements = append(movements, dex.MovementOf(dynamicWorkStep{}, item))
	}
	return dex.GoToMany(movements...), nil
}

type dynamicWorkStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (dynamicWorkStep) GetStepType() string { return "DoWorkStep" }

func (dynamicWorkStep) Execute(_ dex.Context, item string) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(50+rand.Intn(450)) * time.Millisecond)
	return dex.GracefulComplete(item), nil
}

var _ dex.Flow = (*DynamicParallelStepsFlow)(nil)

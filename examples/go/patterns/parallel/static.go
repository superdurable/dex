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
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type StaticParallelStepsFlow struct{ dex.FlowDefaults }

func NewStaticParallelStepsFlow() *StaticParallelStepsFlow { return &StaticParallelStepsFlow{} }

func (*StaticParallelStepsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(staticInitStep{}), dex.DefineStep(workAStep{}), dex.DefineStep(workBStep{})}
}

func (*StaticParallelStepsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type staticInitStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (staticInitStep) GetStepType() string { return "InitStep" }

func (staticInitStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GoToMany(dex.MovementOf(workAStep{}, input), dex.MovementOf(workBStep{}, input)), nil
}

type workAStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (workAStep) GetStepType() string { return "WorkAStep" }

func (workAStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(fmt.Sprintf("A:%s", input)), nil
}

type workBStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (workBStep) GetStepType() string { return "WorkBStep" }

func (workBStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(fmt.Sprintf("B:%s", input)), nil
}

var _ dex.Flow = (*StaticParallelStepsFlow)(nil)

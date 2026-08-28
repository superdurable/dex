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

type FirstWinParallelStepsFlow struct{ dex.FlowDefaults }

func NewFirstWinParallelStepsFlow() *FirstWinParallelStepsFlow { return &FirstWinParallelStepsFlow{} }

func (*FirstWinParallelStepsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(firstWinInitStep{}), dex.DefineStep(firstWinWorkStep{})}
}

func (*FirstWinParallelStepsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type firstWinInitStep struct{ dex.StepDefaultsNoWaitFor[int] }

func (firstWinInitStep) GetStepType() string { return "InitStep" }

func (firstWinInitStep) Execute(_ dex.Context, count int) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, count)
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(firstWinWorkStep{}, index))
	}
	return dex.GoToMulti(movements...), nil
}

type firstWinWorkStep struct{ dex.StepDefaultsNoWaitFor[int] }

func (firstWinWorkStep) GetStepType() string { return "DoWorkStep" }

func (firstWinWorkStep) Execute(_ dex.Context, index int) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(50+rand.Intn(450)) * time.Millisecond)
	return dex.GracefulComplete(index).CancelSiblingSteps(firstWinWorkStep{}), nil
}

var _ dex.Flow = (*FirstWinParallelStepsFlow)(nil)

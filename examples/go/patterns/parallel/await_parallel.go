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

var WorkCompletedJoin = dex.DefineJoin[int]("parallel-complete")

type AwaitParallelStepsFlow struct{ dex.FlowDefaults }

func NewAwaitParallelStepsFlow() *AwaitParallelStepsFlow {
	return &AwaitParallelStepsFlow{}
}

func (*AwaitParallelStepsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitInit{}),
		dex.DefineStep(awaitWork{}),
		dex.DefineStep(awaitStep{}),
	}
}

func (*AwaitParallelStepsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{WorkCompletedJoin}}
}

type awaitInit struct{ dex.StepDefaultsNoWaitFor[int] }

func (awaitInit) GetStepType() string { return "Init" }

func (awaitInit) Execute(ctx dex.Context, count int) (*dex.StepDecision, error) {
	branchBuilders := make([]func(dex.JoinRound) dex.StepMovement, 0, count)
	for index := range count {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(awaitWork{}, awaitWorkInput{Index: index, Round: round})
		})
	}
	movements, err := WorkCompletedJoin.Fork(ctx, branchBuilders, func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(awaitStep{}, round)
	})
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type awaitWorkInput struct {
	Index int           `json:"index"`
	Round dex.JoinRound `json:"round"`
}

type awaitWork struct {
	dex.StepDefaultsNoWaitFor[awaitWorkInput]
}

func (awaitWork) GetStepType() string { return "DoWork" }

func (awaitWork) Execute(ctx dex.Context, input awaitWorkInput) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(50+rand.Intn(450)) * time.Millisecond)
	return WorkCompletedJoin.EndBranch(ctx, input.Round, input.Index)
}

type awaitStep struct{ dex.StepDefaults }

func (awaitStep) GetStepType() string { return "Await" }

func (awaitStep) WaitFor(_ dex.Context, round dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(WorkCompletedJoin.ForAllBranches(round)), nil
}

func (awaitStep) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	completedIndexes, err := WorkCompletedJoin.BranchResults(ctx, round)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(len(completedIndexes)), nil
}

var _ dex.Flow = (*AwaitParallelStepsFlow)(nil)

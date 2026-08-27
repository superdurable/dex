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

package cron

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	Trigger = dex.DefineChannel[dex.None]("cron-schedule-trigger")
	Skip    = dex.DefineChannel[dex.None]("cron-schedule-skip")
)

type IntervalUnit string

const (
	Minute IntervalUnit = "minute"
	Hour   IntervalUnit = "hour"
	Day    IntervalUnit = "day"
)

type Interval struct {
	Value int
	Unit  IntervalUnit
}

func (interval Interval) Duration() time.Duration {
	switch interval.Unit {
	case Minute:
		return time.Duration(interval.Value) * time.Minute
	case Hour:
		return time.Duration(interval.Value) * time.Hour
	case Day:
		return time.Duration(interval.Value) * 24 * time.Hour
	default:
		return 0
	}
}

type CronScheduleInput struct {
	Interval Interval
	RunCount int
}

type cronScheduleState struct {
	Interval      Interval
	RemainingRuns int
}

type cronScheduleRun struct {
	RunNumber int
	IsFinal   bool
}

type CronScheduleFlow struct {
	dex.FlowDefaults
}

func NewCronScheduleFlow() *CronScheduleFlow {
	return &CronScheduleFlow{}
}

func (*CronScheduleFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(startCronSchedule{}),
		dex.DefineStep(waitForCronSchedule{}),
		dex.DefineStep(runCronSchedule{}),
	}
}

func (*CronScheduleFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Trigger, Skip}}
}

type startCronSchedule struct {
	dex.StepDefaultsNoWaitFor[CronScheduleInput]
}

func (startCronSchedule) Execute(
	_ dex.Context,
	input CronScheduleInput,
) (*dex.StepDecision, error) {
	if input.RunCount <= 0 || input.Interval.Value <= 0 || input.Interval.Duration() <= 0 {
		return dex.ForceFail("interval value and run count must be positive"), nil
	}
	return dex.GoTo(waitForCronSchedule{}, cronScheduleState{
		Interval:      input.Interval,
		RemainingRuns: input.RunCount,
	}), nil
}

type waitForCronSchedule struct {
	dex.StepDefaults
}

func (waitForCronSchedule) WaitFor(
	_ dex.Context,
	state cronScheduleState,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(state.Interval.Duration()),
		Trigger.ForOne(),
		Skip.ForOne(),
	), nil
}

func (waitForCronSchedule) Execute(
	ctx dex.Context,
	state cronScheduleState,
) (*dex.StepDecision, error) {
	skipResults, err := Skip.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(skipResults) > 0 {
		return nextCronSchedule(state), nil
	}
	run := cronScheduleRun{
		RunNumber: state.RemainingRuns,
		IsFinal:   state.RemainingRuns == 1,
	}
	if run.IsFinal {
		return dex.GoTo(runCronSchedule{}, run), nil
	}
	state.RemainingRuns--
	return dex.GoToMulti(
		dex.MovementOf(runCronSchedule{}, run),
		dex.MovementOf(waitForCronSchedule{}, state),
	), nil
}

func nextCronSchedule(state cronScheduleState) *dex.StepDecision {
	if state.RemainingRuns == 1 {
		return dex.GracefulComplete(nil)
	}
	state.RemainingRuns--
	return dex.GoTo(waitForCronSchedule{}, state)
}

type runCronSchedule struct {
	dex.StepDefaultsNoWaitFor[cronScheduleRun]
}

func (runCronSchedule) Execute(
	ctx dex.Context,
	run cronScheduleRun,
) (*dex.StepDecision, error) {
	if err := ctx.RecordEvent("cron-schedule-run", fmt.Sprintf("run-%d", run.RunNumber)); err != nil {
		return nil, err
	}
	if run.IsFinal {
		return dex.GracefulComplete(nil), nil
	}
	return dex.DeadEnd(), nil
}

var _ dex.Flow = (*CronScheduleFlow)(nil)

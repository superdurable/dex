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

package pollingwithtimer

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/examples/go/patterns/polling"
	"github.com/superdurable/dex/sdk-go/dex"
)

// maxPollRounds bounds the number of checks, not elapsed time; calls, retries, and queueing add time.
const (
	pollInterval        = 10 * time.Second
	PollIntervalTimerID = "poll-interval"
	maxPollRounds       = 60
	jobCallTimeout      = 10 * time.Second
)

type JobPollRound struct {
	JobID       string `json:"jobId"`
	RoundNumber int    `json:"roundNumber"`
}

type PollingWithTimerFlow struct {
	dex.FlowDefaults
	jobs *polling.FakeJobService
}

func NewPollingWithTimerFlow(jobs *polling.FakeJobService) *PollingWithTimerFlow {
	return &PollingWithTimerFlow{jobs: jobs}
}

func (flow *PollingWithTimerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartJob{jobs: flow.jobs}),
		dex.DefineStep(CheckJobStatus{jobs: flow.jobs}),
		dex.DefineStep(RecordJobWaitFailure{}),
	}
}

func (*PollingWithTimerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type StartJob struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	jobs *polling.FakeJobService
}

func (step StartJob) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	jobID := ctx.FlowID()
	callCtx, cancel := context.WithTimeout(ctx, jobCallTimeout)
	defer cancel()
	if err := step.jobs.StartJob(callCtx, jobID); err != nil {
		return nil, dex.ErrorWithStack(err)
	}
	return dex.GoTo(CheckJobStatus{}, JobPollRound{JobID: jobID, RoundNumber: 1}), nil
}

type CheckJobStatus struct {
	dex.StepDefaults
	jobs *polling.FakeJobService
}

func (CheckJobStatus) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    3,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(RecordJobWaitFailure{}, nil),
	}
}

func (CheckJobStatus) WaitFor(ctx dex.Context, _ JobPollRound) (*dex.Wait, error) {
	return dex.Until(dex.Timer(pollInterval, dex.WithConditionID(PollIntervalTimerID))), nil
}

func (step CheckJobStatus) Execute(ctx dex.Context, pollRound JobPollRound) (*dex.StepDecision, error) {
	callCtx, cancel := context.WithTimeout(ctx, jobCallTimeout)
	defer cancel()
	status, err := step.jobs.GetJobStatus(callCtx, pollRound.JobID)
	if err != nil {
		return nil, dex.ErrorWithStack(err)
	}
	switch {
	case status.State == polling.JobSucceeded:
		return dex.GracefulComplete(status.Result), nil
	case status.State == polling.JobFailed:
		return dex.ForceFail("job " + pollRound.JobID + " failed"), nil
	case pollRound.RoundNumber >= maxPollRounds:
		return dex.ForceFail(fmt.Sprintf("job %s did not finish in %d rounds", pollRound.JobID, maxPollRounds)), nil
	}
	nextRound := JobPollRound{JobID: pollRound.JobID, RoundNumber: pollRound.RoundNumber + 1}
	return dex.GoTo(CheckJobStatus{}, nextRound), nil
}

type RecordJobWaitFailure struct {
	dex.StepDefaultsNoWaitFor[JobPollRound]
}

func (RecordJobWaitFailure) Execute(ctx dex.Context, pollRound JobPollRound) (*dex.StepDecision, error) {
	failure := ctx.RecoveryError()
	return dex.ForceFail("checking job " + pollRound.JobID + " failed: " + failure.ErrorType + ": " + failure.Detail), nil
}

var _ dex.Flow = (*PollingWithTimerFlow)(nil)

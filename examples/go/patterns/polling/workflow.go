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

package polling

import (
	"context"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// Keep pollInterval + jobCallTimeout <= HeartbeatTimeout - 10s.
// maxJobWait bounds one attempt and all retries; Dex enforces it.
const (
	pollInterval   = 2 * time.Second
	jobCallTimeout = 10 * time.Second
	maxJobWait     = 10 * time.Minute
)

var JobProgress = dex.DefineStream[JobStatus]("JobProgress", 1<<20)

type PollingFlow struct {
	dex.FlowDefaults
	jobs *FakeJobService
}

func NewPollingFlow(jobs *FakeJobService) *PollingFlow {
	return &PollingFlow{jobs: jobs}
}

func (flow *PollingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartJob{jobs: flow.jobs}),
		dex.DefineStep(AwaitJob{jobs: flow.jobs}),
		dex.DefineStep(RecordJobWaitFailure{}),
	}
}

func (*PollingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{JobProgress}}
}

type StartJob struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	jobs *FakeJobService
}

func (step StartJob) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	jobID := ctx.FlowID()
	callCtx, cancel := context.WithTimeout(ctx, jobCallTimeout)
	defer cancel()
	if err := step.jobs.StartJob(callCtx, jobID); err != nil {
		return nil, dex.ErrorWithStack(err)
	}
	return dex.GoTo(AwaitJob{}, jobID), nil
}

type AwaitJob struct {
	dex.StepDefaultsNoWaitFor[string]
	jobs *FakeJobService
}

func (AwaitJob) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: maxJobWait,
		HeartbeatTimeout:     time.Minute,
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    3,
			TotalDuration:      maxJobWait,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(RecordJobWaitFailure{}, nil),
	}
}

func (step AwaitJob) Execute(ctx dex.Context, jobID string) (*dex.StepDecision, error) {
	var reportedState JobState
	if _, err := ctx.GetLastHeartbeatValue(&reportedState); err != nil {
		return nil, err
	}
	var status JobStatus
	for {
		var err error
		status, err = step.getJobStatus(ctx, jobID)
		if err != nil {
			return nil, dex.ErrorWithStack(err)
		}
		if status.State == JobSucceeded || status.State == JobFailed {
			break
		}
		if status.State != reportedState {
			if err := JobProgress.Write(ctx, status); err != nil {
				return nil, err
			}
			reportedState = status.State
		} else if err := ctx.RecordHeartbeat(reportedState); err != nil {
			return nil, err
		}
		time.Sleep(pollInterval)
	}
	if status.State == JobFailed {
		return dex.ForceFail("job " + jobID + " failed"), nil
	}
	return dex.GracefulComplete(status.Result), nil
}

func (step AwaitJob) getJobStatus(ctx context.Context, jobID string) (JobStatus, error) {
	callCtx, cancel := context.WithTimeout(ctx, jobCallTimeout)
	defer cancel()
	return step.jobs.GetJobStatus(callCtx, jobID)
}

type RecordJobWaitFailure struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (RecordJobWaitFailure) Execute(ctx dex.Context, jobID string) (*dex.StepDecision, error) {
	failure := ctx.RecoveryError()
	return dex.ForceFail("waiting for job " + jobID + " failed: " + failure.ErrorType + ": " + failure.Detail), nil
}

var _ dex.Flow = (*PollingFlow)(nil)

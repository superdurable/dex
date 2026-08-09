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

package jobpost

import (
	"errors"
	"time"

	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	Title = dex.DefineAttribute[string](
		"Title",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexFullText}),
	)
	JobDescription = dex.DefineAttribute[string](
		"JobDescription",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexFullText}),
	)
	LastUpdateTimeMillis = dex.DefineAttribute[int64](
		"LastUpdateTimeMillis",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexInt}),
	)
	Notes = dex.DefineAttribute[string]("Notes")
)

type JobInfo struct {
	Title       string
	Description string
	Notes       string
}

type JobPostFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewJobPostFlow(applicationService service.MyService) *JobPostFlow {
	return &JobPostFlow{service: applicationService}
}

func (flow *JobPostFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStep(externalUpdateStep{service: flow.service}),
	}
}

func (*JobPostFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Title, JobDescription, LastUpdateTimeMillis, Notes},
	}
}

func (*JobPostFlow) Get(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[JobInfo], error) {
	info, err := readJobInfo(ctx)
	return dex.RPCResult[JobInfo]{Output: info}, err
}

func (*JobPostFlow) GetWithStrongConsistency(
	ctx dex.Context,
	_ dex.None,
) (dex.RPCResult[JobInfo], error) {
	info, err := readJobInfo(ctx)
	return dex.RPCResult[JobInfo]{Output: info}, err
}

func (*JobPostFlow) Update(
	ctx dex.Context,
	input JobInfo,
) (dex.RPCResult[dex.None], error) {
	if err := Title.Set(ctx, input.Title); err != nil {
		return dex.RPCResult[dex.None]{}, err
	}
	if err := JobDescription.Set(ctx, input.Description); err != nil {
		return dex.RPCResult[dex.None]{}, err
	}
	if err := LastUpdateTimeMillis.Set(ctx, time.Now().UnixMilli()); err != nil {
		return dex.RPCResult[dex.None]{}, err
	}
	if input.Notes != "" {
		if err := Notes.Set(ctx, input.Notes); err != nil {
			return dex.RPCResult[dex.None]{}, err
		}
	}
	return dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(externalUpdateStep{}, nil)},
	}, nil
}

func readJobInfo(ctx dex.Context) (JobInfo, error) {
	title, err := Title.Get(ctx)
	if err != nil {
		return JobInfo{}, err
	}
	description, err := JobDescription.Get(ctx)
	if err != nil {
		return JobInfo{}, err
	}
	notes, err := Notes.Get(ctx)
	if err != nil {
		var notFound *dex.AttributeNotFoundError
		if !errors.As(err, &notFound) {
			return JobInfo{}, err
		}
		notes = ""
	}
	return JobInfo{Title: title, Description: description, Notes: notes}, nil
}

type externalUpdateStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	service service.MyService
}

func (externalUpdateStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    3 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    60 * time.Second,
			MaximumAttempts:    100,
			TotalDuration:      time.Hour,
		},
	}
}

func (step externalUpdateStep) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	step.service.UpdateExternalSystem("this is an update to external service")
	return dex.DeadEnd(), nil
}

var _ dex.Flow = (*JobPostFlow)(nil)

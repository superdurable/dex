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

	"github.com/superdurable/dex/examples/go/shared/service"
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
	Notes                     = dex.DefineAttribute[string]("Notes")
	UpdateLinkedInPostingLock = dex.DefineAttribute[dex.None]("UpdateLinkedInPostingLock")
	UpdateIndeedPostingLock   = dex.DefineAttribute[dex.None]("UpdateIndeedPostingLock")
)

const (
	UpdateLinkedInPostingStepType = "UpdateLinkedInPosting"
	UpdateIndeedPostingStepType   = "UpdateIndeedPosting"
)

type JobInfo struct {
	Title       string
	Description string
	Notes       string
}

type JobPostingFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewJobPostingFlow(applicationService service.MyService) *JobPostingFlow {
	return &JobPostingFlow{service: applicationService}
}

func (flow *JobPostingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStep(UpdateLinkedInPosting{service: flow.service}),
		dex.DefineStep(UpdateIndeedPosting{service: flow.service}),
	}
}

func (*JobPostingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			Title,
			JobDescription,
			LastUpdateTimeMillis,
			Notes,
			UpdateLinkedInPostingLock,
			UpdateIndeedPostingLock,
		},
	}
}

func (*JobPostingFlow) Get(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[JobInfo], error) {
	info, err := readJobInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[JobInfo]{Output: info}, nil
}

func (*JobPostingFlow) GetWithStrongConsistency(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[JobInfo], error) {
	info, err := readJobInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[JobInfo]{Output: info}, nil
}

func (*JobPostingFlow) Update(
	ctx dex.Context,
	input JobInfo,
) (*dex.RPCResult[dex.None], error) {
	if err := Title.Set(ctx, input.Title); err != nil {
		return nil, err
	}
	if err := JobDescription.Set(ctx, input.Description); err != nil {
		return nil, err
	}
	if err := LastUpdateTimeMillis.Set(ctx, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	if input.Notes != "" {
		if err := Notes.Set(ctx, input.Notes); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{
			dex.MovementOf(UpdateLinkedInPosting{}, nil),
			dex.MovementOf(UpdateIndeedPosting{}, nil),
		},
	}, nil
}

func UpdateInvokeOptions() dex.InvokeOptions {
	return dex.InvokeOptions{
		LockAttributes: []dex.AttributeLock{dex.LockAttribute(Title)},
	}
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

type UpdateLinkedInPosting struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	service service.MyService
}

func (UpdateLinkedInPosting) GetStepOptions() *dex.StepOptions {
	return jobBoardUpdateStepOptions(dex.LockAttribute(UpdateLinkedInPostingLock))
}

func (UpdateLinkedInPosting) GetStepType() string {
	return UpdateLinkedInPostingStepType
}

func (step UpdateLinkedInPosting) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	step.service.UpdateExternalSystem("update LinkedIn job posting")
	return dex.DeadEnd(), nil
}

type UpdateIndeedPosting struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	service service.MyService
}

func (UpdateIndeedPosting) GetStepOptions() *dex.StepOptions {
	return jobBoardUpdateStepOptions(dex.LockAttribute(UpdateIndeedPostingLock))
}

func (UpdateIndeedPosting) GetStepType() string {
	return UpdateIndeedPostingStepType
}

func (step UpdateIndeedPosting) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	step.service.UpdateExternalSystem("update Indeed job posting")
	return dex.DeadEnd(), nil
}

func jobBoardUpdateStepOptions(executeLock dex.AttributeLock) *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLockAttributes: []dex.AttributeLock{executeLock},
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    3 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    60 * time.Second,
			MaximumAttempts:    100,
			TotalDuration:      time.Hour,
		},
	}
}

var _ dex.Flow = (*JobPostingFlow)(nil)

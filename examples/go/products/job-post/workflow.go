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
	"fmt"
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
	Notes                  = dex.DefineAttribute[string]("Notes")
	UpdateVersion          = dex.DefineAttribute[int]("UpdateVersion")
	UpdatePostingLock      = dex.DefineAttribute[dex.None]("UpdatePostingLock")
	LinkedInPostingUpdates = dex.DefineChannel[PostingUpdate](
		"LinkedInPostingUpdates",
	)
	IndeedPostingUpdates = dex.DefineChannel[PostingUpdate](
		"IndeedPostingUpdates",
	)
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

type PostingUpdate struct {
	Version        int
	IdempotencyKey string
	Posting        JobInfo
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
		dex.DefineStartStep(InitStep{}),
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
			UpdateVersion,
			UpdatePostingLock,
		},
		Channels: []dex.ChannelDef{LinkedInPostingUpdates, IndeedPostingUpdates},
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
) (*dex.RPCResult[int], error) {
	version, err := UpdateVersion.Get(ctx)
	if err != nil {
		return nil, err
	}
	version++
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
	if err := UpdateVersion.Set(ctx, version); err != nil {
		return nil, err
	}
	update := PostingUpdate{
		Version:        version,
		IdempotencyKey: fmt.Sprintf("%s:%d", ctx.FlowID(), version),
		Posting:        input,
	}
	if err := LinkedInPostingUpdates.Publish(ctx, update); err != nil {
		return nil, err
	}
	if err := IndeedPostingUpdates.Publish(ctx, update); err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: version}, nil
}

func UpdateInvokeOptions() dex.InvokeOptions {
	return dex.InvokeOptions{
		LockAttributes: []dex.AttributeLock{dex.LockAttribute(UpdatePostingLock)},
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

type InitStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (InitStep) GetStepType() string {
	return "InitStep"
}

func (InitStep) Execute(
	_ dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(UpdateLinkedInPosting{}, nil),
		dex.MovementOf(UpdateIndeedPosting{}, nil),
	), nil
}

type UpdateLinkedInPosting struct {
	dex.StepDefaults
	service service.MyService
}

func (UpdateLinkedInPosting) GetStepOptions() *dex.StepOptions {
	return jobBoardUpdateStepOptions()
}

func (UpdateLinkedInPosting) GetStepType() string {
	return UpdateLinkedInPostingStepType
}

func (UpdateLinkedInPosting) WaitFor(
	_ dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.Until(LinkedInPostingUpdates.ForOne()), nil
}

func (step UpdateLinkedInPosting) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	update, err := linkedInPostingUpdate(ctx)
	if err != nil {
		return nil, err
	}
	step.service.UpdateExternalSystem(fmt.Sprintf(
		"update LinkedIn job posting v%d [%s]: %s",
		update.Version,
		update.IdempotencyKey,
		update.Posting.Title,
	))
	return dex.GoTo(UpdateLinkedInPosting{}, nil), nil
}

func linkedInPostingUpdate(ctx dex.Context) (PostingUpdate, error) {
	updates, err := LinkedInPostingUpdates.GetConditionResults(ctx)
	if err != nil {
		return PostingUpdate{}, err
	}
	if len(updates) != 1 {
		return PostingUpdate{}, fmt.Errorf("expected one LinkedIn posting update, got %d", len(updates))
	}
	return updates[0], nil
}

type UpdateIndeedPosting struct {
	dex.StepDefaults
	service service.MyService
}

func (UpdateIndeedPosting) GetStepOptions() *dex.StepOptions {
	return jobBoardUpdateStepOptions()
}

func (UpdateIndeedPosting) GetStepType() string {
	return UpdateIndeedPostingStepType
}

func (UpdateIndeedPosting) WaitFor(
	_ dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.Until(IndeedPostingUpdates.ForOne()), nil
}

func (step UpdateIndeedPosting) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	update, err := indeedPostingUpdate(ctx)
	if err != nil {
		return nil, err
	}
	step.service.UpdateExternalSystem(fmt.Sprintf(
		"update Indeed job posting v%d [%s]: %s",
		update.Version,
		update.IdempotencyKey,
		update.Posting.Title,
	))
	return dex.GoTo(UpdateIndeedPosting{}, nil), nil
}

func indeedPostingUpdate(ctx dex.Context) (PostingUpdate, error) {
	updates, err := IndeedPostingUpdates.GetConditionResults(ctx)
	if err != nil {
		return PostingUpdate{}, err
	}
	if len(updates) != 1 {
		return PostingUpdate{}, fmt.Errorf("expected one Indeed posting update, got %d", len(updates))
	}
	return updates[0], nil
}

func jobBoardUpdateStepOptions() *dex.StepOptions {
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

var _ dex.Flow = (*JobPostingFlow)(nil)

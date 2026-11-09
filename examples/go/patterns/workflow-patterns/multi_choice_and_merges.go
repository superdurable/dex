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

package workflowpatterns

import (
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

var NotificationSentChannel = dex.DefineChannel[string]("notificationSent")

// NotificationSelection chooses which notification branches run.
type NotificationSelection struct {
	SendEmail bool `json:"sendEmail"`
	SendSms   bool `json:"sendSms"`
	SendPush  bool `json:"sendPush"`
}

// MultiChoiceSynchronizingMergeFlow implements WCP-6 Multi-Choice and, by passing
// the selected branch count to the join, WCP-7 and WCP-37 synchronizing merges.
type MultiChoiceSynchronizingMergeFlow struct{ dex.FlowDefaults }

func (*MultiChoiceSynchronizingMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SelectNotificationChannels{}),
		dex.DefineStep(SendEmail{}),
		dex.DefineStep(SendSms{}),
		dex.DefineStep(SendPush{}),
		dex.DefineStep(SynchronizingMerge{}),
	}
}

func (*MultiChoiceSynchronizingMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{NotificationSentChannel}}
}

type SelectNotificationChannels struct {
	dex.StepDefaultsNoWaitFor[NotificationSelection]
}

func (SelectNotificationChannels) Execute(
	_ dex.Context,
	selection NotificationSelection,
) (*dex.StepDecision, error) {
	var branches []dex.StepMovement
	if selection.SendEmail {
		branches = append(branches, dex.MovementOf(SendEmail{}, nil))
	}
	if selection.SendSms {
		branches = append(branches, dex.MovementOf(SendSms{}, nil))
	}
	if selection.SendPush {
		branches = append(branches, dex.MovementOf(SendPush{}, nil))
	}
	if len(branches) == 0 {
		return dex.GracefulComplete([]string{}), nil
	}
	movements := append([]dex.StepMovement{dex.MovementOf(SynchronizingMerge{}, len(branches))}, branches...)
	return dex.GoToMany(movements...), nil
}

type SendEmail struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SendEmail) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, NotificationSentChannel, "SendEmail")
}

type SendSms struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SendSms) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, NotificationSentChannel, "SendSms")
}

type SendPush struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SendPush) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, NotificationSentChannel, "SendPush")
}

type SynchronizingMerge struct{ dex.StepDefaults }

func (SynchronizingMerge) WaitFor(_ dex.Context, branchCount int) (*dex.Wait, error) {
	return dex.Until(NotificationSentChannel.ForN(branchCount)), nil
}

func (SynchronizingMerge) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	branches, err := NotificationSentChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(branches)), nil
}

// MultiMergeFlow implements WCP-8 Multi-Merge: every branch arrival runs RecordReview again.
type MultiMergeFlow struct{ dex.FlowDefaults }

func (*MultiMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartReviews{}),
		dex.DefineStep(LegalReview{}),
		dex.DefineStep(FinanceReview{}),
		dex.DefineStep(RecordReview{}),
	}
}

func (*MultiMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type StartReviews struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartReviews) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(dex.MovementOf(LegalReview{}, nil), dex.MovementOf(FinanceReview{}, nil)), nil
}

type LegalReview struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (LegalReview) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(RecordReview{}, "LegalReview"), nil
}

type FinanceReview struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (FinanceReview) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(RecordReview{}, "FinanceReview"), nil
}

type RecordReview struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (RecordReview) Execute(_ dex.Context, reviewer string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(reviewer), nil
}

// GeneralSynchronizingMergeFlow implements WCP-38 General Synchronizing Merge. The
// region with a cycle runs as a SubFlow that completes once none of its Steps can run.
type GeneralSynchronizingMergeFlow struct {
	dex.FlowDefaults
	reviewRegionFlow *ReviewRegionFlow
}

// NewGeneralSynchronizingMergeFlow creates the parent Flow of reviewRegionFlow.
func NewGeneralSynchronizingMergeFlow(reviewRegionFlow *ReviewRegionFlow) *GeneralSynchronizingMergeFlow {
	if reviewRegionFlow == nil {
		panic("workflowpatterns: ReviewRegionFlow is required")
	}
	return &GeneralSynchronizingMergeFlow{reviewRegionFlow: reviewRegionFlow}
}

func (flow *GeneralSynchronizingMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RunReviewRegion{reviewRegionFlow: flow.reviewRegionFlow}),
		dex.DefineStep(PublishDecision{}),
	}
}

func (*GeneralSynchronizingMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type RunReviewRegion struct {
	dex.StepDefaults
	reviewRegionFlow *ReviewRegionFlow
}

func (step RunReviewRegion) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.reviewRegionFlow, 0)), nil
}

func (RunReviewRegion) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	outputs := make([]string, 0, len(result.Completions))
	for _, completion := range result.Completions {
		var output string
		if err := completion.Output.Decode(&output); err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	return dex.GoTo(PublishDecision{}, sortedCopy(outputs)), nil
}

type PublishDecision struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (PublishDecision) Execute(_ dex.Context, outputs []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(outputs), nil
}

// ReviewRegionFlow is the SubFlow region of GeneralSynchronizingMergeFlow. Its
// review loops back to DraftProposal once while translation runs in parallel.
type ReviewRegionFlow struct{ dex.FlowDefaults }

func (*ReviewRegionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(DraftProposal{}),
		dex.DefineStep(ReviewProposal{}),
		dex.DefineStep(TranslateProposal{}),
	}
}

func (*ReviewRegionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type DraftProposal struct{ dex.StepDefaultsNoWaitFor[int] }

func (DraftProposal) Execute(_ dex.Context, revision int) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(ReviewProposal{}, revision),
		dex.MovementOf(TranslateProposal{}, revision),
	), nil
}

type ReviewProposal struct{ dex.StepDefaultsNoWaitFor[int] }

func (ReviewProposal) Execute(_ dex.Context, revision int) (*dex.StepDecision, error) {
	if revision < 1 {
		return dex.GoTo(DraftProposal{}, revision+1), nil
	}
	return dex.GracefulComplete(fmt.Sprintf("ReviewProposal-%d", revision)), nil
}

type TranslateProposal struct{ dex.StepDefaultsNoWaitFor[int] }

func (TranslateProposal) Execute(_ dex.Context, revision int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(fmt.Sprintf("TranslateProposal-%d", revision)), nil
}

var (
	_ dex.Flow = (*MultiChoiceSynchronizingMergeFlow)(nil)
	_ dex.Flow = (*MultiMergeFlow)(nil)
	_ dex.Flow = (*GeneralSynchronizingMergeFlow)(nil)
	_ dex.Flow = (*ReviewRegionFlow)(nil)
)

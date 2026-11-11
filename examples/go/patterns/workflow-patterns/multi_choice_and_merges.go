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

var NotificationSentJoin = dex.DefineJoin[string]("notificationSent")

// NotificationSelection chooses which notification branches run.
type NotificationSelection struct {
	SendEmail bool `json:"sendEmail"`
	SendSms   bool `json:"sendSms"`
	SendPush  bool `json:"sendPush"`
}

// MultiChoiceSynchronizingMergeFlow implements WCP-6 Multi-Choice and, by forking
// only the selected branches, WCP-7 and WCP-37 synchronizing merges.
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
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{NotificationSentJoin}}
}

type SelectNotificationChannels struct {
	dex.StepDefaultsNoWaitFor[NotificationSelection]
}

func (SelectNotificationChannels) Execute(
	ctx dex.Context,
	selection NotificationSelection,
) (*dex.StepDecision, error) {
	var branchBuilders []func(dex.JoinRound) dex.StepMovement
	if selection.SendEmail {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(SendEmail{}, round)
		})
	}
	if selection.SendSms {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(SendSms{}, round)
		})
	}
	if selection.SendPush {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(SendPush{}, round)
		})
	}
	// A round with no selected branch satisfies ForAllBranches immediately.
	movements, err := NotificationSentJoin.Fork(ctx, branchBuilders, func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(SynchronizingMerge{}, round)
	})
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type SendEmail struct {
	dex.StepDefaultsNoWaitFor[dex.JoinRound]
}

func (SendEmail) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return NotificationSentJoin.EndBranch(ctx, round, "SendEmail")
}

type SendSms struct {
	dex.StepDefaultsNoWaitFor[dex.JoinRound]
}

func (SendSms) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return NotificationSentJoin.EndBranch(ctx, round, "SendSms")
}

type SendPush struct {
	dex.StepDefaultsNoWaitFor[dex.JoinRound]
}

func (SendPush) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return NotificationSentJoin.EndBranch(ctx, round, "SendPush")
}

type SynchronizingMerge struct{ dex.StepDefaults }

func (SynchronizingMerge) WaitFor(_ dex.Context, round dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(NotificationSentJoin.ForAllBranches(round)), nil
}

func (SynchronizingMerge) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	branches, err := NotificationSentJoin.BranchResults(ctx, round)
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

var (
	LiveBranchCountAttribute = dex.DefineAttribute[int]("liveBranchCount")
	MergedReviewsAttribute   = dex.DefineAttribute[[]string]("mergedReviews")
	MergeSentinelChannel     = dex.DefineChannel[string]("mergeSentinel")
	liveBranchCountLock      = executeLockOptions(LiveBranchCountAttribute)
)

// DocumentReviewPlan selects the checks and how the finance branch behaves.
type DocumentReviewPlan struct {
	IsLegalCheckNeeded   bool `json:"isLegalCheckNeeded"`
	IsFinanceCheckNeeded bool `json:"isFinanceCheckNeeded"`
	FinanceReworkRounds  int  `json:"financeReworkRounds"`
	IsFinanceArchived    bool `json:"isFinanceArchived"`
}

// DocumentDraft is one revision of the reviewed document.
type DocumentDraft struct {
	Plan     DocumentReviewPlan `json:"plan"`
	Revision int                `json:"revision"`
}

// GeneralSynchronizingMergeFlow implements WCP-38: a live-thread counter decides when the merge fires.
type GeneralSynchronizingMergeFlow struct{ dex.FlowDefaults }

func (*GeneralSynchronizingMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartDocumentReview{}),
		dex.DefineStep(DraftDocument{}),
		dex.DefineStep(CheckLegalTerms{}),
		dex.DefineStep(CheckFinanceTerms{}),
		dex.DefineStep(ArchiveDocument{}),
		dex.DefineStep(PublishDocument{}),
	}
}

func (*GeneralSynchronizingMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{LiveBranchCountAttribute, MergedReviewsAttribute},
		Channels:   []dex.ChannelDef{MergeSentinelChannel},
	}
}

type StartDocumentReview struct {
	dex.StepDefaultsNoWaitFor[DocumentReviewPlan]
}

func (StartDocumentReview) GetStepOptions() *dex.StepOptions { return liveBranchCountLock }

func (StartDocumentReview) Execute(ctx dex.Context, plan DocumentReviewPlan) (*dex.StepDecision, error) {
	if err := LiveBranchCountAttribute.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(DraftDocument{}, DocumentDraft{Plan: plan, Revision: 0}),
		dex.MovementOf(PublishDocument{}, nil),
	), nil
}

type DraftDocument struct {
	dex.StepDefaultsNoWaitFor[DocumentDraft]
}

func (DraftDocument) GetStepOptions() *dex.StepOptions { return liveBranchCountLock }

func (DraftDocument) Execute(ctx dex.Context, draft DocumentDraft) (*dex.StepDecision, error) {
	isRework := draft.Revision > 0
	var checks []dex.StepMovement
	if isRework || draft.Plan.IsLegalCheckNeeded {
		checks = append(checks, dex.MovementOf(CheckLegalTerms{}, draft.Revision))
	}
	if isRework || draft.Plan.IsFinanceCheckNeeded {
		checks = append(checks, dex.MovementOf(CheckFinanceTerms{}, draft))
	}
	if err := addLiveBranches(ctx, len(checks)-1); err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return dex.DeadEnd(), nil
	}
	return dex.GoToMany(checks...), nil
}

type CheckLegalTerms struct{ dex.StepDefaultsNoWaitFor[int] }

func (CheckLegalTerms) GetStepOptions() *dex.StepOptions { return liveBranchCountLock }

func (CheckLegalTerms) Execute(ctx dex.Context, revision int) (*dex.StepDecision, error) {
	if err := arriveAtMerge(ctx, fmt.Sprintf("CheckLegalTerms-%d", revision)); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type CheckFinanceTerms struct {
	dex.StepDefaultsNoWaitFor[DocumentDraft]
}

func (CheckFinanceTerms) GetStepOptions() *dex.StepOptions { return liveBranchCountLock }

func (CheckFinanceTerms) Execute(ctx dex.Context, draft DocumentDraft) (*dex.StepDecision, error) {
	if draft.Revision < draft.Plan.FinanceReworkRounds {
		return dex.GoTo(DraftDocument{}, DocumentDraft{Plan: draft.Plan, Revision: draft.Revision + 1}), nil
	}
	if draft.Plan.IsFinanceArchived {
		if err := addLiveBranches(ctx, -1); err != nil {
			return nil, err
		}
		return dex.GoTo(ArchiveDocument{}, nil), nil
	}
	if err := arriveAtMerge(ctx, fmt.Sprintf("CheckFinanceTerms-%d", draft.Revision)); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type ArchiveDocument struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ArchiveDocument) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ArchiveDocument"), nil
}

// PublishDocument is the merge: it fires once no thread can still reach it.
type PublishDocument struct{ dex.StepDefaults }

func (PublishDocument) GetStepOptions() *dex.StepOptions { return liveBranchCountLock }

func (PublishDocument) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(MergeSentinelChannel.ForOne()), nil
}

func (PublishDocument) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	mergedReviews, err := getAttributeOrZero(ctx, MergedReviewsAttribute)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(mergedReviews)), nil
}

// arriveAtMerge records one arrival and retires its thread.
func arriveAtMerge(ctx dex.Context, review string) error {
	mergedReviews, err := getAttributeOrZero(ctx, MergedReviewsAttribute)
	if err != nil {
		return err
	}
	if err := MergedReviewsAttribute.Set(ctx, appendTrail(mergedReviews, review)); err != nil {
		return err
	}
	return addLiveBranches(ctx, -1)
}

// addLiveBranches changes the live-thread count and publishes the sentinel when it reaches 0.
func addLiveBranches(ctx dex.Context, delta int) error {
	liveBranchCount, err := getAttributeOrZero(ctx, LiveBranchCountAttribute)
	if err != nil {
		return err
	}
	liveBranchCount += delta
	if err := LiveBranchCountAttribute.Set(ctx, liveBranchCount); err != nil {
		return err
	}
	if liveBranchCount == 0 {
		return MergeSentinelChannel.Publish(ctx, "done")
	}
	return nil
}

var (
	_ dex.Flow = (*MultiChoiceSynchronizingMergeFlow)(nil)
	_ dex.Flow = (*MultiMergeFlow)(nil)
	_ dex.Flow = (*GeneralSynchronizingMergeFlow)(nil)
)

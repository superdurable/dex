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
	"github.com/superdurable/dex/sdk-go/dex"
)

var CourierArrivedChannel = dex.DefineChannel[string]("courierArrived")

// CancelTaskFlow implements WCP-19 Cancel Task: an RPC cancels the waiting pickup
// Step and starts the fallback.
type CancelTaskFlow struct{ dex.FlowDefaults }

func (*CancelTaskFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitCourierPickup{}),
		dex.DefineStep(ArrangeSelfDelivery{}),
	}
}

func (flow *CancelTaskFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ConfirmCourierPickup, nil),
		dex.DefineRPC(flow.CancelCourierPickup, nil),
	}
}

func (*CancelTaskFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{CourierArrivedChannel}}
}

// ConfirmCourierPickup records that the courier arrived.
func (*CancelTaskFlow) ConfirmCourierPickup(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := CourierArrivedChannel.Publish(ctx, "arrived"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// CancelCourierPickup cancels the courier pickup and arranges self delivery.
func (*CancelTaskFlow) CancelCourierPickup(dex.Context, dex.None) (*dex.RPCResult[dex.None], error) {
	result := &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(ArrangeSelfDelivery{}, nil)},
	}
	return result.CancelSteps(AwaitCourierPickup{}), nil
}

type AwaitCourierPickup struct{ dex.StepDefaults }

func (AwaitCourierPickup) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CourierArrivedChannel.ForOne()), nil
}

func (AwaitCourierPickup) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("AwaitCourierPickup"), nil
}

type ArrangeSelfDelivery struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ArrangeSelfDelivery) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ArrangeSelfDelivery"), nil
}

var ApplicationReviewedChannel = dex.DefineChannel[string]("applicationReviewed")

// CancelCaseFlow implements WCP-20 Cancel Case: the Client stops the whole Flow
// with the cancel stop type.
type CancelCaseFlow struct{ dex.FlowDefaults }

func (*CancelCaseFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(ReviewApplication{})}
}

func (*CancelCaseFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ApplicationReviewedChannel}}
}

type ReviewApplication struct{ dex.StepDefaults }

func (ReviewApplication) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(ApplicationReviewedChannel.ForOne()), nil
}

func (ReviewApplication) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ReviewApplication"), nil
}

var (
	IdentityVerifiedChannel     = dex.DefineChannel[string]("identityVerified")
	CreditHistoryCheckedChannel = dex.DefineChannel[string]("creditHistoryChecked")
	SanctionsScreenedChannel    = dex.DefineChannel[string]("sanctionsScreened")
)

// CancelRegionFlow implements WCP-25 Cancel Region: one RPC cancels two Step types
// together while ScreenSanctions keeps running.
type CancelRegionFlow struct{ dex.FlowDefaults }

func (*CancelRegionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartApplicationChecks{}),
		dex.DefineStep(VerifyIdentity{}),
		dex.DefineStep(CheckCreditHistory{}),
		dex.DefineStep(ScreenSanctions{}),
		dex.DefineStep(NotifyRejection{}),
	}
}

func (flow *CancelRegionFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RejectApplication, nil),
		dex.DefineRPC(flow.CompleteSanctionsScreening, nil),
	}
}

func (*CancelRegionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{IdentityVerifiedChannel, CreditHistoryCheckedChannel, SanctionsScreenedChannel},
	}
}

// RejectApplication cancels the identity and credit checks and notifies the applicant.
func (*CancelRegionFlow) RejectApplication(dex.Context, dex.None) (*dex.RPCResult[dex.None], error) {
	result := &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(NotifyRejection{}, nil)},
	}
	return result.CancelSteps(VerifyIdentity{}, CheckCreditHistory{}), nil
}

// CompleteSanctionsScreening finishes the sanctions screening.
func (*CancelRegionFlow) CompleteSanctionsScreening(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := SanctionsScreenedChannel.Publish(ctx, "screened"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type StartApplicationChecks struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartApplicationChecks) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(VerifyIdentity{}, nil),
		dex.MovementOf(CheckCreditHistory{}, nil),
		dex.MovementOf(ScreenSanctions{}, nil),
	), nil
}

type VerifyIdentity struct{ dex.StepDefaults }

func (VerifyIdentity) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(IdentityVerifiedChannel.ForOne()), nil
}

func (VerifyIdentity) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("VerifyIdentity"), nil
}

type CheckCreditHistory struct{ dex.StepDefaults }

func (CheckCreditHistory) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CreditHistoryCheckedChannel.ForOne()), nil
}

func (CheckCreditHistory) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("CheckCreditHistory"), nil
}

type ScreenSanctions struct{ dex.StepDefaults }

func (ScreenSanctions) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(SanctionsScreenedChannel.ForOne()), nil
}

func (ScreenSanctions) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ScreenSanctions"), nil
}

type NotifyRejection struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (NotifyRejection) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("NotifyRejection"), nil
}

var (
	FinishedTranslationCountAttribute = dex.DefineAttribute[int]("finishedTranslationCount")
	TranslationReleasedChannel        = dex.DefineChannel[string]("translationReleased")
)

// CancelMultipleInstanceTaskFlow implements WCP-26: an RPC cancels every running
// translation instance, and finished instances keep their results.
type CancelMultipleInstanceTaskFlow struct{ dex.FlowDefaults }

func (*CancelMultipleInstanceTaskFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartTranslations{}),
		dex.DefineStep(TranslateDocument{}),
		dex.DefineStep(ReportTranslations{}),
	}
}

func (flow *CancelMultipleInstanceTaskFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ReleaseTranslation, nil),
		dex.DefineRPC(flow.CancelTranslations, nil),
	}
}

func (*CancelMultipleInstanceTaskFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{FinishedTranslationCountAttribute},
		Channels:   []dex.ChannelDef{TranslationReleasedChannel},
	}
}

// ReleaseTranslation lets one waiting translation finish.
func (*CancelMultipleInstanceTaskFlow) ReleaseTranslation(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := TranslationReleasedChannel.Publish(ctx, "released"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// CancelTranslations cancels every unfinished translation and reports the finished count.
func (*CancelMultipleInstanceTaskFlow) CancelTranslations(dex.Context, dex.None) (*dex.RPCResult[dex.None], error) {
	result := &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(ReportTranslations{}, nil)},
	}
	return result.CancelSteps(TranslateDocument{}), nil
}

type StartTranslations struct{ dex.StepDefaultsNoWaitFor[int] }

func (StartTranslations) Execute(_ dex.Context, translationCount int) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, translationCount)
	for index := range translationCount {
		movements = append(movements, dex.MovementOf(TranslateDocument{}, index))
	}
	return dex.GoToMany(movements...), nil
}

type TranslateDocument struct{ dex.StepDefaults }

func (TranslateDocument) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(FinishedTranslationCountAttribute)},
	}
}

func (TranslateDocument) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(TranslationReleasedChannel.ForOne()), nil
}

func (TranslateDocument) Execute(ctx dex.Context, index int) (*dex.StepDecision, error) {
	finishedCount, err := getAttributeOrZero(ctx, FinishedTranslationCountAttribute)
	if err != nil {
		return nil, err
	}
	if err := FinishedTranslationCountAttribute.Set(ctx, finishedCount+1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(index), nil
}

type ReportTranslations struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ReportTranslations) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	finishedCount, err := getAttributeOrZero(ctx, FinishedTranslationCountAttribute)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(finishedCount), nil
}

var (
	PeerReviewReleasedChannel  = dex.DefineChannel[string]("peerReviewReleased")
	PeerReviewSubmittedChannel = dex.DefineChannel[int]("peerReviewSubmitted")
)

// CompleteMultipleInstanceTaskFlow implements WCP-27: an RPC ends the review early.
type CompleteMultipleInstanceTaskFlow struct{ dex.FlowDefaults }

func (*CompleteMultipleInstanceTaskFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartPeerReviews{}),
		dex.DefineStep(SubmitPeerReview{}),
		dex.DefineStep(CollectPeerReviews{}),
		dex.DefineStep(PublishReviewResults{}),
	}
}

func (flow *CompleteMultipleInstanceTaskFlow) GetRPCs() []dex.RPCDef {
	loadSubmittedReviews := &dex.RPCOptions{LoadChannels: []dex.ChannelDef{PeerReviewSubmittedChannel}}
	return []dex.RPCDef{
		dex.DefineRPC(flow.ReleasePeerReview, nil),
		dex.DefineRPC(flow.GetSubmittedPeerReviewCount, loadSubmittedReviews),
		dex.DefineRPC(flow.CompletePeerReviewsEarly, loadSubmittedReviews),
	}
}

func (*CompleteMultipleInstanceTaskFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{PeerReviewReleasedChannel, PeerReviewSubmittedChannel},
	}
}

// ReleasePeerReview lets one waiting reviewer submit.
func (*CompleteMultipleInstanceTaskFlow) ReleasePeerReview(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := PeerReviewReleasedChannel.Publish(ctx, "released"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// GetSubmittedPeerReviewCount returns how many submitted reviews wait for the join.
func (*CompleteMultipleInstanceTaskFlow) GetSubmittedPeerReviewCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	submitted, err := PeerReviewSubmittedChannel.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: len(submitted)}, nil
}

// CompletePeerReviewsEarly cancels the remaining reviewers and the join, and
// publishes the reviews submitted so far.
func (*CompleteMultipleInstanceTaskFlow) CompletePeerReviewsEarly(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	submitted, err := PeerReviewSubmittedChannel.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	indexes := make([]int, 0, len(submitted))
	for _, message := range submitted {
		indexes = append(indexes, message.Value)
	}
	result := &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(PublishReviewResults{}, sortedCopy(indexes))},
	}
	return result.CancelSteps(SubmitPeerReview{}, CollectPeerReviews{}), nil
}

type StartPeerReviews struct{ dex.StepDefaultsNoWaitFor[int] }

func (StartPeerReviews) Execute(_ dex.Context, reviewerCount int) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, reviewerCount+1)
	for index := range reviewerCount {
		movements = append(movements, dex.MovementOf(SubmitPeerReview{}, index))
	}
	movements = append(movements, dex.MovementOf(CollectPeerReviews{}, reviewerCount))
	return dex.GoToMany(movements...), nil
}

type SubmitPeerReview struct{ dex.StepDefaults }

func (SubmitPeerReview) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(PeerReviewReleasedChannel.ForOne()), nil
}

func (SubmitPeerReview) Execute(ctx dex.Context, index int) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, PeerReviewSubmittedChannel, index)
}

type CollectPeerReviews struct{ dex.StepDefaults }

func (CollectPeerReviews) WaitFor(_ dex.Context, reviewerCount int) (*dex.Wait, error) {
	return dex.Until(PeerReviewSubmittedChannel.ForN(reviewerCount)), nil
}

func (CollectPeerReviews) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	indexes, err := PeerReviewSubmittedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(PublishReviewResults{}, sortedCopy(indexes)), nil
}

type PublishReviewResults struct {
	dex.StepDefaultsNoWaitFor[[]int]
}

func (PublishReviewResults) Execute(_ dex.Context, indexes []int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(indexes), nil
}

var (
	_ dex.Flow = (*CancelTaskFlow)(nil)
	_ dex.Flow = (*CancelCaseFlow)(nil)
	_ dex.Flow = (*CancelRegionFlow)(nil)
	_ dex.Flow = (*CancelMultipleInstanceTaskFlow)(nil)
	_ dex.Flow = (*CompleteMultipleInstanceTaskFlow)(nil)
)

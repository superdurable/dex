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

var InvitationReleasedChannel = dex.DefineChannel[string]("invitationReleased")

// MultipleInstancesWithoutSynchronizationFlow implements WCP-12: it starts one
// SendInvitation per attendee and nothing waits for them.
type MultipleInstancesWithoutSynchronizationFlow struct{ dex.FlowDefaults }

func (*MultipleInstancesWithoutSynchronizationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RegisterAttendees{}),
		dex.DefineStep(SendInvitation{}),
		dex.DefineStep(ConfirmRegistration{}),
	}
}

func (flow *MultipleInstancesWithoutSynchronizationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseInvitations, nil)}
}

func (*MultipleInstancesWithoutSynchronizationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{InvitationReleasedChannel}}
}

// ReleaseInvitations lets count waiting invitations send.
func (*MultipleInstancesWithoutSynchronizationFlow) ReleaseInvitations(
	ctx dex.Context,
	count int,
) (*dex.RPCResult[dex.None], error) {
	if err := publishMessages(ctx, InvitationReleasedChannel, "released", count); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type RegisterAttendees struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (RegisterAttendees) Execute(_ dex.Context, attendees []string) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, len(attendees)+1)
	for _, attendee := range attendees {
		movements = append(movements, dex.MovementOf(SendInvitation{}, attendee))
	}
	movements = append(movements, dex.MovementOf(ConfirmRegistration{}, nil))
	return dex.GoToMany(movements...), nil
}

type SendInvitation struct{ dex.StepDefaults }

func (SendInvitation) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.Until(InvitationReleasedChannel.ForOne()), nil
}

func (SendInvitation) Execute(_ dex.Context, attendee string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(attendee), nil
}

type ConfirmRegistration struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ConfirmRegistration) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ConfirmRegistration"), nil
}

var ReviewCompletedChannel = dex.DefineChannel[string]("reviewCompleted")

// DesignTimeMultipleInstancesFlow implements WCP-13: the instance count is fixed
// in the Flow code and a Channel ForN join waits for every instance.
type DesignTimeMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*DesignTimeMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartDesignTimeReviews{}),
		dex.DefineStep(ReviewDocument{}),
		dex.DefineStep(CollectReviews{}),
	}
}

func (*DesignTimeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ReviewCompletedChannel}}
}

type StartDesignTimeReviews struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartDesignTimeReviews) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return startReviewInstances([]string{"legal", "finance", "security"}), nil
}

// RunTimeMultipleInstancesFlow implements WCP-14: the instance count comes from
// the Flow input and a Channel ForN join waits for every instance.
type RunTimeMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*RunTimeMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartRunTimeReviews{}),
		dex.DefineStep(ReviewDocument{}),
		dex.DefineStep(CollectReviews{}),
	}
}

func (*RunTimeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ReviewCompletedChannel}}
}

type StartRunTimeReviews struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (StartRunTimeReviews) Execute(_ dex.Context, reviewers []string) (*dex.StepDecision, error) {
	return startReviewInstances(reviewers), nil
}

func startReviewInstances(reviewers []string) *dex.StepDecision {
	movements := make([]dex.StepMovement, 0, len(reviewers)+1)
	for _, reviewer := range reviewers {
		movements = append(movements, dex.MovementOf(ReviewDocument{}, reviewer))
	}
	movements = append(movements, dex.MovementOf(CollectReviews{}, len(reviewers)))
	return dex.GoToMany(movements...)
}

type ReviewDocument struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (ReviewDocument) Execute(ctx dex.Context, reviewer string) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, ReviewCompletedChannel, reviewer)
}

type CollectReviews struct{ dex.StepDefaults }

func (CollectReviews) WaitFor(_ dex.Context, reviewerCount int) (*dex.Wait, error) {
	return dex.Until(ReviewCompletedChannel.ForN(reviewerCount)), nil
}

func (CollectReviews) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	reviewers, err := ReviewCompletedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(reviewers)), nil
}

var (
	StartedSampleCountAttribute   = dex.DefineAttribute[int]("startedSampleCount")
	IsSampleIntakeClosedAttribute = dex.DefineAttribute[bool]("isSampleIntakeClosed")
	SampleAnalyzedChannel         = dex.DefineChannel[string]("sampleAnalyzed")
	SampleIntakeClosedChannel     = dex.DefineChannel[string]("sampleIntakeClosed")
)

// NoPriorKnowledgeMultipleInstancesFlow implements WCP-15: RPCs add instances
// while they run, and the join loops until intake closes and every instance reported.
type NoPriorKnowledgeMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*NoPriorKnowledgeMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(JoinSamples{}),
		dex.DefineStep(AnalyzeSample{}),
		dex.DefineStep(ReportSamples{}),
	}
}

func (flow *NoPriorKnowledgeMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	sampleCountLock := &dex.RPCOptions{
		LockAttributes: []dex.AttributeLock{dex.LockAttribute(StartedSampleCountAttribute)},
	}
	return []dex.RPCDef{
		dex.DefineRPC(flow.AddSample, sampleCountLock),
		dex.DefineRPC(flow.CloseSampleIntake, sampleCountLock),
	}
}

func (*NoPriorKnowledgeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{StartedSampleCountAttribute, IsSampleIntakeClosedAttribute},
		Channels:   []dex.ChannelDef{SampleAnalyzedChannel, SampleIntakeClosedChannel},
	}
}

// AddSample starts one AnalyzeSample instance and returns true, or returns false
// once intake is closed.
func (*NoPriorKnowledgeMultipleInstancesFlow) AddSample(
	ctx dex.Context,
	sampleID string,
) (*dex.RPCResult[bool], error) {
	isClosed, err := getAttributeOrZero(ctx, IsSampleIntakeClosedAttribute)
	if err != nil {
		return nil, err
	}
	if isClosed {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	startedCount, err := getAttributeOrZero(ctx, StartedSampleCountAttribute)
	if err != nil {
		return nil, err
	}
	if err := StartedSampleCountAttribute.Set(ctx, startedCount+1); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{
		Output:    true,
		NextSteps: []dex.StepMovement{dex.MovementOf(AnalyzeSample{}, sampleID)},
	}, nil
}

// CloseSampleIntake stops new samples so the join can finish.
func (*NoPriorKnowledgeMultipleInstancesFlow) CloseSampleIntake(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := IsSampleIntakeClosedAttribute.Set(ctx, true); err != nil {
		return nil, err
	}
	if err := SampleIntakeClosedChannel.Publish(ctx, "closed"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// JoinSamples needs no lock: the started count cannot change once intake is closed.
type JoinSamples struct{ dex.StepDefaults }

func (JoinSamples) WaitFor(ctx dex.Context, analyzed []string) (*dex.Wait, error) {
	isClosed, isComplete, err := readSampleIntake(ctx, analyzed)
	if err != nil {
		return nil, err
	}
	if isComplete {
		return dex.SkipWaitImmediately(), nil
	}
	if isClosed {
		return dex.Until(SampleAnalyzedChannel.ForOne()), nil
	}
	return dex.AnyOf(SampleAnalyzedChannel.ForOne(), SampleIntakeClosedChannel.ForOne()), nil
}

func (JoinSamples) Execute(ctx dex.Context, analyzed []string) (*dex.StepDecision, error) {
	consumed, err := SampleAnalyzedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	updated := make([]string, 0, len(analyzed)+len(consumed))
	updated = append(append(updated, analyzed...), consumed...)
	_, isComplete, err := readSampleIntake(ctx, updated)
	if err != nil {
		return nil, err
	}
	if isComplete {
		return dex.GoTo(ReportSamples{}, updated), nil
	}
	return dex.GoTo(JoinSamples{}, updated), nil
}

// readSampleIntake reports whether intake is closed, and whether it is closed
// with every started sample analyzed.
func readSampleIntake(ctx dex.Context, analyzed []string) (isClosed bool, isComplete bool, err error) {
	isClosed, err = getAttributeOrZero(ctx, IsSampleIntakeClosedAttribute)
	if err != nil {
		return false, false, err
	}
	startedCount, err := getAttributeOrZero(ctx, StartedSampleCountAttribute)
	if err != nil {
		return false, false, err
	}
	return isClosed, isClosed && len(analyzed) == startedCount, nil
}

type AnalyzeSample struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (AnalyzeSample) Execute(ctx dex.Context, sampleID string) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, SampleAnalyzedChannel, sampleID)
}

type ReportSamples struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (ReportSamples) Execute(_ dex.Context, analyzed []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(sortedCopy(analyzed)), nil
}

var (
	InspectionReleasedChannel = dex.DefineChannel[string]("inspectionReleased")
	UnitInspectedChannel      = dex.DefineChannel[int]("unitInspected")
)

// InspectionPlan sets how many units run and how many complete the join.
type InspectionPlan struct {
	InspectionCount     int `json:"inspectionCount"`
	RequiredInspections int `json:"requiredInspections"`
}

// InspectionQuorum is the join input: the units it needs and the units the reset consumes.
type InspectionQuorum struct {
	RequiredInspections  int `json:"requiredInspections"`
	RemainingInspections int `json:"remainingInspections"`
}

// StaticPartialJoinForMultipleInstancesFlow implements WCP-34: the join fires after
// n of m instances, and a reset Step consumes the remaining m - n.
type StaticPartialJoinForMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*StaticPartialJoinForMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartInspections{}),
		dex.DefineStep(InspectUnit{}),
		dex.DefineStep(AcceptInspections{}),
		dex.DefineStep(ApproveBatch{}),
		dex.DefineStep(ResetInspectionJoin{}),
	}
}

func (flow *StaticPartialJoinForMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseInspections, nil)}
}

func (*StaticPartialJoinForMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{InspectionReleasedChannel, UnitInspectedChannel},
	}
}

// ReleaseInspections lets count waiting inspections finish.
func (*StaticPartialJoinForMultipleInstancesFlow) ReleaseInspections(
	ctx dex.Context,
	count int,
) (*dex.RPCResult[dex.None], error) {
	if err := publishMessages(ctx, InspectionReleasedChannel, "released", count); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type StartInspections struct {
	dex.StepDefaultsNoWaitFor[InspectionPlan]
}

func (StartInspections) Execute(_ dex.Context, plan InspectionPlan) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, plan.InspectionCount+1)
	for index := range plan.InspectionCount {
		movements = append(movements, dex.MovementOf(InspectUnit{}, index))
	}
	movements = append(movements, dex.MovementOf(AcceptInspections{}, InspectionQuorum{
		RequiredInspections:  plan.RequiredInspections,
		RemainingInspections: plan.InspectionCount - plan.RequiredInspections,
	}))
	return dex.GoToMany(movements...), nil
}

type InspectUnit struct{ dex.StepDefaults }

func (InspectUnit) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(InspectionReleasedChannel.ForOne()), nil
}

func (InspectUnit) Execute(ctx dex.Context, index int) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, UnitInspectedChannel, index)
}

type AcceptInspections struct{ dex.StepDefaults }

func (AcceptInspections) WaitFor(_ dex.Context, quorum InspectionQuorum) (*dex.Wait, error) {
	return dex.Until(UnitInspectedChannel.ForN(quorum.RequiredInspections)), nil
}

func (AcceptInspections) Execute(ctx dex.Context, quorum InspectionQuorum) (*dex.StepDecision, error) {
	indexes, err := UnitInspectedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(ApproveBatch{}, indexes),
		dex.MovementOf(ResetInspectionJoin{}, quorum.RemainingInspections),
	), nil
}

type ApproveBatch struct {
	dex.StepDefaultsNoWaitFor[[]int]
}

func (ApproveBatch) Execute(_ dex.Context, indexes []int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(sortedCopy(indexes)), nil
}

type ResetInspectionJoin struct{ dex.StepDefaults }

func (ResetInspectionJoin) WaitFor(_ dex.Context, remainingInspections int) (*dex.Wait, error) {
	if remainingInspections == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(UnitInspectedChannel.ForN(remainingInspections)), nil
}

func (ResetInspectionJoin) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	indexes, err := UnitInspectedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(indexes)), nil
}

var (
	QuorumInspectionReleasedChannel = dex.DefineChannel[string]("quorumInspectionReleased")
	QuorumUnitInspectedChannel      = dex.DefineChannel[int]("quorumUnitInspected")
)

// CancellingPartialJoinForMultipleInstancesFlow implements WCP-35: the join fires
// after n instances and cancels the instances still running.
type CancellingPartialJoinForMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*CancellingPartialJoinForMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartQuorumInspections{}),
		dex.DefineStep(InspectQuorumUnit{}),
		dex.DefineStep(AcceptQuorumInspections{}),
	}
}

func (flow *CancellingPartialJoinForMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseQuorumInspections, nil)}
}

func (*CancellingPartialJoinForMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{QuorumInspectionReleasedChannel, QuorumUnitInspectedChannel},
	}
}

// ReleaseQuorumInspections lets count waiting inspections finish.
func (*CancellingPartialJoinForMultipleInstancesFlow) ReleaseQuorumInspections(
	ctx dex.Context,
	count int,
) (*dex.RPCResult[dex.None], error) {
	if err := publishMessages(ctx, QuorumInspectionReleasedChannel, "released", count); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type StartQuorumInspections struct {
	dex.StepDefaultsNoWaitFor[InspectionPlan]
}

func (StartQuorumInspections) Execute(_ dex.Context, plan InspectionPlan) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, plan.InspectionCount+1)
	for index := range plan.InspectionCount {
		movements = append(movements, dex.MovementOf(InspectQuorumUnit{}, index))
	}
	movements = append(movements, dex.MovementOf(AcceptQuorumInspections{}, plan.RequiredInspections))
	return dex.GoToMany(movements...), nil
}

type InspectQuorumUnit struct{ dex.StepDefaults }

func (InspectQuorumUnit) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(QuorumInspectionReleasedChannel.ForOne()), nil
}

func (InspectQuorumUnit) Execute(ctx dex.Context, index int) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, QuorumUnitInspectedChannel, index)
}

type AcceptQuorumInspections struct{ dex.StepDefaults }

func (AcceptQuorumInspections) WaitFor(_ dex.Context, requiredInspections int) (*dex.Wait, error) {
	return dex.Until(QuorumUnitInspectedChannel.ForN(requiredInspections)), nil
}

func (AcceptQuorumInspections) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	indexes, err := QuorumUnitInspectedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(indexes)).CancelSteps(InspectQuorumUnit{}), nil
}

var (
	IsBidAdmissionClosedAttribute = dex.DefineAttribute[bool]("isBidAdmissionClosed")
	BidEvaluatedChannel           = dex.DefineChannel[BidEvaluation]("bidEvaluated")
)

// BidEvaluation is the result of one EvaluateBid instance.
type BidEvaluation struct {
	Amount     int  `json:"amount"`
	IsAccepted bool `json:"isAccepted"`
}

// BidJoinProgress is the join input: the accepted bids needed and those seen so far.
type BidJoinProgress struct {
	RequiredAcceptedBids int   `json:"requiredAcceptedBids"`
	AcceptedBids         []int `json:"acceptedBids"`
}

// DynamicPartialJoinForMultipleInstancesFlow implements WCP-36: RPCs add instances
// while they run, and the join closes admission once enough bids are accepted.
type DynamicPartialJoinForMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*DynamicPartialJoinForMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(JoinBids{}),
		dex.DefineStep(EvaluateBid{}),
		dex.DefineStep(AwardContract{}),
	}
}

func (flow *DynamicPartialJoinForMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AddBid, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(IsBidAdmissionClosedAttribute)},
		}),
	}
}

func (*DynamicPartialJoinForMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{IsBidAdmissionClosedAttribute},
		Channels:   []dex.ChannelDef{BidEvaluatedChannel},
	}
}

// AddBid starts one EvaluateBid instance and returns true, or returns false once
// admission is closed.
func (*DynamicPartialJoinForMultipleInstancesFlow) AddBid(
	ctx dex.Context,
	amount int,
) (*dex.RPCResult[bool], error) {
	isClosed, err := getAttributeOrZero(ctx, IsBidAdmissionClosedAttribute)
	if err != nil {
		return nil, err
	}
	if isClosed {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	return &dex.RPCResult[bool]{
		Output:    true,
		NextSteps: []dex.StepMovement{dex.MovementOf(EvaluateBid{}, amount)},
	}, nil
}

type JoinBids struct{ dex.StepDefaults }

func (JoinBids) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(IsBidAdmissionClosedAttribute)},
	}
}

func (JoinBids) WaitFor(dex.Context, BidJoinProgress) (*dex.Wait, error) {
	return dex.Until(BidEvaluatedChannel.ForOne()), nil
}

func (JoinBids) Execute(ctx dex.Context, progress BidJoinProgress) (*dex.StepDecision, error) {
	evaluations, err := BidEvaluatedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	accepted := make([]int, 0, len(progress.AcceptedBids)+len(evaluations))
	accepted = append(accepted, progress.AcceptedBids...)
	for _, evaluation := range evaluations {
		if evaluation.IsAccepted {
			accepted = append(accepted, evaluation.Amount)
		}
	}
	if len(accepted) >= progress.RequiredAcceptedBids {
		if err := IsBidAdmissionClosedAttribute.Set(ctx, true); err != nil {
			return nil, err
		}
		return dex.GoTo(AwardContract{}, accepted), nil
	}
	return dex.GoTo(JoinBids{}, BidJoinProgress{
		RequiredAcceptedBids: progress.RequiredAcceptedBids,
		AcceptedBids:         accepted,
	}), nil
}

type EvaluateBid struct{ dex.StepDefaultsNoWaitFor[int] }

func (EvaluateBid) Execute(ctx dex.Context, amount int) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, BidEvaluatedChannel, BidEvaluation{Amount: amount, IsAccepted: amount <= 1000})
}

type AwardContract struct {
	dex.StepDefaultsNoWaitFor[[]int]
}

func (AwardContract) Execute(_ dex.Context, accepted []int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(sortedCopy(accepted)), nil
}

var (
	_ dex.Flow = (*MultipleInstancesWithoutSynchronizationFlow)(nil)
	_ dex.Flow = (*DesignTimeMultipleInstancesFlow)(nil)
	_ dex.Flow = (*RunTimeMultipleInstancesFlow)(nil)
	_ dex.Flow = (*NoPriorKnowledgeMultipleInstancesFlow)(nil)
	_ dex.Flow = (*StaticPartialJoinForMultipleInstancesFlow)(nil)
	_ dex.Flow = (*CancellingPartialJoinForMultipleInstancesFlow)(nil)
	_ dex.Flow = (*DynamicPartialJoinForMultipleInstancesFlow)(nil)
)

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
	"strconv"

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

var (
	StartedReviewersAttributeMap = dex.DefineAttributeMap[bool]("startedReviewers")
	ReviewGateOpenedChannel      = dex.DefineChannel[string]("reviewGateOpened")
	ReviewCompletedChannel       = dex.DefineChannel[string]("reviewCompleted")
	reviewSchema                 = dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{StartedReviewersAttributeMap},
		Channels:   []dex.ChannelDef{ReviewGateOpenedChannel, ReviewCompletedChannel},
	}
	loadStartedReviewersOptions = &dex.RPCOptions{
		LoadAttributeMaps: []dex.AttributeDef{StartedReviewersAttributeMap},
	}
)

// DesignTimeMultipleInstancesFlow implements WCP-13 with a fixed instance count.
type DesignTimeMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*DesignTimeMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartDesignTimeReviews{}),
		dex.DefineStep(ReviewDocument{}),
		dex.DefineStep(CollectReviews{}),
	}
}

func (flow *DesignTimeMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetStartedDesignTimeReviewCount, loadStartedReviewersOptions),
		dex.DefineRPC(flow.OpenDesignTimeReviewGate, nil),
	}
}

func (*DesignTimeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return reviewSchema
}

// GetStartedDesignTimeReviewCount returns how many review instances are in flight or done.
func (*DesignTimeMultipleInstancesFlow) GetStartedDesignTimeReviewCount(ctx dex.Context, _ dex.None) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: StartedReviewersAttributeMap.MapSize(ctx)}, nil
}

// OpenDesignTimeReviewGate lets count waiting review instances finish.
func (*DesignTimeMultipleInstancesFlow) OpenDesignTimeReviewGate(ctx dex.Context, count int) (*dex.RPCResult[dex.None], error) {
	return openGate(ctx, ReviewGateOpenedChannel, count)
}

type StartDesignTimeReviews struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartDesignTimeReviews) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return startReviewInstances([]string{"legal", "finance", "security"}), nil
}

// RunTimeMultipleInstancesFlow implements WCP-14 with an instance count from the input.
type RunTimeMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*RunTimeMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartRunTimeReviews{}),
		dex.DefineStep(ReviewDocument{}),
		dex.DefineStep(CollectReviews{}),
	}
}

func (flow *RunTimeMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetStartedRunTimeReviewCount, loadStartedReviewersOptions),
		dex.DefineRPC(flow.OpenRunTimeReviewGate, nil),
	}
}

func (*RunTimeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return reviewSchema
}

// GetStartedRunTimeReviewCount returns how many review instances are in flight or done.
func (*RunTimeMultipleInstancesFlow) GetStartedRunTimeReviewCount(ctx dex.Context, _ dex.None) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: StartedReviewersAttributeMap.MapSize(ctx)}, nil
}

// OpenRunTimeReviewGate lets count waiting review instances finish.
func (*RunTimeMultipleInstancesFlow) OpenRunTimeReviewGate(ctx dex.Context, count int) (*dex.RPCResult[dex.None], error) {
	return openGate(ctx, ReviewGateOpenedChannel, count)
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

type ReviewDocument struct{ dex.StepDefaults }

func (ReviewDocument) WaitFor(ctx dex.Context, reviewer string) (*dex.Wait, error) {
	if err := StartedReviewersAttributeMap.Set(ctx, reviewer, true); err != nil {
		return nil, err
	}
	return dex.Until(ReviewGateOpenedChannel.ForOne()), nil
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
	StartedSampleCountAttribute        = dex.DefineAttribute[int]("startedSampleCount")
	IsSampleIntakeClosedAttribute      = dex.DefineAttribute[bool]("isSampleIntakeClosed")
	SampleAnalyzedChannel              = dex.DefineChannel[string]("sampleAnalyzed")
	SampleIntakeClosedChannel          = dex.DefineChannel[string]("sampleIntakeClosed")
	AnalysisStartedSamplesAttributeMap = dex.DefineAttributeMap[bool]("analysisStartedSamples")
	SampleGateOpenedChannel            = dex.DefineChannel[string]("sampleGateOpened")
)

// NoPriorKnowledgeMultipleInstancesFlow implements WCP-15: RPCs add instances until intake closes.
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
		dex.DefineRPC(flow.GetStartedAnalysisCount, &dex.RPCOptions{
			LoadAttributeMaps: []dex.AttributeDef{AnalysisStartedSamplesAttributeMap},
		}),
		dex.DefineRPC(flow.OpenSampleGate, nil),
	}
}

func (*NoPriorKnowledgeMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			StartedSampleCountAttribute,
			IsSampleIntakeClosedAttribute,
			AnalysisStartedSamplesAttributeMap,
		},
		Channels: []dex.ChannelDef{SampleAnalyzedChannel, SampleIntakeClosedChannel, SampleGateOpenedChannel},
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

// GetStartedAnalysisCount returns how many analysis instances are in flight or done.
func (*NoPriorKnowledgeMultipleInstancesFlow) GetStartedAnalysisCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: AnalysisStartedSamplesAttributeMap.MapSize(ctx)}, nil
}

// OpenSampleGate lets count waiting analysis instances finish.
func (*NoPriorKnowledgeMultipleInstancesFlow) OpenSampleGate(
	ctx dex.Context,
	count int,
) (*dex.RPCResult[dex.None], error) {
	return openGate(ctx, SampleGateOpenedChannel, count)
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

type AnalyzeSample struct{ dex.StepDefaults }

func (AnalyzeSample) WaitFor(ctx dex.Context, sampleID string) (*dex.Wait, error) {
	if err := AnalysisStartedSamplesAttributeMap.Set(ctx, sampleID, true); err != nil {
		return nil, err
	}
	return dex.Until(SampleGateOpenedChannel.ForOne()), nil
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
	UnitInspectedChannelMap   = dex.DefineChannelMap[int]("unitInspected")
)

// InspectionPlan sets how many units run and how many complete the join.
type InspectionPlan struct {
	InspectionCount     int `json:"inspectionCount"`
	RequiredInspections int `json:"requiredInspections"`
}

// InspectionRound is the input of one static partial join round.
type InspectionRound struct {
	InspectionCount     int `json:"inspectionCount"`
	RequiredInspections int `json:"requiredInspections"`
	Round               int `json:"round"`
	RoundCount          int `json:"roundCount"`
}

// UnitInspection identifies one inspection instance of one round.
type UnitInspection struct {
	Round     int `json:"round"`
	UnitIndex int `json:"unitIndex"`
}

// InspectedUnits lists the unit indexes one join or reset consumed in a round.
type InspectedUnits struct {
	Round       int   `json:"round"`
	UnitIndexes []int `json:"unitIndexes"`
}

// StaticPartialJoinForMultipleInstancesFlow implements WCP-34 with a per-round join and reset Step.
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
		Channels: []dex.ChannelDef{InspectionReleasedChannel, UnitInspectedChannelMap},
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
	dex.StepDefaultsNoWaitFor[InspectionRound]
}

func (StartInspections) Execute(_ dex.Context, round InspectionRound) (*dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 0, round.InspectionCount+1)
	for unitIndex := range round.InspectionCount {
		movements = append(movements, dex.MovementOf(InspectUnit{}, UnitInspection{Round: round.Round, UnitIndex: unitIndex}))
	}
	movements = append(movements, dex.MovementOf(AcceptInspections{}, round))
	return dex.GoToMany(movements...), nil
}

type InspectUnit struct{ dex.StepDefaults }

func (InspectUnit) WaitFor(dex.Context, UnitInspection) (*dex.Wait, error) {
	return dex.Until(InspectionReleasedChannel.ForOne()), nil
}

func (InspectUnit) Execute(ctx dex.Context, inspection UnitInspection) (*dex.StepDecision, error) {
	if err := UnitInspectedChannelMap.Publish(ctx, roundInstance(inspection.Round), inspection.UnitIndex); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type AcceptInspections struct{ dex.StepDefaults }

func (AcceptInspections) WaitFor(_ dex.Context, round InspectionRound) (*dex.Wait, error) {
	return dex.Until(UnitInspectedChannelMap.ForN(roundInstance(round.Round), round.RequiredInspections)), nil
}

func (AcceptInspections) Execute(ctx dex.Context, round InspectionRound) (*dex.StepDecision, error) {
	indexes, err := UnitInspectedChannelMap.GetConditionResults(ctx, roundInstance(round.Round))
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(ApproveBatch{}, InspectedUnits{Round: round.Round, UnitIndexes: sortedCopy(indexes)}),
		dex.MovementOf(ResetInspectionJoin{}, round),
	), nil
}

type ApproveBatch struct {
	dex.StepDefaultsNoWaitFor[InspectedUnits]
}

func (ApproveBatch) Execute(_ dex.Context, approved InspectedUnits) (*dex.StepDecision, error) {
	return dex.GracefulComplete(approved), nil
}

// ResetInspectionJoin consumes the rest of the round, then re-enables the join.
type ResetInspectionJoin struct{ dex.StepDefaults }

func (ResetInspectionJoin) WaitFor(_ dex.Context, round InspectionRound) (*dex.Wait, error) {
	remainingInspections := round.InspectionCount - round.RequiredInspections
	if remainingInspections == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(UnitInspectedChannelMap.ForN(roundInstance(round.Round), remainingInspections)), nil
}

func (ResetInspectionJoin) Execute(ctx dex.Context, round InspectionRound) (*dex.StepDecision, error) {
	indexes, err := UnitInspectedChannelMap.GetConditionResults(ctx, roundInstance(round.Round))
	if err != nil {
		return nil, err
	}
	if round.Round < round.RoundCount {
		next := round
		next.Round++
		return dex.GoTo(StartInspections{}, next), nil
	}
	return dex.GracefulComplete(InspectedUnits{Round: round.Round, UnitIndexes: sortedCopy(indexes)}), nil
}

var (
	QuorumInspectionReleasedChannel = dex.DefineChannel[string]("quorumInspectionReleased")
	QuorumUnitInspectedChannel      = dex.DefineChannel[int]("quorumUnitInspected")
	QuorumCertifiedChannel          = dex.DefineChannel[string]("quorumCertified")
)

// CancellingPartialJoinForMultipleInstancesFlow implements WCP-35: the join fires
// after n instances and cancels the instances still running.
type CancellingPartialJoinForMultipleInstancesFlow struct{ dex.FlowDefaults }

func (*CancellingPartialJoinForMultipleInstancesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartQuorumInspections{}),
		dex.DefineStep(InspectQuorumUnit{}),
		dex.DefineStep(AcceptQuorumInspections{}),
		dex.DefineStep(CertifyQuorum{}),
	}
}

func (flow *CancellingPartialJoinForMultipleInstancesFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ReleaseQuorumInspections, nil),
		dex.DefineRPC(flow.GetQueuedQuorumReleaseCount, &dex.RPCOptions{
			LoadChannels: []dex.ChannelDef{QuorumInspectionReleasedChannel},
		}),
		dex.DefineRPC(flow.CertifyQuorum, nil),
	}
}

func (*CancellingPartialJoinForMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{
			QuorumInspectionReleasedChannel,
			QuorumUnitInspectedChannel,
			QuorumCertifiedChannel,
		},
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

// GetQueuedQuorumReleaseCount returns how many releases no inspection has consumed yet.
func (*CancellingPartialJoinForMultipleInstancesFlow) GetQueuedQuorumReleaseCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	queuedCount, err := countPendingMessages(ctx, QuorumInspectionReleasedChannel)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: queuedCount}, nil
}

// CertifyQuorum certifies the accepted inspections.
func (*CancellingPartialJoinForMultipleInstancesFlow) CertifyQuorum(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := QuorumCertifiedChannel.Publish(ctx, "certified"); err != nil {
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
	return dex.GoTo(CertifyQuorum{}, sortedCopy(indexes)).CancelSteps(InspectQuorumUnit{}), nil
}

type CertifyQuorum struct{ dex.StepDefaults }

func (CertifyQuorum) WaitFor(dex.Context, []int) (*dex.Wait, error) {
	return dex.Until(QuorumCertifiedChannel.ForOne()), nil
}

func (CertifyQuorum) Execute(_ dex.Context, indexes []int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(indexes), nil
}

var (
	IsBidAdmissionClosedAttribute     = dex.DefineAttribute[bool]("isBidAdmissionClosed")
	BidEvaluatedChannel               = dex.DefineChannel[BidEvaluation]("bidEvaluated")
	EvaluationStartedBidsAttributeMap = dex.DefineAttributeMap[bool]("evaluationStartedBids")
	BidGateOpenedChannel              = dex.DefineChannel[string]("bidGateOpened")
	ContractSignedChannel             = dex.DefineChannel[string]("contractSigned")
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
		dex.DefineRPC(flow.GetStartedEvaluationCount, &dex.RPCOptions{
			LoadAttributeMaps: []dex.AttributeDef{EvaluationStartedBidsAttributeMap},
		}),
		dex.DefineRPC(flow.OpenBidGate, nil),
		dex.DefineRPC(flow.SignContract, nil),
	}
}

func (*DynamicPartialJoinForMultipleInstancesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{IsBidAdmissionClosedAttribute, EvaluationStartedBidsAttributeMap},
		Channels:   []dex.ChannelDef{BidEvaluatedChannel, BidGateOpenedChannel, ContractSignedChannel},
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

// GetStartedEvaluationCount returns how many bid evaluations are in flight or done.
func (*DynamicPartialJoinForMultipleInstancesFlow) GetStartedEvaluationCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: EvaluationStartedBidsAttributeMap.MapSize(ctx)}, nil
}

// OpenBidGate lets count waiting bid evaluations finish.
func (*DynamicPartialJoinForMultipleInstancesFlow) OpenBidGate(
	ctx dex.Context,
	count int,
) (*dex.RPCResult[dex.None], error) {
	return openGate(ctx, BidGateOpenedChannel, count)
}

// SignContract signs the awarded contract.
func (*DynamicPartialJoinForMultipleInstancesFlow) SignContract(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := ContractSignedChannel.Publish(ctx, "signed"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
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

type EvaluateBid struct{ dex.StepDefaults }

func (EvaluateBid) WaitFor(ctx dex.Context, amount int) (*dex.Wait, error) {
	if err := EvaluationStartedBidsAttributeMap.Set(ctx, strconv.Itoa(amount), true); err != nil {
		return nil, err
	}
	return dex.Until(BidGateOpenedChannel.ForOne()), nil
}

func (EvaluateBid) Execute(ctx dex.Context, amount int) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, BidEvaluatedChannel, BidEvaluation{Amount: amount, IsAccepted: amount <= 1000})
}

type AwardContract struct{ dex.StepDefaults }

func (AwardContract) WaitFor(dex.Context, []int) (*dex.Wait, error) {
	return dex.Until(ContractSignedChannel.ForOne()), nil
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

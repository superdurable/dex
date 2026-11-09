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
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	interleavedTaskDuration = 200 * time.Millisecond
	criticalSectionPause    = 100 * time.Millisecond
)

var (
	OfferPresentedChannel = dex.DefineChannel[string]("offerPresented")
	OfferAcceptedChannel  = dex.DefineChannel[string]("offerAccepted")
	OfferDeclinedChannel  = dex.DefineChannel[string]("offerDeclined")
)

// DeferredChoiceFlow implements WCP-16 Deferred Choice: the first customer response
// selects the branch, and Dex consumes only that response.
type DeferredChoiceFlow struct{ dex.FlowDefaults }

func (*DeferredChoiceFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(PresentOffer{}),
		dex.DefineStep(AwaitCustomerResponse{}),
		dex.DefineStep(FulfillOffer{}),
		dex.DefineStep(ArchiveOffer{}),
	}
}

func (flow *DeferredChoiceFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.PresentOffer, nil),
		dex.DefineRPC(flow.AcceptOffer, nil),
		dex.DefineRPC(flow.DeclineOffer, nil),
	}
}

func (*DeferredChoiceFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{OfferPresentedChannel, OfferAcceptedChannel, OfferDeclinedChannel},
	}
}

// PresentOffer shows the offer to the customer.
func (*DeferredChoiceFlow) PresentOffer(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := OfferPresentedChannel.Publish(ctx, "presented"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// AcceptOffer records that the customer accepted the offer.
func (*DeferredChoiceFlow) AcceptOffer(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := OfferAcceptedChannel.Publish(ctx, "accepted"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// DeclineOffer records that the customer declined the offer.
func (*DeferredChoiceFlow) DeclineOffer(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := OfferDeclinedChannel.Publish(ctx, "declined"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type PresentOffer struct{ dex.StepDefaults }

func (PresentOffer) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(OfferPresentedChannel.ForOne()), nil
}

func (PresentOffer) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(AwaitCustomerResponse{}, nil), nil
}

type AwaitCustomerResponse struct{ dex.StepDefaults }

func (AwaitCustomerResponse) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(OfferAcceptedChannel.ForOne(), OfferDeclinedChannel.ForOne()), nil
}

func (AwaitCustomerResponse) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	accepted, err := OfferAcceptedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(accepted) > 0 {
		return dex.GoTo(FulfillOffer{}, nil), nil
	}
	return dex.GoTo(ArchiveOffer{}, nil), nil
}

type FulfillOffer struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (FulfillOffer) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("FulfillOffer"), nil
}

type ArchiveOffer struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (ArchiveOffer) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("ArchiveOffer"), nil
}

var (
	OnboardingTaskLockAttribute   = dex.DefineAttribute[bool]("onboardingTaskLock")
	OnboardingTaskFinishedChannel = dex.DefineChannel[TaskInterval]("onboardingTaskFinished")
	onboardingTaskLockStepOptions = executeLockOptions(OnboardingTaskLockAttribute)
)

// InterleavedParallelRoutingFlow implements WCP-17: one shared Execute lock keeps
// tasks from overlapping, and GoTo keeps PrepareContract before SignContract.
type InterleavedParallelRoutingFlow struct{ dex.FlowDefaults }

func (*InterleavedParallelRoutingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartOnboardingTasks{}),
		dex.DefineStep(PrepareContract{}),
		dex.DefineStep(SignContract{}),
		dex.DefineStep(RunCreditCheck{}),
		dex.DefineStep(CompleteOnboardingTasks{}),
	}
}

func (*InterleavedParallelRoutingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OnboardingTaskLockAttribute},
		Channels:   []dex.ChannelDef{OnboardingTaskFinishedChannel},
	}
}

type StartOnboardingTasks struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartOnboardingTasks) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(PrepareContract{}, nil),
		dex.MovementOf(RunCreditCheck{}, nil),
		dex.MovementOf(CompleteOnboardingTasks{}, 3),
	), nil
}

type PrepareContract struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (PrepareContract) GetStepOptions() *dex.StepOptions { return onboardingTaskLockStepOptions }

func (PrepareContract) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := runInterleavedTask(ctx, OnboardingTaskFinishedChannel, "PrepareContract"); err != nil {
		return nil, err
	}
	return dex.GoTo(SignContract{}, nil), nil
}

type SignContract struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SignContract) GetStepOptions() *dex.StepOptions { return onboardingTaskLockStepOptions }

func (SignContract) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := runInterleavedTask(ctx, OnboardingTaskFinishedChannel, "SignContract"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type RunCreditCheck struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (RunCreditCheck) GetStepOptions() *dex.StepOptions { return onboardingTaskLockStepOptions }

func (RunCreditCheck) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := runInterleavedTask(ctx, OnboardingTaskFinishedChannel, "RunCreditCheck"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type CompleteOnboardingTasks struct{ dex.StepDefaults }

func (CompleteOnboardingTasks) WaitFor(_ dex.Context, taskCount int) (*dex.Wait, error) {
	return dex.Until(OnboardingTaskFinishedChannel.ForN(taskCount)), nil
}

func (CompleteOnboardingTasks) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	return completeWithIntervals(ctx, OnboardingTaskFinishedChannel)
}

var (
	IsChangeWindowOpenAttribute = dex.DefineAttribute[bool]("isChangeWindowOpen")
	ReleaseApprovedChannel      = dex.DefineChannel[string]("releaseApproved")
	ReleaseFinalizedChannel     = dex.DefineChannel[string]("releaseFinalized")
)

// MilestoneFlow implements WCP-18 Milestone: a config change can start only while
// the change window milestone holds.
type MilestoneFlow struct{ dex.FlowDefaults }

func (*MilestoneFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(OpenChangeWindow{}),
		dex.DefineStep(AwaitReleaseApproval{}),
		dex.DefineStep(AwaitReleaseFinalization{}),
		dex.DefineStep(ApplyConfigChange{}),
	}
}

func (flow *MilestoneFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RequestConfigChange, nil),
		dex.DefineRPC(flow.ApproveRelease, nil),
		dex.DefineRPC(flow.FinalizeRelease, nil),
	}
}

func (*MilestoneFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{IsChangeWindowOpenAttribute},
		Channels:   []dex.ChannelDef{ReleaseApprovedChannel, ReleaseFinalizedChannel},
	}
}

// RequestConfigChange starts ApplyConfigChange and returns true while the change
// window is open; otherwise it returns false.
func (*MilestoneFlow) RequestConfigChange(ctx dex.Context, change string) (*dex.RPCResult[bool], error) {
	isOpen, err := getAttributeOrZero(ctx, IsChangeWindowOpenAttribute)
	if err != nil {
		return nil, err
	}
	if !isOpen {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	return &dex.RPCResult[bool]{
		Output:    true,
		NextSteps: []dex.StepMovement{dex.MovementOf(ApplyConfigChange{}, change)},
	}, nil
}

// ApproveRelease approves the release, which closes the change window.
func (*MilestoneFlow) ApproveRelease(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ReleaseApprovedChannel.Publish(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// FinalizeRelease finishes the release.
func (*MilestoneFlow) FinalizeRelease(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ReleaseFinalizedChannel.Publish(ctx, "finalized"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type OpenChangeWindow struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (OpenChangeWindow) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := IsChangeWindowOpenAttribute.Set(ctx, true); err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitReleaseApproval{}, nil), nil
}

type AwaitReleaseApproval struct{ dex.StepDefaults }

func (AwaitReleaseApproval) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(ReleaseApprovedChannel.ForOne()), nil
}

func (AwaitReleaseApproval) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := IsChangeWindowOpenAttribute.Set(ctx, false); err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitReleaseFinalization{}, nil), nil
}

type AwaitReleaseFinalization struct{ dex.StepDefaults }

func (AwaitReleaseFinalization) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(ReleaseFinalizedChannel.ForOne()), nil
}

func (AwaitReleaseFinalization) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("AwaitReleaseFinalization"), nil
}

type ApplyConfigChange struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (ApplyConfigChange) Execute(_ dex.Context, change string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(change), nil
}

var (
	CriticalSectionTokenChannel    = dex.DefineChannel[string]("criticalSectionToken")
	CriticalSectionFinishedChannel = dex.DefineChannel[TaskInterval]("criticalSectionFinished")
)

// CriticalSectionFlow implements WCP-39 with a single token message guarding each section.
type CriticalSectionFlow struct{ dex.FlowDefaults }

func (*CriticalSectionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartCriticalSections{}),
		dex.DefineStep(ReserveStock{}),
		dex.DefineStep(CommitStock{}),
		dex.DefineStep(RecalculatePrice{}),
		dex.DefineStep(PublishPrice{}),
		dex.DefineStep(FinishCriticalSections{}),
	}
}

func (*CriticalSectionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{CriticalSectionTokenChannel, CriticalSectionFinishedChannel},
	}
}

type StartCriticalSections struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartCriticalSections) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := CriticalSectionTokenChannel.Publish(ctx, "token"); err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(ReserveStock{}, nil),
		dex.MovementOf(RecalculatePrice{}, nil),
		dex.MovementOf(FinishCriticalSections{}, 2),
	), nil
}

type ReserveStock struct{ dex.StepDefaults }

func (ReserveStock) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CriticalSectionTokenChannel.ForOne()), nil
}

func (ReserveStock) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	startedAtMillis := time.Now().UnixMilli()
	time.Sleep(criticalSectionPause)
	return dex.GoTo(CommitStock{}, startedAtMillis), nil
}

type CommitStock struct {
	dex.StepDefaultsNoWaitFor[int64]
}

func (CommitStock) Execute(ctx dex.Context, startedAtMillis int64) (*dex.StepDecision, error) {
	return finishCriticalSection(ctx, "inventory", startedAtMillis)
}

type RecalculatePrice struct{ dex.StepDefaults }

func (RecalculatePrice) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CriticalSectionTokenChannel.ForOne()), nil
}

func (RecalculatePrice) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	startedAtMillis := time.Now().UnixMilli()
	time.Sleep(criticalSectionPause)
	return dex.GoTo(PublishPrice{}, startedAtMillis), nil
}

type PublishPrice struct {
	dex.StepDefaultsNoWaitFor[int64]
}

func (PublishPrice) Execute(ctx dex.Context, startedAtMillis int64) (*dex.StepDecision, error) {
	return finishCriticalSection(ctx, "pricing", startedAtMillis)
}

// finishCriticalSection records the section interval and hands the token back.
func finishCriticalSection(ctx dex.Context, section string, startedAtMillis int64) (*dex.StepDecision, error) {
	time.Sleep(criticalSectionPause)
	interval := TaskInterval{Task: section, StartedAtMillis: startedAtMillis, FinishedAtMillis: time.Now().UnixMilli()}
	if err := CriticalSectionFinishedChannel.Publish(ctx, interval); err != nil {
		return nil, err
	}
	if err := CriticalSectionTokenChannel.Publish(ctx, "token"); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type FinishCriticalSections struct{ dex.StepDefaults }

func (FinishCriticalSections) WaitFor(_ dex.Context, sectionCount int) (*dex.Wait, error) {
	return dex.Until(CriticalSectionFinishedChannel.ForN(sectionCount)), nil
}

func (FinishCriticalSections) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	return completeWithIntervals(ctx, CriticalSectionFinishedChannel)
}

var (
	AuditLockAttribute   = dex.DefineAttribute[bool]("auditLock")
	AuditFinishedChannel = dex.DefineChannel[TaskInterval]("auditFinished")
	auditLockStepOptions = executeLockOptions(AuditLockAttribute)
)

// InterleavedRoutingFlow implements WCP-40 Interleaved Routing: one shared Execute
// lock runs the audits one at a time in any order.
type InterleavedRoutingFlow struct{ dex.FlowDefaults }

func (*InterleavedRoutingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartAudits{}),
		dex.DefineStep(AuditPayroll{}),
		dex.DefineStep(AuditExpenses{}),
		dex.DefineStep(AuditTaxes{}),
		dex.DefineStep(CompleteAudits{}),
	}
}

func (*InterleavedRoutingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{AuditLockAttribute},
		Channels:   []dex.ChannelDef{AuditFinishedChannel},
	}
}

type StartAudits struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (StartAudits) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(AuditPayroll{}, nil),
		dex.MovementOf(AuditExpenses{}, nil),
		dex.MovementOf(AuditTaxes{}, nil),
		dex.MovementOf(CompleteAudits{}, 3),
	), nil
}

type AuditPayroll struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (AuditPayroll) GetStepOptions() *dex.StepOptions { return auditLockStepOptions }

func (AuditPayroll) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return runInterleavedTaskAndDeadEnd(ctx, AuditFinishedChannel, "AuditPayroll")
}

type AuditExpenses struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (AuditExpenses) GetStepOptions() *dex.StepOptions { return auditLockStepOptions }

func (AuditExpenses) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return runInterleavedTaskAndDeadEnd(ctx, AuditFinishedChannel, "AuditExpenses")
}

type AuditTaxes struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (AuditTaxes) GetStepOptions() *dex.StepOptions { return auditLockStepOptions }

func (AuditTaxes) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return runInterleavedTaskAndDeadEnd(ctx, AuditFinishedChannel, "AuditTaxes")
}

type CompleteAudits struct{ dex.StepDefaults }

func (CompleteAudits) WaitFor(_ dex.Context, auditCount int) (*dex.Wait, error) {
	return dex.Until(AuditFinishedChannel.ForN(auditCount)), nil
}

func (CompleteAudits) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	return completeWithIntervals(ctx, AuditFinishedChannel)
}

func executeLockOptions[T any](lockAttribute dex.Attribute[T]) *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(lockAttribute)}}
}

// runInterleavedTask works for interleavedTaskDuration and publishes its interval.
func runInterleavedTask(ctx dex.Context, finished dex.Channel[TaskInterval], task string) error {
	startedAtMillis := time.Now().UnixMilli()
	time.Sleep(interleavedTaskDuration)
	return finished.Publish(ctx, TaskInterval{
		Task:             task,
		StartedAtMillis:  startedAtMillis,
		FinishedAtMillis: time.Now().UnixMilli(),
	})
}

func runInterleavedTaskAndDeadEnd(
	ctx dex.Context,
	finished dex.Channel[TaskInterval],
	task string,
) (*dex.StepDecision, error) {
	if err := runInterleavedTask(ctx, finished, task); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

func completeWithIntervals(ctx dex.Context, finished dex.Channel[TaskInterval]) (*dex.StepDecision, error) {
	intervals, err := finished.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if intervals == nil {
		intervals = []TaskInterval{}
	}
	return dex.GracefulComplete(intervals), nil
}

var (
	_ dex.Flow = (*DeferredChoiceFlow)(nil)
	_ dex.Flow = (*InterleavedParallelRoutingFlow)(nil)
	_ dex.Flow = (*MilestoneFlow)(nil)
	_ dex.Flow = (*CriticalSectionFlow)(nil)
	_ dex.Flow = (*InterleavedRoutingFlow)(nil)
)

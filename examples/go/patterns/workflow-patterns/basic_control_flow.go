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

// SequenceFlow implements WCP-1 Sequence: each Step moves to the next by GoTo.
type SequenceFlow struct{ dex.FlowDefaults }

func (*SequenceFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(ReceiveOrder{}),
		dex.DefineStep(CheckInventory{}),
		dex.DefineStep(ShipOrder{}),
	}
}

func (*SequenceFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type ReceiveOrder struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (ReceiveOrder) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GoTo(CheckInventory{}, appendTrail(trail, "ReceiveOrder")), nil
}

type CheckInventory struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (CheckInventory) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GoTo(ShipOrder{}, appendTrail(trail, "CheckInventory")), nil
}

type ShipOrder struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (ShipOrder) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(appendTrail(trail, "ShipOrder")), nil
}

var (
	FulfillmentBranchCompletedJoin = dex.DefineJoin[string]("fulfillmentBranchCompleted")
	PaymentApprovedChannel         = dex.DefineChannel[string]("paymentApproved")
)

// ParallelSplitSynchronizationFlow implements WCP-2 Parallel Split with a Join Fork
// and WCP-3 Synchronization with a ForAllBranches join.
type ParallelSplitSynchronizationFlow struct{ dex.FlowDefaults }

func (*ParallelSplitSynchronizationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SplitOrder{}),
		dex.DefineStep(PackItems{}),
		dex.DefineStep(ChargePayment{}),
		dex.DefineStep(SynchronizeFulfillment{}),
	}
}

func (flow *ParallelSplitSynchronizationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ApprovePayment, nil)}
}

func (*ParallelSplitSynchronizationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{FulfillmentBranchCompletedJoin, PaymentApprovedChannel},
	}
}

// ApprovePayment releases the ChargePayment branch.
func (*ParallelSplitSynchronizationFlow) ApprovePayment(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := PaymentApprovedChannel.Publish(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type SplitOrder struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (SplitOrder) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	movements, err := FulfillmentBranchCompletedJoin.Fork(ctx,
		[]func(dex.JoinRound) dex.StepMovement{
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(PackItems{}, round) },
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(ChargePayment{}, round) },
		},
		func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(SynchronizeFulfillment{}, round) },
	)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type PackItems struct {
	dex.StepDefaultsNoWaitFor[dex.JoinRound]
}

func (PackItems) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return FulfillmentBranchCompletedJoin.EndBranch(ctx, round, "PackItems")
}

type ChargePayment struct{ dex.StepDefaults }

func (ChargePayment) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(PaymentApprovedChannel.ForOne()), nil
}

func (ChargePayment) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return FulfillmentBranchCompletedJoin.EndBranch(ctx, round, "ChargePayment")
}

type SynchronizeFulfillment struct{ dex.StepDefaults }

func (SynchronizeFulfillment) WaitFor(_ dex.Context, round dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(FulfillmentBranchCompletedJoin.ForAllBranches(round)), nil
}

func (SynchronizeFulfillment) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	branches, err := FulfillmentBranchCompletedJoin.BranchResults(ctx, round)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(branches)), nil
}

// ExclusiveChoiceSimpleMergeFlow implements WCP-4 Exclusive Choice, where Execute
// picks one branch, and WCP-5 Simple Merge, where both branches reach CloseClaim.
type ExclusiveChoiceSimpleMergeFlow struct{ dex.FlowDefaults }

func (*ExclusiveChoiceSimpleMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RouteClaim{}),
		dex.DefineStep(ManualReview{}),
		dex.DefineStep(AutoApprove{}),
		dex.DefineStep(CloseClaim{}),
	}
}

func (*ExclusiveChoiceSimpleMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type RouteClaim struct{ dex.StepDefaultsNoWaitFor[int] }

func (RouteClaim) Execute(_ dex.Context, claimAmount int) (*dex.StepDecision, error) {
	trail := []string{"RouteClaim"}
	if claimAmount >= 1000 {
		return dex.GoTo(ManualReview{}, trail), nil
	}
	return dex.GoTo(AutoApprove{}, trail), nil
}

type ManualReview struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (ManualReview) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GoTo(CloseClaim{}, appendTrail(trail, "ManualReview")), nil
}

type AutoApprove struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (AutoApprove) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GoTo(CloseClaim{}, appendTrail(trail, "AutoApprove")), nil
}

type CloseClaim struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (CloseClaim) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(appendTrail(trail, "CloseClaim")), nil
}

var (
	_ dex.Flow = (*SequenceFlow)(nil)
	_ dex.Flow = (*ParallelSplitSynchronizationFlow)(nil)
	_ dex.Flow = (*ExclusiveChoiceSimpleMergeFlow)(nil)
)

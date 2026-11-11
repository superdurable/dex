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

var (
	InvoiceReceivedChannel = dex.DefineChannel[string]("invoiceReceived")
	PaymentReceivedChannel = dex.DefineChannel[string]("paymentReceived")
)

// MatchProgress counts the invoice and payment pairs matched so far.
type MatchProgress struct {
	ExpectedMatches int `json:"expectedMatches"`
	MatchedCount    int `json:"matchedCount"`
}

// InvoicePaymentMatch pairs one invoice with one payment.
type InvoicePaymentMatch struct {
	InvoiceID string `json:"invoiceId"`
	PaymentID string `json:"paymentId"`
}

// GeneralizedAndJoinFlow implements WCP-33: each firing joins one invoice and one payment.
type GeneralizedAndJoinFlow struct{ dex.FlowDefaults }

func (*GeneralizedAndJoinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(MatchInvoiceAndPayment{}),
		dex.DefineStep(RecordMatch{}),
	}
}

func (flow *GeneralizedAndJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.DeliverInvoice, nil),
		dex.DefineRPC(flow.DeliverPayment, nil),
	}
}

func (*GeneralizedAndJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{InvoiceReceivedChannel, PaymentReceivedChannel},
	}
}

// DeliverInvoice queues one invoice for matching.
func (*GeneralizedAndJoinFlow) DeliverInvoice(
	ctx dex.Context,
	invoiceID string,
) (*dex.RPCResult[dex.None], error) {
	if err := InvoiceReceivedChannel.Publish(ctx, invoiceID); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// DeliverPayment queues one payment for matching.
func (*GeneralizedAndJoinFlow) DeliverPayment(
	ctx dex.Context,
	paymentID string,
) (*dex.RPCResult[dex.None], error) {
	if err := PaymentReceivedChannel.Publish(ctx, paymentID); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type MatchInvoiceAndPayment struct{ dex.StepDefaults }

func (MatchInvoiceAndPayment) WaitFor(dex.Context, MatchProgress) (*dex.Wait, error) {
	return dex.AllOf(InvoiceReceivedChannel.ForOne(), PaymentReceivedChannel.ForOne()), nil
}

func (MatchInvoiceAndPayment) Execute(ctx dex.Context, progress MatchProgress) (*dex.StepDecision, error) {
	invoices, err := InvoiceReceivedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	payments, err := PaymentReceivedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	movements := []dex.StepMovement{
		dex.MovementOf(RecordMatch{}, InvoicePaymentMatch{InvoiceID: invoices[0], PaymentID: payments[0]}),
	}
	matchedCount := progress.MatchedCount + 1
	if matchedCount < progress.ExpectedMatches {
		movements = append(movements, dex.MovementOf(MatchInvoiceAndPayment{}, MatchProgress{
			ExpectedMatches: progress.ExpectedMatches,
			MatchedCount:    matchedCount,
		}))
	}
	return dex.GoToMany(movements...), nil
}

type RecordMatch struct {
	dex.StepDefaultsNoWaitFor[InvoicePaymentMatch]
}

func (RecordMatch) Execute(_ dex.Context, match InvoicePaymentMatch) (*dex.StepDecision, error) {
	return dex.GracefulComplete(match), nil
}

var ParcelPackedJoin = dex.DefineJoin[int]("parcelPacked")

// ThreadSplitMergeFlow implements WCP-41 and WCP-42 with k PackParcel executions.
type ThreadSplitMergeFlow struct{ dex.FlowDefaults }

func (*ThreadSplitMergeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SplitShipment{}),
		dex.DefineStep(PackParcel{}),
		dex.DefineStep(MergeParcels{}),
	}
}

func (*ThreadSplitMergeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ParcelPackedJoin}}
}

type SplitShipment struct{ dex.StepDefaultsNoWaitFor[int] }

func (SplitShipment) Execute(ctx dex.Context, parcelCount int) (*dex.StepDecision, error) {
	branchBuilders := make([]func(dex.JoinRound) dex.StepMovement, 0, parcelCount)
	for index := range parcelCount {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(PackParcel{}, ParcelBranch{Index: index, Round: round})
		})
	}
	movements, err := ParcelPackedJoin.Fork(ctx, branchBuilders, func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(MergeParcels{}, round)
	})
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

// ParcelBranch is the input of one PackParcel thread.
type ParcelBranch struct {
	Index int           `json:"index"`
	Round dex.JoinRound `json:"round"`
}

type PackParcel struct {
	dex.StepDefaultsNoWaitFor[ParcelBranch]
}

func (PackParcel) Execute(ctx dex.Context, branch ParcelBranch) (*dex.StepDecision, error) {
	return ParcelPackedJoin.EndBranch(ctx, branch.Round, branch.Index)
}

type MergeParcels struct{ dex.StepDefaults }

func (MergeParcels) WaitFor(_ dex.Context, round dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(ParcelPackedJoin.ForAllBranches(round)), nil
}

func (MergeParcels) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	indexes, err := ParcelPackedJoin.BranchResults(ctx, round)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(indexes)), nil
}

var (
	_ dex.Flow = (*GeneralizedAndJoinFlow)(nil)
	_ dex.Flow = (*ThreadSplitMergeFlow)(nil)
)

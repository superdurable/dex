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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	SupplierAReleasedChannel = dex.DefineChannel[string]("supplierAReleased")
	SupplierBReleasedChannel = dex.DefineChannel[string]("supplierBReleased")
	SupplierCReleasedChannel = dex.DefineChannel[string]("supplierCReleased")
	QuoteReceivedChannel     = dex.DefineChannel[string]("quoteReceived")
)

const supplierCount = 3

// StructuredPartialJoinFlow implements WCP-9 Structured Discriminator with
// requiredQuotes 1 and WCP-30 Structured Partial Join with requiredQuotes 2.
// ResetQuoteJoin consumes the remaining quotes so the join resets.
type StructuredPartialJoinFlow struct{ dex.FlowDefaults }

func (*StructuredPartialJoinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RequestQuotes{}),
		dex.DefineStep(QuoteFromSupplierA{}),
		dex.DefineStep(QuoteFromSupplierB{}),
		dex.DefineStep(QuoteFromSupplierC{}),
		dex.DefineStep(AcceptQuotes{}),
		dex.DefineStep(PlaceOrder{}),
		dex.DefineStep(ResetQuoteJoin{}),
	}
}

func (flow *StructuredPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseSupplierQuote, nil)}
}

func (*StructuredPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{
			SupplierAReleasedChannel,
			SupplierBReleasedChannel,
			SupplierCReleasedChannel,
			QuoteReceivedChannel,
		},
	}
}

// ReleaseSupplierQuote lets supplier "A", "B", or "C" send its quote.
func (*StructuredPartialJoinFlow) ReleaseSupplierQuote(
	ctx dex.Context,
	supplier string,
) (*dex.RPCResult[dex.None], error) {
	channel, err := selectLetterChannel(
		supplier,
		SupplierAReleasedChannel,
		SupplierBReleasedChannel,
		SupplierCReleasedChannel,
	)
	if err != nil {
		return nil, err
	}
	if err := channel.Publish(ctx, supplier); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type RequestQuotes struct{ dex.StepDefaultsNoWaitFor[int] }

func (RequestQuotes) Execute(_ dex.Context, requiredQuotes int) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(QuoteFromSupplierA{}, nil),
		dex.MovementOf(QuoteFromSupplierB{}, nil),
		dex.MovementOf(QuoteFromSupplierC{}, nil),
		dex.MovementOf(AcceptQuotes{}, requiredQuotes),
	), nil
}

type QuoteFromSupplierA struct{ dex.StepDefaults }

func (QuoteFromSupplierA) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(SupplierAReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierA) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, QuoteReceivedChannel, "A")
}

type QuoteFromSupplierB struct{ dex.StepDefaults }

func (QuoteFromSupplierB) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(SupplierBReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierB) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, QuoteReceivedChannel, "B")
}

type QuoteFromSupplierC struct{ dex.StepDefaults }

func (QuoteFromSupplierC) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(SupplierCReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierC) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, QuoteReceivedChannel, "C")
}

type AcceptQuotes struct{ dex.StepDefaults }

func (AcceptQuotes) WaitFor(_ dex.Context, requiredQuotes int) (*dex.Wait, error) {
	return dex.Until(QuoteReceivedChannel.ForN(requiredQuotes)), nil
}

func (AcceptQuotes) Execute(ctx dex.Context, requiredQuotes int) (*dex.StepDecision, error) {
	accepted, err := QuoteReceivedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(PlaceOrder{}, accepted),
		dex.MovementOf(ResetQuoteJoin{}, supplierCount-requiredQuotes),
	), nil
}

type PlaceOrder struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (PlaceOrder) Execute(_ dex.Context, accepted []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(sortedCopy(accepted)), nil
}

type ResetQuoteJoin struct{ dex.StepDefaults }

func (ResetQuoteJoin) WaitFor(_ dex.Context, remainingQuotes int) (*dex.Wait, error) {
	if remainingQuotes == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(QuoteReceivedChannel.ForN(remainingQuotes)), nil
}

func (ResetQuoteJoin) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	ignored, err := QuoteReceivedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(ignored)), nil
}

var (
	BranchASignalChannel = dex.DefineChannel[string]("branchASignal")
	BranchBSignalChannel = dex.DefineChannel[string]("branchBSignal")
	BranchCSignalChannel = dex.DefineChannel[string]("branchCSignal")
)

// BlockingJoinRound is the input of one BlockingJoin round.
type BlockingJoinRound struct {
	RequiredSignals int `json:"requiredSignals"`
	Round           int `json:"round"`
}

// ProcessedRound records the branches that fired one BlockingJoin round.
type ProcessedRound struct {
	Round    int      `json:"round"`
	Branches []string `json:"branches"`
}

// BlockingJoinReset lists the branches a round still waits for before it resets.
type BlockingJoinReset struct {
	RequiredSignals int      `json:"requiredSignals"`
	Round           int      `json:"round"`
	PendingBranches []string `json:"pendingBranches"`
}

// BlockingPartialJoinFlow implements WCP-28 Blocking Discriminator with
// requiredSignals 1 and WCP-31 Blocking Partial Join with requiredSignals 2.
// Signals sent before the reset stay queued for the next round.
type BlockingPartialJoinFlow struct{ dex.FlowDefaults }

func (*BlockingPartialJoinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(BlockingJoin{}),
		dex.DefineStep(ProcessRound{}),
		dex.DefineStep(ResetBlockingJoin{}),
	}
}

func (flow *BlockingPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.SendBranchSignal, nil)}
}

func (*BlockingPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{BranchASignalChannel, BranchBSignalChannel, BranchCSignalChannel},
	}
}

// SendBranchSignal signals branch "A", "B", or "C".
func (*BlockingPartialJoinFlow) SendBranchSignal(
	ctx dex.Context,
	branch string,
) (*dex.RPCResult[dex.None], error) {
	channel, err := selectLetterChannel(branch, BranchASignalChannel, BranchBSignalChannel, BranchCSignalChannel)
	if err != nil {
		return nil, err
	}
	if err := channel.Publish(ctx, branch); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type BlockingJoin struct{ dex.StepDefaults }

func (BlockingJoin) WaitFor(_ dex.Context, round BlockingJoinRound) (*dex.Wait, error) {
	switch round.RequiredSignals {
	case 1:
		return dex.AnyOf(
			BranchASignalChannel.ForOne(),
			BranchBSignalChannel.ForOne(),
			BranchCSignalChannel.ForOne(),
		), nil
	case 2:
		return dex.AnyComboOf(
			dex.Combo(
				BranchASignalChannel.ForOne(dex.WithConditionID("ab-a")),
				BranchBSignalChannel.ForOne(dex.WithConditionID("ab-b")),
			),
			dex.Combo(
				BranchASignalChannel.ForOne(dex.WithConditionID("ac-a")),
				BranchCSignalChannel.ForOne(dex.WithConditionID("ac-c")),
			),
			dex.Combo(
				BranchBSignalChannel.ForOne(dex.WithConditionID("bc-b")),
				BranchCSignalChannel.ForOne(dex.WithConditionID("bc-c")),
			),
		), nil
	default:
		return nil, fmt.Errorf("requiredSignals must be 1 or 2, got %d", round.RequiredSignals)
	}
}

func (BlockingJoin) Execute(ctx dex.Context, round BlockingJoinRound) (*dex.StepDecision, error) {
	fired := []string{}
	pending := []string{}
	for _, branch := range []struct {
		letter  string
		channel dex.Channel[string]
	}{
		{"A", BranchASignalChannel},
		{"B", BranchBSignalChannel},
		{"C", BranchCSignalChannel},
	} {
		signals, err := branch.channel.GetConditionResults(ctx)
		if err != nil {
			return nil, err
		}
		if len(signals) > 0 {
			fired = append(fired, branch.letter)
		} else {
			pending = append(pending, branch.letter)
		}
	}
	return dex.GoToMany(
		dex.MovementOf(ProcessRound{}, ProcessedRound{Round: round.Round, Branches: fired}),
		dex.MovementOf(ResetBlockingJoin{}, BlockingJoinReset{
			RequiredSignals: round.RequiredSignals,
			Round:           round.Round,
			PendingBranches: pending,
		}),
	), nil
}

type ProcessRound struct {
	dex.StepDefaultsNoWaitFor[ProcessedRound]
}

func (ProcessRound) Execute(_ dex.Context, processed ProcessedRound) (*dex.StepDecision, error) {
	return dex.GracefulComplete(processed), nil
}

type ResetBlockingJoin struct{ dex.StepDefaults }

func (ResetBlockingJoin) WaitFor(_ dex.Context, reset BlockingJoinReset) (*dex.Wait, error) {
	conditions := make([]dex.Condition, 0, len(reset.PendingBranches))
	for _, branch := range reset.PendingBranches {
		channel, err := selectLetterChannel(branch, BranchASignalChannel, BranchBSignalChannel, BranchCSignalChannel)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, channel.ForOne())
	}
	if len(conditions) == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.AllOf(conditions...), nil
}

func (ResetBlockingJoin) Execute(_ dex.Context, reset BlockingJoinReset) (*dex.StepDecision, error) {
	if reset.Round < 2 {
		return dex.GoTo(BlockingJoin{}, BlockingJoinRound{
			RequiredSignals: reset.RequiredSignals,
			Round:           reset.Round + 1,
		}), nil
	}
	return dex.GracefulComplete(reset.Round), nil
}

var (
	CarrierAReleasedChannel = dex.DefineChannel[string]("carrierAReleased")
	CarrierBReleasedChannel = dex.DefineChannel[string]("carrierBReleased")
	CarrierCReleasedChannel = dex.DefineChannel[string]("carrierCReleased")
	BidReceivedChannel      = dex.DefineChannel[string]("bidReceived")
)

// CancellingPartialJoinFlow implements WCP-29 Cancelling Discriminator with
// requiredBids 1 and WCP-32 Cancelling Partial Join with requiredBids 2.
type CancellingPartialJoinFlow struct{ dex.FlowDefaults }

func (*CancellingPartialJoinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RequestBids{}),
		dex.DefineStep(BidFromCarrierA{}),
		dex.DefineStep(BidFromCarrierB{}),
		dex.DefineStep(BidFromCarrierC{}),
		dex.DefineStep(AcceptBids{}),
	}
}

func (flow *CancellingPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseCarrierBid, nil)}
}

func (*CancellingPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{
			CarrierAReleasedChannel,
			CarrierBReleasedChannel,
			CarrierCReleasedChannel,
			BidReceivedChannel,
		},
	}
}

// ReleaseCarrierBid lets carrier "A", "B", or "C" send its bid.
func (*CancellingPartialJoinFlow) ReleaseCarrierBid(
	ctx dex.Context,
	carrier string,
) (*dex.RPCResult[dex.None], error) {
	channel, err := selectLetterChannel(
		carrier,
		CarrierAReleasedChannel,
		CarrierBReleasedChannel,
		CarrierCReleasedChannel,
	)
	if err != nil {
		return nil, err
	}
	if err := channel.Publish(ctx, carrier); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type RequestBids struct{ dex.StepDefaultsNoWaitFor[int] }

func (RequestBids) Execute(_ dex.Context, requiredBids int) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(BidFromCarrierA{}, nil),
		dex.MovementOf(BidFromCarrierB{}, nil),
		dex.MovementOf(BidFromCarrierC{}, nil),
		dex.MovementOf(AcceptBids{}, requiredBids),
	), nil
}

type BidFromCarrierA struct{ dex.StepDefaults }

func (BidFromCarrierA) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CarrierAReleasedChannel.ForOne()), nil
}

func (BidFromCarrierA) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, BidReceivedChannel, "A")
}

type BidFromCarrierB struct{ dex.StepDefaults }

func (BidFromCarrierB) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CarrierBReleasedChannel.ForOne()), nil
}

func (BidFromCarrierB) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, BidReceivedChannel, "B")
}

type BidFromCarrierC struct{ dex.StepDefaults }

func (BidFromCarrierC) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CarrierCReleasedChannel.ForOne()), nil
}

func (BidFromCarrierC) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return publishAndDeadEnd(ctx, BidReceivedChannel, "C")
}

type AcceptBids struct{ dex.StepDefaults }

func (AcceptBids) WaitFor(_ dex.Context, requiredBids int) (*dex.Wait, error) {
	return dex.Until(BidReceivedChannel.ForN(requiredBids)), nil
}

func (AcceptBids) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	accepted, err := BidReceivedChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(sortedCopy(accepted)).
		CancelSteps(BidFromCarrierA{}, BidFromCarrierB{}, BidFromCarrierC{}), nil
}

// selectLetterChannel returns the Channel for letter "A", "B", or "C", or an input error.
func selectLetterChannel(
	letter string,
	channelA dex.Channel[string],
	channelB dex.Channel[string],
	channelC dex.Channel[string],
) (dex.Channel[string], error) {
	switch letter {
	case "A":
		return channelA, nil
	case "B":
		return channelB, nil
	case "C":
		return channelC, nil
	default:
		return dex.Channel[string]{}, status.Errorf(codes.InvalidArgument, "%q must be A, B, or C", letter)
	}
}

func publishAndDeadEnd[T any](ctx dex.Context, channel dex.Channel[T], value T) (*dex.StepDecision, error) {
	if err := channel.Publish(ctx, value); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

var (
	_ dex.Flow = (*StructuredPartialJoinFlow)(nil)
	_ dex.Flow = (*BlockingPartialJoinFlow)(nil)
	_ dex.Flow = (*CancellingPartialJoinFlow)(nil)
)

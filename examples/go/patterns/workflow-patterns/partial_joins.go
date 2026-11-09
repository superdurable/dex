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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	SupplierAReleasedChannel = dex.DefineChannel[string]("supplierAReleased")
	SupplierBReleasedChannel = dex.DefineChannel[string]("supplierBReleased")
	SupplierCReleasedChannel = dex.DefineChannel[string]("supplierCReleased")
	QuoteReceivedChannelMap  = dex.DefineChannelMap[string]("quoteReceived")
)

const (
	supplierCount          = 3
	branchCount            = 3
	blockingJoinRoundCount = 2
)

// StructuredPartialJoinFlow implements WCP-9 (requiredQuotes 1) and WCP-30 (requiredQuotes 2).
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
			QuoteReceivedChannelMap,
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

// QuoteRound is the input of one structured partial join round.
type QuoteRound struct {
	RequiredQuotes int `json:"requiredQuotes"`
	Round          int `json:"round"`
	RoundCount     int `json:"roundCount"`
}

// AcceptedQuotes lists the quotes that fired the join in one round.
type AcceptedQuotes struct {
	Round          int      `json:"round"`
	AcceptedQuotes []string `json:"acceptedQuotes"`
}

// IgnoredQuotes lists the quotes the last reset consumed.
type IgnoredQuotes struct {
	Round         int      `json:"round"`
	IgnoredQuotes []string `json:"ignoredQuotes"`
}

type RequestQuotes struct {
	dex.StepDefaultsNoWaitFor[QuoteRound]
}

func (RequestQuotes) Execute(_ dex.Context, round QuoteRound) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(QuoteFromSupplierA{}, round.Round),
		dex.MovementOf(QuoteFromSupplierB{}, round.Round),
		dex.MovementOf(QuoteFromSupplierC{}, round.Round),
		dex.MovementOf(AcceptQuotes{}, round),
	), nil
}

type QuoteFromSupplierA struct{ dex.StepDefaults }

func (QuoteFromSupplierA) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(SupplierAReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierA) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, QuoteReceivedChannelMap, round, "A")
}

type QuoteFromSupplierB struct{ dex.StepDefaults }

func (QuoteFromSupplierB) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(SupplierBReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierB) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, QuoteReceivedChannelMap, round, "B")
}

type QuoteFromSupplierC struct{ dex.StepDefaults }

func (QuoteFromSupplierC) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(SupplierCReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierC) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, QuoteReceivedChannelMap, round, "C")
}

type AcceptQuotes struct{ dex.StepDefaults }

func (AcceptQuotes) WaitFor(_ dex.Context, round QuoteRound) (*dex.Wait, error) {
	return dex.Until(QuoteReceivedChannelMap.ForN(roundInstance(round.Round), round.RequiredQuotes)), nil
}

func (AcceptQuotes) Execute(ctx dex.Context, round QuoteRound) (*dex.StepDecision, error) {
	accepted, err := QuoteReceivedChannelMap.GetConditionResults(ctx, roundInstance(round.Round))
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(PlaceOrder{}, AcceptedQuotes{Round: round.Round, AcceptedQuotes: sortedCopy(accepted)}),
		dex.MovementOf(ResetQuoteJoin{}, round),
	), nil
}

type PlaceOrder struct {
	dex.StepDefaultsNoWaitFor[AcceptedQuotes]
}

func (PlaceOrder) Execute(_ dex.Context, accepted AcceptedQuotes) (*dex.StepDecision, error) {
	return dex.GracefulComplete(accepted), nil
}

// ResetQuoteJoin consumes the rest of the round, then re-enables the join.
type ResetQuoteJoin struct{ dex.StepDefaults }

func (ResetQuoteJoin) WaitFor(_ dex.Context, round QuoteRound) (*dex.Wait, error) {
	remainingQuotes := supplierCount - round.RequiredQuotes
	if remainingQuotes == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(QuoteReceivedChannelMap.ForN(roundInstance(round.Round), remainingQuotes)), nil
}

func (ResetQuoteJoin) Execute(ctx dex.Context, round QuoteRound) (*dex.StepDecision, error) {
	ignored, err := QuoteReceivedChannelMap.GetConditionResults(ctx, roundInstance(round.Round))
	if err != nil {
		return nil, err
	}
	if round.Round < round.RoundCount {
		next := round
		next.Round++
		return dex.GoTo(RequestQuotes{}, next), nil
	}
	return dex.GracefulComplete(IgnoredQuotes{Round: round.Round, IgnoredQuotes: sortedCopy(ignored)}), nil
}

var (
	BranchASignalChannel      = dex.DefineChannel[string]("branchASignal")
	BranchBSignalChannel      = dex.DefineChannel[string]("branchBSignal")
	BranchCSignalChannel      = dex.DefineChannel[string]("branchCSignal")
	BranchCompletedChannelMap = dex.DefineChannelMap[string]("branchCompleted")
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

// BlockingPartialJoinFlow implements WCP-28 (requiredSignals 1) and WCP-31 (requiredSignals 2).
type BlockingPartialJoinFlow struct{ dex.FlowDefaults }

func (*BlockingPartialJoinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartBlockingRound{}),
		dex.DefineStep(SignalFromBranchA{}),
		dex.DefineStep(SignalFromBranchB{}),
		dex.DefineStep(SignalFromBranchC{}),
		dex.DefineStep(BlockingJoin{}),
		dex.DefineStep(ProcessRound{}),
		dex.DefineStep(ResetBlockingJoin{}),
	}
}

func (flow *BlockingPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SendBranchSignal, nil),
		dex.DefineRPC(flow.GetQueuedBranchSignalCount, &dex.RPCOptions{
			LoadChannels: []dex.ChannelDef{BranchASignalChannel, BranchBSignalChannel, BranchCSignalChannel},
		}),
	}
}

func (*BlockingPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{
			BranchASignalChannel,
			BranchBSignalChannel,
			BranchCSignalChannel,
			BranchCompletedChannelMap,
		},
	}
}

// SendBranchSignal enables branch "A", "B", or "C".
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

// GetQueuedBranchSignalCount returns how many branch signals no Step has consumed yet.
func (*BlockingPartialJoinFlow) GetQueuedBranchSignalCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	queuedCount, err := countPendingMessages(ctx, BranchASignalChannel, BranchBSignalChannel, BranchCSignalChannel)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: queuedCount}, nil
}

type StartBlockingRound struct {
	dex.StepDefaultsNoWaitFor[BlockingJoinRound]
}

func (StartBlockingRound) Execute(_ dex.Context, round BlockingJoinRound) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(SignalFromBranchA{}, round.Round),
		dex.MovementOf(SignalFromBranchB{}, round.Round),
		dex.MovementOf(SignalFromBranchC{}, round.Round),
		dex.MovementOf(BlockingJoin{}, round),
	), nil
}

type SignalFromBranchA struct{ dex.StepDefaults }

func (SignalFromBranchA) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(BranchASignalChannel.ForOne()), nil
}

func (SignalFromBranchA) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, BranchCompletedChannelMap, round, "A")
}

type SignalFromBranchB struct{ dex.StepDefaults }

func (SignalFromBranchB) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(BranchBSignalChannel.ForOne()), nil
}

func (SignalFromBranchB) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, BranchCompletedChannelMap, round, "B")
}

type SignalFromBranchC struct{ dex.StepDefaults }

func (SignalFromBranchC) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(BranchCSignalChannel.ForOne()), nil
}

func (SignalFromBranchC) Execute(ctx dex.Context, round int) (*dex.StepDecision, error) {
	return publishRoundSignalAndDeadEnd(ctx, BranchCompletedChannelMap, round, "C")
}

type BlockingJoin struct{ dex.StepDefaults }

func (BlockingJoin) WaitFor(_ dex.Context, round BlockingJoinRound) (*dex.Wait, error) {
	return dex.Until(BranchCompletedChannelMap.ForN(roundInstance(round.Round), round.RequiredSignals)), nil
}

func (BlockingJoin) Execute(ctx dex.Context, round BlockingJoinRound) (*dex.StepDecision, error) {
	fired, err := BranchCompletedChannelMap.GetConditionResults(ctx, roundInstance(round.Round))
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(ProcessRound{}, ProcessedRound{Round: round.Round, Branches: sortedCopy(fired)}),
		dex.MovementOf(ResetBlockingJoin{}, round),
	), nil
}

type ProcessRound struct {
	dex.StepDefaultsNoWaitFor[ProcessedRound]
}

func (ProcessRound) Execute(_ dex.Context, processed ProcessedRound) (*dex.StepDecision, error) {
	return dex.GracefulComplete(processed), nil
}

// ResetBlockingJoin waits for the rest of the round before the next round may start.
type ResetBlockingJoin struct{ dex.StepDefaults }

func (ResetBlockingJoin) WaitFor(_ dex.Context, round BlockingJoinRound) (*dex.Wait, error) {
	remainingSignals := branchCount - round.RequiredSignals
	if remainingSignals == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(BranchCompletedChannelMap.ForN(roundInstance(round.Round), remainingSignals)), nil
}

func (ResetBlockingJoin) Execute(_ dex.Context, round BlockingJoinRound) (*dex.StepDecision, error) {
	if round.Round < blockingJoinRoundCount {
		next := round
		next.Round++
		return dex.GoTo(StartBlockingRound{}, next), nil
	}
	return dex.GracefulComplete(round.Round), nil
}

var (
	CarrierAReleasedChannel        = dex.DefineChannel[string]("carrierAReleased")
	CarrierBReleasedChannel        = dex.DefineChannel[string]("carrierBReleased")
	CarrierCReleasedChannel        = dex.DefineChannel[string]("carrierCReleased")
	BidReceivedChannel             = dex.DefineChannel[string]("bidReceived")
	CarrierBookingConfirmedChannel = dex.DefineChannel[string]("carrierBookingConfirmed")
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
		dex.DefineStep(ConfirmCarrierBooking{}),
	}
}

func (flow *CancellingPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ReleaseCarrierBid, nil),
		dex.DefineRPC(flow.GetQueuedCarrierReleaseCount, &dex.RPCOptions{
			LoadChannels: []dex.ChannelDef{CarrierAReleasedChannel, CarrierBReleasedChannel, CarrierCReleasedChannel},
		}),
		dex.DefineRPC(flow.ConfirmCarrierBooking, nil),
	}
}

func (*CancellingPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{
			CarrierAReleasedChannel,
			CarrierBReleasedChannel,
			CarrierCReleasedChannel,
			BidReceivedChannel,
			CarrierBookingConfirmedChannel,
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

// GetQueuedCarrierReleaseCount returns how many carrier releases no Step has consumed yet.
func (*CancellingPartialJoinFlow) GetQueuedCarrierReleaseCount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	queuedCount, err := countPendingMessages(
		ctx,
		CarrierAReleasedChannel,
		CarrierBReleasedChannel,
		CarrierCReleasedChannel,
	)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: queuedCount}, nil
}

// ConfirmCarrierBooking confirms the booking with the accepted carriers.
func (*CancellingPartialJoinFlow) ConfirmCarrierBooking(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := CarrierBookingConfirmedChannel.Publish(ctx, "confirmed"); err != nil {
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
	return dex.GoTo(ConfirmCarrierBooking{}, sortedCopy(accepted)).
		CancelSteps(BidFromCarrierA{}, BidFromCarrierB{}, BidFromCarrierC{}), nil
}

type ConfirmCarrierBooking struct{ dex.StepDefaults }

func (ConfirmCarrierBooking) WaitFor(dex.Context, []string) (*dex.Wait, error) {
	return dex.Until(CarrierBookingConfirmedChannel.ForOne()), nil
}

func (ConfirmCarrierBooking) Execute(_ dex.Context, accepted []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(accepted), nil
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

// countPendingMessages returns the number of unconsumed messages across channels.
func countPendingMessages(ctx dex.Context, channels ...dex.Channel[string]) (int, error) {
	pendingCount := 0
	for _, channel := range channels {
		messages, err := channel.PendingMessages(ctx)
		if err != nil {
			return 0, err
		}
		pendingCount += len(messages)
	}
	return pendingCount, nil
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

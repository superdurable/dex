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
	QuoteReceivedJoin        = dex.DefineJoin[string]("quoteReceived")
	IgnoredQuotesAttribute   = dex.DefineAttribute[IgnoredQuotes]("ignoredQuotes")
)

const blockingJoinRoundCount = 2

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
	}
}

func (flow *StructuredPartialJoinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseSupplierQuote, nil)}
}

func (*StructuredPartialJoinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{IgnoredQuotesAttribute},
		Channels: []dex.ChannelDef{
			SupplierAReleasedChannel,
			SupplierBReleasedChannel,
			SupplierCReleasedChannel,
			QuoteReceivedJoin,
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
	if err := channel.Publish(ctx, "released"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// QuoteRound is the input of one structured partial join round.
type QuoteRound struct {
	RequiredQuotes int            `json:"requiredQuotes"`
	Round          int            `json:"round"`
	RoundCount     int            `json:"roundCount"`
	PreviousRound  *dex.JoinRound `json:"previousRound,omitempty"`
}

// QuoteJoinInput is the input of the AcceptQuotes join of one round.
type QuoteJoinInput struct {
	Request   QuoteRound    `json:"request"`
	JoinRound dex.JoinRound `json:"joinRound"`
}

// AcceptedQuotes lists the quotes that fired the join in one round.
type AcceptedQuotes struct {
	Round          int      `json:"round"`
	AcceptedQuotes []string `json:"acceptedQuotes"`
}

// IgnoredQuotes lists the late quotes that the next round's reset consumed.
type IgnoredQuotes struct {
	Round         int      `json:"round"`
	IgnoredQuotes []string `json:"ignoredQuotes"`
}

// RequestQuotes resets the previous round, then starts the next one.
type RequestQuotes struct{ dex.StepDefaults }

func (RequestQuotes) WaitFor(_ dex.Context, round QuoteRound) (*dex.Wait, error) {
	if round.PreviousRound == nil {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(QuoteReceivedJoin.ForRemainingBranches(*round.PreviousRound, round.RequiredQuotes)), nil
}

func (RequestQuotes) Execute(ctx dex.Context, round QuoteRound) (*dex.StepDecision, error) {
	if round.PreviousRound != nil {
		ignored, err := QuoteReceivedJoin.RemainingBranchResults(ctx, *round.PreviousRound)
		if err != nil {
			return nil, err
		}
		ignoredQuotes := IgnoredQuotes{Round: round.Round - 1, IgnoredQuotes: sortedCopy(ignored)}
		if err := IgnoredQuotesAttribute.Set(ctx, ignoredQuotes); err != nil {
			return nil, err
		}
	}
	movements, err := QuoteReceivedJoin.Fork(ctx,
		[]func(dex.JoinRound) dex.StepMovement{
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierA{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierB{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierC{}, joinRound) },
		},
		func(joinRound dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(AcceptQuotes{}, QuoteJoinInput{Request: round, JoinRound: joinRound})
		},
	)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type QuoteFromSupplierA struct{ dex.StepDefaults }

func (QuoteFromSupplierA) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(SupplierAReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierA) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return QuoteReceivedJoin.EndBranch(ctx, joinRound, "A")
}

type QuoteFromSupplierB struct{ dex.StepDefaults }

func (QuoteFromSupplierB) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(SupplierBReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierB) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return QuoteReceivedJoin.EndBranch(ctx, joinRound, "B")
}

type QuoteFromSupplierC struct{ dex.StepDefaults }

func (QuoteFromSupplierC) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(SupplierCReleasedChannel.ForOne()), nil
}

func (QuoteFromSupplierC) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return QuoteReceivedJoin.EndBranch(ctx, joinRound, "C")
}

type AcceptQuotes struct{ dex.StepDefaults }

func (AcceptQuotes) WaitFor(_ dex.Context, input QuoteJoinInput) (*dex.Wait, error) {
	return dex.Until(QuoteReceivedJoin.ForBranches(input.JoinRound, input.Request.RequiredQuotes)), nil
}

func (AcceptQuotes) Execute(ctx dex.Context, input QuoteJoinInput) (*dex.StepDecision, error) {
	accepted, err := QuoteReceivedJoin.BranchResults(ctx, input.JoinRound)
	if err != nil {
		return nil, err
	}
	round := input.Request
	movements := []dex.StepMovement{
		dex.MovementOf(PlaceOrder{}, AcceptedQuotes{Round: round.Round, AcceptedQuotes: sortedCopy(accepted)}),
	}
	// The last round's late quotes stay pending until the Flow closes.
	if round.Round < round.RoundCount {
		next := round
		next.Round++
		next.PreviousRound = &input.JoinRound
		movements = append(movements, dex.MovementOf(RequestQuotes{}, next))
	}
	return dex.GoToMany(movements...), nil
}

type PlaceOrder struct {
	dex.StepDefaultsNoWaitFor[AcceptedQuotes]
}

func (PlaceOrder) Execute(_ dex.Context, accepted AcceptedQuotes) (*dex.StepDecision, error) {
	return dex.GracefulComplete(accepted), nil
}

var (
	BranchASignalChannel = dex.DefineChannel[string]("branchASignal")
	BranchBSignalChannel = dex.DefineChannel[string]("branchBSignal")
	BranchCSignalChannel = dex.DefineChannel[string]("branchCSignal")
	BranchCompletedJoin  = dex.DefineJoin[string]("branchCompleted")
)

// BlockingJoinRound is the input of one BlockingJoin round.
type BlockingJoinRound struct {
	RequiredSignals int            `json:"requiredSignals"`
	Round           int            `json:"round"`
	PreviousRound   *dex.JoinRound `json:"previousRound,omitempty"`
}

// BlockingJoinInput is the input of the BlockingJoin of one round.
type BlockingJoinInput struct {
	Request   BlockingJoinRound `json:"request"`
	JoinRound dex.JoinRound     `json:"joinRound"`
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
			BranchCompletedJoin,
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

// StartBlockingRound waits for the rest of the previous round, then starts the next one.
type StartBlockingRound struct{ dex.StepDefaults }

func (StartBlockingRound) WaitFor(_ dex.Context, round BlockingJoinRound) (*dex.Wait, error) {
	if round.PreviousRound == nil {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(BranchCompletedJoin.ForRemainingBranches(*round.PreviousRound, round.RequiredSignals)), nil
}

func (StartBlockingRound) Execute(ctx dex.Context, round BlockingJoinRound) (*dex.StepDecision, error) {
	movements, err := BranchCompletedJoin.Fork(ctx,
		[]func(dex.JoinRound) dex.StepMovement{
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(SignalFromBranchA{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(SignalFromBranchB{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(SignalFromBranchC{}, joinRound) },
		},
		func(joinRound dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(BlockingJoin{}, BlockingJoinInput{Request: round, JoinRound: joinRound})
		},
	)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type SignalFromBranchA struct{ dex.StepDefaults }

func (SignalFromBranchA) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(BranchASignalChannel.ForOne()), nil
}

func (SignalFromBranchA) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BranchCompletedJoin.EndBranch(ctx, joinRound, "A")
}

type SignalFromBranchB struct{ dex.StepDefaults }

func (SignalFromBranchB) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(BranchBSignalChannel.ForOne()), nil
}

func (SignalFromBranchB) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BranchCompletedJoin.EndBranch(ctx, joinRound, "B")
}

type SignalFromBranchC struct{ dex.StepDefaults }

func (SignalFromBranchC) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(BranchCSignalChannel.ForOne()), nil
}

func (SignalFromBranchC) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BranchCompletedJoin.EndBranch(ctx, joinRound, "C")
}

type BlockingJoin struct{ dex.StepDefaults }

func (BlockingJoin) WaitFor(_ dex.Context, input BlockingJoinInput) (*dex.Wait, error) {
	return dex.Until(BranchCompletedJoin.ForBranches(input.JoinRound, input.Request.RequiredSignals)), nil
}

func (BlockingJoin) Execute(ctx dex.Context, input BlockingJoinInput) (*dex.StepDecision, error) {
	fired, err := BranchCompletedJoin.BranchResults(ctx, input.JoinRound)
	if err != nil {
		return nil, err
	}
	round := input.Request
	movements := []dex.StepMovement{
		dex.MovementOf(ProcessRound{}, ProcessedRound{Round: round.Round, Branches: sortedCopy(fired)}),
	}
	// The last round's late signals stay pending until the Flow closes.
	if round.Round < blockingJoinRoundCount {
		next := round
		next.Round++
		next.PreviousRound = &input.JoinRound
		movements = append(movements, dex.MovementOf(StartBlockingRound{}, next))
	}
	return dex.GoToMany(movements...), nil
}

type ProcessRound struct {
	dex.StepDefaultsNoWaitFor[ProcessedRound]
}

func (ProcessRound) Execute(_ dex.Context, processed ProcessedRound) (*dex.StepDecision, error) {
	return dex.GracefulComplete(processed), nil
}

var (
	CarrierAReleasedChannel        = dex.DefineChannel[string]("carrierAReleased")
	CarrierBReleasedChannel        = dex.DefineChannel[string]("carrierBReleased")
	CarrierCReleasedChannel        = dex.DefineChannel[string]("carrierCReleased")
	BidReceivedJoin                = dex.DefineJoin[string]("bidReceived")
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
			BidReceivedJoin,
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
	if err := channel.Publish(ctx, "released"); err != nil {
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

// BidJoinInput is the input of the AcceptBids join.
type BidJoinInput struct {
	RequiredBids int           `json:"requiredBids"`
	JoinRound    dex.JoinRound `json:"joinRound"`
}

type RequestBids struct{ dex.StepDefaultsNoWaitFor[int] }

func (RequestBids) Execute(ctx dex.Context, requiredBids int) (*dex.StepDecision, error) {
	movements, err := BidReceivedJoin.Fork(ctx,
		[]func(dex.JoinRound) dex.StepMovement{
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(BidFromCarrierA{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(BidFromCarrierB{}, joinRound) },
			func(joinRound dex.JoinRound) dex.StepMovement { return dex.MovementOf(BidFromCarrierC{}, joinRound) },
		},
		func(joinRound dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(AcceptBids{}, BidJoinInput{RequiredBids: requiredBids, JoinRound: joinRound})
		},
	)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type BidFromCarrierA struct{ dex.StepDefaults }

func (BidFromCarrierA) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(CarrierAReleasedChannel.ForOne()), nil
}

func (BidFromCarrierA) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BidReceivedJoin.EndBranch(ctx, joinRound, "A")
}

type BidFromCarrierB struct{ dex.StepDefaults }

func (BidFromCarrierB) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(CarrierBReleasedChannel.ForOne()), nil
}

func (BidFromCarrierB) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BidReceivedJoin.EndBranch(ctx, joinRound, "B")
}

type BidFromCarrierC struct{ dex.StepDefaults }

func (BidFromCarrierC) WaitFor(dex.Context, dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(CarrierCReleasedChannel.ForOne()), nil
}

func (BidFromCarrierC) Execute(ctx dex.Context, joinRound dex.JoinRound) (*dex.StepDecision, error) {
	return BidReceivedJoin.EndBranch(ctx, joinRound, "C")
}

type AcceptBids struct{ dex.StepDefaults }

func (AcceptBids) WaitFor(_ dex.Context, input BidJoinInput) (*dex.Wait, error) {
	return dex.Until(BidReceivedJoin.ForBranches(input.JoinRound, input.RequiredBids)), nil
}

func (AcceptBids) Execute(ctx dex.Context, input BidJoinInput) (*dex.StepDecision, error) {
	accepted, err := BidReceivedJoin.BranchResults(ctx, input.JoinRound)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(ConfirmCarrierBooking{}, sortedCopy(accepted)).
		CancelSiblingSteps(BidFromCarrierA{}, BidFromCarrierB{}, BidFromCarrierC{}), nil
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

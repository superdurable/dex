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

package deferredchoice

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// OfferExpiry is how long the customer has to respond.
const OfferExpiry = 3 * 24 * time.Hour

var (
	OfferAccepted = dex.DefineChannel[string]("offerAccepted")
	OfferRejected = dex.DefineChannel[string]("offerRejected")
)

// OfferResponseFlow routes on the first of three events: the customer accepts,
// the customer rejects, or the offer expires.
type OfferResponseFlow struct{ dex.FlowDefaults }

func NewOfferResponseFlow() *OfferResponseFlow {
	return &OfferResponseFlow{}
}

func (*OfferResponseFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitOfferResponse{}),
		dex.DefineStep(FulfillOffer{}),
		dex.DefineStep(CloseRejectedOffer{}),
		dex.DefineStep(ExpireOffer{}),
	}
}

func (flow *OfferResponseFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AcceptOffer, nil),
		dex.DefineRPC(flow.RejectOffer, nil),
	}
}

func (*OfferResponseFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{OfferAccepted, OfferRejected},
	}
}

// AcceptOffer records that the customer accepted the offer.
func (*OfferResponseFlow) AcceptOffer(ctx dex.Context, note string) (*dex.RPCResult[dex.None], error) {
	if err := OfferAccepted.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// RejectOffer records that the customer rejected the offer.
func (*OfferResponseFlow) RejectOffer(ctx dex.Context, note string) (*dex.RPCResult[dex.None], error) {
	if err := OfferRejected.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// AwaitOfferResponse waits for whichever event happens first and routes on it.
type AwaitOfferResponse struct{ dex.StepDefaults }

func (AwaitOfferResponse) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.AnyOf(
		OfferAccepted.ForOne(),
		OfferRejected.ForOne(),
		dex.Timer(OfferExpiry),
	), nil
}

func (AwaitOfferResponse) Execute(ctx dex.Context, offerID string) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.GoTo(ExpireOffer{}, offerID), nil
	}
	accepted, err := OfferAccepted.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(accepted) > 0 {
		return dex.GoTo(FulfillOffer{}, offerID), nil
	}
	return dex.GoTo(CloseRejectedOffer{}, offerID), nil
}

type FulfillOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (FulfillOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("fulfilled " + offerID), nil
}

type CloseRejectedOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (CloseRejectedOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("rejected " + offerID), nil
}

type ExpireOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (ExpireOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("expired " + offerID), nil
}

var _ dex.Flow = (*OfferResponseFlow)(nil)

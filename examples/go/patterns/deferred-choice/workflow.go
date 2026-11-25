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
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// OfferExpiry is how long the customer has to respond.
const OfferExpiry = 3 * 24 * time.Hour

// The response window is open until AwaitOfferResponse decides. Unset reads as open.
const (
	ResponseWindowOpen   = "open"
	ResponseWindowClosed = "closed"
)

// The outcomes of a customer response RPC.
const (
	OfferResponseRecorded     = "recorded"
	OfferResponseWindowClosed = "window closed"
)

var (
	OfferAccepted = dex.DefineChannel[string]("offerAccepted")
	OfferRejected = dex.DefineChannel[string]("offerRejected")
	// OfferResponseWindow is locked by both response RPCs and by AwaitOfferResponse.Execute.
	OfferResponseWindow = dex.DefineAttribute[string]("offerResponseWindow")
)

var responseWindowLock = []dex.AttributeLock{dex.LockAttribute(OfferResponseWindow)}

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
		dex.DefineStep(RouteResponseBeforeExpiry{}),
		dex.DefineStep(ExpireOffer{}),
	}
}

func (flow *OfferResponseFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AcceptCustomerOffer, &dex.RPCOptions{LockAttributes: responseWindowLock}),
		dex.DefineRPC(flow.RejectCustomerOffer, &dex.RPCOptions{LockAttributes: responseWindowLock}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*OfferResponseFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OfferResponseWindow},
		Channels:   []dex.ChannelDef{OfferAccepted, OfferRejected},
	}
}

// AcceptCustomerOffer records that the customer accepted the offer.
// It returns OfferResponseWindowClosed once the offer has decided.
func (*OfferResponseFlow) AcceptCustomerOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return publishWhileWindowOpen(ctx, OfferAccepted, note)
}

// RejectCustomerOffer records that the customer rejected the offer.
// It returns OfferResponseWindowClosed once the offer has decided.
func (*OfferResponseFlow) RejectCustomerOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return publishWhileWindowOpen(ctx, OfferRejected, note)
}

func (*OfferResponseFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*OfferResponseFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// publishWhileWindowOpen publishes the response unless the window is closed.
// The publish commits together with this check, because the RPC holds the window lock.
func publishWhileWindowOpen(ctx dex.Context, response dex.Channel[string], note string) (*dex.RPCResult[string], error) {
	window, err := readResponseWindow(ctx)
	if err != nil {
		return nil, err
	}
	if window == ResponseWindowClosed {
		return &dex.RPCResult[string]{Output: OfferResponseWindowClosed}, nil
	}
	if err := response.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: OfferResponseRecorded}, nil
}

// readResponseWindow returns ResponseWindowOpen while the window was never closed.
// It returns an error when the value cannot be read.
func readResponseWindow(ctx dex.Context) (string, error) {
	window, err := OfferResponseWindow.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return ResponseWindowOpen, nil
	}
	return window, err
}

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Waits for an acceptance, a rejection, or the 3-day deadline, closes the response window, and routes on the winner."
type AwaitOfferResponse struct{ dex.StepDefaults }

// GetStepOptions locks the response window in Execute, the same lock as the response RPCs.
func (AwaitOfferResponse) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: responseWindowLock}
}

func (AwaitOfferResponse) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.AnyOf(
		OfferAccepted.ForOne(),
		OfferRejected.ForOne(),
		dex.Timer(OfferExpiry),
	), nil
}

func (AwaitOfferResponse) Execute(ctx dex.Context, offerID string) (*dex.StepDecision, error) {
	// Every later response RPC now returns OfferResponseWindowClosed.
	if err := OfferResponseWindow.Set(ctx, ResponseWindowClosed); err != nil {
		return nil, err
	}
	if ctx.HasTimerFired() {
		// A response committed after the Timer won but before this Execute took the lock.
		if OfferAccepted.Size(ctx)+OfferRejected.Size(ctx) > 0 {
			return dex.GoTo(RouteResponseBeforeExpiry{}, offerID), nil
		}
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

// dex:group group-id:decide group-label:"Decide"
// dex:explanation text:"Takes the response that the customer sent before the window closed and routes on it."
type RouteResponseBeforeExpiry struct{ dex.StepDefaults }

func (RouteResponseBeforeExpiry) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.AnyOf(OfferAccepted.ForOne(), OfferRejected.ForOne()), nil
}

func (RouteResponseBeforeExpiry) Execute(ctx dex.Context, offerID string) (*dex.StepDecision, error) {
	accepted, err := OfferAccepted.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(accepted) > 0 {
		return dex.GoTo(FulfillOffer{}, offerID), nil
	}
	return dex.GoTo(CloseRejectedOffer{}, offerID), nil
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the fulfilled offer."
type FulfillOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (FulfillOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("fulfilled " + offerID), nil
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the rejected offer."
type CloseRejectedOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (CloseRejectedOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("rejected " + offerID), nil
}

// dex:group group-id:outcome group-label:"Outcome"
// dex:explanation text:"Completes the Flow with the expired offer."
type ExpireOffer struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (ExpireOffer) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("expired " + offerID), nil
}

var _ dex.Flow = (*OfferResponseFlow)(nil)

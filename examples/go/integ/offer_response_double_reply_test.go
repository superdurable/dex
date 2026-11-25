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

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	deferredchoice "github.com/superdurable/dex/examples/go/patterns/deferred-choice"
	"github.com/superdurable/dex/sdk-go/dex"
)

var offerDecisionGateOpened = dex.DefineChannel[dex.None]("offerDecisionGateOpened")

// OfferResponseDoubleReplyProbeFlow holds AwaitOfferResponse behind a gate,
// so both replies commit before any Execute can close the window.
type OfferResponseDoubleReplyProbeFlow struct{ dex.FlowDefaults }

func (*OfferResponseDoubleReplyProbeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(HoldOfferDecision{}),
		dex.DefineStep(deferredchoice.AwaitOfferResponse{}),
		dex.DefineStep(deferredchoice.FulfillOffer{}),
		dex.DefineStep(deferredchoice.CloseRejectedOffer{}),
		dex.DefineStep(deferredchoice.RouteResponseBeforeExpiry{}),
		dex.DefineStep(deferredchoice.ExpireOffer{}),
	}
}

func (flow *OfferResponseDoubleReplyProbeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AcceptGatedOffer, &dex.RPCOptions{LockAttributes: offerProbeResponseWindowLock}),
		dex.DefineRPC(flow.RejectGatedOffer, &dex.RPCOptions{LockAttributes: offerProbeResponseWindowLock}),
		dex.DefineRPC(flow.OpenOfferDecisionGate, nil),
	}
}

func (*OfferResponseDoubleReplyProbeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{deferredchoice.OfferResponseWindow},
		Channels:   []dex.ChannelDef{offerDecisionGateOpened, deferredchoice.OfferAccepted, deferredchoice.OfferRejected},
	}
}

func (*OfferResponseDoubleReplyProbeFlow) AcceptGatedOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return (&deferredchoice.OfferResponseFlow{}).AcceptCustomerOffer(ctx, note)
}

func (*OfferResponseDoubleReplyProbeFlow) RejectGatedOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return (&deferredchoice.OfferResponseFlow{}).RejectCustomerOffer(ctx, note)
}

func (*OfferResponseDoubleReplyProbeFlow) OpenOfferDecisionGate(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := offerDecisionGateOpened.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// HoldOfferDecision starts AwaitOfferResponse only after the test opens the decision gate.
type HoldOfferDecision struct{ dex.StepDefaults }

func (HoldOfferDecision) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.Until(offerDecisionGateOpened.ForOne()), nil
}

func (HoldOfferDecision) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GoTo(deferredchoice.AwaitOfferResponse{}, offerID), nil
}

func TestDeferredChoiceSecondReplyGetsWindowClosed(t *testing.T) {
	flow := &OfferResponseDoubleReplyProbeFlow{}
	flowID := startWorkflowPattern(t, flow, "pattern-deferred-choice-double", "offer-5")
	// AwaitOfferResponse has not started, so only the first reply can close the window.
	var first, second string
	invokeWorkflowPatternRPC(t, flowID, flow.AcceptGatedOffer, "yes", &first)
	invokeWorkflowPatternRPC(t, flowID, flow.RejectGatedOffer, "no", &second)
	require.Equal(t, deferredchoice.OfferResponseRecorded, first)
	require.Equal(t, deferredchoice.OfferResponseWindowClosed, second)

	invokeWorkflowPatternRPC(t, flowID, flow.OpenOfferDecisionGate, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"fulfilled offer-5"}, decodeStepOutputs[string](t, result, "FulfillOffer"))
	require.Empty(t, decodeStepOutputs[string](t, result, "CloseRejectedOffer"))
	require.Empty(t, decodeStepOutputs[string](t, result, "ExpireOffer"))
}

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
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	deferredchoice "github.com/superdurable/dex/examples/go/patterns/deferred-choice"
	"github.com/superdurable/dex/sdk-go/dex"
)

// OfferResponseWindowProbeFlow runs the Decision with Deadline Steps beside a gated Step,
// so the Flow stays open after the decision and late responses reach the window check.
type OfferResponseWindowProbeFlow struct{ dex.FlowDefaults }

// Each test that uses the held response RPC creates fresh channels before it starts.
var (
	heldResponseEntered  chan struct{}
	heldResponseReleased chan struct{}
)

var offerProbeResponseWindowLock = []dex.AttributeLock{dex.LockAttribute(deferredchoice.OfferResponseWindow)}

func (*OfferResponseWindowProbeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(OpenOfferProbe{}),
		dex.DefineStep(HoldOfferProbeOpen{}),
		dex.DefineStep(deferredchoice.AwaitOfferResponse{}),
		dex.DefineStep(deferredchoice.FulfillOffer{}),
		dex.DefineStep(deferredchoice.CloseRejectedOffer{}),
		dex.DefineStep(deferredchoice.RouteResponseBeforeExpiry{}),
		dex.DefineStep(deferredchoice.ExpireOffer{}),
	}
}

func (flow *OfferResponseWindowProbeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AcceptProbeOffer, &dex.RPCOptions{LockAttributes: offerProbeResponseWindowLock}),
		dex.DefineRPC(flow.RejectProbeOffer, &dex.RPCOptions{LockAttributes: offerProbeResponseWindowLock}),
		dex.DefineRPC(flow.AcceptProbeOfferWhenReleased, &dex.RPCOptions{LockAttributes: offerProbeResponseWindowLock}),
		dex.DefineRPC(flow.OpenProbeGate, nil),
	}
}

func (*OfferResponseWindowProbeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{deferredchoice.OfferResponseWindow},
		Channels:   []dex.ChannelDef{probeGateOpened, deferredchoice.OfferAccepted, deferredchoice.OfferRejected},
	}
}

func (*OfferResponseWindowProbeFlow) AcceptProbeOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return (&deferredchoice.OfferResponseFlow{}).AcceptCustomerOffer(ctx, note)
}

func (*OfferResponseWindowProbeFlow) RejectProbeOffer(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	return (&deferredchoice.OfferResponseFlow{}).RejectCustomerOffer(ctx, note)
}

// AcceptProbeOfferWhenReleased holds the window lock until the test releases it.
func (*OfferResponseWindowProbeFlow) AcceptProbeOfferWhenReleased(ctx dex.Context, note string) (*dex.RPCResult[string], error) {
	close(heldResponseEntered)
	<-heldResponseReleased
	return (&deferredchoice.OfferResponseFlow{}).AcceptCustomerOffer(ctx, note)
}

func (*OfferResponseWindowProbeFlow) OpenProbeGate(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := probeGateOpened.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type OpenOfferProbe struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (OpenOfferProbe) Execute(_ dex.Context, offerID string) (*dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(deferredchoice.AwaitOfferResponse{}, offerID),
		dex.MovementOf(HoldOfferProbeOpen{}, nil),
	), nil
}

// HoldOfferProbeOpen keeps the Flow open until the test opens the gate.
type HoldOfferProbeOpen struct{ dex.StepDefaults }

func (HoldOfferProbeOpen) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(probeGateOpened.ForOne()), nil
}

func (HoldOfferProbeOpen) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

// requireDeferredChoiceSmokeAcceptance accepts the offer over HTTP and waits for the fulfillment.
func requireDeferredChoiceSmokeAcceptance(t *testing.T, flowID string) {
	t.Helper()
	query := url.Values{"workflowId": {flowID}, "note": {"looks good"}}
	request, err := http.NewRequestWithContext(
		integrationContext(t),
		http.MethodGet,
		examplesAPIURL+"/patterns/deferred-choice/accept?"+query.Encode(),
		nil,
	)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	require.Equal(t, "offer response recorded", string(body))
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"fulfilled offer-smoke"}, decodeStepOutputs[string](t, result, "FulfillOffer"))
}

func TestDeferredChoiceResponseAfterTheDecisionGetsWindowClosed(t *testing.T) {
	flow := &OfferResponseWindowProbeFlow{}
	flowID := startWorkflowPattern(t, flow, "pattern-deferred-choice-window", "offer-3")
	var outcome string
	invokeWorkflowPatternRPC(t, flowID, flow.AcceptProbeOffer, "yes", &outcome)
	require.Equal(t, deferredchoice.OfferResponseRecorded, outcome)
	waitForWorkflowPatternStep(t, flowID, "AwaitOfferResponse")

	invokeWorkflowPatternRPC(t, flowID, flow.RejectProbeOffer, "changed my mind", &outcome)
	require.Equal(t, deferredchoice.OfferResponseWindowClosed, outcome)

	invokeWorkflowPatternRPC(t, flowID, flow.OpenProbeGate, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"fulfilled offer-3"}, decodeStepOutputs[string](t, result, "FulfillOffer"))
	require.Empty(t, decodeStepOutputs[string](t, result, "CloseRejectedOffer"))
}

func TestDeferredChoiceResponseCommittedAfterTheTimerWonIsTaken(t *testing.T) {
	heldResponseEntered = make(chan struct{})
	heldResponseReleased = make(chan struct{})
	flow := &OfferResponseWindowProbeFlow{}
	flowID := startWorkflowPattern(t, flow, "pattern-deferred-choice-late", "offer-4")
	waitForWorkflowPatternStep(t, flowID, "OpenOfferProbe")

	type heldOutcome struct {
		outcome string
		err     error
	}
	held := make(chan heldOutcome, 1)
	go func() {
		var outcome string
		err := integClient.InvokeRPC(integrationContext(t), flowID, flow.AcceptProbeOfferWhenReleased, "yes, just in time", &outcome)
		held <- heldOutcome{outcome: outcome, err: err}
	}()
	select {
	case <-heldResponseEntered:
	case <-time.After(workflowPatternWaitTimeout):
		t.Fatalf("Flow %s: the held response RPC never ran", flowID)
	}
	// The Timer wins while the RPC holds the window lock, so Execute waits for the RPC.
	skipOfferExpiry(t, flowID)
	close(heldResponseReleased)
	response := <-held
	require.NoError(t, response.err)
	require.Equal(t, deferredchoice.OfferResponseRecorded, response.outcome)

	waitForWorkflowPatternStep(t, flowID, "RouteResponseBeforeExpiry")
	invokeWorkflowPatternRPC(t, flowID, flow.OpenProbeGate, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"fulfilled offer-4"}, decodeStepOutputs[string](t, result, "FulfillOffer"))
	require.Empty(t, decodeStepOutputs[string](t, result, "ExpireOffer"))
}

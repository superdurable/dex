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
	cancelcase "github.com/superdurable/dex/examples/go/patterns/cancel-case"
	deferredchoice "github.com/superdurable/dex/examples/go/patterns/deferred-choice"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The probe Flows hold their first wait behind a gate, so both competing
// messages are pending before the AnyOf registers.

var probeGateOpened = dex.DefineChannel[dex.None]("probeGateOpened")

// ConditionOrderOutcome reports which condition won and how many messages the other left pending.
type ConditionOrderOutcome struct {
	Winner            string `json:"winner"`
	LoserPendingCount int    `json:"loserPendingCount"`
}

func conditionOrderProbeFlows() []dex.Flow {
	return []dex.Flow{&DeferredChoiceOrderProbeFlow{}, &CancelCaseOrderProbeFlow{}}
}

// DeferredChoiceOrderProbeFlow waits like AwaitOfferResponse, after a gate.
type DeferredChoiceOrderProbeFlow struct{ dex.FlowDefaults }

func (*DeferredChoiceOrderProbeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(HoldOfferProbe{}),
		dex.DefineStep(AwaitOfferProbeResponse{}),
	}
}

func (flow *DeferredChoiceOrderProbeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AcceptProbeOffer, nil),
		dex.DefineRPC(flow.RejectProbeOffer, nil),
		dex.DefineRPC(flow.OpenProbeGate, nil),
	}
}

func (*DeferredChoiceOrderProbeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{probeGateOpened, deferredchoice.OfferAccepted, deferredchoice.OfferRejected},
	}
}

func (*DeferredChoiceOrderProbeFlow) AcceptProbeOffer(ctx dex.Context, note string) (*dex.RPCResult[dex.None], error) {
	if err := deferredchoice.OfferAccepted.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*DeferredChoiceOrderProbeFlow) RejectProbeOffer(ctx dex.Context, note string) (*dex.RPCResult[dex.None], error) {
	if err := deferredchoice.OfferRejected.Publish(ctx, note); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*DeferredChoiceOrderProbeFlow) OpenProbeGate(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := probeGateOpened.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type HoldOfferProbe struct{ dex.StepDefaults }

func (HoldOfferProbe) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(probeGateOpened.ForOne()), nil
}

func (HoldOfferProbe) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(AwaitOfferProbeResponse{}, nil), nil
}

// AwaitOfferProbeResponse declares its conditions in the same order as AwaitOfferResponse.
type AwaitOfferProbeResponse struct{ dex.StepDefaults }

func (AwaitOfferProbeResponse) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(
		deferredchoice.OfferAccepted.ForOne(),
		deferredchoice.OfferRejected.ForOne(),
		dex.Timer(deferredchoice.OfferExpiry),
	), nil
}

func (AwaitOfferProbeResponse) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	accepted, err := deferredchoice.OfferAccepted.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(accepted) > 0 {
		return dex.GracefulComplete(ConditionOrderOutcome{
			Winner:            "accepted",
			LoserPendingCount: deferredchoice.OfferRejected.Size(ctx),
		}), nil
	}
	return dex.GracefulComplete(ConditionOrderOutcome{
		Winner:            "rejected",
		LoserPendingCount: deferredchoice.OfferAccepted.Size(ctx),
	}), nil
}

// CancelCaseOrderProbeFlow waits like AwaitUnderwriterDecision, after a gate.
type CancelCaseOrderProbeFlow struct{ dex.FlowDefaults }

func (*CancelCaseOrderProbeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(HoldDecisionProbe{}),
		dex.DefineStep(AwaitProbeDecision{}),
	}
}

func (flow *CancelCaseOrderProbeFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.CancelProbeApplication, nil),
		dex.DefineRPC(flow.DecideProbeApplication, nil),
		dex.DefineRPC(flow.OpenProbeGate, nil),
	}
}

func (*CancelCaseOrderProbeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{probeGateOpened, cancelcase.ApplicationCanceled, cancelcase.UnderwriterDecided},
	}
}

func (*CancelCaseOrderProbeFlow) CancelProbeApplication(ctx dex.Context, reason string) (*dex.RPCResult[dex.None], error) {
	if err := cancelcase.ApplicationCanceled.Publish(ctx, reason); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*CancelCaseOrderProbeFlow) DecideProbeApplication(ctx dex.Context, decision string) (*dex.RPCResult[dex.None], error) {
	if err := cancelcase.UnderwriterDecided.Publish(ctx, decision); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*CancelCaseOrderProbeFlow) OpenProbeGate(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := probeGateOpened.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type HoldDecisionProbe struct{ dex.StepDefaults }

func (HoldDecisionProbe) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(probeGateOpened.ForOne()), nil
}

func (HoldDecisionProbe) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(AwaitProbeDecision{}, nil), nil
}

// AwaitProbeDecision declares its conditions in the same order as AwaitUnderwriterDecision.
type AwaitProbeDecision struct{ dex.StepDefaults }

func (AwaitProbeDecision) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AnyOf(cancelcase.ApplicationCanceled.ForOne(), cancelcase.UnderwriterDecided.ForOne()), nil
}

func (AwaitProbeDecision) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	reasons, err := cancelcase.ApplicationCanceled.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(reasons) > 0 {
		return dex.GracefulComplete(ConditionOrderOutcome{
			Winner:            "canceled",
			LoserPendingCount: cancelcase.UnderwriterDecided.Size(ctx),
		}), nil
	}
	return dex.GracefulComplete(ConditionOrderOutcome{
		Winner:            "decided",
		LoserPendingCount: cancelcase.ApplicationCanceled.Size(ctx),
	}), nil
}

func TestDeferredChoiceDeclaredFirstResponseWinsWhenBothArePending(t *testing.T) {
	flow := &DeferredChoiceOrderProbeFlow{}
	flowID := startWorkflowPattern(t, flow, "pattern-deferred-choice-order", nil)
	// Publish the later-declared response first: declaration order wins, not publish order.
	invokeWorkflowPatternRPC(t, flowID, flow.RejectProbeOffer, "no", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.AcceptProbeOffer, "yes", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenProbeGate, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[]ConditionOrderOutcome{{Winner: "accepted", LoserPendingCount: 1}},
		decodeStepOutputs[ConditionOrderOutcome](t, result, "AwaitOfferProbeResponse"))
}

func TestCancelCaseCancelWinsWhenBothArePending(t *testing.T) {
	flow := &CancelCaseOrderProbeFlow{}
	flowID := startWorkflowPattern(t, flow, "pattern-cancel-case-order", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.DecideProbeApplication, "approved", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.CancelProbeApplication, "applicant withdrew", nil)
	invokeWorkflowPatternRPC(t, flowID, flow.OpenProbeGate, nil, nil)
	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t,
		[]ConditionOrderOutcome{{Winner: "canceled", LoserPendingCount: 1}},
		decodeStepOutputs[ConditionOrderOutcome](t, result, "AwaitProbeDecision"))
}

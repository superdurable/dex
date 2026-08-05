// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package main

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var ApprovalChannel = dex.DefineChannel[Approval]("approvals")

type Approval struct {
	Approved bool
}

type ApproveOrderInput struct {
	OrderID string
	Timeout time.Duration
}

type ShipOrderInput struct {
	OrderID string
}

type ApproveOrderStep struct {
	dex.StepDefaults
}

func (ApproveOrderStep) WaitFor(
	ctx dex.Context,
	input ApproveOrderInput,
) (*dex.Wait, error) {
	return dex.AnyOf(
		ApprovalChannel.ForOne(),
		dex.Timer(
			input.Timeout,
			dex.WithConditionID("approval-timeout"),
		),
	), nil
}

func (ApproveOrderStep) Execute(
	ctx dex.Context,
	input ApproveOrderInput,
) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("approval timed out"), nil
	}
	approvals, err := ApprovalChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(approvals) == 0 || !approvals[0].Approved {
		return dex.ForceFail("order rejected"), nil
	}
	return dex.GoTo(ShipOrder, ShipOrderInput{
		OrderID: input.OrderID,
	}), nil
}

var ApproveOrder = ApproveOrderStep{}
var _ dex.Step[ApproveOrderInput] = ApproveOrder

type ShipOrderStep struct {
	dex.StepDefaultsNoWaitFor[ShipOrderInput]
}

func (ShipOrderStep) Execute(
	ctx dex.Context,
	input ShipOrderInput,
) (*dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

var ShipOrder = ShipOrderStep{}
var _ dex.Step[ShipOrderInput] = ShipOrder

type OrderFlow struct {
	dex.FlowDefaults
}

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(ApproveOrder),
		dex.DefineStep(ShipOrder),
	}
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{ApprovalChannel},
	}
}

var Orders = OrderFlow{}
var _ dex.Flow = Orders

func main() {}

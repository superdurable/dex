// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	dex.DefaultStepOptions
}

func (ApproveOrderStep) GetStepType() string {
	return "approve-order"
}

func (ApproveOrderStep) WaitFor(
	ctx dex.Context,
	input ApproveOrderInput,
) (dex.Wait, error) {
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
) (dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("approval timed out"), nil
	}
	approvals, err := ApprovalChannel.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
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
	dex.StepDefaults[ShipOrderInput]
}

func (ShipOrderStep) GetStepType() string {
	return "ship-order"
}

func (ShipOrderStep) Execute(
	ctx dex.Context,
	input ShipOrderInput,
) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

var ShipOrder = ShipOrderStep{}
var _ dex.Step[ShipOrderInput] = ShipOrder

type OrderFlow struct{}

func (OrderFlow) GetFlowType() string {
	return "order-transitions"
}

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStepAsStart(ApproveOrder),
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

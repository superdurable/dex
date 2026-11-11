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

package quickstart

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// ShipDelayTimer is the condition ID of the Timer that Ship waits on.
const ShipDelayTimer = "ship-delay"

// OrderFlow charges an order, waits 30 seconds, and then ships it.
type OrderFlow struct {
	dex.FlowDefaults
	shop *ShopAPI
}

// NewOrderFlow returns an OrderFlow whose Steps call shop.
func NewOrderFlow(shop *ShopAPI) *OrderFlow {
	return &OrderFlow{shop: shop}
}

// GetSteps returns Charge as the start Step, then Ship.
func (flow *OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(Charge{shop: flow.shop}),
		dex.DefineStep(Ship{shop: flow.shop}),
	}
}

// GetPersistenceSchema returns an empty schema: OrderFlow keeps no Attributes.
func (*OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// Charge charges the order and moves to Ship.
type Charge struct {
	dex.StepDefaultsNoWaitFor[string]
	shop *ShopAPI
}

// Execute charges the order.
func (step Charge) Execute(_ dex.Context, orderID string) (*dex.StepDecision, error) {
	step.shop.Charge(orderID)
	return dex.GoTo(Ship{}, orderID), nil
}

// Ship waits on a 30-second Timer, then ships the order.
type Ship struct {
	dex.StepDefaults
	shop *ShopAPI
}

// WaitFor waits on the ship-delay Timer.
func (Ship) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return dex.Until(dex.Timer(30*time.Second, dex.WithConditionID(ShipDelayTimer))), nil
}

// Execute ships the order and completes the Flow.
func (step Ship) Execute(_ dex.Context, orderID string) (*dex.StepDecision, error) {
	step.shop.Ship(orderID)
	return dex.GracefulComplete("shipped " + orderID), nil
}

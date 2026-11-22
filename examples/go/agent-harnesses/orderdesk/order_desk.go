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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

// Package orderdesk is the order system that every order support agent reads and writes.
//
// Each agent has the same two tools: look_up_order reads an order, and
// cancel_order changes it. A real application calls its order service here.
package orderdesk

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// OrderSupportInstructions is the instruction every order support agent gets.
const OrderSupportInstructions = "You are an order support agent. Look up an order before you answer a " +
	"question about it. Cancel an order only when the customer asks, and say " +
	"whether the cancellation succeeded."

// Order is one order in the order desk.
type Order struct {
	OrderID string
	Items   string
	Status  string
}

// OrderDesk is an in-memory order system that records every cancel_order call it receives.
type OrderDesk struct {
	// toolDelay makes each tool call wait first, so a demo can stop the Worker mid-call.
	toolDelay time.Duration

	mutex  sync.Mutex
	orders map[string]*Order
	// cancelOrderCalls holds the order ID of every cancel_order call, including calls that change nothing.
	cancelOrderCalls []string
}

// NewOrderDesk returns an order desk with orders 1001 (shipped), 1002, and 1003 (processing).
//
// Each tool call waits toolDelay before it reads or changes an order.
func NewOrderDesk(toolDelay time.Duration) *OrderDesk {
	return &OrderDesk{
		toolDelay: toolDelay,
		orders: map[string]*Order{
			"1001": {OrderID: "1001", Items: "2 notebooks", Status: "shipped"},
			"1002": {OrderID: "1002", Items: "1 desk lamp", Status: "processing"},
			"1003": {OrderID: "1003", Items: "3 pens", Status: "processing"},
		},
	}
}

// DescribeOrder returns the order's items and status, or says that the order does not exist.
//
// It returns ctx's error when ctx ends during the tool delay.
func (desk *OrderDesk) DescribeOrder(ctx context.Context, orderID string) (string, error) {
	if err := desk.waitToolDelay(ctx); err != nil {
		return "", err
	}
	desk.mutex.Lock()
	defer desk.mutex.Unlock()
	order, isKnown := desk.orders[orderID]
	if !isKnown {
		return fmt.Sprintf("Order %s does not exist.", orderID), nil
	}
	return fmt.Sprintf("Order %s: %s, %s.", order.OrderID, order.Items, order.Status), nil
}

// CancelOrder cancels an order that is still processing and says what happened.
//
// It records the call even when the order does not exist or cannot be
// cancelled. It returns ctx's error when ctx ends during the tool delay.
func (desk *OrderDesk) CancelOrder(ctx context.Context, orderID string, reason string) (string, error) {
	if err := desk.waitToolDelay(ctx); err != nil {
		return "", err
	}
	desk.mutex.Lock()
	defer desk.mutex.Unlock()
	desk.cancelOrderCalls = append(desk.cancelOrderCalls, orderID)
	order, isKnown := desk.orders[orderID]
	if !isKnown {
		return fmt.Sprintf("Order %s does not exist.", orderID), nil
	}
	if order.Status != "processing" {
		return fmt.Sprintf("Order %s is %s and cannot be cancelled.", orderID, order.Status), nil
	}
	order.Status = "cancelled"
	return fmt.Sprintf("Order %s is cancelled: %s.", orderID, reason), nil
}

// CancelOrderCalls returns the order ID of every cancel_order call so far, in order.
func (desk *OrderDesk) CancelOrderCalls() []string {
	desk.mutex.Lock()
	defer desk.mutex.Unlock()
	return append([]string(nil), desk.cancelOrderCalls...)
}

func (desk *OrderDesk) waitToolDelay(ctx context.Context) error {
	if desk.toolDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(desk.toolDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

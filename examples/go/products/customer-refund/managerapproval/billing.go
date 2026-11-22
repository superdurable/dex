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

package managerapproval

import (
	"fmt"
	"log"
)

// Order is one charge that billing knows about.
type Order struct {
	OrderID         string  `json:"orderId"`
	Description     string  `json:"description"`
	AmountUSD       float64 `json:"amountUsd"`
	DaysSinceCharge int     `json:"daysSinceCharge"`
	OutageHours     int     `json:"outageHours"`
}

// RecordedRefund is one refund in the billing ledger.
type RecordedRefund struct {
	RefundID       string
	OrderID        string
	AmountUSD      float64
	IdempotencyKey string
}

// Orders are the sample charges; the scenarios pick one by order ID.
var Orders = map[string]Order{
	"INV-1249": {OrderID: "INV-1249", Description: "Studio plan renewal", AmountUSD: 1249.00, DaysSinceCharge: 6, OutageHours: 31},
	"INV-0388": {OrderID: "INV-0388", Description: "Studio plan renewal", AmountUSD: 1249.00, DaysSinceCharge: 47, OutageHours: 31},
	"INV-0455": {OrderID: "INV-0455", Description: "Studio plan renewal", AmountUSD: 1249.00, DaysSinceCharge: 5, OutageHours: 0},
	"INV-0049": {OrderID: "INV-0049", Description: "Extra seat", AmountUSD: 49.00, DaysSinceCharge: 3, OutageHours: 31},
}

// BillingService looks up orders and refunds them, once per idempotency key.
type BillingService interface {
	GetOrder(orderID string) (Order, error)
	IssueRefund(order Order, idempotencyKey string, simulateTimeout bool) (string, error)
}

// PaymentTimeoutError means the payment provider did not answer in time; a retry may succeed.
type PaymentTimeoutError struct {
	OrderID string
}

func (timeout *PaymentTimeoutError) Error() string {
	return "PaymentTimeout: no answer from the payment provider for " + timeout.OrderID
}

// IdempotencyKeyConflictError means the key already refunded a different order; a retry cannot succeed.
type IdempotencyKeyConflictError struct {
	IdempotencyKey  string
	RefundID        string
	RefundedOrderID string
	RefundedUSD     float64
	RequestedOrder  string
}

func (conflict *IdempotencyKeyConflictError) Error() string {
	return fmt.Sprintf(
		"IdempotencyKeyConflict: key %s already refunded %s (%s USD) as %s, not %s; use one ledger per Dex environment",
		conflict.IdempotencyKey, conflict.RefundedOrderID, formatUSD(conflict.RefundedUSD), conflict.RefundID, conflict.RequestedOrder,
	)
}

// FakeBillingService keeps refunds in the shared ledger, one per idempotency key.
type FakeBillingService struct {
	ledger *RefundLedger
}

// NewFakeBillingService returns billing over ledger.
func NewFakeBillingService(ledger *RefundLedger) *FakeBillingService {
	return &FakeBillingService{ledger: ledger}
}

func (*FakeBillingService) GetOrder(orderID string) (Order, error) {
	order, found := Orders[orderID]
	if !found {
		return Order{}, fmt.Errorf("billing has no order %q", orderID)
	}
	return order, nil
}

func (billing *FakeBillingService) IssueRefund(order Order, idempotencyKey string, simulateTimeout bool) (string, error) {
	if simulateTimeout {
		return "", &PaymentTimeoutError{OrderID: order.OrderID}
	}
	row, inserted, err := billing.ledger.insertRefundOnce(idempotencyKey, order.OrderID, order.AmountUSD)
	if err != nil {
		return "", err
	}
	refundID := refundIDForRow(row.RowID)
	if row.OrderID != order.OrderID || row.AmountUSD != order.AmountUSD {
		return "", &IdempotencyKeyConflictError{
			IdempotencyKey: idempotencyKey, RefundID: refundID,
			RefundedOrderID: row.OrderID, RefundedUSD: row.AmountUSD, RequestedOrder: order.OrderID,
		}
	}
	if inserted {
		log.Printf("Refunded %s USD as %s", formatUSD(order.AmountUSD), refundID)
	} else {
		log.Printf("Refund %s already issued (key %s)", refundID, idempotencyKey)
	}
	return refundID, nil
}

// ListRefunds returns every refund in the ledger, oldest first.
func (billing *FakeBillingService) ListRefunds() ([]RecordedRefund, error) {
	rows, err := billing.ledger.readRows()
	if err != nil {
		return nil, err
	}
	refunds := make([]RecordedRefund, 0, len(rows.Refunds))
	for _, row := range rows.Refunds {
		refunds = append(refunds, RecordedRefund{
			RefundID: refundIDForRow(row.RowID), OrderID: row.OrderID,
			AmountUSD: row.AmountUSD, IdempotencyKey: row.IdempotencyKey,
		})
	}
	return refunds, nil
}

// refundIDForRow returns the refund ID of a ledger row, such as rf_1042 for the first row.
func refundIDForRow(rowID int) string {
	return fmt.Sprintf("rf_%d", 1041+rowID)
}

// CallMissingBillingService stands in for a release whose Issue refund Step forgot the billing call.
type CallMissingBillingService struct {
	*FakeBillingService
}

func (CallMissingBillingService) IssueRefund(order Order, _ string, _ bool) (string, error) {
	log.Printf("Billing call missing: no refund for %s", order.OrderID)
	return "", nil
}

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

// Command list-refunds prints the refunds that the fake billing service recorded.
package main

import (
	"fmt"
	"log"

	"github.com/superdurable/dex/examples/go/products/customer-refund/managerapproval"
)

func main() {
	billing := managerapproval.NewRefundServices(managerapproval.DefaultLedgerDirectory).Billing
	refunds, err := billing.ListRefunds()
	if err != nil {
		log.Fatal(err)
	}
	if len(refunds) == 0 {
		fmt.Println("No refunds recorded.")
	}
	for _, refund := range refunds {
		fmt.Printf("%s  %s  %.2f USD  key %s\n", refund.RefundID, refund.OrderID, refund.AmountUSD, refund.IdempotencyKey)
	}
}

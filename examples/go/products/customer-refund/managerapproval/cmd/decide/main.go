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

// Command decide approves or rejects a waiting refund: decide FLOW_ID approve|reject REASON.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/superdurable/dex/examples/go/products/customer-refund/managerapproval"
	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: decide FLOW_ID approve|reject REASON")
	}
	if err := decideRefund(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		log.Fatal(err)
	}
}

func decideRefund(flowID string, decision string, reason string) error {
	flow, err := managerapproval.NewFlowForBillingMode(managerapproval.BillingModeNormal, managerapproval.DefaultLedgerDirectory)
	if err != nil {
		return err
	}
	var rpc func(dex.Context, managerapproval.DecisionReason) (*dex.RPCResult[dex.None], error)
	outcome := ""
	switch decision {
	case "approve":
		rpc, outcome = flow.ApproveRefund, "approved"
	case "reject":
		rpc, outcome = flow.RejectRefund, "rejected"
	default:
		return fmt.Errorf("decision must be approve or reject, not %q", decision)
	}
	client, cache, err := managerapproval.OpenCommandClient(flow)
	if err != nil {
		return err
	}
	decideErr := waitAndInvokeDecision(client, flowID, rpc, reason)
	if decideErr == nil {
		fmt.Printf("Refund %s: %s.\n", flowID, outcome)
	}
	return errors.Join(decideErr, client.Close(), cache.Close())
}

func waitAndInvokeDecision(
	client *dex.Client,
	flowID string,
	rpc func(dex.Context, managerapproval.DecisionReason) (*dex.RPCResult[dex.None], error),
	reason string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var status string
	err := client.WaitForAttributeMatch(
		ctx, flowID, managerapproval.CaseStatus, dex.AttributeMatchEqual(managerapproval.AwaitingManager),
		&status, dex.WaitForAttributeOptions{RequestTimeout: 60 * time.Second},
	)
	if err != nil {
		return err
	}
	return client.InvokeRPC(ctx, flowID, rpc, managerapproval.DecisionReason{Reason: reason}, nil)
}

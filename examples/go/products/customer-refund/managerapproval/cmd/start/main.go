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

// Command start starts one refund: start FLOW_ID [normal|fail-then-succeed|deny|small].
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/superdurable/dex/examples/go/products/customer-refund/managerapproval"
	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		log.Fatalf("usage: start FLOW_ID [%s]", strings.Join(managerapproval.ScenarioNames, "|"))
	}
	flowID, scenario := os.Args[1], "normal"
	if len(os.Args) == 3 {
		scenario = os.Args[2]
	}
	if err := startRefund(flowID, scenario); err != nil {
		log.Fatal(err)
	}
}

func startRefund(flowID string, scenario string) error {
	request, found := managerapproval.ScenarioRequest(scenario)
	if !found {
		return fmt.Errorf("scenario must be one of %s", strings.Join(managerapproval.ScenarioNames, ", "))
	}
	flow, err := managerapproval.NewFlowForBillingMode(managerapproval.BillingModeNormal, managerapproval.DefaultLedgerDirectory)
	if err != nil {
		return err
	}
	client, cache, err := managerapproval.OpenCommandClient(flow)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, startErr := client.StartFlow(ctx, flow, flowID, request, dex.StartFlowOptions{})
	if startErr == nil {
		fmt.Printf("Started refund %s (%s).\n", flowID, scenario)
	}
	return errors.Join(startErr, client.Close(), cache.Close())
}

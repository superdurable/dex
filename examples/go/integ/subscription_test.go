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
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestSubscriptionStartRPCAndChannels(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "subscription")
	customer := subscription.Customer{
		FirstName: "Example",
		LastName:  "Customer",
		ID:        flowID,
		Email:     "customer@example.com",
		Subscription: subscription.Subscription{
			TrialPeriod:         30 * time.Second,
			BillingPeriod:       30 * time.Second,
			MaxBillingPeriods:   2,
			BillingPeriodCharge: 100,
		},
	}
	runID, err := integClient.StartFlow(
		ctx,
		workflows.Subscription,
		flowID,
		customer,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)
	require.NoError(t, integClient.WaitForAttributeEqual(
		ctx,
		flowID,
		subscription.BillingPeriodNumber,
		0,
		dex.WaitOptions{Timeout: 20 * time.Second},
	))

	var current subscription.Subscription
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		workflows.Subscription.Describe,
		nil,
		&current,
		dex.InvokeOptions{},
	))
	require.Equal(t, 100, current.BillingPeriodCharge)
	require.NoError(t, integClient.PublishToChannel(
		ctx,
		flowID,
		subscription.UpdateChargeAmount,
		250,
	))
	require.Eventually(t, func() bool {
		err = integClient.InvokeRPC(
			ctx,
			flowID,
			workflows.Subscription.Describe,
			nil,
			&current,
			dex.InvokeOptions{},
		)
		return err == nil && current.BillingPeriodCharge == 250
	}, 20*time.Second, 200*time.Millisecond, "Describe failed: %v", err)
	require.NoError(t, integClient.PublishToChannel(
		ctx,
		flowID,
		subscription.CancelSubscription,
		nil,
	))

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output string
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, "subscription canceled", output)
}

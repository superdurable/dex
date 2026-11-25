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
	failfast "github.com/superdurable/dex/examples/go/patterns/fail-fast"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestFailFastRoutesRejectedBankAccountAfterOneAttempt(t *testing.T) {
	require.Equal(t, time.Hour, failfast.SubmitPayout{}.GetStepOptions().ExecuteRetry.TotalDuration)
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-fail-fast")
	_, err := integClient.StartFlow(ctx, registry.ExpenseReimbursement, flowID,
		failfast.ExpenseClaim{AmountCents: 12000, BankAccount: "not-a-bank-account"}, dex.StartFlowOptions{})
	require.NoError(t, err)

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, failfast.PayoutOutcome{
		Status: "REJECTED", Detail: "bank account not-a-bank-account is invalid", SubmitAttempt: 1,
	}, readPayoutOutcome(t, flowID))
	require.Equal(t, 1, registry.PaymentProvider.CountSubmissions(flowID))
}

func TestFailFastRetriesProviderTimeout(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-fail-fast")
	_, err := integClient.StartFlow(ctx, registry.ExpenseReimbursement, flowID,
		failfast.ExpenseClaim{AmountCents: 12000, BankAccount: "12345678"}, dex.StartFlowOptions{})
	require.NoError(t, err)

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, failfast.PayoutOutcome{
		Status: "PAID", Detail: "payout-" + flowID, SubmitAttempt: 2,
	}, readPayoutOutcome(t, flowID))
	require.Equal(t, 2, registry.PaymentProvider.CountSubmissions(flowID))
}

func readPayoutOutcome(t *testing.T, flowID string) failfast.PayoutOutcome {
	t.Helper()
	var outcome failfast.PayoutOutcome
	_, err := integClient.GetAttributes(integrationContext(t), flowID,
		dex.ReadAttribute(failfast.PayoutOutcomeAttribute, &outcome))
	require.NoError(t, err)
	return outcome
}

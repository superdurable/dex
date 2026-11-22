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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrentRefundsWithOneKeyRecordOneRefund(t *testing.T) {
	services := NewRefundServices(t.TempDir())
	order := Orders["INV-1249"]
	refundIDs := make([]string, 16)
	var wait sync.WaitGroup
	for index := range refundIDs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			// A separate service per call opens its own lock file, like a separate Worker.
			billing := NewFakeBillingService(services.Billing.ledger)
			refundID, err := billing.IssueRefund(order, "refund-shared", false)
			require.NoError(t, err)
			refundIDs[index] = refundID
		}()
	}
	wait.Wait()
	refunds, err := services.Billing.ListRefunds()
	require.NoError(t, err)
	require.Len(t, refunds, 1)
	for _, refundID := range refundIDs {
		require.Equal(t, refunds[0].RefundID, refundID)
	}
}

func TestConcurrentRefundsWithDifferentKeysGetDifferentIDs(t *testing.T) {
	services := NewRefundServices(t.TempDir())
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := services.Billing.IssueRefund(Orders["INV-0049"], fmt.Sprintf("refund-%d", index), false)
			require.NoError(t, err)
		}()
	}
	wait.Wait()
	refunds, err := services.Billing.ListRefunds()
	require.NoError(t, err)
	require.Len(t, refunds, 16)
	seen := map[string]bool{}
	for _, refund := range refunds {
		require.False(t, seen[refund.RefundID], "duplicate refund ID %s", refund.RefundID)
		seen[refund.RefundID] = true
	}
}

func TestRefundKeyReusedForAnotherOrderIsAConflict(t *testing.T) {
	services := NewRefundServices(t.TempDir())
	refundID, err := services.Billing.IssueRefund(Orders["INV-1249"], "refund-reused", false)
	require.NoError(t, err)
	require.Equal(t, "rf_1042", refundID)

	_, err = services.Billing.IssueRefund(Orders["INV-0049"], "refund-reused", false)
	var conflict *IdempotencyKeyConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "INV-1249", conflict.RefundedOrderID)
	refunds, err := services.Billing.ListRefunds()
	require.NoError(t, err)
	require.Len(t, refunds, 1)
}

func TestConcurrentEmailsWithOneKeySendOnce(t *testing.T) {
	services := NewRefundServices(t.TempDir())
	var wait sync.WaitGroup
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			require.NoError(t, services.Email.SendEmail("billing@northstar.example", "hello", "email-shared"))
		}()
	}
	wait.Wait()
	keys, err := services.Email.ListSentKeys()
	require.NoError(t, err)
	require.Equal(t, []string{"email-shared"}, keys)
}

func TestFormatUSDGroupsThousands(t *testing.T) {
	require.Equal(t, "1,249.00", formatUSD(1249))
	require.Equal(t, "49.00", formatUSD(49))
	require.Equal(t, "1,000,000.50", formatUSD(1_000_000.5))
}

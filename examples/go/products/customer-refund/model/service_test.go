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

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeServiceMakesRefundEffectsIdempotent(t *testing.T) {
	service := NewFakeService()
	refundCase := RefundCase{CaseID: "order-42", AmountCents: 4200}

	first := service.IssueRefund("refund-key", refundCase)
	second := service.IssueRefund("refund-key", refundCase)

	require.Equal(t, BillingConfirmed, first)
	require.Equal(t, first, second)
	require.Equal(t, 2, service.RefundCalls("refund-key"))
}

func TestFakeServicePreservesUnknownAndDeclinedOutcomes(t *testing.T) {
	service := NewFakeService()

	require.Equal(t, BillingDeclined, service.IssueRefund("declined-key", RefundCase{CaseID: "declined"}))
	require.Equal(t, BillingDeclined, service.LookupBillingOutcome("declined-key"))
	require.Equal(t, BillingUnknown, service.IssueRefund("unknown-key", RefundCase{CaseID: "unproven"}))
	require.Equal(t, BillingUnknown, service.LookupBillingOutcome("unknown-key"))
}

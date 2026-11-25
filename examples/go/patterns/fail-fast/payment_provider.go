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

package failfast

import (
	"errors"
	"fmt"
	"sync"
)

type ExpenseClaim struct {
	AmountCents int64  `json:"amountCents"`
	BankAccount string `json:"bankAccount"`
}

// PayoutRejectedError means the provider confirmed that the request can never succeed.
type PayoutRejectedError struct {
	Reason string
}

func (err *PayoutRejectedError) Error() string {
	return err.Reason
}

// ErrPayoutTimeout means the provider did not answer in time; the same request may succeed later.
var ErrPayoutTimeout = errors.New("payment provider timed out")

// FakePaymentProvider stands in for a payment provider API.
// It rejects a bank account that is not eight digits at once.
// It times out on the first submission of every valid claim, then pays it.
type FakePaymentProvider struct {
	mutex       sync.Mutex
	submissions map[string]int
}

func NewFakePaymentProvider() *FakePaymentProvider {
	return &FakePaymentProvider{submissions: map[string]int{}}
}

// SubmitPayout pays a claim once per claim ID, so a retried Step does not pay twice.
func (provider *FakePaymentProvider) SubmitPayout(claimID string, claim ExpenseClaim) (string, error) {
	provider.mutex.Lock()
	provider.submissions[claimID]++
	submissions := provider.submissions[claimID]
	provider.mutex.Unlock()
	if !isEightDigits(claim.BankAccount) {
		return "", &PayoutRejectedError{Reason: fmt.Sprintf("bank account %s is invalid", claim.BankAccount)}
	}
	if submissions == 1 {
		return "", ErrPayoutTimeout
	}
	return "payout-" + claimID, nil
}

func (provider *FakePaymentProvider) CountSubmissions(claimID string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.submissions[claimID]
}

func isEightDigits(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

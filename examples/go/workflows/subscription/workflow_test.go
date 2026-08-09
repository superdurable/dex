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

package subscription

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var testCustomer = Customer{
	FirstName: "Quanzheng",
	LastName:  "Long",
	ID:        "123",
	Email:     "qlong.seattle@gmail.com",
	Subscription: Subscription{
		BillingPeriod:       time.Second,
		MaxBillingPeriods:   10,
		TrialPeriod:         2 * time.Second,
		BillingPeriodCharge: 100,
	},
}

func TestInitializeSubscription(t *testing.T) {
	decision := initializeSubscription()

	require.Equal(t, dex.GoToMulti(
		dex.MovementOf(trialStep{}, nil),
		dex.MovementOf(cancelStep{}, nil),
		dex.MovementOf(updateChargeAmountStep{}, nil),
	), decision)
}

func TestWaitForTrial(t *testing.T) {
	applicationService := &recordingService{}

	wait := waitForTrial(testCustomer, applicationService)

	require.Equal(t, dex.Until(dex.Timer(testCustomer.Subscription.TrialPeriod)), wait)
	require.Equal(t, []recordedEmail{{
		recipient: testCustomer.Email,
		subject:   "welcome email",
		content:   "hello content",
	}}, applicationService.emails)
}

func TestExecuteTrial(t *testing.T) {
	decision := executeTrial()

	require.Equal(t, dex.GoTo(chargeCurrentBillStep{}, nil), decision)
}

func TestWaitForCharge(t *testing.T) {
	t.Run("active subscription", func(t *testing.T) {
		wait, subscriptionOver := waitForCharge(testCustomer, 0)

		require.False(t, subscriptionOver)
		require.Equal(t, dex.Until(dex.Timer(testCustomer.Subscription.BillingPeriod)), wait)
	})

	t.Run("completed subscription", func(t *testing.T) {
		wait, subscriptionOver := waitForCharge(
			testCustomer,
			testCustomer.Subscription.MaxBillingPeriods,
		)

		require.True(t, subscriptionOver)
		require.Equal(t, dex.SkipWaitImmediately(), wait)
	})
}

func TestExecuteCharge(t *testing.T) {
	t.Run("charge and continue", func(t *testing.T) {
		applicationService := &recordingService{}

		decision := executeCharge(testCustomer, false, applicationService)

		require.Empty(t, applicationService.emails)
		require.Equal(t, []recordedCharge{{
			email:      testCustomer.Email,
			customerID: testCustomer.ID,
			amount:     testCustomer.Subscription.BillingPeriodCharge,
		}}, applicationService.charges)
		require.Equal(t, dex.GoTo(chargeCurrentBillStep{}, nil), decision)
	})

	t.Run("complete subscription", func(t *testing.T) {
		applicationService := &recordingService{}

		decision := executeCharge(testCustomer, true, applicationService)

		require.Empty(t, applicationService.charges)
		require.Equal(t, []recordedEmail{{
			recipient: testCustomer.Email,
			subject:   "subscription over",
			content:   "hello content",
		}}, applicationService.emails)
		require.Equal(t, dex.ForceComplete("subscription ended"), decision)
	})
}

func TestCancelSubscription(t *testing.T) {
	applicationService := &recordingService{}

	wait, err := (cancelStep{}).WaitFor(nil, nil)
	require.NoError(t, err)
	require.Equal(t, dex.Until(CancelSubscription.ForOne()), wait)

	decision := executeCancel(testCustomer, applicationService)
	require.Equal(t, []recordedEmail{{
		recipient: testCustomer.Email,
		subject:   "subscription canceled",
		content:   "hello content",
	}}, applicationService.emails)
	require.Equal(t, dex.ForceComplete("subscription canceled"), decision)
}

func TestUpdateChargeAmount(t *testing.T) {
	wait, err := (updateChargeAmountStep{}).WaitFor(nil, nil)
	require.NoError(t, err)
	require.Equal(t, dex.Until(UpdateChargeAmount.ForOne()), wait)

	updatedCustomer, decision, err := executeUpdateChargeAmount(testCustomer, []int{200})
	require.NoError(t, err)
	require.Equal(t, 200, updatedCustomer.Subscription.BillingPeriodCharge)
	require.Equal(t, dex.GoTo(updateChargeAmountStep{}, nil), decision)
}

func TestUpdateChargeAmountRejectsUnexpectedResults(t *testing.T) {
	for _, amounts := range [][]int{nil, {100, 200}} {
		_, _, err := executeUpdateChargeAmount(testCustomer, amounts)

		require.Error(t, err)
	}
}

type recordedEmail struct {
	recipient string
	subject   string
	content   string
}

type recordedCharge struct {
	email      string
	customerID string
	amount     int
}

type recordingService struct {
	emails  []recordedEmail
	charges []recordedCharge
}

func (recorder *recordingService) SendEmail(recipient, subject, content string) {
	recorder.emails = append(recorder.emails, recordedEmail{
		recipient: recipient,
		subject:   subject,
		content:   content,
	})
}

func (recorder *recordingService) ChargeUser(email, customerID string, amount int) {
	recorder.charges = append(recorder.charges, recordedCharge{
		email:      email,
		customerID: customerID,
		amount:     amount,
	})
}

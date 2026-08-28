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
	"fmt"
	"time"

	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	BillingPeriodNumber = dex.DefineAttribute[int]("billing-period-number")
	CustomerDetails     = dex.DefineAttribute[Customer]("customer")
	CancelSubscription  = dex.DefineChannel[dex.None]("cancel-subscription")
	UpdateChargeAmount  = dex.DefineChannel[int]("update-charge-amount")
)

type Subscription struct {
	TrialPeriod         time.Duration
	BillingPeriod       time.Duration
	MaxBillingPeriods   int
	BillingPeriodCharge int
}

type Customer struct {
	FirstName    string
	LastName     string
	ID           string
	Email        string
	Subscription Subscription
}

type SubscriptionFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewSubscriptionFlow(applicationService service.MyService) *SubscriptionFlow {
	return &SubscriptionFlow{service: applicationService}
}

func (flow *SubscriptionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initializeStep{}),
		dex.DefineStep(trialStep{service: flow.service}),
		dex.DefineStep(chargeCurrentBillStep{service: flow.service}),
		dex.DefineStep(cancelStep{service: flow.service}),
		dex.DefineStep(updateChargeAmountStep{}),
	}
}

func (*SubscriptionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{BillingPeriodNumber, CustomerDetails},
		Channels:   []dex.ChannelDef{CancelSubscription, UpdateChargeAmount},
	}
}

func (*SubscriptionFlow) Describe(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[Subscription], error) {
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[Subscription]{Output: customer.Subscription}, nil
}

type initializeStep struct {
	dex.StepDefaultsNoWaitFor[Customer]
}

func (initializeStep) Execute(
	ctx dex.Context,
	customer Customer,
) (*dex.StepDecision, error) {
	if err := CustomerDetails.Set(ctx, customer); err != nil {
		return nil, err
	}
	if err := BillingPeriodNumber.Set(ctx, 0); err != nil {
		return nil, err
	}
	return initializeSubscription(), nil
}

type trialStep struct {
	dex.StepDefaults
	service service.MyService
}

func (step trialStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	return waitForTrial(customer, step.service), nil
}

func (trialStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	return executeTrial(), nil
}

const subscriptionOverKey = "subscription-over"

type chargeCurrentBillStep struct {
	dex.StepDefaults
	service service.MyService
}

func (chargeCurrentBillStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	periodNumber, err := BillingPeriodNumber.Get(ctx)
	if err != nil {
		return nil, err
	}
	wait, subscriptionOver := waitForCharge(customer, periodNumber)
	if subscriptionOver {
		if err := ctx.SetStepExecutionLocal(subscriptionOverKey, true); err != nil {
			return nil, err
		}
		return wait, nil
	}
	if err := BillingPeriodNumber.Set(ctx, periodNumber+1); err != nil {
		return nil, err
	}
	return wait, nil
}

func (step chargeCurrentBillStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	var subscriptionOver bool
	found, err := ctx.GetStepExecutionLocal(subscriptionOverKey, &subscriptionOver)
	if err != nil {
		return nil, err
	}
	return executeCharge(customer, found && subscriptionOver, step.service), nil
}

type cancelStep struct {
	dex.StepDefaults
	service service.MyService
}

func (cancelStep) WaitFor(
	dex.Context,
	dex.None,
) (*dex.Wait, error) {
	return dex.Until(CancelSubscription.ForOne()), nil
}

func (step cancelStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	return executeCancel(customer, step.service), nil
}

type updateChargeAmountStep struct {
	dex.StepDefaults
}

func (updateChargeAmountStep) WaitFor(
	dex.Context,
	dex.None,
) (*dex.Wait, error) {
	return dex.Until(UpdateChargeAmount.ForOne()), nil
}

func (updateChargeAmountStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	amounts, err := UpdateChargeAmount.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	customer, err := CustomerDetails.Get(ctx)
	if err != nil {
		return nil, err
	}
	updatedCustomer, decision, err := executeUpdateChargeAmount(customer, amounts)
	if err != nil {
		return nil, err
	}
	if err := CustomerDetails.Set(ctx, updatedCustomer); err != nil {
		return nil, err
	}
	return decision, nil
}

func initializeSubscription() *dex.StepDecision {
	return dex.GoToMany(
		dex.MovementOf(trialStep{}, nil),
		dex.MovementOf(cancelStep{}, nil),
		dex.MovementOf(updateChargeAmountStep{}, nil),
	)
}

func waitForTrial(
	customer Customer,
	applicationService subscriptionService,
) *dex.Wait {
	// send welcome email
	applicationService.SendEmail(customer.Email, "welcome email", "hello content")
	return dex.Until(dex.Timer(customer.Subscription.TrialPeriod))
}

func executeTrial() *dex.StepDecision {
	return dex.GoTo(chargeCurrentBillStep{}, nil)
}

func waitForCharge(customer Customer, periodNumber int) (*dex.Wait, bool) {
	if periodNumber >= customer.Subscription.MaxBillingPeriods {
		return dex.SkipWaitImmediately(), true
	}
	return dex.Until(dex.Timer(customer.Subscription.BillingPeriod)), false
}

func executeCharge(
	customer Customer,
	subscriptionOver bool,
	applicationService subscriptionService,
) *dex.StepDecision {
	if subscriptionOver {
		applicationService.SendEmail(customer.Email, "subscription over", "hello content")
		// use force completing because the cancel state is still waiting for signal
		return dex.ForceComplete("subscription ended")
	}
	applicationService.ChargeUser(
		customer.Email,
		customer.ID,
		customer.Subscription.BillingPeriodCharge,
	)
	return dex.GoTo(chargeCurrentBillStep{}, nil)
}

func executeCancel(
	customer Customer,
	applicationService subscriptionService,
) *dex.StepDecision {
	applicationService.SendEmail(customer.Email, "subscription canceled", "hello content")
	return dex.ForceComplete("subscription canceled")
}

func executeUpdateChargeAmount(
	customer Customer,
	amounts []int,
) (Customer, *dex.StepDecision, error) {
	if len(amounts) != 1 {
		return Customer{}, nil, fmt.Errorf(
			"expected one charge amount, got %d",
			len(amounts),
		)
	}
	customer.Subscription.BillingPeriodCharge = amounts[0]
	return customer, dex.GoTo(updateChargeAmountStep{}, nil), nil
}

type subscriptionService interface {
	SendEmail(recipient, subject, content string)
	ChargeUser(email, customerID string, amount int)
}

var (
	_ dex.Flow                        = (*SubscriptionFlow)(nil)
	_ dex.RPC[dex.None, Subscription] = (*SubscriptionFlow)(nil).Describe
)

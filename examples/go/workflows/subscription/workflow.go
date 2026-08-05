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

	"github.com/superdurable/dex/examples/go/workflows/service"
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
	service service.MyService
}

func NewSubscriptionFlow(applicationService service.MyService) *SubscriptionFlow {
	return &SubscriptionFlow{service: applicationService}
}

func (*SubscriptionFlow) GetFlowType() string {
	return "subscription"
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
) (dex.RPCResult[Subscription], error) {
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.RPCResult[Subscription]{}, err
	}
	return dex.RPCResult[Subscription]{Output: customer.Subscription}, nil
}

type initializeStep struct {
	dex.StepDefaults[Customer]
}

func (initializeStep) GetStepType() string {
	return "initialize"
}

func (initializeStep) Execute(
	ctx dex.Context,
	customer Customer,
) (dex.StepDecision, error) {
	if err := CustomerDetails.Set(ctx, customer); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoToMulti(
		dex.MovementOf(trialStep{}, dex.None{}),
		dex.MovementOf(cancelStep{}, dex.None{}),
		dex.MovementOf(updateChargeAmountStep{}, dex.None{}),
	), nil
}

type trialStep struct {
	dex.DefaultStepOptions
	service service.MyService
}

func (trialStep) GetStepType() string {
	return "trial"
}

func (step trialStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (dex.Wait, error) {
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	// send welcome email
	step.service.SendEmail(customer.Email, "welcome email", "hello content")
	return dex.AllOf(dex.Timer(customer.Subscription.TrialPeriod)), nil
}

func (trialStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (dex.StepDecision, error) {
	if err := BillingPeriodNumber.Set(ctx, 0); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoTo(chargeCurrentBillStep{}, dex.None{}), nil
}

const subscriptionOverKey = "subscription-over"

type chargeCurrentBillStep struct {
	dex.DefaultStepOptions
	service service.MyService
}

func (chargeCurrentBillStep) GetStepType() string {
	return "charge-current-bill"
}

func (chargeCurrentBillStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (dex.Wait, error) {
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	periodNumber, found, err := BillingPeriodNumber.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found {
		periodNumber = 0
	}
	if periodNumber >= customer.Subscription.MaxBillingPeriods {
		if err := ctx.SetStepExecutionLocal(subscriptionOverKey, true); err != nil {
			return dex.Wait{}, err
		}
		return dex.SkipWaitImmediately(), nil
	}
	if err := BillingPeriodNumber.Set(ctx, periodNumber+1); err != nil {
		return dex.Wait{}, err
	}
	return dex.AllOf(dex.Timer(customer.Subscription.BillingPeriod)), nil
}

func (step chargeCurrentBillStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (dex.StepDecision, error) {
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	var subscriptionOver bool
	found, err := ctx.GetStepExecutionLocal(subscriptionOverKey, &subscriptionOver)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if found && subscriptionOver {
		step.service.SendEmail(customer.Email, "subscription over", "hello content")
		// use force completing because the cancel state is still waiting for signal
		return dex.ForceComplete("subscription ended"), nil
	}
	step.service.ChargeUser(
		customer.Email,
		customer.ID,
		customer.Subscription.BillingPeriodCharge,
	)
	return dex.GoTo(chargeCurrentBillStep{}, dex.None{}), nil
}

type cancelStep struct {
	dex.DefaultStepOptions
	service service.MyService
}

func (cancelStep) GetStepType() string {
	return "cancel"
}

func (cancelStep) WaitFor(
	dex.Context,
	dex.None,
) (dex.Wait, error) {
	return dex.AllOf(CancelSubscription.ForOne()), nil
}

func (step cancelStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (dex.StepDecision, error) {
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	step.service.SendEmail(customer.Email, "subscription canceled", "hello content")
	return dex.ForceComplete("subscription canceled"), nil
}

type updateChargeAmountStep struct {
	dex.DefaultStepOptions
}

func (updateChargeAmountStep) GetStepType() string {
	return "update-charge-amount"
}

func (updateChargeAmountStep) WaitFor(
	dex.Context,
	dex.None,
) (dex.Wait, error) {
	return dex.AllOf(UpdateChargeAmount.ForOne()), nil
}

func (updateChargeAmountStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (dex.StepDecision, error) {
	amounts, err := UpdateChargeAmount.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if len(amounts) != 1 {
		return dex.StepDecision{}, fmt.Errorf("expected one charge amount, got %d", len(amounts))
	}
	customer, _, err := CustomerDetails.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	customer.Subscription.BillingPeriodCharge = amounts[0]
	if err := CustomerDetails.Set(ctx, customer); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoTo(updateChargeAmountStep{}, dex.None{}), nil
}

var (
	_ dex.Flow                        = (*SubscriptionFlow)(nil)
	_ dex.RPC[dex.None, Subscription] = (*SubscriptionFlow)(nil).Describe
)

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
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

type SubscriptionWorkflow struct {
	dex.DefaultWorkflowType

	svc service.MyService
}

func NewSubscriptionWorkflow(svc service.MyService) dex.ObjectWorkflow {
	return &SubscriptionWorkflow{
		svc: svc,
	}
}

const (
	keyBillingPeriodNum = "billingPeriodNum"
	keyCustomer         = "customer"

	SignalCancelSubscription              = "cancelSubscription"
	SignalUpdateBillingPeriodChargeAmount = "updateBillingPeriodChargeAmount"
)

func (b SubscriptionWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(NewInitState()),
		dex.NonStartingStateDef(NewTrialState(b.svc)),
		dex.NonStartingStateDef(NewChargeCurrentBillState(b.svc)),
		dex.NonStartingStateDef(NewCancelState(b.svc)),
		dex.NonStartingStateDef(NewUpdateChargeAmountState()),
	}
}

func (b SubscriptionWorkflow) GetPersistenceSchema() []dex.PersistenceFieldDef {
	return []dex.PersistenceFieldDef{
		dex.DataAttributeDef(keyBillingPeriodNum),
		dex.DataAttributeDef(keyCustomer),
	}
}

func (b SubscriptionWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(SignalCancelSubscription),
		dex.SignalChannelDef(SignalUpdateBillingPeriodChargeAmount),
		dex.RPCMethodDef(b.Describe, nil),
	}
}

func (b SubscriptionWorkflow) Describe(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)
	return customer.Subscription, nil
}

type Subscription struct {
	TrialPeriod         time.Duration
	BillingPeriod       time.Duration
	MaxBillingPeriods   int
	BillingPeriodCharge int
}

type Customer struct {
	FirstName    string
	LastName     string
	Id           string
	Email        string
	Subscription Subscription
}

func NewInitState() dex.WorkflowState {
	return initState{}
}

type initState struct {
	dex.WorkflowStateDefaults
}

func (b initState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var customer Customer
	input.Get(&customer)
	persistence.SetDataAttribute(keyCustomer, customer)
	return dex.EmptyCommandRequest(), nil
}

func (b initState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.MultiNextStates(trialState{}, cancelState{}, updateChargeAmountState{}), nil
}

func NewTrialState(svc service.MyService) dex.WorkflowState {
	return trialState{
		svc: svc,
	}
}

type trialState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (b trialState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	// send welcome email
	b.svc.SendEmail(customer.Email, "welcome email", "hello content")

	return dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", customer.Subscription.TrialPeriod),
	), nil
}

func (b trialState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	persistence.SetDataAttribute(keyBillingPeriodNum, 0)
	return dex.SingleNextState(chargeCurrentBillState{}, nil), nil
}

func NewChargeCurrentBillState(svc service.MyService) dex.WorkflowState {
	return chargeCurrentBillState{
		svc: svc,
	}
}

type chargeCurrentBillState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

const subscriptionOverKey = "subscriptionOver"

func (b chargeCurrentBillState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var periodNum int
	persistence.GetDataAttribute(keyBillingPeriodNum, &periodNum)

	if periodNum >= customer.Subscription.MaxBillingPeriods {
		persistence.SetStateExecutionLocal(subscriptionOverKey, true)
		return dex.EmptyCommandRequest(), nil
	}

	persistence.SetDataAttribute(keyBillingPeriodNum, periodNum+1)

	return dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", customer.Subscription.BillingPeriod),
	), nil
}

func (b chargeCurrentBillState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var subscriptionOver bool
	persistence.GetStateExecutionLocal(subscriptionOverKey, &subscriptionOver)
	if subscriptionOver {
		b.svc.SendEmail(customer.Email, "subscription over", "hello content")
		// use force completing because the cancel state is still waiting for signal
		return dex.ForceCompletingWorkflow, nil
	}

	b.svc.ChargeUser(customer.Email, customer.Id, customer.Subscription.BillingPeriodCharge)

	return dex.SingleNextState(chargeCurrentBillState{}, nil), nil
}

func NewCancelState(svc service.MyService) dex.WorkflowState {
	return cancelState{
		svc: svc,
	}
}

type cancelState struct {
	dex.WorkflowStateDefaults
	svc service.MyService
}

func (b cancelState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AllCommandsCompletedRequest(
		dex.NewSignalCommand("", SignalCancelSubscription),
	), nil
}

func (b cancelState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	b.svc.SendEmail(customer.Email, "subscription canceled", "hello content")
	return dex.ForceCompletingWorkflow, nil
}

func NewUpdateChargeAmountState() dex.WorkflowState {
	return updateChargeAmountState{}
}

type updateChargeAmountState struct {
	dex.WorkflowStateDefaults
}

func (b updateChargeAmountState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AllCommandsCompletedRequest(
		dex.NewSignalCommand("", SignalUpdateBillingPeriodChargeAmount),
	), nil
}

func (b updateChargeAmountState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var newAmount int
	commandResults.GetSignalCommandResultByChannel(SignalUpdateBillingPeriodChargeAmount).SignalValue.Get(&newAmount)

	customer.Subscription.BillingPeriodCharge = newAmount
	persistence.SetDataAttribute(keyCustomer, customer)

	return dex.SingleNextState(updateChargeAmountState{}, nil), nil
}

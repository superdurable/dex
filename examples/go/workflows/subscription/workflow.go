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
	"github.com/superdurable/iwf/examples/go/workflows/service"
	"github.com/superdurable/iwf/sdk-go/iwf"
	"time"
)

type SubscriptionWorkflow struct {
	iwf.DefaultWorkflowType

	svc service.MyService
}

func NewSubscriptionWorkflow(svc service.MyService) iwf.ObjectWorkflow {
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

func (b SubscriptionWorkflow) GetWorkflowStates() []iwf.StateDef {
	return []iwf.StateDef{
		iwf.StartingStateDef(NewInitState()),
		iwf.NonStartingStateDef(NewTrialState(b.svc)),
		iwf.NonStartingStateDef(NewChargeCurrentBillState(b.svc)),
		iwf.NonStartingStateDef(NewCancelState(b.svc)),
		iwf.NonStartingStateDef(NewUpdateChargeAmountState()),
	}
}

func (b SubscriptionWorkflow) GetPersistenceSchema() []iwf.PersistenceFieldDef {
	return []iwf.PersistenceFieldDef{
		iwf.DataAttributeDef(keyBillingPeriodNum),
		iwf.DataAttributeDef(keyCustomer),
	}
}

func (b SubscriptionWorkflow) GetCommunicationSchema() []iwf.CommunicationMethodDef {
	return []iwf.CommunicationMethodDef{
		iwf.SignalChannelDef(SignalCancelSubscription),
		iwf.SignalChannelDef(SignalUpdateBillingPeriodChargeAmount),
		iwf.RPCMethodDef(b.Describe, nil),
	}
}

func (b SubscriptionWorkflow) Describe(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (interface{}, error) {
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

func NewInitState() iwf.WorkflowState {
	return initState{}
}

type initState struct {
	iwf.WorkflowStateDefaults
}

func (b initState) WaitUntil(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (*iwf.CommandRequest, error) {
	var customer Customer
	input.Get(&customer)
	persistence.SetDataAttribute(keyCustomer, customer)
	return iwf.EmptyCommandRequest(), nil
}

func (b initState) Execute(ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence, communication iwf.Communication) (*iwf.StateDecision, error) {
	return iwf.MultiNextStates(trialState{}, cancelState{}, updateChargeAmountState{}), nil
}

func NewTrialState(svc service.MyService) iwf.WorkflowState {
	return trialState{
		svc: svc,
	}
}

type trialState struct {
	iwf.WorkflowStateDefaults
	svc service.MyService
}

func (b trialState) WaitUntil(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (*iwf.CommandRequest, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	// send welcome email
	b.svc.SendEmail(customer.Email, "welcome email", "hello content")

	return iwf.AllCommandsCompletedRequest(
		iwf.NewTimerCommandByDuration("", customer.Subscription.TrialPeriod),
	), nil
}

func (b trialState) Execute(ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence, communication iwf.Communication) (*iwf.StateDecision, error) {
	persistence.SetDataAttribute(keyBillingPeriodNum, 0)
	return iwf.SingleNextState(chargeCurrentBillState{}, nil), nil
}

func NewChargeCurrentBillState(svc service.MyService) iwf.WorkflowState {
	return chargeCurrentBillState{
		svc: svc,
	}
}

type chargeCurrentBillState struct {
	iwf.WorkflowStateDefaults
	svc service.MyService
}

const subscriptionOverKey = "subscriptionOver"

func (b chargeCurrentBillState) WaitUntil(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (*iwf.CommandRequest, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var periodNum int
	persistence.GetDataAttribute(keyBillingPeriodNum, &periodNum)

	if periodNum >= customer.Subscription.MaxBillingPeriods {
		persistence.SetStateExecutionLocal(subscriptionOverKey, true)
		return iwf.EmptyCommandRequest(), nil
	}

	persistence.SetDataAttribute(keyBillingPeriodNum, periodNum+1)

	return iwf.AllCommandsCompletedRequest(
		iwf.NewTimerCommandByDuration("", customer.Subscription.BillingPeriod),
	), nil
}

func (b chargeCurrentBillState) Execute(ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence, communication iwf.Communication) (*iwf.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var subscriptionOver bool
	persistence.GetStateExecutionLocal(subscriptionOverKey, &subscriptionOver)
	if subscriptionOver {
		b.svc.SendEmail(customer.Email, "subscription over", "hello content")
		// use force completing because the cancel state is still waiting for signal
		return iwf.ForceCompletingWorkflow, nil
	}

	b.svc.ChargeUser(customer.Email, customer.Id, customer.Subscription.BillingPeriodCharge)

	return iwf.SingleNextState(chargeCurrentBillState{}, nil), nil
}

func NewCancelState(svc service.MyService) iwf.WorkflowState {
	return cancelState{
		svc: svc,
	}
}

type cancelState struct {
	iwf.WorkflowStateDefaults
	svc service.MyService
}

func (b cancelState) WaitUntil(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (*iwf.CommandRequest, error) {
	return iwf.AllCommandsCompletedRequest(
		iwf.NewSignalCommand("", SignalCancelSubscription),
	), nil
}

func (b cancelState) Execute(ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence, communication iwf.Communication) (*iwf.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	b.svc.SendEmail(customer.Email, "subscription canceled", "hello content")
	return iwf.ForceCompletingWorkflow, nil
}

func NewUpdateChargeAmountState() iwf.WorkflowState {
	return updateChargeAmountState{}
}

type updateChargeAmountState struct {
	iwf.WorkflowStateDefaults
}

func (b updateChargeAmountState) WaitUntil(ctx iwf.WorkflowContext, input iwf.Object, persistence iwf.Persistence, communication iwf.Communication) (*iwf.CommandRequest, error) {
	return iwf.AllCommandsCompletedRequest(
		iwf.NewSignalCommand("", SignalUpdateBillingPeriodChargeAmount),
	), nil
}

func (b updateChargeAmountState) Execute(ctx iwf.WorkflowContext, input iwf.Object, commandResults iwf.CommandResults, persistence iwf.Persistence, communication iwf.Communication) (*iwf.StateDecision, error) {
	var customer Customer
	persistence.GetDataAttribute(keyCustomer, &customer)

	var newAmount int
	commandResults.GetSignalCommandResultByChannel(SignalUpdateBillingPeriodChargeAmount).SignalValue.Get(&newAmount)

	customer.Subscription.BillingPeriodCharge = newAmount
	persistence.SetDataAttribute(keyCustomer, customer)

	return iwf.SingleNextState(updateChargeAmountState{}, nil), nil
}

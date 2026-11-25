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
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type ClaimRejection struct {
	Reason        string `json:"reason"`
	SubmitAttempt int32  `json:"submitAttempt"`
}

type PayoutOutcome struct {
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	SubmitAttempt int32  `json:"submitAttempt"`
}

var PayoutOutcomeAttribute = dex.DefineAttribute[PayoutOutcome]("PayoutOutcome")

type ExpenseReimbursementFlow struct {
	dex.FlowDefaults
	provider *FakePaymentProvider
}

func NewExpenseReimbursementFlow(provider *FakePaymentProvider) *ExpenseReimbursementFlow {
	return &ExpenseReimbursementFlow{provider: provider}
}

func (flow *ExpenseReimbursementFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SubmitPayout{provider: flow.provider}),
		dex.DefineStep(RecordRejectedClaim{}),
	}
}

func (flow *ExpenseReimbursementFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*ExpenseReimbursementFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{PayoutOutcomeAttribute}}
}

func (*ExpenseReimbursementFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ExpenseReimbursementFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:payout group-label:"Payout"
// dex:explanation text:"Submit the payout; retry a timeout and route a rejection at once."
type SubmitPayout struct {
	dex.StepDefaultsNoWaitFor[ExpenseClaim]
	provider *FakePaymentProvider
}

func (SubmitPayout) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Minute,
			TotalDuration:      time.Hour,
		},
	}
}

func (step SubmitPayout) Execute(ctx dex.Context, claim ExpenseClaim) (*dex.StepDecision, error) {
	payoutID, err := step.provider.SubmitPayout(ctx.FlowID(), claim)
	var rejection *PayoutRejectedError
	if errors.As(err, &rejection) {
		return dex.GoTo(RecordRejectedClaim{}, ClaimRejection{Reason: rejection.Reason, SubmitAttempt: ctx.Attempt()}), nil
	}
	if err != nil {
		return nil, dex.ErrorWithStack(err)
	}
	if err := PayoutOutcomeAttribute.Set(ctx, PayoutOutcome{Status: "PAID", Detail: payoutID, SubmitAttempt: ctx.Attempt()}); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(payoutID), nil
}

// dex:group group-id:rejection group-label:"Rejection"
// dex:explanation text:"Record the rejected claim and its reason."
type RecordRejectedClaim struct {
	dex.StepDefaultsNoWaitFor[ClaimRejection]
}

func (RecordRejectedClaim) Execute(ctx dex.Context, rejection ClaimRejection) (*dex.StepDecision, error) {
	outcome := PayoutOutcome{Status: "REJECTED", Detail: rejection.Reason, SubmitAttempt: rejection.SubmitAttempt}
	if err := PayoutOutcomeAttribute.Set(ctx, outcome); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("rejected: " + rejection.Reason), nil
}

var _ dex.Flow = (*ExpenseReimbursementFlow)(nil)

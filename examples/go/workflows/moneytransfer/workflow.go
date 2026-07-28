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

package moneytransfer

import (
	"fmt"
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

func NewMoneyTransferWorkflow(svc service.MyService) dex.ObjectWorkflow {

	return &MoneyTransferWorkflow{
		svc: svc,
	}
}

type MoneyTransferWorkflow struct {
	dex.WorkflowDefaults

	svc service.MyService
}

func (e MoneyTransferWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&checkBalanceState{svc: e.svc}),
		dex.NonStartingStateDef(&createDebitMemoState{svc: e.svc}),
		dex.NonStartingStateDef(&debitState{svc: e.svc}),
		dex.NonStartingStateDef(&createCreditMemoState{svc: e.svc}),
		dex.NonStartingStateDef(&creditState{svc: e.svc}),
		dex.NonStartingStateDef(&compensateState{svc: e.svc}),
	}
}

type TransferRequest struct {
	FromAccount string
	ToAccount   string
	Amount      int
	Notes       string
}

type checkBalanceState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i checkBalanceState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var request TransferRequest
	input.Get(&request)

	hasSufficientFunds := i.svc.CheckBalance(request.FromAccount, request.Amount)
	if !hasSufficientFunds {
		return dex.ForceFailWorkflow("insufficient funds"), nil
	}

	return dex.SingleNextState(&createDebitMemoState{}, request), nil
}

type createDebitMemoState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i createDebitMemoState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var request TransferRequest
	input.Get(&request)

	err := i.svc.CreateDebitMemo(request.FromAccount, request.Amount, request.Notes)
	if err != nil {
		return nil, err
	}

	// uncomment this to test error case 
	//if true {
	//	return nil, fmt.Errorf("test error for testing error handling")
	//}

	return dex.SingleNextState(&debitState{}, request), nil
}

func (i createDebitMemoState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttemptsDurationSeconds: ptr.Any(int32(3600)),
			// uncomment this to test a short retry
			//MaximumAttemptsDurationSeconds: ptr.Any(int32(3)),
		},
		ExecuteApiFailureProceedState: &compensateState{},
	}
}

type debitState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i debitState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var request TransferRequest
	input.Get(&request)

	err := i.svc.Debit(request.FromAccount, request.Amount)
	if err != nil {
		return nil, err
	}

	return dex.SingleNextState(&createCreditMemoState{}, request), nil
}

func (i debitState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttemptsDurationSeconds: ptr.Any(int32(3600)),
		},
		ExecuteApiFailureProceedState: &compensateState{},
	}
}

type createCreditMemoState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i createCreditMemoState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var request TransferRequest
	input.Get(&request)

	err := i.svc.CreateCreditMemo(request.ToAccount, request.Amount, request.Notes)
	if err != nil {
		return nil, err
	}

	return dex.SingleNextState(&creditState{}, request), nil
}

func (i createCreditMemoState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttemptsDurationSeconds: ptr.Any(int32(3600)),
		},
		ExecuteApiFailureProceedState: &compensateState{},
	}
}

type creditState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i creditState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	var request TransferRequest
	input.Get(&request)

	err := i.svc.Credit(request.ToAccount, request.Amount)
	if err != nil {
		return nil, err
	}

	return dex.GracefulCompleteWorkflow(fmt.Sprintf("transfer is done from %v to %v for amount %v", request.FromAccount, request.ToAccount, request.Amount)), nil
}

func (i creditState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttemptsDurationSeconds: ptr.Any(int32(3600)),
		},
		ExecuteApiFailureProceedState: &compensateState{},
	}
}

type compensateState struct {
	dex.WorkflowStateDefaultsNoWaitUntil
	svc service.MyService
}

func (i compensateState) Execute(
	ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence,
	communication dex.Communication,
) (*dex.StateDecision, error) {
	// NOTE: to improve, we can use Dex data attributes to track whether each step has been attempted to execute
	// and check a flag to see if we should undo it or not

	var request TransferRequest
	input.Get(&request)

	err := i.svc.UndoCredit(request.ToAccount, request.Amount)
	if err != nil {
		return nil, err
	}
	err = i.svc.UndoCreateCreditMemo(request.ToAccount, request.Amount, request.Notes)
	if err != nil {
		return nil, err
	}
	err = i.svc.UndoCreateDebitMemo(request.FromAccount, request.Amount, request.Notes)
	if err != nil {
		return nil, err
	}
	err = i.svc.UndoDebit(request.FromAccount, request.Amount)
	if err != nil {
		return nil, err
	}

	return dex.ForceFailWorkflow(fmt.Sprintf("transfer has failed: from %v to %v for amount %v", request.FromAccount, request.ToAccount, request.Amount)), nil
}

func (i compensateState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttemptsDurationSeconds: ptr.Any(int32(86400)),
		},
	}
}

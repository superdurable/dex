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
	"time"

	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

type TransferRequest struct {
	FromAccount string
	ToAccount   string
	Amount      int
	Notes       string
}

type MoneyTransferFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewMoneyTransferFlow(applicationService service.MyService) *MoneyTransferFlow {
	return &MoneyTransferFlow{service: applicationService}
}

func (flow *MoneyTransferFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(checkBalance{service: flow.service}),
		dex.DefineStep(createDebitMemo{service: flow.service}),
		dex.DefineStep(debit{service: flow.service}),
		dex.DefineStep(createCreditMemo{service: flow.service}),
		dex.DefineStep(creditStep{service: flow.service}),
		dex.DefineStep(compensate{service: flow.service}),
	}
}

func (*MoneyTransferFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type checkBalance struct {
	dex.StepDefaultsNoWaitFor[TransferRequest]
	service service.MyService
}

func (step checkBalance) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	if !step.service.CheckBalance(request.FromAccount, request.Amount) {
		return dex.ForceFail("insufficient funds"), nil
	}
	return dex.GoTo(createDebitMemo{}, request), nil
}

type createDebitMemo struct {
	dex.DefaultStepType
	dex.NoWaitFor[TransferRequest]
	service service.MyService
}

func (createDebitMemo) GetStepOptions() *dex.StepOptions {
	return compensatedStepOptions(time.Hour)
}

func (step createDebitMemo) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	if err := step.service.CreateDebitMemo(
		request.FromAccount,
		request.Amount,
		request.Notes,
	); err != nil {
		return nil, err
	}
	return dex.GoTo(debit{}, request), nil
}

type debit struct {
	dex.DefaultStepType
	dex.NoWaitFor[TransferRequest]
	service service.MyService
}

func (debit) GetStepOptions() *dex.StepOptions {
	return compensatedStepOptions(time.Hour)
}

func (step debit) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	if err := step.service.Debit(request.FromAccount, request.Amount); err != nil {
		return nil, err
	}
	return dex.GoTo(createCreditMemo{}, request), nil
}

type createCreditMemo struct {
	dex.DefaultStepType
	dex.NoWaitFor[TransferRequest]
	service service.MyService
}

func (createCreditMemo) GetStepOptions() *dex.StepOptions {
	return compensatedStepOptions(time.Hour)
}

func (step createCreditMemo) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	if err := step.service.CreateCreditMemo(
		request.ToAccount,
		request.Amount,
		request.Notes,
	); err != nil {
		return nil, err
	}
	return dex.GoTo(creditStep{}, request), nil
}

type creditStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[TransferRequest]
	service service.MyService
}

func (creditStep) GetStepOptions() *dex.StepOptions {
	return compensatedStepOptions(time.Hour)
}

func (step creditStep) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	if err := step.service.Credit(request.ToAccount, request.Amount); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(fmt.Sprintf(
		"transfer is done from %s to %s for amount %d",
		request.FromAccount,
		request.ToAccount,
		request.Amount,
	)), nil
}

type compensate struct {
	dex.DefaultStepType
	dex.NoWaitFor[TransferRequest]
	service service.MyService
}

func (compensate) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{TotalDuration: 24 * time.Hour},
	}
}

func (step compensate) Execute(
	_ dex.Context,
	request TransferRequest,
) (*dex.StepDecision, error) {
	// NOTE: to improve, we can use Dex data attributes to track whether each step has been attempted to execute
	// and check a flag to see if we should undo it or not
	if err := step.service.UndoCredit(request.ToAccount, request.Amount); err != nil {
		return nil, err
	}
	if err := step.service.UndoCreateCreditMemo(
		request.ToAccount,
		request.Amount,
		request.Notes,
	); err != nil {
		return nil, err
	}
	if err := step.service.UndoCreateDebitMemo(
		request.FromAccount,
		request.Amount,
		request.Notes,
	); err != nil {
		return nil, err
	}
	if err := step.service.UndoDebit(request.FromAccount, request.Amount); err != nil {
		return nil, err
	}
	return dex.ForceFail(fmt.Sprintf(
		"transfer has failed from %s to %s for amount %d",
		request.FromAccount,
		request.ToAccount,
		request.Amount,
	)), nil
}

func compensatedStepOptions(totalDuration time.Duration) *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{TotalDuration: totalDuration},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(
			compensate{},
			&dex.StepOptions{
				ExecuteRetry: &dex.RetryPolicy{TotalDuration: 24 * time.Hour},
			},
		),
	}
}

var _ dex.Flow = (*MoneyTransferFlow)(nil)

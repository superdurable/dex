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

package entityflow

import (
	"errors"
	"strings"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	DisplayName   = dex.DefineAttribute[string]("display_name")
	Email         = dex.DefineAttribute[string]("email")
	CreditBalance = dex.DefineAttribute[int64]("credit_balance")
	Retired       = dex.DefineAttribute[bool]("retired")
)

func CustomerAccountFlowID(accountID string) string {
	return "customer-account-" + accountID
}

type ContactDetails struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func (contact ContactDetails) Validate() error {
	if strings.TrimSpace(contact.DisplayName) == "" {
		return errors.New("display name is required")
	}
	if !strings.Contains(contact.Email, "@") {
		return errors.New("email must contain @")
	}
	return nil
}

type CreditAdjustment struct {
	Amount int64 `json:"amount"`
}

type CustomerAccountFlow struct {
	dex.FlowDefaults
}

func NewCustomerAccountFlow() *CustomerAccountFlow {
	return &CustomerAccountFlow{}
}

func (*CustomerAccountFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStep(CloseAccountStep{})}
}

func (flow *CustomerAccountFlow) GetRPCs() []dex.RPCDef {
	balanceLock := &dex.RPCOptions{
		LockAttributes: []dex.AttributeLock{dex.LockAttribute(CreditBalance)},
	}
	return []dex.RPCDef{
		dex.DefineRPC(flow.UpdateContactDetails, nil),
		dex.DefineRPC(flow.AdjustCreditBalance, balanceLock),
		dex.DefineRPC(flow.RetireAccount, balanceLock),
	}
}

func (*CustomerAccountFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{DisplayName, Email, CreditBalance, Retired},
	}
}

func (*CustomerAccountFlow) UpdateContactDetails(
	ctx dex.Context,
	contact ContactDetails,
) (*dex.RPCResult[dex.None], error) {
	if err := contact.Validate(); err != nil {
		return nil, err
	}
	if err := DisplayName.Set(ctx, contact.DisplayName); err != nil {
		return nil, err
	}
	if err := Email.Set(ctx, contact.Email); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*CustomerAccountFlow) AdjustCreditBalance(
	ctx dex.Context,
	adjustment CreditAdjustment,
) (*dex.RPCResult[int64], error) {
	retired, err := Retired.Get(ctx)
	if err != nil {
		return nil, err
	}
	if retired {
		return nil, errors.New("account is retired")
	}
	if adjustment.Amount == 0 {
		return nil, errors.New("amount must not be zero")
	}
	balance, err := CreditBalance.Get(ctx)
	if err != nil {
		return nil, err
	}
	balance += adjustment.Amount
	if balance < 0 {
		return nil, errors.New("credit balance cannot go below zero")
	}
	if err := CreditBalance.Set(ctx, balance); err != nil {
		return nil, err
	}
	return &dex.RPCResult[int64]{Output: balance}, nil
}

func (*CustomerAccountFlow) RetireAccount(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	balance, err := CreditBalance.Get(ctx)
	if err != nil {
		return nil, err
	}
	if balance != 0 {
		return nil, errors.New("spend or refund the credit balance before retiring")
	}
	if err := Retired.Set(ctx, true); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{
		NextSteps: []dex.StepMovement{dex.MovementOf(CloseAccountStep{}, nil)},
	}, nil
}

// dex:group group-id:account-retirement group-label:"Account retirement"
// dex:explanation text:"Complete the account Flow after the retire request passes its checks."
type CloseAccountStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (CloseAccountStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

var (
	_ dex.Flow                          = (*CustomerAccountFlow)(nil)
	_ dex.RPC[ContactDetails, dex.None] = (*CustomerAccountFlow)(nil).UpdateContactDetails
	_ dex.RPC[CreditAdjustment, int64]  = (*CustomerAccountFlow)(nil).AdjustCreditBalance
	_ dex.RPC[dex.None, dex.None]       = (*CustomerAccountFlow)(nil).RetireAccount
)

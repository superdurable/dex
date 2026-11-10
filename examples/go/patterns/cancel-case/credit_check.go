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

package cancelcase

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// CreditCheckRequest is the input of one credit check child Flow.
type CreditCheckRequest struct {
	ApplicantID string `json:"applicantId"`
	// ReportDelaySeconds simulates how long the credit bureau takes to answer.
	ReportDelaySeconds int `json:"reportDelaySeconds"`
}

// CreditCheckFlow is the child Flow that LoanApplicationFlow starts.
type CreditCheckFlow struct{ dex.FlowDefaults }

func NewCreditCheckFlow() *CreditCheckFlow {
	return &CreditCheckFlow{}
}

func (*CreditCheckFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(AwaitCreditReport{})}
}

func (*CreditCheckFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// AwaitCreditReport waits for the simulated credit bureau to answer.
type AwaitCreditReport struct{ dex.StepDefaults }

func (AwaitCreditReport) WaitFor(_ dex.Context, request CreditCheckRequest) (*dex.Wait, error) {
	return dex.Until(dex.Timer(time.Duration(request.ReportDelaySeconds) * time.Second)), nil
}

func (AwaitCreditReport) Execute(_ dex.Context, request CreditCheckRequest) (*dex.StepDecision, error) {
	return dex.GracefulComplete("credit report for " + request.ApplicantID), nil
}

var _ dex.Flow = (*CreditCheckFlow)(nil)

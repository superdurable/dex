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
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// CreditCheckTimeout is the child's own Flow timeout. It stops the child even
// when the parent closes without stopping it.
const CreditCheckTimeout = 7 * 24 * time.Hour

const applicationCanceledPrefix = "application canceled: "

// LoanApplication is the input of LoanApplicationFlow.
type LoanApplication struct {
	ApplicantID              string `json:"applicantId"`
	CreditReportDelaySeconds int    `json:"creditReportDelaySeconds"`
}

var (
	StoppedCreditCheckFlowID = dex.DefineAttribute[string]("stoppedCreditCheckFlowId")
	ApplicationCanceled      = dex.DefineChannel[string]("applicationCanceled")
	UnderwriterDecided       = dex.DefineChannel[string]("underwriterDecided")
	errClientUnavailable     = errors.New("cancelcase: Client is not ready")
)

// LoanApplicationFlow runs a credit check in a child Flow. Canceling the
// application also stops the child Flow.
type LoanApplicationFlow struct {
	dex.FlowDefaults
	getClient       func() *dex.Client
	creditCheckFlow *CreditCheckFlow
}

// NewLoanApplicationFlow creates the parent Flow; getClient may return nil until bootstrap completes.
func NewLoanApplicationFlow(getClient func() *dex.Client, creditCheckFlow *CreditCheckFlow) *LoanApplicationFlow {
	if getClient == nil {
		panic("cancelcase: Client provider is required")
	}
	if creditCheckFlow == nil {
		panic("cancelcase: CreditCheckFlow is required")
	}
	return &LoanApplicationFlow{getClient: getClient, creditCheckFlow: creditCheckFlow}
}

func (flow *LoanApplicationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RunCreditCheck{getClient: flow.getClient, creditCheckFlow: flow.creditCheckFlow}),
		dex.DefineStep(AwaitUnderwriterDecision{}),
	}
}

func (flow *LoanApplicationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.CancelApplication, nil),
		dex.DefineRPC(flow.DecideApplication, nil),
	}
}

func (*LoanApplicationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{StoppedCreditCheckFlowID},
		Channels:   []dex.ChannelDef{ApplicationCanceled, UnderwriterDecided},
	}
}

// CancelApplication cancels the application and the credit check it started.
func (*LoanApplicationFlow) CancelApplication(ctx dex.Context, reason string) (*dex.RPCResult[dex.None], error) {
	if err := ApplicationCanceled.Publish(ctx, reason); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// DecideApplication records the underwriter's decision.
func (*LoanApplicationFlow) DecideApplication(ctx dex.Context, decision string) (*dex.RPCResult[dex.None], error) {
	if err := UnderwriterDecided.Publish(ctx, decision); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// RunCreditCheck waits for its child Flow or a cancellation, whichever comes first.
type RunCreditCheck struct {
	dex.StepDefaults
	getClient       func() *dex.Client
	creditCheckFlow *CreditCheckFlow
}

func (step RunCreditCheck) WaitFor(_ dex.Context, application LoanApplication) (*dex.Wait, error) {
	timeout := CreditCheckTimeout
	request := CreditCheckRequest{
		ApplicantID:        application.ApplicantID,
		ReportDelaySeconds: application.CreditReportDelaySeconds,
	}
	return dex.AnyOf(
		dex.SubFlow(step.creditCheckFlow, request, dex.SubFlowOptions{
			Timeout:     &timeout,
			ReusePolicy: dex.RestartSubFlowIfPreviousExitedAbnormally,
		}),
		ApplicationCanceled.ForOne(),
	), nil
}

func (step RunCreditCheck) Execute(ctx dex.Context, _ LoanApplication) (*dex.StepDecision, error) {
	reasons, err := ApplicationCanceled.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(reasons) > 0 {
		childFlowID, err := dex.SubFlowID(ctx)
		if err != nil {
			return nil, err
		}
		if err := step.stopCreditCheck(ctx, childFlowID); err != nil {
			return nil, err
		}
		if err := StoppedCreditCheckFlowID.Set(ctx, childFlowID); err != nil {
			return nil, err
		}
		return dex.ForceFail(applicationCanceledPrefix + reasons[0]), nil
	}
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	if result.Status != dex.FlowCompleted {
		return dex.ForceFail(fmt.Sprintf("credit check ended with status %v", result.Status)), nil
	}
	var report string
	if err := result.DecodeSingleOutput(&report); err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitUnderwriterDecision{}, report), nil
}

// stopCreditCheck cancels the child; a missing or already closed child counts as stopped.
func (step RunCreditCheck) stopCreditCheck(ctx dex.Context, childFlowID string) error {
	client := step.getClient()
	if client == nil {
		return errClientUnavailable
	}
	err := client.StopFlow(ctx, childFlowID, dex.StopOptions{
		Type:   dex.CancelFlow,
		Reason: "loan application canceled",
	})
	var notFound *dex.FlowNotFoundError
	var notActive *dex.FlowNotActiveOrNotFoundError
	var closing *dex.FlowClosingError
	if errors.As(err, &notFound) || errors.As(err, &notActive) || errors.As(err, &closing) {
		return nil
	}
	return err
}

// AwaitUnderwriterDecision also waits for the cancel Channel, so a cancellation
// after the credit check still closes the application.
type AwaitUnderwriterDecision struct{ dex.StepDefaults }

func (AwaitUnderwriterDecision) WaitFor(dex.Context, string) (*dex.Wait, error) {
	return dex.AnyOf(UnderwriterDecided.ForOne(), ApplicationCanceled.ForOne()), nil
}

func (AwaitUnderwriterDecision) Execute(ctx dex.Context, report string) (*dex.StepDecision, error) {
	reasons, err := ApplicationCanceled.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(reasons) > 0 {
		return dex.ForceFail(applicationCanceledPrefix + reasons[0]), nil
	}
	decisions, err := UnderwriterDecided.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(decisions[0] + " after " + report), nil
}

var _ dex.Flow = (*LoanApplicationFlow)(nil)

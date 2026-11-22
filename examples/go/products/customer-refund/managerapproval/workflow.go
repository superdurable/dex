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

// Package managerapproval checks a refund request, asks a manager when needed, refunds, and tells the customer.
package managerapproval

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	RefundWindowDays            = 30
	ManagerApprovalLimitUSD     = 1000.00
	AwaitingManager             = "awaiting-manager"
	ManageRefundsPermission     = "refund.manage"
	refundIdempotencyPrefix     = "refund-"
	emailIdempotencyPrefix      = "email-"
	outcomeRefunded             = "refunded"
	outcomeDenied               = "denied"
	refundWindowDeniedNote      = "The charge is older than our 30-day refund window."
	noInterruptionDeniedNote    = "We found no service interruption on this order."
	reasonRequiredMessage       = "a reason is required"
	notAwaitingManagerMessage   = "this refund is not waiting for a manager"
	notAwaitingManagerErrorType = "managerapproval.RefundNotAwaitingManagerError"
)

// RefundNotAwaitingManagerError rejects a decision when the case is outside manager review.
type RefundNotAwaitingManagerError struct{}

func (RefundNotAwaitingManagerError) Error() string {
	return notAwaitingManagerMessage
}

// Readable without the payload key, so Dex Web can show and match them.
var (
	CaseStatus    = dex.DefineAttribute[string]("case-status", dex.Plaintext())
	CustomerEmail = dex.DefineAttribute[string]("customer-email", dex.Plaintext())
)

// Encrypted when the Worker has a payload key.
var (
	RefundID        = dex.DefineAttribute[string]("refund-id")
	CustomerMessage = dex.DefineAttribute[string]("customer-message")
)

var managerDecisions = dex.DefineChannel[ManagerDecision]("manager-decisions")

// RefundRequest is the run input: who asks, for which order, and why.
type RefundRequest struct {
	Customer                  string `json:"customer"`
	CustomerEmail             string `json:"customerEmail"`
	OrderID                   string `json:"orderId"`
	Reason                    string `json:"reason"`
	PaymentTimeoutsToSimulate int    `json:"paymentTimeoutsToSimulate,omitempty"`
}

// ManagerDecision is the message a manager's Action publishes to the run.
type ManagerDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
}

// DecisionReason is the input of the Approve refund and Reject refund Actions.
type DecisionReason struct {
	Reason string `json:"reason"`
}

// RefundCase carries the request, the order, and the outcome from Step to Step.
type RefundCase struct {
	Request  RefundRequest `json:"request"`
	Order    Order         `json:"order"`
	Outcome  string        `json:"outcome,omitempty"`
	Note     string        `json:"note,omitempty"`
	RefundID string        `json:"refundId,omitempty"`
	Message  string        `json:"message,omitempty"`
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Record the refund request and the customer's email address."
type ReceiveRequest struct {
	dex.StepDefaultsNoWaitFor[RefundRequest]
}

func (ReceiveRequest) Execute(ctx dex.Context, input RefundRequest) (*dex.StepDecision, error) {
	if err := CaseStatus.Set(ctx, "received"); err != nil {
		return nil, err
	}
	if err := CustomerEmail.Set(ctx, input.CustomerEmail); err != nil {
		return nil, err
	}
	return dex.GoTo(CheckOrder{}, input), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Look up the order in billing."
type CheckOrder struct {
	dex.StepDefaultsNoWaitFor[RefundRequest]
	billing BillingService
}

func (step CheckOrder) Execute(_ dex.Context, input RefundRequest) (*dex.StepDecision, error) {
	order, err := step.billing.GetOrder(input.OrderID)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(CheckPolicy{}, RefundCase{Request: input, Order: order}), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Refund, deny, or ask a manager, by the refund policy."
type CheckPolicy struct {
	dex.StepDefaultsNoWaitFor[RefundCase]
}

func (CheckPolicy) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	order := input.Order
	if order.DaysSinceCharge > RefundWindowDays {
		return denyRefund(ctx, input, refundWindowDeniedNote)
	}
	if order.OutageHours == 0 {
		return denyRefund(ctx, input, noInterruptionDeniedNote)
	}
	if order.AmountUSD > ManagerApprovalLimitUSD {
		if err := CaseStatus.Set(ctx, AwaitingManager); err != nil {
			return nil, err
		}
		return dex.GoTo(RequestManagerApproval{}, input), nil
	}
	return dex.GoTo(IssueRefund{}, input), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Wait for a manager to approve or reject the refund."
type RequestManagerApproval struct {
	dex.StepDefaults
}

func (RequestManagerApproval) WaitFor(dex.Context, RefundCase) (*dex.Wait, error) {
	return dex.Until(managerDecisions.ForOne()), nil
}

func (RequestManagerApproval) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	decisions, err := managerDecisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(decisions) != 1 {
		return nil, fmt.Errorf("expected one manager decision, got %d", len(decisions))
	}
	if decisions[0].Approved {
		return dex.GoTo(IssueRefund{}, input), nil
	}
	return denyRefund(ctx, input, decisions[0].Reason)
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Refund the order through billing, once per case."
type IssueRefund struct {
	dex.StepDefaultsNoWaitFor[RefundCase]
	billing BillingService
}

func (IssueRefund) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 1.0,
			MaximumAttempts:    10,
		},
	}
}

func (step IssueRefund) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	simulateTimeout := int(ctx.Attempt()) <= input.Request.PaymentTimeoutsToSimulate
	refundID, err := step.billing.IssueRefund(input.Order, refundIdempotencyPrefix+ctx.FlowID(), simulateTimeout)
	if err != nil {
		return nil, err
	}
	if err := RefundID.Set(ctx, refundID); err != nil {
		return nil, err
	}
	if err := CaseStatus.Set(ctx, "refunded"); err != nil {
		return nil, err
	}
	input.Outcome = outcomeRefunded
	input.RefundID = refundID
	return dex.GoTo(WriteCustomerMessage{}, input), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Ask the model to write the customer message."
type WriteCustomerMessage struct {
	dex.StepDefaultsNoWaitFor[RefundCase]
	model *MockMessageModel
}

func (step WriteCustomerMessage) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	message := step.model.WriteMessage(input.Request.Customer, input.Outcome, input.Order.AmountUSD, input.Note)
	if err := CustomerMessage.Set(ctx, message); err != nil {
		return nil, err
	}
	input.Message = message
	return dex.GoTo(SendMessage{}, input), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Email the message to the customer, once per case."
type SendMessage struct {
	dex.StepDefaultsNoWaitFor[RefundCase]
	email *FakeEmailService
}

func (step SendMessage) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	err := step.email.SendEmail(input.Request.CustomerEmail, input.Message, emailIdempotencyPrefix+ctx.FlowID())
	if err != nil {
		return nil, err
	}
	return dex.GoTo(CloseCase{}, input), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Close the case with its outcome."
type CloseCase struct {
	dex.StepDefaultsNoWaitFor[RefundCase]
}

func (CloseCase) Execute(ctx dex.Context, input RefundCase) (*dex.StepDecision, error) {
	if err := CaseStatus.Set(ctx, "closed-"+input.Outcome); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(input.Outcome), nil
}

func denyRefund(ctx dex.Context, input RefundCase, note string) (*dex.StepDecision, error) {
	if err := CaseStatus.Set(ctx, outcomeDenied); err != nil {
		return nil, err
	}
	input.Outcome = outcomeDenied
	input.Note = note
	return dex.GoTo(WriteCustomerMessage{}, input), nil
}

// ManagerApprovalRefundFlow is the customer refund with a manager approval above 1,000 USD.
type ManagerApprovalRefundFlow struct {
	dex.FlowDefaults
	billing BillingService
	email   *FakeEmailService
	model   *MockMessageModel
}

// NewManagerApprovalRefundFlow returns the Flow over its billing, email, and model services.
func NewManagerApprovalRefundFlow(
	billing BillingService,
	email *FakeEmailService,
	model *MockMessageModel,
) *ManagerApprovalRefundFlow {
	if billing == nil || email == nil || model == nil {
		panic("manager approval refund: billing, email, and model are required")
	}
	return &ManagerApprovalRefundFlow{billing: billing, email: email, model: model}
}

func (flow *ManagerApprovalRefundFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(ReceiveRequest{}),
		dex.DefineStep(CheckOrder{billing: flow.billing}),
		dex.DefineStep(CheckPolicy{}),
		dex.DefineStep(RequestManagerApproval{}),
		dex.DefineStep(IssueRefund{billing: flow.billing}),
		dex.DefineStep(WriteCustomerMessage{model: flow.model}),
		dex.DefineStep(SendMessage{email: flow.email}),
		dex.DefineStep(CloseCase{}),
	}
}

func (*ManagerApprovalRefundFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{CaseStatus, CustomerEmail, RefundID, CustomerMessage},
		Channels:   []dex.ChannelDef{managerDecisions},
	}
}

func (flow *ManagerApprovalRefundFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveRefund, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Approve refund",
				dex.WhenAttributeMatches(CaseStatus, dex.AttributeMatchEqual(AwaitingManager)),
				dex.ActionRequiresPermission(ManageRefundsPermission),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(CaseStatus)},
		}),
		dex.DefineRPC(flow.RejectRefund, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Reject refund",
				dex.WhenAttributeMatches(CaseStatus, dex.AttributeMatchEqual(AwaitingManager)),
				dex.ActionRequiresPermission(ManageRefundsPermission),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(CaseStatus)},
		}),
	}
}

// dex:field attribute-key:case-status value-type:string editable:false description:"Case status"
// dex:field attribute-key:customer-email value-type:string editable:false description:"Customer email"
func (*ManagerApprovalRefundFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	values, err := readViewAttributes(ctx, CaseStatus, CustomerEmail)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"case-status":    values[CaseStatus.AttributeName()],
		"customer-email": values[CustomerEmail.AttributeName()],
	}}, nil
}

//dex:field attribute-key:customer-email value-type:string description:"Customer\u0020email"editable:false ui-slot:title
//dex:field attribute-key:case-status value-type:string description:"Case\u0020status"editable:false ui-slot:status
//dex:field attribute-key:refund-id value-type:string description:"Refund\u0020ID"editable:false
//dex:field attribute-key:customer-message value-type:string description:"Customer\u0020message"editable:false
func (*ManagerApprovalRefundFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	values, err := readViewAttributes(ctx, CustomerEmail, CaseStatus, RefundID, CustomerMessage)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"customer-email":   values[CustomerEmail.AttributeName()],
		"case-status":      values[CaseStatus.AttributeName()],
		"refund-id":        values[RefundID.AttributeName()],
		"customer-message": values[CustomerMessage.AttributeName()],
	}}, nil
}

// readViewAttributes returns each Attribute by key; an unset one reads as nil.
func readViewAttributes(ctx dex.Context, attributes ...dex.Attribute[string]) (map[string]any, error) {
	output := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		value, err := attribute.Get(ctx)
		var notFound *dex.AttributeNotFoundError
		switch {
		case errors.As(err, &notFound):
			output[attribute.AttributeName()] = nil
		case err != nil:
			return nil, err
		default:
			output[attribute.AttributeName()] = value
		}
	}
	return output, nil
}

// dex:input field-name:reason value-type:string source:user required:true description:"Reason"
func (*ManagerApprovalRefundFlow) ApproveRefund(
	ctx dex.Context,
	input DecisionReason,
) (*dex.RPCResult[dex.None], error) {
	return publishManagerDecision(ctx, input, true)
}

// dex:input field-name:reason value-type:string source:user required:true description:"Reason"
func (*ManagerApprovalRefundFlow) RejectRefund(
	ctx dex.Context,
	input DecisionReason,
) (*dex.RPCResult[dex.None], error) {
	return publishManagerDecision(ctx, input, false)
}

func publishManagerDecision(
	ctx dex.Context,
	input DecisionReason,
	approved bool,
) (*dex.RPCResult[dex.None], error) {
	if strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New(reasonRequiredMessage)
	}
	status, err := CaseStatus.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	if status != AwaitingManager {
		// Preserve the application error type through the SDK's RPC wrapper with zero retry delay.
		return nil, &dex.RetryAfterError{Cause: RefundNotAwaitingManagerError{}}
	}
	decidedStatus := "rejected"
	if approved {
		decidedStatus = "approved"
	}
	if err := CaseStatus.Set(ctx, decidedStatus); err != nil {
		return nil, err
	}
	if err := managerDecisions.Publish(ctx, ManagerDecision{Approved: approved, Reason: input.Reason}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

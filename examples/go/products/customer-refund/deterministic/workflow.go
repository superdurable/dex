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

package deterministic

import (
	"errors"
	"fmt"
	"time"

	refundmodel "github.com/superdurable/dex/examples/go/products/customer-refund/model"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	statusReceived           = "received"
	statusOrderChecked       = "order-checked"
	statusOrderNotFound      = "order-not-found"
	statusRefunded           = "refunded"
	statusBillingDeclined    = "billing-declined"
	statusBillingUnconfirmed = "billing-unconfirmed"
	statusDenied             = "denied"
	statusResolved           = "resolved"
	standardWindowDays       = int64(30)
)

var deterministicChargeReference = dex.DefineAttribute[string]("charge-reference")

var deterministicRefundAmount = dex.DefineAttribute[string]("refund-amount")

var deterministicOrderAgeDays = dex.DefineAttribute[int64]("order-age-days")

var deterministicOrderLookup = dex.DefineAttribute[string]("order-lookup")

var deterministicRefundKey = dex.DefineAttribute[string]("refund-key")

var deterministicBillingOutcome = dex.DefineAttribute[string]("billing-outcome")

var deterministicRecommendation = dex.DefineAttribute[string]("recommended-action")

var deterministicOperatorNote = dex.DefineAttribute[string]("operator-note")

// dex:indexed-attribute attribute-key:case-status index-key:CustomKeyword2 index-type:keyword value-type:string description:"Current case status"
var deterministicCaseStatus = dex.DefineAttribute[string](
	"case-status",
	dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "CustomKeyword2"}),
)

type CustomerRefundFlow struct {
	dex.FlowDefaults
	service refundmodel.Service
}

func NewCustomerRefundFlow(service refundmodel.Service) *CustomerRefundFlow {
	if service == nil {
		panic("customer refund service is required")
	}
	return &CustomerRefundFlow{service: service}
}

func (*CustomerRefundFlow) GetFlowType() string {
	return "CustomerRefundFlow"
}

func (flow *CustomerRefundFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(deterministicReceiveRequestStep{}),
		dex.DefineStep(deterministicCheckOrderStep{}),
		dex.DefineStep(deterministicCheckPolicyStep{}),
		dex.DefineStep(deterministicIssueRefundStep{service: flow.service}),
		dex.DefineStep(deterministicDenyRefundStep{}),
		dex.DefineStep(deterministicNotifyCustomerStep{service: flow.service}),
		dex.DefineStep(deterministicCloseCaseStep{}),
	}
}

func (flow *CustomerRefundFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*CustomerRefundFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		deterministicChargeReference,
		deterministicRefundAmount,
		deterministicOrderAgeDays,
		deterministicOrderLookup,
		deterministicRefundKey,
		deterministicBillingOutcome,
		deterministicRecommendation,
		deterministicOperatorNote,
		deterministicCaseStatus,
	}}
}

// dex:field description:"Charge reference" editable:false attribute-key:charge-reference value-type:string
// dex:field attribute-key:refund-amount value-type:string editable:false description:"Refund amount"
// dex:field attribute-key:recommended-action value-type:string editable:false description:"Recommended action"
func (*CustomerRefundFlow) GetDexSummary(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	chargeReference, err := deterministicOptionalAttribute(ctx, deterministicChargeReference)
	if err != nil {
		return nil, err
	}
	refundAmount, err := deterministicOptionalAttribute(ctx, deterministicRefundAmount)
	if err != nil {
		return nil, err
	}
	recommendation, err := deterministicOptionalAttribute(ctx, deterministicRecommendation)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"charge-reference":   chargeReference,
		"refund-amount":      refundAmount,
		"recommended-action": recommendation,
	}}, nil
}

// dex:field attribute-key:charge-reference value-type:string editable:false description:"Charge reference"
// dex:field attribute-key:refund-amount value-type:string editable:false description:"Requested amount"
// dex:field attribute-key:order-lookup value-type:string editable:false description:"Order evidence"
// dex:field attribute-key:order-age-days value-type:int64 editable:false description:"Order age in days"
// dex:field attribute-key:recommended-action value-type:string editable:false description:"Policy recommendation"
// dex:field attribute-key:billing-outcome value-type:string editable:false description:"Billing outcome"
// dex:field attribute-key:operator-note value-type:string editable:true description:"Operator note"
func (*CustomerRefundFlow) GetDexDisplay(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	chargeReference, err := deterministicOptionalAttribute(ctx, deterministicChargeReference)
	if err != nil {
		return nil, err
	}
	refundAmount, err := deterministicOptionalAttribute(ctx, deterministicRefundAmount)
	if err != nil {
		return nil, err
	}
	orderLookup, err := deterministicOptionalAttribute(ctx, deterministicOrderLookup)
	if err != nil {
		return nil, err
	}
	orderAgeDays, err := deterministicOptionalAttribute(ctx, deterministicOrderAgeDays)
	if err != nil {
		return nil, err
	}
	recommendation, err := deterministicOptionalAttribute(ctx, deterministicRecommendation)
	if err != nil {
		return nil, err
	}
	billingOutcome, err := deterministicOptionalAttribute(ctx, deterministicBillingOutcome)
	if err != nil {
		return nil, err
	}
	operatorNote, err := deterministicOptionalAttribute(ctx, deterministicOperatorNote)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"charge-reference":   chargeReference,
		"refund-amount":      refundAmount,
		"order-lookup":       orderLookup,
		"order-age-days":     orderAgeDays,
		"recommended-action": recommendation,
		"billing-outcome":    billingOutcome,
		"operator-note":      operatorNote,
	}}, nil
}

// dex:group group-label:"Intake" group-id:intake
// dex:explanation text:"Store the inbound refund request and start the case."
type deterministicReceiveRequestStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicReceiveRequestStep) GetStepType() string {
	return "ReceiveRequestStep"
}

func (deterministicReceiveRequestStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	writes := []struct {
		attribute dex.Attribute[string]
		value     string
	}{
		{deterministicChargeReference, refundCase.CaseID},
		{deterministicRefundAmount, fmt.Sprintf("%.2f", float64(refundCase.AmountCents)/100)},
		{deterministicOperatorNote, ""},
		{deterministicCaseStatus, statusReceived},
	}
	for _, write := range writes {
		if err := write.attribute.Set(ctx, write.value); err != nil {
			return nil, err
		}
	}
	return dex.GoTo(deterministicCheckOrderStep{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Check the order and charge evidence for the refund."
type deterministicCheckOrderStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCheckOrderStep) GetStepType() string {
	return "CheckOrderStep"
}

func (deterministicCheckOrderStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	lookup := "found"
	if refundCase.CaseID == "no-such-order" {
		lookup = "missing"
	} else if err := deterministicOrderAgeDays.Set(ctx, refundCase.OrderAgeDays); err != nil {
		return nil, err
	}
	if err := deterministicOrderLookup.Set(ctx, lookup); err != nil {
		return nil, err
	}
	if err := deterministicCaseStatus.Set(ctx, statusOrderChecked); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicCheckPolicyStep{}, refundCase), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Evaluate refund policy and choose approve or deny."
type deterministicCheckPolicyStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCheckPolicyStep) GetStepType() string {
	return "CheckPolicyStep"
}

func (deterministicCheckPolicyStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	lookup, err := deterministicOrderLookup.Get(ctx)
	if err != nil {
		return nil, err
	}
	if lookup != "found" {
		if err := deterministicRecommendation.Set(ctx, "manual-order-follow-up"); err != nil {
			return nil, err
		}
		if err := deterministicCaseStatus.Set(ctx, statusOrderNotFound); err != nil {
			return nil, err
		}
		return dex.GoTo(deterministicNotifyCustomerStep{}, refundCase), nil
	}
	orderAgeDays, err := deterministicOrderAgeDays.Get(ctx)
	if err != nil {
		return nil, err
	}
	if orderAgeDays <= standardWindowDays {
		if err := deterministicRecommendation.Set(ctx, "refund"); err != nil {
			return nil, err
		}
		return dex.GoTo(deterministicIssueRefundStep{}, refundCase), nil
	}
	if err := deterministicRecommendation.Set(ctx, "deny-outside-window"); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicDenyRefundStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Issue the refund through billing."
type deterministicIssueRefundStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (deterministicIssueRefundStep) GetStepType() string {
	return "IssueRefundStep"
}

func (deterministicIssueRefundStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{
		InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 4,
	}}
}

func (step deterministicIssueRefundStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	key := refundCase.CaseID + ":refund"
	if err := deterministicRefundKey.Set(ctx, key); err != nil {
		return nil, err
	}
	outcome := step.service.IssueRefund(key, refundCase)
	if err := deterministicBillingOutcome.Set(ctx, outcome); err != nil {
		return nil, err
	}
	status := statusBillingUnconfirmed
	if outcome == refundmodel.BillingConfirmed {
		status = statusRefunded
	} else if outcome == refundmodel.BillingDeclined {
		status = statusBillingDeclined
	}
	if err := deterministicCaseStatus.Set(ctx, status); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicNotifyCustomerStep{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Record a policy denial for the refund request."
type deterministicDenyRefundStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicDenyRefundStep) GetStepType() string {
	return "DenyRefundStep"
}

func (deterministicDenyRefundStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := deterministicCaseStatus.Set(ctx, statusDenied); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicNotifyCustomerStep{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Notify the customer of the refund decision."
type deterministicNotifyCustomerStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (deterministicNotifyCustomerStep) GetStepType() string {
	return "NotifyCustomerStep"
}

func (step deterministicNotifyCustomerStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	status, err := deterministicCaseStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	message := "We are still confirming this refund and will write again."
	switch status {
	case statusRefunded:
		message = "Your refund is on its way."
	case statusDenied:
		message = "Your order falls outside the 30-day refund window."
	case statusOrderNotFound:
		message = "We could not find the order this request refers to."
	case statusBillingDeclined:
		message = "The payment provider declined this refund."
	}
	if err := step.service.SendCustomerMessage(refundCase, message); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicCloseCaseStep{}, refundCase), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Close the refund case after notification."
type deterministicCloseCaseStep struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCloseCaseStep) GetStepType() string {
	return "CloseCaseStep"
}

func (deterministicCloseCaseStep) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	status, err := deterministicCaseStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != statusOrderNotFound && status != statusDenied && status != statusBillingDeclined && status != statusBillingUnconfirmed {
		if err := deterministicCaseStatus.Set(ctx, statusResolved); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete("refund:" + refundCase.CaseID), nil
}

func deterministicOptionalAttribute[T any](ctx dex.Context, attribute dex.Attribute[T]) (any, error) {
	value, err := attribute.Get(ctx)
	if err == nil {
		return value, nil
	}
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return nil, nil
	}
	return nil, err
}

var (
	_ dex.Flow                          = (*CustomerRefundFlow)(nil)
	_ dex.RPC[dex.None, map[string]any] = (*CustomerRefundFlow)(nil).GetDexSummary
	_ dex.RPC[dex.None, map[string]any] = (*CustomerRefundFlow)(nil).GetDexDisplay
	_ dex.Step[refundmodel.RefundCase]  = deterministicReceiveRequestStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCheckOrderStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCheckPolicyStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicIssueRefundStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicDenyRefundStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicNotifyCustomerStep{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCloseCaseStep{}
)

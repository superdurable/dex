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
		dex.DefineStartStep(deterministicReceiveRequest{}),
		dex.DefineStep(deterministicCheckOrder{}),
		dex.DefineStep(deterministicCheckPolicy{}),
		dex.DefineStep(deterministicIssueRefund{service: flow.service}),
		dex.DefineStep(deterministicDenyRefund{}),
		dex.DefineStep(deterministicNotifyCustomer{service: flow.service}),
		dex.DefineStep(deterministicCloseCase{}),
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
type deterministicReceiveRequest struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicReceiveRequest) GetStepType() string {
	return "ReceiveRequest"
}

func (deterministicReceiveRequest) Execute(
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
	return dex.GoTo(deterministicCheckOrder{}, refundCase), nil
}

// dex:group group-id:evidence group-label:"Evidence"
// dex:explanation text:"Check the order and charge evidence for the refund."
type deterministicCheckOrder struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCheckOrder) GetStepType() string {
	return "CheckOrder"
}

func (deterministicCheckOrder) Execute(
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
	return dex.GoTo(deterministicCheckPolicy{}, refundCase), nil
}

// dex:group group-id:control group-label:"Control"
// dex:explanation text:"Evaluate refund policy and choose approve or deny."
type deterministicCheckPolicy struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCheckPolicy) GetStepType() string {
	return "CheckPolicy"
}

func (deterministicCheckPolicy) Execute(
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
		return dex.GoTo(deterministicNotifyCustomer{}, refundCase), nil
	}
	orderAgeDays, err := deterministicOrderAgeDays.Get(ctx)
	if err != nil {
		return nil, err
	}
	if orderAgeDays <= standardWindowDays {
		if err := deterministicRecommendation.Set(ctx, "refund"); err != nil {
			return nil, err
		}
		return dex.GoTo(deterministicIssueRefund{}, refundCase), nil
	}
	if err := deterministicRecommendation.Set(ctx, "deny-outside-window"); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicDenyRefund{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Issue the refund through billing."
type deterministicIssueRefund struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (deterministicIssueRefund) GetStepType() string {
	return "IssueRefund"
}

func (deterministicIssueRefund) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{
		InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 4,
	}}
}

func (step deterministicIssueRefund) Execute(
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
	return dex.GoTo(deterministicNotifyCustomer{}, refundCase), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Record a policy denial for the refund request."
type deterministicDenyRefund struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicDenyRefund) GetStepType() string {
	return "DenyRefund"
}

func (deterministicDenyRefund) Execute(
	ctx dex.Context,
	refundCase refundmodel.RefundCase,
) (*dex.StepDecision, error) {
	if err := deterministicCaseStatus.Set(ctx, statusDenied); err != nil {
		return nil, err
	}
	return dex.GoTo(deterministicNotifyCustomer{}, refundCase), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Notify the customer of the refund decision."
type deterministicNotifyCustomer struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
	service refundmodel.Service
}

func (deterministicNotifyCustomer) GetStepType() string {
	return "NotifyCustomer"
}

func (step deterministicNotifyCustomer) Execute(
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
	return dex.GoTo(deterministicCloseCase{}, refundCase), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Close the refund case after notification."
type deterministicCloseCase struct {
	dex.StepDefaultsNoWaitFor[refundmodel.RefundCase]
}

func (deterministicCloseCase) GetStepType() string {
	return "CloseCase"
}

func (deterministicCloseCase) Execute(
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
	_ dex.Step[refundmodel.RefundCase]  = deterministicReceiveRequest{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCheckOrder{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCheckPolicy{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicIssueRefund{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicDenyRefund{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicNotifyCustomer{}
	_ dex.Step[refundmodel.RefundCase]  = deterministicCloseCase{}
)

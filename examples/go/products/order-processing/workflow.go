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

package orderprocessing

import (
	"time"

	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	ChargeStepType = "ChargeStep"
	ShipStepType   = "ShipStep"
	RefundStepType = "RefundStep"
)

var (
	OrderStatus = dex.DefineAttribute[string](
		"order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	SellerOK = dex.DefineChannel[string]("seller-ok")
)

type OrderRequest struct {
	OrderID            string
	Email              string
	CustomerID         string
	Amount             int
	TestFailAtShipping bool
}

type OrderProcessingFlow struct {
	dex.FlowDefaults
	service service.MyService
}

func NewOrderProcessingFlow(applicationService service.MyService) *OrderProcessingFlow {
	return &OrderProcessingFlow{service: applicationService}
}

func (flow *OrderProcessingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(chargeStep{service: flow.service}),
		dex.DefineStep(shipStep{service: flow.service}),
		dex.DefineStep(refundStep{service: flow.service}),
	}
}

func (*OrderProcessingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OrderStatus},
		Channels:   []dex.ChannelDef{SellerOK},
	}
}

func (*OrderProcessingFlow) Approve(
	ctx dex.Context,
	_ string,
) (*dex.RPCResult[string], error) {
	if err := SellerOK.Publish(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: "ok"}, nil
}

func (*OrderProcessingFlow) Describe(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[string], error) {
	status, err := OrderStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: status}, nil
}

type chargeStep struct {
	dex.StepDefaultsNoWaitFor[OrderRequest]
	service service.MyService
}

func (chargeStep) GetStepType() string {
	return ChargeStepType
}

func (chargeStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			// TotalDuration: time.Hour,
			TotalDuration: 3 * time.Second,
		},
	}
}

func (step chargeStep) Execute(
	ctx dex.Context,
	order OrderRequest,
) (*dex.StepDecision, error) {
	step.service.ChargeUser(order.Email, order.CustomerID, order.Amount)
	if err := OrderStatus.Set(ctx, "charged"); err != nil {
		return nil, err
	}
	return dex.GoTo(shipStep{}, order), nil
}

type shipStep struct {
	dex.DefaultStepType
	service service.MyService
}

func (shipStep) GetStepType() string {
	return ShipStepType
}

func (shipStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			// TotalDuration: time.Hour,
			TotalDuration: 3 * time.Second,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(
			refundStep{},
			&dex.StepOptions{
				ExecuteRetry: &dex.RetryPolicy{
					// TotalDuration: time.Hour,
					TotalDuration: 3 * time.Second,
				},
			},
		),
	}
}

func (shipStep) WaitFor(
	dex.Context,
	OrderRequest,
) (*dex.Wait, error) {
	return dex.AnyOf(
		SellerOK.ForOne(),
		dex.Timer(24 * time.Hour),
	), nil
}

func (step shipStep) Execute(
	ctx dex.Context,
	order OrderRequest,
) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		step.service.SendEmail(
			order.Email,
			"Reminder: approve shipment",
			"Please approve or provide a tracking number.",
		)
		return dex.GoTo(shipStep{}, order), nil
	}
	if err := step.service.ShipItem(order.OrderID, order.TestFailAtShipping); err != nil {
		return nil, err
	}
	if err := OrderStatus.Set(ctx, "shipped"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("shipped:" + order.OrderID), nil
}

type refundStep struct {
	dex.StepDefaultsNoWaitFor[OrderRequest]
	service service.MyService
}

func (refundStep) GetStepType() string {
	return RefundStepType
}

func (step refundStep) Execute(
	ctx dex.Context,
	order OrderRequest,
) (*dex.StepDecision, error) {
	step.service.UpdateExternalSystem("refund " + order.OrderID)
	if err := OrderStatus.Set(ctx, "refunded"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("refunded:" + order.OrderID), nil
}

var (
	_ dex.Flow                  = (*OrderProcessingFlow)(nil)
	_ dex.RPC[string, string]   = (*OrderProcessingFlow)(nil).Approve
	_ dex.RPC[dex.None, string] = (*OrderProcessingFlow)(nil).Describe
)

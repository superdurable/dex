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

package childflows

import (
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// Shipment is one parcel of an order, identified by its shipment number.
type Shipment struct {
	ShipmentNumber string `json:"shipmentNumber"`
	Destination    string `json:"destination"`
	// DeliveryDelaySeconds simulates how long the carrier takes to deliver.
	DeliveryDelaySeconds int `json:"deliveryDelaySeconds"`
}

// ShipmentRequest is the input of ShipmentFlow.
type ShipmentRequest struct {
	OrderFlowID string   `json:"orderFlowId"`
	Shipment    Shipment `json:"shipment"`
}

// ShipmentOutcome is the shipment's committed outcome. The order reads it when no report arrives.
var ShipmentOutcome = dex.DefineAttribute[string]("shipmentOutcome")

// ShipmentFlow delivers one shipment. Its Flow ID is the shipment number, so people
// and systems can look it up, retry it, or stop it by that number.
type ShipmentFlow struct {
	dex.FlowDefaults
	getClient func() *dex.Client
}

// NewShipmentFlow creates the shipment Flow; getClient may return nil until bootstrap completes.
func NewShipmentFlow(getClient func() *dex.Client) *ShipmentFlow {
	if getClient == nil {
		panic("childflows: Client provider is required")
	}
	return &ShipmentFlow{getClient: getClient}
}

func (flow *ShipmentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(DeliverShipment{}),
		dex.DefineStep(ReportShipmentOutcome{getClient: flow.getClient}),
	}
}

func (*ShipmentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{ShipmentOutcome}}
}

// dex:group group-id:deliver group-label:"Deliver"
// dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
type DeliverShipment struct{ dex.StepDefaults }

// GetStepOptions stores the outcome before the report Step starts, even when the Flow defaults to ASYNC.
func (DeliverShipment) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync}
}

func (DeliverShipment) WaitFor(_ dex.Context, request ShipmentRequest) (*dex.Wait, error) {
	return dex.Until(dex.Timer(time.Duration(request.Shipment.DeliveryDelaySeconds) * time.Second)), nil
}

func (DeliverShipment) Execute(ctx dex.Context, request ShipmentRequest) (*dex.StepDecision, error) {
	outcome := "delivered to " + request.Shipment.Destination
	if err := ShipmentOutcome.Set(ctx, outcome); err != nil {
		return nil, err
	}
	return dex.GoTo(ReportShipmentOutcome{}, PendingReport{
		OrderFlowID: request.OrderFlowID,
		Report:      ShipmentReport{ShipmentNumber: request.Shipment.ShipmentNumber, Outcome: outcome},
	}), nil
}

// PendingReport is the report a shipment still has to send to its order.
type PendingReport struct {
	OrderFlowID string         `json:"orderFlowId"`
	Report      ShipmentReport `json:"report"`
}

// dex:group group-id:report group-label:"Report"
// dex:explanation text:"Reports the committed outcome to the order that started this shipment."
type ReportShipmentOutcome struct {
	dex.StepDefaultsNoWaitFor[PendingReport]
	getClient func() *dex.Client
}

// GetStepOptions keeps the report SYNC, like the Step that stores the outcome.
func (ReportShipmentOutcome) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync}
}

func (step ReportShipmentOutcome) Execute(ctx dex.Context, pending PendingReport) (*dex.StepDecision, error) {
	client := step.getClient()
	if client == nil {
		return nil, errClientUnavailable
	}
	// Stable per shipment, so a retried report changes the order once.
	requestID := "report:" + ctx.FlowID()
	err := client.InvokeRPCWithOptions(ctx, pending.OrderFlowID, (&OrderFulfillmentFlow{}).RecordShipmentOutcome,
		pending.Report, nil, dex.RPCInvokeOptions{RequestID: &requestID})
	// A closed order ends the attempts; the outcome stays readable through the Client.
	var notFound *dex.FlowNotFoundError
	var notActive *dex.FlowNotActiveOrNotFoundError
	if err != nil && !errors.As(err, &notFound) && !errors.As(err, &notActive) {
		return nil, err
	}
	return dex.GracefulComplete(pending.Report.Outcome), nil
}

var _ dex.Flow = (*ShipmentFlow)(nil)

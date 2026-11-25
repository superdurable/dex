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
	"context"
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// ShipmentTimeout is each shipment's own Flow timeout. A closing order never stops its shipments.
const ShipmentTimeout = 30 * 24 * time.Hour

// Shipment record statuses that are not a terminal Flow status.
const (
	ShipmentRejected    = "rejected"
	ShipmentUnavailable = "unavailable"
)

// Order is the input of OrderFulfillmentFlow. The order number is the Flow ID.
type Order struct {
	Shipments []Shipment `json:"shipments"`
	// ReconcileIntervalSeconds is how long the order waits for a report before it reads the shipment.
	ReconcileIntervalSeconds int `json:"reconcileIntervalSeconds"`
}

// TrackedShipment is the input of the Steps that start and track one shipment.
type TrackedShipment struct {
	Shipment                 Shipment `json:"shipment"`
	ReconcileIntervalSeconds int      `json:"reconcileIntervalSeconds"`
}

// ShipmentReport is what a shipment sends to its order when it ends.
type ShipmentReport struct {
	ShipmentNumber string `json:"shipmentNumber"`
	Outcome        string `json:"outcome"`
}

// ShipmentRecord is the order's record of one shipment. A report fills Outcome;
// a reconcile read fills Status and, when the shipment stored one, Outcome.
type ShipmentRecord struct {
	Status  string `json:"status,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

var (
	// ShipmentRecords holds one record per shipment, keyed by shipment number.
	ShipmentRecords = dex.DefineAttributeMap[ShipmentRecord]("shipmentRecords")
	// ShipmentReports receives each shipment's report, keyed by shipment number.
	ShipmentReports      = dex.DefineChannelMap[string]("shipmentReports")
	errClientUnavailable = errors.New("childflows: Client is not ready")
)

// OrderFulfillmentFlow ships an order as one ShipmentFlow per shipment.
// Each shipment is a top-level Flow whose Flow ID is its shipment number.
type OrderFulfillmentFlow struct {
	dex.FlowDefaults
	getClient    func() *dex.Client
	shipmentFlow *ShipmentFlow
}

// NewOrderFulfillmentFlow creates the order Flow; getClient may return nil until bootstrap completes.
func NewOrderFulfillmentFlow(getClient func() *dex.Client, shipmentFlow *ShipmentFlow) *OrderFulfillmentFlow {
	if getClient == nil {
		panic("childflows: Client provider is required")
	}
	if shipmentFlow == nil {
		panic("childflows: ShipmentFlow is required")
	}
	return &OrderFulfillmentFlow{getClient: getClient, shipmentFlow: shipmentFlow}
}

func (flow *OrderFulfillmentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartShipments{}),
		dex.DefineStep(StartShipment{getClient: flow.getClient, shipmentFlow: flow.shipmentFlow}),
		dex.DefineStep(AwaitShipment{getClient: flow.getClient}),
	}
}

func (flow *OrderFulfillmentFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.RecordShipmentOutcome, nil)}
}

func (*OrderFulfillmentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{ShipmentRecords},
		Channels:   []dex.ChannelDef{ShipmentReports},
	}
}

// RecordShipmentOutcome receives a shipment's report. The shipment sends it with
// a stable Request ID, so a retried report is applied once.
func (*OrderFulfillmentFlow) RecordShipmentOutcome(ctx dex.Context, report ShipmentReport) (*dex.RPCResult[dex.None], error) {
	if err := ShipmentReports.Publish(ctx, report.ShipmentNumber, report.Outcome); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts one branch per shipment in the order."
type StartShipments struct {
	dex.StepDefaultsNoWaitFor[Order]
}

func (StartShipments) Execute(_ dex.Context, order Order) (*dex.StepDecision, error) {
	if len(order.Shipments) == 0 {
		return dex.GracefulComplete(nil), nil
	}
	movements := make([]dex.StepMovement, 0, len(order.Shipments))
	for _, shipment := range order.Shipments {
		movements = append(movements, dex.MovementOf(StartShipment{}, TrackedShipment{
			Shipment:                 shipment,
			ReconcileIntervalSeconds: order.ReconcileIntervalSeconds,
		}))
	}
	return dex.GoToMany(movements...), nil
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number."
type StartShipment struct {
	dex.StepDefaultsNoWaitFor[TrackedShipment]
	getClient    func() *dex.Client
	shipmentFlow *ShipmentFlow
}

func (step StartShipment) Execute(ctx dex.Context, tracked TrackedShipment) (*dex.StepDecision, error) {
	client := step.getClient()
	if client == nil {
		return nil, errClientUnavailable
	}
	number := tracked.Shipment.ShipmentNumber
	// Derived from this order and shipment, so a retried Execute repeats the same start.
	requestID := "start:" + ctx.FlowID() + ":" + number
	timeout := ShipmentTimeout
	_, err := client.StartFlow(ctx, step.shipmentFlow, number, ShipmentRequest{
		OrderFlowID: ctx.FlowID(),
		Shipment:    tracked.Shipment,
	}, dex.StartFlowOptions{
		Timeout:        &timeout,
		IDReusePolicy:  dex.IDReuseDisallow,
		RequestID:      &requestID,
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
	})
	var alreadyStarted *dex.FlowAlreadyStartedError
	if errors.As(err, &alreadyStarted) {
		// Another request owns this shipment number; this order does not track it.
		if err := ShipmentRecords.Set(ctx, number, ShipmentRecord{Status: ShipmentRejected}); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(nil), nil
	}
	if err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitShipment{}, tracked), nil
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Records the shipment's report, or reads the shipment when no report arrives in time."
type AwaitShipment struct {
	dex.StepDefaults
	getClient func() *dex.Client
}

func (AwaitShipment) WaitFor(_ dex.Context, tracked TrackedShipment) (*dex.Wait, error) {
	interval := time.Duration(tracked.ReconcileIntervalSeconds) * time.Second
	return dex.AnyOf(
		ShipmentReports.ForOne(tracked.Shipment.ShipmentNumber),
		dex.Timer(interval),
	), nil
}

func (step AwaitShipment) Execute(ctx dex.Context, tracked TrackedShipment) (*dex.StepDecision, error) {
	number := tracked.Shipment.ShipmentNumber
	outcomes, err := ShipmentReports.GetConditionResults(ctx, number)
	if err != nil {
		return nil, err
	}
	record := ShipmentRecord{}
	if len(outcomes) > 0 {
		record.Outcome = outcomes[0]
	} else {
		client := step.getClient()
		if client == nil {
			return nil, errClientUnavailable
		}
		record, err = reconcileShipment(ctx, client, number)
		if err != nil {
			return nil, err
		}
		if record.Status == dex.FlowRunning.String() {
			return dex.GoTo(AwaitShipment{}, tracked), nil
		}
	}
	if err := ShipmentRecords.Set(ctx, number, record); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

// reconcileShipment reads a shipment that has not reported: its status first, then its outcome.
// It returns a "running" record while the shipment runs, an "unavailable" record when the
// shipment does not exist, and an error for any other failure so the Step retries.
func reconcileShipment(ctx context.Context, client *dex.Client, number string) (ShipmentRecord, error) {
	info, err := client.DescribeFlow(ctx, number)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		return ShipmentRecord{Status: ShipmentUnavailable}, nil
	}
	if err != nil {
		return ShipmentRecord{}, err
	}
	record := ShipmentRecord{Status: info.Status.String()}
	if info.Status == dex.FlowRunning {
		return record, nil
	}
	// An unset outcome Attribute leaves Outcome empty: the shipment ended without one.
	_, err = client.GetAttributes(ctx, number, dex.ReadAttribute(ShipmentOutcome, &record.Outcome))
	if errors.As(err, &notFound) {
		return record, nil
	}
	return record, err
}

var _ dex.Flow = (*OrderFulfillmentFlow)(nil)

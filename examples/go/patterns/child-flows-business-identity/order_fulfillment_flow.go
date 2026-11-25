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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// ShipmentTimeout is each shipment's own Flow timeout. A closing order never stops its shipments.
const ShipmentTimeout = 30 * 24 * time.Hour

// Shipment record statuses that are not a terminal Flow status.
const (
	// ShipmentStarting marks a shipment of the order that has no claimed run yet.
	ShipmentStarting = "starting"
	// ShipmentStarted marks a shipment whose run this order started and claimed.
	ShipmentStarted = "started"
	// ShipmentRejected marks a shipment number that another request owns.
	ShipmentRejected = "rejected"
	// ShipmentUnavailable marks a claimed run that can no longer be read.
	ShipmentUnavailable = "unavailable"
)

const duplicateShipmentPrefix = "duplicate shipment number: "

// Order is the input of OrderFulfillmentFlow. The order number is the Flow ID.
type Order struct {
	Shipments []Shipment `json:"shipments"`
	// ReconcileIntervalSeconds is how often the order reads a shipment that has not ended.
	ReconcileIntervalSeconds int `json:"reconcileIntervalSeconds"`
}

// TrackedShipment is the input of the Steps that start and track one shipment.
type TrackedShipment struct {
	Shipment                 Shipment `json:"shipment"`
	ReconcileIntervalSeconds int      `json:"reconcileIntervalSeconds"`
	// ShipmentRunID is the run this order started and tracks; empty before the start.
	ShipmentRunID string `json:"shipmentRunId,omitempty"`
}

// ShipmentReport is what a shipment run sends to its order when it ends.
type ShipmentReport struct {
	ShipmentNumber string `json:"shipmentNumber"`
	ShipmentRunID  string `json:"shipmentRunId"`
	Outcome        string `json:"outcome"`
}

// ShipmentRecord is the order's record of one shipment. RunID is the claimed run.
// A report or a reconcile read fills Outcome; a reconcile read fills the terminal Status.
type ShipmentRecord struct {
	Status  string `json:"status,omitempty"`
	RunID   string `json:"runId,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

var (
	// ShipmentRecords holds one record per shipment, keyed by shipment number.
	ShipmentRecords = dex.DefineAttributeMap[ShipmentRecord]("shipmentRecords")
	// ShipmentRecordsLock serializes every write to ShipmentRecords; it stores no value.
	ShipmentRecordsLock = dex.DefineAttribute[bool]("shipmentRecordsLock")
	// ErrShipmentNotClaimed asks a shipment to retry its report after the order claims its run.
	ErrShipmentNotClaimed = errors.New("childflows: the order has not claimed this shipment run yet")
	errClientUnavailable  = errors.New("childflows: Client is not ready")
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
	return []dex.RPCDef{dex.DefineRPC(flow.RecordShipmentOutcome, &dex.RPCOptions{
		LockAttributes:    []dex.AttributeLock{dex.LockAttribute(ShipmentRecordsLock)},
		LoadAttributeMaps: []dex.AttributeDef{ShipmentRecords},
	})}
}

func (*OrderFulfillmentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{ShipmentRecords, ShipmentRecordsLock},
	}
}

// RecordShipmentOutcome fills the outcome of a shipment's record. It accepts only the run
// this order claimed; the first report wins, and any other report changes nothing.
// It returns ErrShipmentNotClaimed while the order is still starting that shipment.
func (*OrderFulfillmentFlow) RecordShipmentOutcome(ctx dex.Context, report ShipmentReport) (*dex.RPCResult[dex.None], error) {
	record, found, err := loadShipmentRecord(ctx, report.ShipmentNumber)
	if err != nil {
		return nil, err
	}
	if found && record.Status == ShipmentStarting {
		return nil, ErrShipmentNotClaimed
	}
	// Ignore an unknown or rejected shipment, another run, and a repeated report.
	if !found || record.RunID != report.ShipmentRunID || record.Outcome != "" {
		return &dex.RPCResult[dex.None]{}, nil
	}
	record.Outcome = report.Outcome
	if err := ShipmentRecords.Set(ctx, report.ShipmentNumber, record); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Registers the order's shipment numbers, then starts one branch per shipment."
type StartShipments struct {
	dex.StepDefaultsNoWaitFor[Order]
}

// GetStepOptions takes the records lock, so registration never races a report.
func (StartShipments) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(ShipmentRecordsLock)}}
}

func (StartShipments) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	if len(order.Shipments) == 0 {
		return dex.GracefulComplete(nil), nil
	}
	numbers := make(map[string]bool, len(order.Shipments))
	for _, shipment := range order.Shipments {
		if numbers[shipment.ShipmentNumber] {
			return dex.ForceFail(duplicateShipmentPrefix + shipment.ShipmentNumber), nil
		}
		numbers[shipment.ShipmentNumber] = true
	}
	movements := make([]dex.StepMovement, 0, len(order.Shipments))
	for _, shipment := range order.Shipments {
		if err := ShipmentRecords.Set(ctx, shipment.ShipmentNumber, ShipmentRecord{Status: ShipmentStarting}); err != nil {
			return nil, err
		}
		movements = append(movements, dex.MovementOf(StartShipment{}, TrackedShipment{
			Shipment:                 shipment,
			ReconcileIntervalSeconds: order.ReconcileIntervalSeconds,
		}))
	}
	return dex.GoToMany(movements...), nil
}

// ShipmentStartRequestID derives the start request ID from the complete start request.
// A retried Execute sends the same ID, so it repeats the same start. Any other request for
// the same shipment number sends another ID, so Dex rejects it as already started.
// It returns an error only when the request cannot be encoded.
func ShipmentStartRequestID(orderFlowID string, shipment Shipment) (string, error) {
	request, err := json.Marshal([]any{
		shipmentFlowType, shipment.ShipmentNumber, orderFlowID, shipment.Destination,
		shipment.DeliveryDelaySeconds, int64(ShipmentTimeout / time.Second), "disallow",
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(request)
	return "start:" + hex.EncodeToString(digest[:]), nil
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number and claims its run."
type StartShipment struct {
	dex.StepDefaultsNoWaitFor[TrackedShipment]
	getClient    func() *dex.Client
	shipmentFlow *ShipmentFlow
}

// GetStepOptions takes the records lock, so the claim and the rejection never race a report.
func (StartShipment) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(ShipmentRecordsLock)}}
}

func (step StartShipment) Execute(ctx dex.Context, tracked TrackedShipment) (*dex.StepDecision, error) {
	client := step.getClient()
	if client == nil {
		return nil, errClientUnavailable
	}
	number := tracked.Shipment.ShipmentNumber
	requestID, err := ShipmentStartRequestID(ctx.FlowID(), tracked.Shipment)
	if err != nil {
		return nil, err
	}
	timeout := ShipmentTimeout
	runID, err := client.StartFlow(ctx, step.shipmentFlow, number, ShipmentRequest{
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
	// Claim the run: reports and reads of any other run of this number are ignored.
	if err := ShipmentRecords.Set(ctx, number, ShipmentRecord{Status: ShipmentStarted, RunID: runID}); err != nil {
		return nil, err
	}
	tracked.ShipmentRunID = runID
	return dex.GoTo(AwaitShipment{}, tracked), nil
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Reads the claimed shipment run at each interval and records how it ended."
type AwaitShipment struct {
	dex.StepDefaults
	getClient func() *dex.Client
}

// GetStepOptions takes the same lock as the report RPC and loads the records.
func (AwaitShipment) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLockAttributes:    []dex.AttributeLock{dex.LockAttribute(ShipmentRecordsLock)},
		ExecuteLoadAttributeMaps: []dex.AttributeDef{ShipmentRecords},
	}
}

func (AwaitShipment) WaitFor(_ dex.Context, tracked TrackedShipment) (*dex.Wait, error) {
	return dex.Until(dex.Timer(time.Duration(tracked.ReconcileIntervalSeconds) * time.Second)), nil
}

func (step AwaitShipment) Execute(ctx dex.Context, tracked TrackedShipment) (*dex.StepDecision, error) {
	client := step.getClient()
	if client == nil {
		return nil, errClientUnavailable
	}
	number := tracked.Shipment.ShipmentNumber
	reading, err := reconcileShipment(ctx, client, number, tracked.ShipmentRunID)
	if err != nil {
		return nil, err
	}
	if reading.Status == dex.FlowRunning.String() {
		return dex.GoTo(AwaitShipment{}, tracked), nil
	}
	// Merge with the record, which may already hold the reported outcome.
	record, _, err := loadShipmentRecord(ctx, number)
	if err != nil {
		return nil, err
	}
	record.Status = reading.Status
	record.RunID = tracked.ShipmentRunID
	if record.Outcome == "" {
		record.Outcome = reading.Outcome
	}
	if err := ShipmentRecords.Set(ctx, number, record); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

// loadShipmentRecord returns the loaded record of a shipment and whether it exists.
// It returns an error when the record cannot be read.
func loadShipmentRecord(ctx dex.Context, number string) (ShipmentRecord, bool, error) {
	record, err := ShipmentRecords.Get(ctx, number)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return ShipmentRecord{}, false, nil
	}
	return record, err == nil, err
}

// reconcileShipment reads the claimed run of a shipment: its status first, then its outcome.
// It returns a "running" record while that run runs, and an "unavailable" record when that
// run no longer exists or another run replaced it. It keeps an outcome only when the run
// is still the claimed one after the read. It returns an error for any other failure so the Step retries.
func reconcileShipment(ctx context.Context, client *dex.Client, number string, runID string) (ShipmentRecord, error) {
	info, found, err := describeClaimedRun(ctx, client, number, runID)
	if err != nil || !found {
		return ShipmentRecord{Status: ShipmentUnavailable}, err
	}
	record := ShipmentRecord{Status: info.Status.String()}
	if info.Status == dex.FlowRunning {
		return record, nil
	}
	// An unset outcome Attribute leaves Outcome empty: the shipment ended without one.
	var outcome string
	_, err = client.GetAttributes(ctx, number, dex.ReadAttribute(ShipmentOutcome, &outcome))
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		return record, nil
	}
	if err != nil {
		return ShipmentRecord{}, err
	}
	// The Attribute read targets the latest run, so confirm it is still the claimed one.
	_, stillClaimed, err := describeClaimedRun(ctx, client, number, runID)
	if err != nil {
		return ShipmentRecord{}, err
	}
	if stillClaimed {
		record.Outcome = outcome
	}
	return record, nil
}

// describeClaimedRun describes the latest run of a shipment.
// It returns found as false when the shipment does not exist or its latest run is not runID,
// and an error for any other failure.
func describeClaimedRun(ctx context.Context, client *dex.Client, number string, runID string) (dex.FlowInfo, bool, error) {
	info, err := client.DescribeFlow(ctx, number)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		return dex.FlowInfo{}, false, nil
	}
	if err != nil {
		return dex.FlowInfo{}, false, err
	}
	return info, info.RunID == runID, nil
}

var _ dex.Flow = (*OrderFulfillmentFlow)(nil)

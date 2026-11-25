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

package integ

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	childflows "github.com/superdurable/dex/examples/go/patterns/child-flows-business-identity"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestChildFlowsBusinessIdentityRecordsTheReportBeforeTheShipmentCloses(t *testing.T) {
	orderID := newFlowID(t, "order")
	delivered := orderID + "-1"
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: delivered, Destination: "Seattle", DeliveryDelaySeconds: 1}},
		ReconcileIntervalSeconds: 3600,
	})
	runID := waitForClaimedRun(t, orderID, delivered)
	// The reconcile Timer waits an hour, so only the report can fill the outcome.
	waitForShipmentRecord(t, orderID, delivered, childflows.ShipmentRecord{
		Status: childflows.ShipmentStarted, RunID: runID, Outcome: "delivered to Seattle",
	})
	requireWorkflowPatternRunning(t, orderID)
	terminateFlows(t, orderID)
}

func TestChildFlowsBusinessIdentityAcceptsReportsOnlyFromTheClaimedRun(t *testing.T) {
	orderID := newFlowID(t, "order")
	delivered := orderID + "-1"
	canceled := orderID + "-2"
	startOrder(t, orderID, childflows.Order{
		Shipments: []childflows.Shipment{
			{ShipmentNumber: delivered, Destination: "Seattle", DeliveryDelaySeconds: 1},
			{ShipmentNumber: canceled, Destination: "Portland", DeliveryDelaySeconds: 3600},
		},
		ReconcileIntervalSeconds: 2,
	})
	deliveredRunID := waitForClaimedRun(t, orderID, delivered)
	canceledRunID := waitForClaimedRun(t, orderID, canceled)
	expectedDelivered := childflows.ShipmentRecord{Status: "completed", RunID: deliveredRunID, Outcome: "delivered to Seattle"}
	waitForShipmentRecord(t, orderID, delivered, expectedDelivered)

	// A repeated report, a report from another run, and an unknown number change nothing.
	for _, report := range []childflows.ShipmentReport{
		{ShipmentNumber: delivered, ShipmentRunID: deliveredRunID, Outcome: "delivered to Boise"},
		{ShipmentNumber: canceled, ShipmentRunID: "another-run", Outcome: "delivered to Boise"},
		{ShipmentNumber: orderID + "-9", ShipmentRunID: "another-run", Outcome: "delivered to Boise"},
	} {
		invokeWorkflowPatternRPC(t, orderID, registry.OrderFulfillment.RecordShipmentOutcome, report, nil)
	}
	require.Equal(t, expectedDelivered, readShipmentRecord(t, orderID, delivered))
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentStarted, RunID: canceledRunID},
		readShipmentRecord(t, orderID, canceled))
	requireNoShipmentRecord(t, orderID, orderID+"-9")

	require.NoError(t, integClient.StopFlow(integrationContext(t), canceled, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "customer canceled the parcel",
	}))
	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, childflows.ShipmentRecord{Status: "canceled", RunID: canceledRunID},
		readShipmentRecord(t, orderID, canceled))
}

func TestChildFlowsBusinessIdentityRejectsAShipmentNumberOwnedByAnotherRequest(t *testing.T) {
	firstOrderID := newFlowID(t, "order")
	shipment := childflows.Shipment{ShipmentNumber: firstOrderID + "-1", Destination: "Boise", DeliveryDelaySeconds: 3600}
	order := childflows.Order{Shipments: []childflows.Shipment{shipment}, ReconcileIntervalSeconds: 3600}
	startOrder(t, firstOrderID, order)
	runID := waitForClaimedRun(t, firstOrderID, shipment.ShipmentNumber)

	// Replaying the same start request returns the claimed run.
	requestID, err := childflows.ShipmentStartRequestID(firstOrderID, shipment)
	require.NoError(t, err)
	timeout := childflows.ShipmentTimeout
	replayedRunID, err := integClient.StartFlow(integrationContext(t), registry.Shipment, shipment.ShipmentNumber,
		childflows.ShipmentRequest{OrderFlowID: firstOrderID, Shipment: shipment},
		dex.StartFlowOptions{
			Timeout:        &timeout,
			IDReusePolicy:  dex.IDReuseDisallow,
			RequestID:      &requestID,
			AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		})
	require.NoError(t, err)
	require.Equal(t, runID, replayedRunID)

	// Another order with the same shipment number sends a different request.
	secondOrderID := newFlowID(t, "order")
	startOrder(t, secondOrderID, childflows.Order{
		Shipments: []childflows.Shipment{
			shipment,
			{ShipmentNumber: secondOrderID + "-2", Destination: "Austin", DeliveryDelaySeconds: 3600},
		},
		ReconcileIntervalSeconds: 3600,
	})
	rejected := childflows.ShipmentRecord{Status: childflows.ShipmentRejected}
	waitForShipmentRecord(t, secondOrderID, shipment.ShipmentNumber, rejected)
	// The run that owns the number reports to the second order: the rejected record stays as it is.
	invokeWorkflowPatternRPC(t, secondOrderID, registry.OrderFulfillment.RecordShipmentOutcome,
		childflows.ShipmentReport{ShipmentNumber: shipment.ShipmentNumber, ShipmentRunID: runID, Outcome: "delivered to Boise"}, nil)
	require.Equal(t, rejected, readShipmentRecord(t, secondOrderID, shipment.ShipmentNumber))
	terminateFlows(t, shipment.ShipmentNumber, firstOrderID, secondOrderID+"-2", secondOrderID)
}

func TestChildFlowsBusinessIdentityIgnoresAReopenedShipmentNumber(t *testing.T) {
	orderID := newFlowID(t, "order")
	number := orderID + "-1"
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: 3600}},
		ReconcileIntervalSeconds: 3,
	})
	claimedRunID := waitForClaimedRun(t, orderID, number)
	require.NoError(t, integClient.StopFlow(integrationContext(t), number, dex.StopOptions{Type: dex.CancelFlow}))
	waitForShipmentStatus(t, number, dex.FlowCanceled)

	// Another caller reopens the number; its run reports to the same order.
	_, err := integClient.StartFlow(integrationContext(t), registry.Shipment, number,
		childflows.ShipmentRequest{
			OrderFlowID: orderID,
			Shipment:    childflows.Shipment{ShipmentNumber: number, Destination: "Portland", DeliveryDelaySeconds: 1},
		},
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseAllowIfNotRunning})
	require.NoError(t, err)
	waitForShipmentStatus(t, number, dex.FlowCompleted)

	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	record := readShipmentRecord(t, orderID, number)
	require.Equal(t, claimedRunID, record.RunID)
	require.Empty(t, record.Outcome, "the reopened run's outcome must not reach the order")
	require.Contains(t, []string{"canceled", childflows.ShipmentUnavailable}, record.Status)
}

func TestChildFlowsBusinessIdentityFailsAnOrderWithADuplicateShipmentNumber(t *testing.T) {
	orderID := newFlowID(t, "order")
	number := orderID + "-1"
	startOrder(t, orderID, childflows.Order{
		Shipments: []childflows.Shipment{
			{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: 1},
			{ShipmentNumber: number, Destination: "Portland", DeliveryDelaySeconds: 1},
		},
		ReconcileIntervalSeconds: 2,
	})
	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Contains(t, result.ErrorMessage, "duplicate shipment number: "+number)
	_, err := integClient.DescribeFlow(integrationContext(t), number)
	var notFound *dex.FlowNotFoundError
	require.ErrorAs(t, err, &notFound)
}

func TestChildFlowsBusinessIdentityStoresTheOutcomeBeforeReportingUnderAsyncDefaults(t *testing.T) {
	orderID := newFlowID(t, "order")
	number := orderID + "-1"
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: number, Destination: "Austin", DeliveryDelaySeconds: 3}},
		ReconcileIntervalSeconds: 3600,
	})
	runID := waitForClaimedRun(t, orderID, number)
	asyncDurability := dex.StepDurabilityAsync
	require.NoError(t, integClient.UpdateFlowConfig(integrationContext(t), number,
		dex.FlowConfig{StepDurability: &asyncDurability}))
	waitForShipmentRecord(t, orderID, number, childflows.ShipmentRecord{
		Status: childflows.ShipmentStarted, RunID: runID, Outcome: "delivered to Austin",
	})
	var outcome string
	_, err := integClient.GetAttributes(integrationContext(t), number, dex.ReadAttribute(childflows.ShipmentOutcome, &outcome))
	require.NoError(t, err)
	require.Equal(t, "delivered to Austin", outcome)
	terminateFlows(t, orderID)
}

func startOrder(t *testing.T, orderID string, order childflows.Order) {
	t.Helper()
	_, err := integClient.StartFlow(integrationContext(t), registry.OrderFulfillment, orderID, order, dex.StartFlowOptions{})
	require.NoError(t, err)
}

func waitForShipmentStatus(t *testing.T, shipmentNumber string, status dex.FlowStatus) {
	t.Helper()
	require.Eventually(t, func() bool {
		info, err := integClient.DescribeFlow(integrationContext(t), shipmentNumber)
		return err == nil && info.Status == status
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "shipment %s", shipmentNumber)
}

// waitForClaimedRun waits until the order claimed a run of the shipment and returns its run ID.
func waitForClaimedRun(t *testing.T, orderID string, shipmentNumber string) string {
	t.Helper()
	var runID string
	require.Eventually(t, func() bool {
		var record childflows.ShipmentRecord
		_, err := integClient.GetAttributes(integrationContext(t), orderID,
			dex.ReadAttributeMapInstance(childflows.ShipmentRecords, shipmentNumber, &record))
		runID = record.RunID
		return err == nil && runID != ""
	}, workflowPatternWaitTimeout, 100*time.Millisecond, "claimed run of %s", shipmentNumber)
	return runID
}

func waitForShipmentRecord(t *testing.T, orderID string, shipmentNumber string, expected childflows.ShipmentRecord) {
	t.Helper()
	require.Eventually(t, func() bool {
		var record childflows.ShipmentRecord
		_, err := integClient.GetAttributes(integrationContext(t), orderID,
			dex.ReadAttributeMapInstance(childflows.ShipmentRecords, shipmentNumber, &record))
		return err == nil && record == expected
	}, workflowPatternWaitTimeout, 100*time.Millisecond, "record of %s", shipmentNumber)
}

func readShipmentRecord(t *testing.T, orderID string, shipmentNumber string) childflows.ShipmentRecord {
	t.Helper()
	var record childflows.ShipmentRecord
	result, err := integClient.GetAttributes(integrationContext(t), orderID,
		dex.ReadAttributeMapInstance(childflows.ShipmentRecords, shipmentNumber, &record))
	require.NoError(t, err)
	require.True(t, result.IsMapInstanceSet(childflows.ShipmentRecords, shipmentNumber), "record of %s", shipmentNumber)
	return record
}

func requireNoShipmentRecord(t *testing.T, orderID string, shipmentNumber string) {
	t.Helper()
	var record childflows.ShipmentRecord
	result, err := integClient.GetAttributes(integrationContext(t), orderID,
		dex.ReadAttributeMapInstance(childflows.ShipmentRecords, shipmentNumber, &record))
	require.NoError(t, err)
	require.False(t, result.IsMapInstanceSet(childflows.ShipmentRecords, shipmentNumber), "record of %s", shipmentNumber)
}

func terminateFlows(t *testing.T, flowIDs ...string) {
	t.Helper()
	for _, flowID := range flowIDs {
		require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{Type: dex.TerminateFlow}))
	}
}

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
		{ShipmentNumber: canceled, ShipmentRunID: "", Outcome: "delivered to Boise"},
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
	// Reports to the second order, from the owning run or with no run ID, leave the rejected record as it is.
	for _, reportRunID := range []string{runID, ""} {
		invokeWorkflowPatternRPC(t, secondOrderID, registry.OrderFulfillment.RecordShipmentOutcome,
			childflows.ShipmentReport{ShipmentNumber: shipment.ShipmentNumber, ShipmentRunID: reportRunID, Outcome: "delivered to Boise"}, nil)
	}
	require.Equal(t, rejected, readShipmentRecord(t, secondOrderID, shipment.ShipmentNumber))
	terminateFlows(t, shipment.ShipmentNumber, firstOrderID, secondOrderID+"-2", secondOrderID)
}

func TestChildFlowsBusinessIdentityIgnoresAReopenedShipmentNumber(t *testing.T) {
	orderID := newFlowID(t, "order")
	number := orderID + "-1"
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: 3600}},
		ReconcileIntervalSeconds: 10,
	})
	claimedRunID := waitForClaimedRun(t, orderID, number)
	require.NoError(t, integClient.StopFlow(integrationContext(t), number, dex.StopOptions{Type: dex.CancelFlow}))
	waitForShipmentStatus(t, number, dex.FlowCanceled)

	// Another caller reopens the number; its run reports to the same order and completes.
	_, err := integClient.StartFlow(integrationContext(t), registry.Shipment, number,
		childflows.ShipmentRequest{
			OrderFlowID: orderID,
			Shipment:    childflows.Shipment{ShipmentNumber: number, Destination: "Portland", DeliveryDelaySeconds: 1},
		},
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseAllowIfNotRunning})
	require.NoError(t, err)
	waitForShipmentStatus(t, number, dex.FlowCompleted)

	// The order has not read the shipment yet, and it ignored the other run's report.
	requireWorkflowPatternRunning(t, orderID)
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentStarted, RunID: claimedRunID},
		readShipmentRecord(t, orderID, number))

	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentUnavailable, RunID: claimedRunID},
		readShipmentRecord(t, orderID, number))
}

// Withholding the Worker's Client keeps every StartShipment retrying, so the reports below
// arrive deterministically before any claim.
func TestChildFlowsBusinessIdentityMergesReportsThatArriveBeforeTheClaim(t *testing.T) {
	orderID := newFlowID(t, "order")
	claimed := childflows.Shipment{ShipmentNumber: orderID + "-1", Destination: "Seattle", DeliveryDelaySeconds: 3600}
	other := childflows.Shipment{ShipmentNumber: orderID + "-2", Destination: "Boise", DeliveryDelaySeconds: 3600}
	owned := childflows.Shipment{ShipmentNumber: orderID + "-3", Destination: "Austin", DeliveryDelaySeconds: 3600}
	workerClientWithheld.Store(true)
	t.Cleanup(func() { workerClientWithheld.Store(false) })
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{claimed, other, owned},
		ReconcileIntervalSeconds: 3600,
	})
	for _, shipment := range []childflows.Shipment{claimed, other, owned} {
		waitForShipmentRecord(t, orderID, shipment.ShipmentNumber, childflows.ShipmentRecord{Status: childflows.ShipmentStarting})
	}

	// Start the first shipment with the order's own request, so the order's retry gets this run.
	requestID, err := childflows.ShipmentStartRequestID(orderID, claimed)
	require.NoError(t, err)
	timeout := childflows.ShipmentTimeout
	claimedRunID, err := integClient.StartFlow(integrationContext(t), registry.Shipment, claimed.ShipmentNumber,
		childflows.ShipmentRequest{OrderFlowID: orderID, Shipment: claimed},
		dex.StartFlowOptions{
			Timeout:        &timeout,
			IDReusePolicy:  dex.IDReuseDisallow,
			RequestID:      &requestID,
			AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		})
	require.NoError(t, err)
	// Another request owns the third shipment number, so the order rejects it.
	_, err = integClient.StartFlow(integrationContext(t), registry.Shipment, owned.ShipmentNumber,
		childflows.ShipmentRequest{OrderFlowID: "another-order", Shipment: owned},
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseDisallow})
	require.NoError(t, err)

	// A report under the wrong number arrives first; it must not hide the claimed run's report.
	for _, report := range []childflows.ShipmentReport{
		{ShipmentNumber: other.ShipmentNumber, ShipmentRunID: claimedRunID, Outcome: "delivered to Boise"},
		{ShipmentNumber: claimed.ShipmentNumber, ShipmentRunID: claimedRunID, Outcome: "delivered to Seattle"},
		{ShipmentNumber: claimed.ShipmentNumber, ShipmentRunID: claimedRunID, Outcome: "delivered to Portland"},
		{ShipmentNumber: owned.ShipmentNumber, ShipmentRunID: "another-run", Outcome: "delivered to Austin"},
	} {
		invokeWorkflowPatternRPC(t, orderID, registry.OrderFulfillment.RecordShipmentOutcome, report, nil)
	}
	require.Equal(t, map[string]childflows.ShipmentReport{
		claimedRunID: {ShipmentNumber: claimed.ShipmentNumber, ShipmentRunID: claimedRunID, Outcome: "delivered to Seattle"},
	}, readPendingShipmentReports(t, orderID, claimed.ShipmentNumber))

	workerClientWithheld.Store(false)
	waitForShipmentRecord(t, orderID, claimed.ShipmentNumber, childflows.ShipmentRecord{
		Status: childflows.ShipmentStarted, RunID: claimedRunID, Outcome: "delivered to Seattle",
	})
	waitForShipmentRecord(t, orderID, owned.ShipmentNumber, childflows.ShipmentRecord{Status: childflows.ShipmentRejected})
	otherRunID := waitForClaimedRun(t, orderID, other.ShipmentNumber)
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentStarted, RunID: otherRunID},
		readShipmentRecord(t, orderID, other.ShipmentNumber))
	// The claim and the rejection leave no early report behind.
	for _, shipment := range []childflows.Shipment{claimed, other, owned} {
		requireNoPendingShipmentReports(t, orderID, shipment.ShipmentNumber)
	}
	terminateFlows(t, claimed.ShipmentNumber, other.ShipmentNumber, owned.ShipmentNumber, orderID)
}

func TestChildFlowsBusinessIdentityFailsAnInvalidOrder(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		order   func(number string) childflows.Order
		message string
	}{
		{
			name: "duplicate shipment number",
			order: func(number string) childflows.Order {
				return childflows.Order{
					Shipments: []childflows.Shipment{
						{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: 1},
						{ShipmentNumber: number, Destination: "Portland", DeliveryDelaySeconds: 1},
					},
					ReconcileIntervalSeconds: 2,
				}
			},
			message: "duplicate shipment number: ",
		},
		{
			name: "negative delivery delay",
			order: func(number string) childflows.Order {
				return childflows.Order{
					Shipments:                []childflows.Shipment{{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: -1}},
					ReconcileIntervalSeconds: 2,
				}
			},
			message: "negative delivery delay for shipment: ",
		},
		{
			name: "zero reconcile interval",
			order: func(number string) childflows.Order {
				return childflows.Order{
					Shipments: []childflows.Shipment{{ShipmentNumber: number, Destination: "Seattle", DeliveryDelaySeconds: 1}},
				}
			},
			message: "reconcile interval must be positive",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			orderID := newFlowID(t, "order")
			number := orderID + "-1"
			startOrder(t, orderID, testCase.order(number))
			result := waitForFlow(t, orderID)
			require.Equal(t, dex.FlowFailed, result.Status)
			require.Contains(t, result.ErrorMessage, testCase.message)
			requireNoShipmentRecord(t, orderID, number)
			_, err := integClient.DescribeFlow(integrationContext(t), number)
			var notFound *dex.FlowNotFoundError
			require.ErrorAs(t, err, &notFound)
		})
	}
}

// The explicit SYNC options override the Flow's ASYNC default, so the outcome stays readable once
// the order recorded it. This does not prove the commit order of the two Steps.
func TestChildFlowsBusinessIdentityKeepsTheOutcomeReadableWhenSyncOverridesAnAsyncDefault(t *testing.T) {
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

func readPendingShipmentReports(t *testing.T, orderID string, shipmentNumber string) map[string]childflows.ShipmentReport {
	t.Helper()
	var pending map[string]childflows.ShipmentReport
	result, err := integClient.GetAttributes(integrationContext(t), orderID,
		dex.ReadAttributeMapInstance(childflows.PendingShipmentReports, shipmentNumber, &pending))
	require.NoError(t, err)
	require.True(t, result.IsMapInstanceSet(childflows.PendingShipmentReports, shipmentNumber), "pending reports of %s", shipmentNumber)
	return pending
}

func requireNoPendingShipmentReports(t *testing.T, orderID string, shipmentNumber string) {
	t.Helper()
	var pending map[string]childflows.ShipmentReport
	result, err := integClient.GetAttributes(integrationContext(t), orderID,
		dex.ReadAttributeMapInstance(childflows.PendingShipmentReports, shipmentNumber, &pending))
	require.NoError(t, err)
	require.False(t, result.IsMapInstanceSet(childflows.PendingShipmentReports, shipmentNumber), "pending reports of %s", shipmentNumber)
}

func terminateFlows(t *testing.T, flowIDs ...string) {
	t.Helper()
	for _, flowID := range flowIDs {
		require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{Type: dex.TerminateFlow}))
	}
}

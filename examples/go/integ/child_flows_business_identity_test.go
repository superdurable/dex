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
	// The reconcile Timer waits an hour, so only the report can fill the outcome.
	waitForShipmentRecord(t, orderID, delivered, childflows.ShipmentRecord{Outcome: "delivered to Seattle"})
	requireWorkflowPatternRunning(t, orderID)
	terminateFlows(t, orderID)
}

func TestChildFlowsBusinessIdentityMergesReportsAndReconcileReads(t *testing.T) {
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
	waitForShipmentRecord(t, orderID, delivered, childflows.ShipmentRecord{Status: "completed", Outcome: "delivered to Seattle"})

	// A repeated or different report for a recorded shipment changes nothing.
	for _, outcome := range []string{"delivered to Seattle", "delivered to Boise"} {
		invokeWorkflowPatternRPC(t, orderID, registry.OrderFulfillment.RecordShipmentOutcome,
			childflows.ShipmentReport{ShipmentNumber: delivered, Outcome: outcome}, nil)
	}
	require.Equal(t, childflows.ShipmentRecord{Status: "completed", Outcome: "delivered to Seattle"},
		readShipmentRecord(t, orderID, delivered))

	waitForShipmentStatus(t, canceled, dex.FlowRunning)
	require.NoError(t, integClient.StopFlow(integrationContext(t), canceled, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "customer canceled the parcel",
	}))
	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, childflows.ShipmentRecord{Status: "canceled"}, readShipmentRecord(t, orderID, canceled))
}

func TestChildFlowsBusinessIdentityRejectsAShipmentNumberOwnedByAnotherRequest(t *testing.T) {
	firstOrderID := newFlowID(t, "order")
	shipment := childflows.Shipment{ShipmentNumber: firstOrderID + "-1", Destination: "Boise", DeliveryDelaySeconds: 3600}
	order := childflows.Order{Shipments: []childflows.Shipment{shipment}, ReconcileIntervalSeconds: 3600}
	startOrder(t, firstOrderID, order)
	waitForShipmentStatus(t, shipment.ShipmentNumber, dex.FlowRunning)

	// Replaying the same start request returns the running shipment.
	requestID, err := childflows.ShipmentStartRequestID(firstOrderID, shipment)
	require.NoError(t, err)
	timeout := childflows.ShipmentTimeout
	runID, err := integClient.StartFlow(integrationContext(t), registry.Shipment, shipment.ShipmentNumber,
		childflows.ShipmentRequest{OrderFlowID: firstOrderID, Shipment: shipment},
		dex.StartFlowOptions{
			Timeout:        &timeout,
			IDReusePolicy:  dex.IDReuseDisallow,
			RequestID:      &requestID,
			AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		})
	require.NoError(t, err)
	info, err := integClient.DescribeFlow(integrationContext(t), shipment.ShipmentNumber)
	require.NoError(t, err)
	require.Equal(t, info.RunID, runID)

	// Another order with the same shipment number sends a different request.
	secondOrderID := newFlowID(t, "order")
	startOrder(t, secondOrderID, order)
	result := waitForFlow(t, secondOrderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentRejected},
		readShipmentRecord(t, secondOrderID, shipment.ShipmentNumber))
	terminateFlows(t, shipment.ShipmentNumber, firstOrderID)
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
	startOrder(t, orderID, childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: orderID + "-1", Destination: "Boise", DeliveryDelaySeconds: 3600}},
		ReconcileIntervalSeconds: 3600,
	})
	number := orderID + "-async"
	asyncDurability := dex.StepDurabilityAsync
	_, err := integClient.StartFlow(integrationContext(t), registry.Shipment, number,
		childflows.ShipmentRequest{
			OrderFlowID: orderID,
			Shipment:    childflows.Shipment{ShipmentNumber: number, Destination: "Austin", DeliveryDelaySeconds: 1},
		},
		dex.StartFlowOptions{ConfigOverride: &dex.FlowConfig{StepDurability: &asyncDurability}})
	require.NoError(t, err)
	waitForShipmentRecord(t, orderID, number, childflows.ShipmentRecord{Outcome: "delivered to Austin"})
	var outcome string
	_, err = integClient.GetAttributes(integrationContext(t), number, dex.ReadAttribute(childflows.ShipmentOutcome, &outcome))
	require.NoError(t, err)
	require.Equal(t, "delivered to Austin", outcome)
	terminateFlows(t, orderID+"-1", orderID)
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

func terminateFlows(t *testing.T, flowIDs ...string) {
	t.Helper()
	for _, flowID := range flowIDs {
		require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{Type: dex.TerminateFlow}))
	}
}

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

	"github.com/stretchr/testify/require"
	childflows "github.com/superdurable/dex/examples/go/patterns/child-flows-business-identity"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestChildFlowsBusinessIdentityRecordsReportedAndCanceledShipments(t *testing.T) {
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
	waitForShipmentStatus(t, canceled, dex.FlowRunning)
	require.NoError(t, integClient.StopFlow(integrationContext(t), canceled, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "customer canceled the parcel",
	}))

	result := waitForFlow(t, orderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, "delivered to Seattle", readShipmentRecord(t, orderID, delivered).Outcome)
	require.Equal(t, childflows.ShipmentRecord{Status: "canceled"}, readShipmentRecord(t, orderID, canceled))

	waitForShipmentStatus(t, delivered, dex.FlowCompleted)
	var outcome string
	_, err := integClient.GetAttributes(integrationContext(t), delivered,
		dex.ReadAttribute(childflows.ShipmentOutcome, &outcome))
	require.NoError(t, err)
	require.Equal(t, "delivered to Seattle", outcome)
}

func TestChildFlowsBusinessIdentityRejectsAShipmentNumberOwnedByAnotherOrder(t *testing.T) {
	firstOrderID := newFlowID(t, "order")
	shared := firstOrderID + "-1"
	order := childflows.Order{
		Shipments:                []childflows.Shipment{{ShipmentNumber: shared, Destination: "Boise", DeliveryDelaySeconds: 3600}},
		ReconcileIntervalSeconds: 3600,
	}
	startOrder(t, firstOrderID, order)
	waitForShipmentStatus(t, shared, dex.FlowRunning)

	secondOrderID := newFlowID(t, "order")
	startOrder(t, secondOrderID, order)
	result := waitForFlow(t, secondOrderID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, childflows.ShipmentRecord{Status: childflows.ShipmentRejected}, readShipmentRecord(t, secondOrderID, shared))

	for _, flowID := range []string{shared, firstOrderID} {
		require.NoError(t, integClient.StopFlow(integrationContext(t), flowID, dex.StopOptions{Type: dex.TerminateFlow}))
	}
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

func readShipmentRecord(t *testing.T, orderID string, shipmentNumber string) childflows.ShipmentRecord {
	t.Helper()
	var record childflows.ShipmentRecord
	result, err := integClient.GetAttributes(integrationContext(t), orderID,
		dex.ReadAttributeMapInstance(childflows.ShipmentRecords, shipmentNumber, &record))
	require.NoError(t, err)
	require.True(t, result.IsMapInstanceSet(childflows.ShipmentRecords, shipmentNumber), "record of %s", shipmentNumber)
	return record
}

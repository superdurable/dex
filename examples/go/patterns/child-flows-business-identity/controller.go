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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

// shipmentFlowType is the registered Flow type of ShipmentFlow.
const shipmentFlowType = "ShipmentFlow"

type controller struct {
	client    *dex.Client
	orderFlow *OrderFulfillmentFlow
}

// RegisterRoutes serves the order routes and the shipment routes that find a shipment by its number.
func RegisterRoutes(router gin.IRouter, client *dex.Client, orderFlow *OrderFulfillmentFlow) {
	controller := &controller{client: client, orderFlow: orderFlow}
	group := router.Group("/patterns/child-flows-business-identity")
	group.GET("/start", controller.startOrder)
	group.GET("/shipment", controller.getShipment)
	group.GET("/cancel-shipment", controller.cancelShipment)
}

// startOrder starts an order with two shipments: one delivers in seconds, one takes a day.
func (controller *controller) startOrder(request *gin.Context) {
	orderID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	timeout := 40 * 24 * time.Hour
	httputil.StartFlow(request, controller.client, controller.orderFlow, orderID, Order{
		Shipments: []Shipment{
			{ShipmentNumber: orderID + "-1", Destination: "Seattle", DeliveryDelaySeconds: 5},
			{ShipmentNumber: orderID + "-2", Destination: "Portland", DeliveryDelaySeconds: 86400},
		},
		ReconcileIntervalSeconds: 60,
	}, dex.StartFlowOptions{Timeout: &timeout})
}

// getShipment looks a shipment up by its shipment number, which is its Flow ID.
func (controller *controller) getShipment(request *gin.Context) {
	number, info, found := controller.describeShipment(request)
	if !found {
		return
	}
	var outcome string
	_, err := controller.client.GetAttributes(request.Request.Context(), number,
		dex.ReadAttribute(ShipmentOutcome, &outcome))
	httputil.Respond(request, gin.H{
		"shipmentNumber": number,
		"status":         info.Status.String(),
		"outcome":        outcome,
	}, err)
}

// cancelShipment stops a shipment by its number. Its order records it at the next reconcile read.
func (controller *controller) cancelShipment(request *gin.Context) {
	number, _, found := controller.describeShipment(request)
	if !found {
		return
	}
	err := controller.client.StopFlow(request.Request.Context(), number, dex.StopOptions{
		Type:   dex.CancelFlow,
		Reason: "shipment canceled",
	})
	httputil.RespondString(request, "canceled", err)
}

// describeShipment describes the Flow named by the shipmentNumber query.
// It returns found as false after it responded: 404 when no ShipmentFlow has that Flow ID.
func (controller *controller) describeShipment(request *gin.Context) (string, dex.FlowInfo, bool) {
	number, found := httputil.RequiredQuery(request, "shipmentNumber")
	if !found {
		return "", dex.FlowInfo{}, false
	}
	info, err := controller.client.DescribeFlow(request.Request.Context(), number)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) || (err == nil && info.FlowType != shipmentFlowType) {
		request.JSON(http.StatusNotFound, gin.H{"error": "shipment " + number + " not found"})
		return "", dex.FlowInfo{}, false
	}
	if err != nil {
		httputil.Respond(request, nil, err)
		return "", dex.FlowInfo{}, false
	}
	return number, info, true
}

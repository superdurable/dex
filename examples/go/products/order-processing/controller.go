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

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *OrderProcessingFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *OrderProcessingFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/products/order-processing")
	group.GET("/start", controller.start)
	group.GET("/approve", controller.approve)
	group.GET("/describe", controller.describe)
}

func (controller *controller) start(request *gin.Context) {
	flowID := httputil.NewFlowID("order-processing")
	input := OrderRequest{
		OrderID:            flowID,
		Email:              "buyer@example.com",
		CustomerID:         "customer-1",
		Amount:             42,
		TestFailAtShipping: request.Query("testFailAtShipping") == "true",
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		input,
		sdk.StartFlowOptions{},
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	err = controller.client.WaitForStepCompletion(
		request.Request.Context(),
		flowID,
		sdk.StepExecutionID{StepType: ChargeStepType},
		sdk.WaitOptions{Timeout: 5 * time.Minute},
	)
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *controller) approve(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output string
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.Approve,
		request.Query("notes"),
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, output, err)
}

func (controller *controller) describe(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output string
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.Describe,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, gin.H{"flowID": flowID, "status": output}, err)
}

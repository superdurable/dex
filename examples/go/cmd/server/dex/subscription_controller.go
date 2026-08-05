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

package dex

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type subscriptionController struct {
	client *sdk.Client
}

func newSubscriptionController(client *sdk.Client) *subscriptionController {
	return &subscriptionController{client: client}
}

func (controller *subscriptionController) registerRoutes(router *gin.Engine) {
	router.GET("/subscription/start", controller.start)
	router.GET("/subscription/cancel", controller.cancel)
	router.GET("/subscription/updateChargeAmount", controller.updateChargeAmount)
	router.GET("/subscription/describe", controller.describe)
}

func (controller *subscriptionController) start(request *gin.Context) {
	flowID := newFlowID("subscription")
	customer := subscription.Customer{
		FirstName: "Quanzheng",
		LastName:  "Long",
		ID:        "qlong",
		Email:     "qlong@example.com",
		Subscription: subscription.Subscription{
			TrialPeriod:         20 * time.Second,
			BillingPeriod:       10 * time.Second,
			MaxBillingPeriods:   10,
			BillingPeriodCharge: 100,
		},
	}
	startFlow(request, controller.client, workflows.Subscription, flowID, customer)
}

func (controller *subscriptionController) cancel(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		subscription.CancelSubscription,
		nil,
	)
	respond(request, struct{}{}, err)
}

func (controller *subscriptionController) updateChargeAmount(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	amount, err := strconv.Atoi(request.Query("newChargeAmount"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "newChargeAmount must be an integer"})
		return
	}
	err = controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		subscription.UpdateChargeAmount,
		amount,
	)
	respond(request, struct{}{}, err)
}

func (controller *subscriptionController) describe(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output subscription.Subscription
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Subscription.Describe,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

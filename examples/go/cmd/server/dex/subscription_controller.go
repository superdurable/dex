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
	"context"
	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	"github.com/superdurable/dex/sdk-go/dex"
	"net/http"
	"strconv"
)

func cancelSubscription(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		err := client.SignalWorkflow(c.Request.Context(), &subscription.SubscriptionWorkflow{}, wfId, "", subscription.SignalCancelSubscription, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func descSubscription(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := subscription.SubscriptionWorkflow{}
		var rpcOutput subscription.Subscription
		err := client.InvokeRPC(context.Background(), wfId, "", wf.Describe, nil, &rpcOutput)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, rpcOutput)
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func updateSubscriptionChargeAmount(c *gin.Context) {
	wfId := c.Query("workflowId")
	newChargeAmountStr := c.Query("newChargeAmount")
	newAmount, err := strconv.Atoi(newChargeAmountStr)

	if wfId != "" && err == nil {
		err := client.SignalWorkflow(c.Request.Context(), &subscription.SubscriptionWorkflow{}, wfId, "", subscription.SignalUpdateBillingPeriodChargeAmount, newAmount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, dex.GetOpenApiErrorBody(err))
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide correct workflowId and newChargeAmount via URL parameter")
}

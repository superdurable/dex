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

package recovery

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *FailureRecoveryFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *FailureRecoveryFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/recovery")
	group.GET("/start", controller.start)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	itemName, found := httputil.RequiredQuery(request, "itemName")
	if !found {
		return
	}
	quantityText, found := httputil.RequiredQuery(request, "quantity")
	if !found {
		return
	}
	quantity, err := strconv.Atoi(quantityText)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "quantity must be an integer"})
		return
	}
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		FailureRecoveryWorkflowInput{
			ItemName:          itemName,
			RequestedQuantity: quantity,
		},
		patternStartOptions(),
	)
	httputil.RespondString(request, "recovery workflow started", err)
}

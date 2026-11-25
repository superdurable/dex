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

package controllongrunningwork

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
	flow   *RecordBackfillFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *RecordBackfillFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/control-long-running-work")
	group.GET("/start", controller.start)
	group.GET("/set-batch-size", controller.setBatchSize)
	group.GET("/stop", controller.requestStop)
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
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		nil,
		patternStartOptions(),
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) setBatchSize(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	sizeText, found := httputil.RequiredQuery(request, "batchSize")
	if !found {
		return
	}
	size, err := strconv.Atoi(sizeText)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "batchSize must be an integer"})
		return
	}
	err = controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.SetBatchSize,
		size,
		nil,
	)
	httputil.RespondString(request, "done", err)
}

func (controller *controller) requestStop(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.RequestStop,
		nil,
		nil,
	)
	httputil.RespondString(request, "done", err)
}

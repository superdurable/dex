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

package waittypes

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *WaitTypesFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/primitives/step/wait-types")
	group.GET("/start", controller.start)
	group.GET("/signal-a", controller.signalA)
	group.GET("/signal-b", controller.signalB)
}

type controller struct {
	client *sdk.Client
	flow   *WaitTypesFlow
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	mode, found := httputil.RequiredQuery(request, "mode")
	if !found {
		return
	}
	timeoutSeconds := 60
	if timeoutText := request.Query("timeoutSeconds"); timeoutText != "" {
		parsed, err := strconv.Atoi(timeoutText)
		if err != nil {
			request.JSON(http.StatusBadRequest, gin.H{"error": "timeoutSeconds must be an integer"})
			return
		}
		timeoutSeconds = parsed
	}
	timeout := time.Hour
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		flowID,
		WaitTypesInput{Mode: mode, TimeoutSeconds: timeoutSeconds},
		sdk.StartFlowOptions{Timeout: &timeout},
	)
}

func (controller *controller) signalA(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.SignalA,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	httputil.RespondString(request, "done", err)
}

func (controller *controller) signalB(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.SignalB,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	httputil.RespondString(request, "done", err)
}

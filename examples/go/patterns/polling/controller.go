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

package polling

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client    *sdk.Client
	timer     *PollingWithTimerFlow
	backoff   *BackoffPollingFlow
	iteration *IterationFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *sdk.Client,
	timer *PollingWithTimerFlow,
	backoff *BackoffPollingFlow,
	iteration *IterationFlow,
) {
	controller := &controller{client: client, timer: timer, backoff: backoff, iteration: iteration}
	group := router.Group("/patterns/polling")
	group.GET("/start/timer", controller.startTimer)
	group.GET("/start/backoff", controller.startBackoff)
	group.GET("/start/iteration", controller.startIteration)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) startTimer(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.timer,
		flowID,
		nil,
		patternStartOptions(),
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) startIteration(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(request.Request.Context(), controller.iteration, flowID, "", patternStartOptions())
	httputil.RespondString(request, runID, err)
}

func (controller *controller) startBackoff(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.backoff,
		flowID,
		nil,
		patternStartOptions(),
	)
	httputil.RespondString(request, runID, err)
}

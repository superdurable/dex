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

package signal

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *DrainSignalChannelsFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *DrainSignalChannelsFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/drain-channels/signal")
	group.GET("/start-or-signal", controller.startOrSignal)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) startOrSignal(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		QueueSignalChannel,
		"signal from startorsignal endpoint",
	)
	if err == nil {
		httputil.RespondString(request, "Signaled the workflow", nil)
		return
	}
	var inactive *sdk.FlowNotActiveError
	if !errors.As(err, &inactive) {
		httputil.RespondString(request, "", err)
		return
	}
	runID, startErr := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		"first message from start",
		patternStartOptions(),
	)
	httputil.RespondString(
		request,
		fmt.Sprintf("Started the workflow with runId %s", runID),
		startErr,
	)
}

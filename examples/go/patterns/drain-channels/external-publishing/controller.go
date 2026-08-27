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

package externalpublishing

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
	flow   *DrainingChannelFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *DrainingChannelFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/drain-channels/external-publishing")
	group.GET("/start-or-publish", controller.startOrPublish)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) startOrPublish(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		QueueChannel,
		"message from start-or-publish endpoint",
	)
	if err == nil {
		httputil.RespondString(request, "Published to the Flow", nil)
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
		"first message from start-or-publish",
		patternStartOptions(),
	)
	httputil.RespondString(
		request,
		fmt.Sprintf("Started the workflow with runId %s", runID),
		startErr,
	)
}

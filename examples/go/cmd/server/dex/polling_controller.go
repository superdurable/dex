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

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/polling"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type pollingController struct {
	client *sdk.Client
}

func newPollingController(client *sdk.Client) *pollingController {
	return &pollingController{client: client}
}

func (controller *pollingController) registerRoutes(router *gin.Engine) {
	router.GET("/polling/start", controller.start)
	router.GET("/polling/complete", controller.completeTask)
}

func (controller *pollingController) start(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	maximumPolls, err := strconv.Atoi(request.Query("pollingCompletionThreshold"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "pollingCompletionThreshold must be an integer"})
		return
	}
	startFlow(request, controller.client, workflows.Polling, flowID, maximumPolls)
}

func (controller *pollingController) completeTask(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var channel sdk.ChannelDef
	switch request.Query("channel") {
	case polling.TaskACompleted.ChannelName():
		channel = polling.TaskACompleted
	case polling.TaskBCompleted.ChannelName():
		channel = polling.TaskBCompleted
	default:
		request.JSON(http.StatusBadRequest, gin.H{"error": "channel must identify task A or task B"})
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		channel,
		nil,
	)
	respond(request, struct{}{}, err)
}

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
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *PollingFlow
	jobs   *FakeJobService
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *PollingFlow, jobs *FakeJobService) {
	controller := &controller{client: client, flow: flow, jobs: jobs}
	group := router.Group("/patterns/polling")
	group.GET("/start", controller.start)
	group.GET("/complete-job", controller.completeJob)
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

func (controller *controller) completeJob(request *gin.Context) {
	jobID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	if err := controller.jobs.CompleteJob(jobID); err != nil {
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	request.String(http.StatusOK, "done")
}

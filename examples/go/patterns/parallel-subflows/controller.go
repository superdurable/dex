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

package parallelsubflows

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client    *dex.Client
	basic     *BasicParentFlow
	longLive  *AdvancedLongLiveParentFlow
	shortLive *AdvancedShortLiveParentFlow
	submit    *SubmitRequestFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *dex.Client,
	basic *BasicParentFlow,
	longLive *AdvancedLongLiveParentFlow,
	shortLive *AdvancedShortLiveParentFlow,
	submit *SubmitRequestFlow,
) {
	controller := &controller{client: client, basic: basic, longLive: longLive, shortLive: shortLive, submit: submit}
	group := router.Group("/patterns/parallel-subflows")
	group.GET("/start/basic", controller.startBasic)
	group.GET("/start/long-lived-parent", controller.startLongLive)
	group.GET("/start/short-lived-parent", controller.startShortLive)
	group.GET("/start/submit", controller.startSubmit)
	group.GET("/send", controller.sendRequest)
	group.GET("/stop", controller.stopLongLive)
}

func (controller *controller) startBasic(request *gin.Context) {
	controller.start(request, controller.basic, []string{"one", "two", "three", "four"})
}

func (controller *controller) startLongLive(request *gin.Context) {
	controller.start(request, controller.longLive, ParentInput{
		Requests: []string{"one", "two", "three"}, Concurrency: 3,
	})
}

func (controller *controller) startShortLive(request *gin.Context) {
	controller.start(request, controller.shortLive, ParentInput{
		Requests: []string{"one", "two", "three"}, Concurrency: 3,
	})
}

func (controller *controller) startSubmit(request *gin.Context) {
	controller.start(request, controller.submit, SubmitRequestInput{
		Request: "one", ParentIDs: []string{"parallel-parent-0", "parallel-parent-1"},
	})
}

func (controller *controller) start(request *gin.Context, flow dex.Flow, input any) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	timeout := time.Hour
	runID, err := controller.client.StartFlow(
		request.Request.Context(), flow, flowID, input, dex.StartFlowOptions{Timeout: &timeout},
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) sendRequest(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var accepted bool
	err := controller.client.InvokeRPC(
		request.Request.Context(), flowID, controller.shortLive.SendRequest, "appended", &accepted, dex.InvokeOptions{},
	)
	httputil.RespondString(request, fmt.Sprintf("%t", accepted), err)
}

func (controller *controller) stopLongLive(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output dex.None
	err := controller.client.InvokeRPC(
		request.Request.Context(), flowID, controller.longLive.Stop, nil, &output, dex.InvokeOptions{},
	)
	httputil.RespondString(request, "stopping", err)
}

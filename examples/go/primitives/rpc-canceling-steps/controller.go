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

package rpccancelingsteps

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *ReportGenerationFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *ReportGenerationFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/primitives/rpc-canceling-steps")
	group.GET("/start", controller.start)
	group.GET("/stop", controller.stop)
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	reportName, found := httputil.RequiredQuery(request, "reportName")
	if !found {
		return
	}
	timeout := time.Hour
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		flowID,
		reportName,
		sdk.StartFlowOptions{Timeout: &timeout},
	)
}

func (controller *controller) stop(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var isStopping bool
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.StopReport,
		nil,
		&isStopping,
	)
	httputil.Respond(request, gin.H{"isStopping": isStopping}, err)
}

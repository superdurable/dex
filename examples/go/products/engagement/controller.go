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

package engagement

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *EngagementFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *EngagementFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/products/engagement")
	group.GET("/start", controller.start)
	group.GET("/describe", controller.describe)
	group.GET("/optout", controller.optOutReminder)
	group.GET("/decline", controller.decline)
	group.GET("/accept", controller.accept)
	group.GET("/list", controller.list)
}

func (controller *controller) start(request *gin.Context) {
	flowID := httputil.NewFlowID("engagement")
	input := EngagementInput{
		EmployerID:  "test-employer-id",
		JobSeekerID: "test-job-seeker-id",
		Notes:       "test-notes",
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		input,
		sdk.StartFlowOptions{},
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	waitContext, cancelWait := context.WithTimeout(request.Request.Context(), 15*time.Second)
	defer cancelWait()
	err = controller.client.WaitForAttributeEqual(
		waitContext,
		flowID,
		EmployerID,
		input.EmployerID,
	)
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *controller) describe(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output EngagementDescription
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.Describe,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, output, err)
}

func (controller *controller) optOutReminder(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		OptOutReminder,
		nil,
	)
	httputil.Respond(request, struct{}{}, err)
}

func (controller *controller) decline(request *gin.Context) {
	controller.update(request, controller.flow.Decline)
}

func (controller *controller) accept(request *gin.Context) {
	controller.update(request, controller.flow.Accept)
}

func (controller *controller) update(
	request *gin.Context,
	rpc sdk.RPC[string, Status],
) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output Status
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		rpc,
		request.Query("notes"),
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, output, err)
}

func (controller *controller) list(request *gin.Context) {
	query, found := httputil.RequiredQuery(request, "query")
	if !found {
		return
	}
	page, err := controller.client.SearchFlows(request.Request.Context(), query, 100, "")
	httputil.Respond(request, page, err)
}

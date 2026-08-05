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
	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type engagementController struct {
	client *sdk.Client
}

func newEngagementController(client *sdk.Client) *engagementController {
	return &engagementController{client: client}
}

func (controller *engagementController) registerRoutes(router *gin.Engine) {
	router.GET("/engagement/start", controller.start)
	router.GET("/engagement/describe", controller.describe)
	router.GET("/engagement/optout", controller.optOutReminder)
	router.GET("/engagement/decline", controller.decline)
	router.GET("/engagement/accept", controller.accept)
	router.GET("/engagement/list", controller.list)
}

func (controller *engagementController) start(request *gin.Context) {
	flowID := newFlowID("engagement")
	input := engagement.EngagementInput{
		EmployerID:  "test-employer-id",
		JobSeekerID: "test-job-seeker-id",
		Notes:       "test-notes",
	}
	startFlow(request, controller.client, workflows.Engagement, flowID, input)
}

func (controller *engagementController) describe(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output engagement.EngagementDescription
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Engagement.Describe,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (controller *engagementController) optOutReminder(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		engagement.OptOutReminder,
		nil,
	)
	respond(request, struct{}{}, err)
}

func (controller *engagementController) decline(request *gin.Context) {
	controller.update(request, workflows.Engagement.Decline)
}

func (controller *engagementController) accept(request *gin.Context) {
	controller.update(request, workflows.Engagement.Accept)
}

func (controller *engagementController) update(
	request *gin.Context,
	rpc sdk.RPC[string, engagement.Status],
) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output engagement.Status
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		rpc,
		request.Query("notes"),
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (controller *engagementController) list(request *gin.Context) {
	query, found := requiredQuery(request, "query")
	if !found {
		return
	}
	page, err := controller.client.SearchFlows(request.Request.Context(), query, 100, "")
	respond(request, page, err)
}

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

package jobpost

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type controller struct {
	client *sdk.Client
	flow   *JobPostFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *JobPostFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/products/job-post")
	group.GET("/create", controller.create)
	group.GET("/read", controller.read)
	group.GET("/update", controller.update)
	group.GET("/delete", controller.delete)
	group.GET("/search", controller.search)
}

func (controller *controller) create(request *gin.Context) {
	title, found := httputil.RequiredQuery(request, "title")
	if !found {
		return
	}
	description, found := httputil.RequiredQuery(request, "description")
	if !found {
		return
	}
	flowID := fmt.Sprintf("job_id_%d", time.Now().Unix())
	timeout := 24 * time.Hour
	titleAttr, err := sdk.InitialAttribute(Title, title)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	descriptionAttr, err := sdk.InitialAttribute(JobDescription, description)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	lastUpdateAttr, err := sdk.InitialAttribute(
		LastUpdateTimeMillis,
		time.Now().UnixMilli(),
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		flowID,
		nil,
		sdk.StartFlowOptions{
			Timeout: &timeout,
			Attributes: []sdk.InitialAttributeDef{
				titleAttr,
				descriptionAttr,
				lastUpdateAttr,
			},
			ConfigOverride: &sdk.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(10)),
			},
		},
	)
}

func (controller *controller) read(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output JobInfo
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.Get,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, output, err)
}

func (controller *controller) update(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	title, found := httputil.RequiredQuery(request, "title")
	if !found {
		return
	}
	description, found := httputil.RequiredQuery(request, "description")
	if !found {
		return
	}
	notes := request.Query("notes")
	if notes == "" {
		notes = "test-notes"
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.Update,
		JobInfo{Title: title, Description: description, Notes: notes},
		&none,
		sdk.InvokeOptions{},
	)
	httputil.Respond(request, gin.H{"updated": true}, err)
}

func (controller *controller) delete(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.StopFlow(request.Request.Context(), flowID, sdk.StopOptions{})
	httputil.Respond(request, gin.H{"stopped": flowID}, err)
}

func (controller *controller) search(request *gin.Context) {
	query := request.Query("query")
	page, err := controller.client.SearchFlows(
		request.Request.Context(),
		query,
		20,
		"",
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	ids := make([]string, 0, len(page.Flows))
	for _, flow := range page.Flows {
		ids = append(ids, flow.FlowID)
	}
	httputil.Respond(request, gin.H{"flowIDs": ids, "nextPageToken": page.NextPageToken}, nil)
}

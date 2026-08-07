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
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/jobpost"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type jobPostController struct {
	client *sdk.Client
}

func newJobPostController(client *sdk.Client) *jobPostController {
	return &jobPostController{client: client}
}

func (controller *jobPostController) registerRoutes(router *gin.Engine) {
	router.GET("/jobpost/create", controller.create)
	router.GET("/jobpost/read", controller.read)
	router.GET("/jobpost/update", controller.update)
	router.GET("/jobpost/delete", controller.delete)
	router.GET("/jobpost/search", controller.search)
}

func (controller *jobPostController) create(request *gin.Context) {
	title, found := requiredQuery(request, "title")
	if !found {
		return
	}
	description, found := requiredQuery(request, "description")
	if !found {
		return
	}
	flowID := fmt.Sprintf("job_id_%d", time.Now().Unix())
	timeout := 24 * time.Hour
	titleAttr, err := sdk.InitialAttribute(jobpost.Title, title)
	if err != nil {
		respond(request, nil, err)
		return
	}
	descriptionAttr, err := sdk.InitialAttribute(jobpost.JobDescription, description)
	if err != nil {
		respond(request, nil, err)
		return
	}
	lastUpdateAttr, err := sdk.InitialAttribute(
		jobpost.LastUpdateTimeMillis,
		time.Now().UnixMilli(),
	)
	if err != nil {
		respond(request, nil, err)
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.JobPost,
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
	respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *jobPostController) read(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output jobpost.JobInfo
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.JobPost.Get,
		nil,
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (controller *jobPostController) update(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	title, found := requiredQuery(request, "title")
	if !found {
		return
	}
	description, found := requiredQuery(request, "description")
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
		workflows.JobPost.Update,
		jobpost.JobInfo{Title: title, Description: description, Notes: notes},
		&none,
		sdk.InvokeOptions{},
	)
	respond(request, gin.H{"updated": true}, err)
}

func (controller *jobPostController) delete(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.StopFlow(request.Request.Context(), flowID, sdk.StopOptions{})
	respond(request, gin.H{"stopped": flowID}, err)
}

func (controller *jobPostController) search(request *gin.Context) {
	query := request.Query("query")
	page, err := controller.client.SearchFlows(
		request.Request.Context(),
		query,
		20,
		"",
	)
	if err != nil {
		respond(request, nil, err)
		return
	}
	ids := make([]string, 0, len(page.Flows))
	for _, flow := range page.Flows {
		ids = append(ids, flow.FlowID)
	}
	respond(request, gin.H{"flowIDs": ids, "nextPageToken": page.NextPageToken}, nil)
}

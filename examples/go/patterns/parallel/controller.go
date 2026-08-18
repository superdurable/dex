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

package parallel

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client    *sdk.Client
	simple    *SimpleParallelStatesFlow
	withAwait *ParallelStatesWithAwaitFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *sdk.Client,
	simple *SimpleParallelStatesFlow,
	withAwait *ParallelStatesWithAwaitFlow,
) {
	controller := &controller{client: client, simple: simple, withAwait: withAwait}
	group := router.Group("/patterns/parallel")
	group.GET("/start/simple", controller.startSimple)
	group.GET("/start/withAwait", controller.startWithAwait)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) startSimple(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	jobSeeker := JobSeeker{
		ID:          "123",
		Email:       "jobseeker@indeed.com",
		PhoneNumber: "0987654321",
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.simple,
		flowID,
		jobSeeker,
		patternStartOptions(),
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) startWithAwait(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.withAwait,
		flowID,
		50,
		patternStartOptions(),
	)
	httputil.RespondString(request, runID, err)
}

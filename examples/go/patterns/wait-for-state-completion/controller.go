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

package waitforstatecompletion

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *WaitForStateCompletionFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *WaitForStateCompletionFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/wait-for-state-completion")
	group.GET("/start", controller.start)
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
	input := JobSeekerData{ID: 1}
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		input,
		patternStartOptions(),
	)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	err = controller.client.WaitForStepCompletion(
		request.Request.Context(),
		flowID,
		sdk.StepExecutionID{StepType: "PersistData"},
		sdk.WaitOptions{Timeout: 5 * time.Minute},
	)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	var persistedData JobSeekerData
	err = controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.flow.GetJobSeekerData,
		nil,
		&persistedData,
		sdk.InvokeOptions{},
	)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	serialized, err := json.Marshal(persistedData)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	httputil.RespondString(
		request,
		fmt.Sprintf("success for workflow %s with data %s", flowID, serialized),
		nil,
	)
}

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

package flow

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

func RegisterRoutes(router gin.IRouter, client *sdk.Client, exampleFlow *ExampleFlow) {
	controller := &controller{client: client, flow: exampleFlow}
	group := router.Group("/primitives/flow")
	group.GET("/start", controller.start)
}

type controller struct {
	client *sdk.Client
	flow   *ExampleFlow
}

func startFlowOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	stepDurability := sdk.StepDurabilitySync
	return sdk.StartFlowOptions{
		Timeout: &timeout,
		ConfigOverride: &sdk.FlowConfig{
			StepDurability: &stepDurability,
		},
	}
}

func reliableStartFlowOptions() (sdk.StartFlowOptions, error) {
	initialStatus, err := sdk.InitialAttribute(Status, "queued")
	if err != nil {
		return sdk.StartFlowOptions{}, err
	}
	timeout := 30 * time.Minute
	startDelay := 5 * time.Minute
	requestID := "start-order-123"
	stepDurability := sdk.StepDurabilitySync
	return sdk.StartFlowOptions{
		Timeout:       &timeout,
		TimeoutPolicy: sdk.TimeoutFail,
		StartDelay:    &startDelay,
		IDReusePolicy: sdk.IDReuseDisallow,
		RetryPolicy: &sdk.FlowRetryPolicy{
			InitialInterval:    time.Minute,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Minute,
			MaximumAttempts:    3,
		},
		Attributes: []sdk.InitialAttributeDef{initialStatus},
		ConfigOverride: &sdk.FlowConfig{
			StepDurability: &stepDurability,
		},
		AlreadyStarted: &sdk.AlreadyStartedOptions{IgnoreError: true},
		RequestID:      &requestID,
	}, nil
}

func rerouteActiveFlow(ctx context.Context, client *sdk.Client, flowID string) error {
	return client.UpdateFlowConfig(ctx, flowID, sdk.FlowConfig{
		WorkerTarget: &sdk.WorkerTarget{Address: "worker-canary:8803"},
	})
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	inputText, found := httputil.RequiredQuery(request, "inputNum")
	if !found {
		return
	}
	inputNum, err := strconv.Atoi(inputText)
	if err != nil {
		request.String(http.StatusBadRequest, "inputNum must be an integer")
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		inputNum,
		startFlowOptions(),
	)
	if err != nil {
		request.String(http.StatusInternalServerError, err.Error())
		return
	}
	request.String(http.StatusOK, runID)
}

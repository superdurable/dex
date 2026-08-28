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
	client   *sdk.Client
	static   *StaticParallelStepsFlow
	dynamic  *DynamicParallelStepsFlow
	await    *AwaitParallelStepsFlow
	firstWin *FirstWinParallelStepsFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *sdk.Client,
	static *StaticParallelStepsFlow,
	dynamic *DynamicParallelStepsFlow,
	await *AwaitParallelStepsFlow,
	firstWin *FirstWinParallelStepsFlow,
) {
	controller := &controller{client: client, static: static, dynamic: dynamic, await: await, firstWin: firstWin}
	group := router.Group("/patterns/parallel")
	group.GET("/start/static", controller.startStatic)
	group.GET("/start/dynamic", controller.startDynamic)
	group.GET("/start/await", controller.startAwait)
	group.GET("/start/first-win", controller.startFirstWin)
}

func (controller *controller) startStatic(request *gin.Context) {
	controller.start(request, controller.static, "work")
}

func (controller *controller) startDynamic(request *gin.Context) {
	controller.start(request, controller.dynamic, []string{"one", "two", "three"})
}

func (controller *controller) startAwait(request *gin.Context) {
	controller.start(request, controller.await, 3)
}

func (controller *controller) startFirstWin(request *gin.Context) {
	controller.start(request, controller.firstWin, 3)
}

func (controller *controller) start(request *gin.Context, flow sdk.Flow, input any) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	timeout := time.Hour
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		flow,
		flowID,
		input,
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	httputil.RespondString(request, runID, err)
}

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
	"github.com/superdurable/dex/examples/go/workflows/microservices"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type microserviceController struct {
	client *sdk.Client
}

func newMicroserviceController(client *sdk.Client) *microserviceController {
	return &microserviceController{client: client}
}

func (controller *microserviceController) registerRoutes(router *gin.Engine) {
	router.GET("/microservice/start", controller.start)
	router.GET("/microservice/swap", controller.swapData)
	router.GET("/microservice/signal", controller.signal)
}

func (controller *microserviceController) start(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	startFlow(request, controller.client, workflows.Microservices, flowID, "test initial data")
}

func (controller *microserviceController) swapData(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output string
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Microservices.Swap,
		request.Query("data"),
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (controller *microserviceController) signal(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		microservices.Ready,
		nil,
	)
	respond(request, struct{}{}, err)
}

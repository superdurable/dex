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

package sequentiallychunkedattributemap

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *dex.Client
	flow   *ChunkedSubscriberFlow
}

func RegisterRoutes(router gin.IRouter, client *dex.Client, flow *ChunkedSubscriberFlow) {
	patternController := &controller{client: client, flow: flow}
	group := router.Group("/patterns/sequentially-chunked-attribute-map")
	group.POST("/start", patternController.start)
	group.POST("/register", patternController.registerSubscriber)
	group.GET("/subscribers", patternController.getSubscriberPage)
}

func (controller *controller) start(request *gin.Context) {
	options, err := StartOptions()
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	httputil.StartFlow(request, controller.client, controller.flow, FlowID, nil, options)
}

func (controller *controller) registerSubscriber(request *gin.Context) {
	var input RegisterSubscriberInput
	if err := request.ShouldBindJSON(&input); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var subscriber Subscriber
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		FlowID,
		controller.flow.RegisterSubscriber,
		input,
		&subscriber,
	)
	httputil.Respond(request, subscriber, err)
}

func (controller *controller) getSubscriberPage(request *gin.Context) {
	pageToken, err := ValidateSubscriberPageToken(request.Query("pageToken"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var page SubscriberPage
	err = controller.client.InvokeRPCWithOptions(
		request.Request.Context(),
		FlowID,
		controller.flow.GetSubscriberPage,
		GetSubscriberPageInput{PageToken: pageToken},
		&page,
		dex.RPCInvokeOptions{
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				SubscriberChunks.Load(pageToken),
			},
		},
	)
	httputil.Respond(request, page, err)
}

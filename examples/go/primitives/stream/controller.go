// Copyright (c) 2026 Super Durable, Inc.
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

package stream

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *StreamFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *StreamFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/primitives/stream")
	group.GET("/start", controller.start)
	group.GET("/write", controller.write)
	group.GET("/read", controller.read)
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	input, found := httputil.RequiredQuery(request, "input")
	if !found {
		return
	}
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		flowID,
		input,
		primitiveStartOptions(),
	)
}

func (controller *controller) write(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	idempotencyKey, found := httputil.RequiredQuery(request, "idempotencyKey")
	if !found {
		return
	}
	message, found := httputil.RequiredQuery(request, "message")
	if !found {
		return
	}
	err := controller.client.WriteStream(
		request.Request.Context(),
		flowID,
		Progress,
		idempotencyKey,
		message,
	)
	httputil.RespondString(request, "done", err)
}

func (controller *controller) read(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var value string
	message, err := controller.client.ReadStream(
		request.Request.Context(),
		flowID,
		Progress,
		request.Query("resumeToken"),
		&value,
	)
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.JSON(http.StatusOK, gin.H{
		"value":          value,
		"resumeToken":    message.ResumeToken,
		"createdTime":    message.CreatedTime.Format(time.RFC3339Nano),
		"idempotencyKey": message.IdempotencyKey,
	})
}

func primitiveStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

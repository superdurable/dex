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

package attribute

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *AttributeFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *AttributeFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/primitives/attribute")
	group.GET("/start", controller.start)
	group.GET("/read", controller.readAttributes)
}

func primitiveStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	message, found := httputil.RequiredQuery(request, "message")
	if !found {
		return
	}
	httputil.StartFlow(
		request,
		controller.client,
		controller.flow,
		flowID,
		message,
		primitiveStartOptions(),
	)
}

func (controller *controller) readAttributes(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	// An unset value leaves its destination unchanged, so these defaults remain.
	status := "not started"
	paymentProgress := "not started"
	var email string
	result, err := controller.client.GetAttributes(
		request.Request.Context(),
		flowID,
		sdk.ReadAttribute(Status, &status),
		sdk.ReadAttributeMapInstance(Progress, "payment", &paymentProgress),
		sdk.ReadAttribute(Email, &email),
	)
	var notFound *sdk.FlowNotFoundError
	if errors.As(err, &notFound) {
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	// This Flow never sets Email, so IsSet reports false.
	var emailOrNil *string
	if result.IsSet(Email) {
		emailOrNil = &email
	}
	httputil.Respond(request, gin.H{
		"status":          status,
		"paymentProgress": paymentProgress,
		"email":           emailOrNil,
	}, nil)
}

func invokeUpdateStatus(ctx context.Context, client *sdk.Client, flowID string) error {
	var updated string
	return client.InvokeRPC(
		ctx,
		flowID,
		(*AttributeFlow).UpdateStatus,
		"completed",
		&updated,
	)
}

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

package externalpublishing

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *DrainingExternalChannelFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *DrainingExternalChannelFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/drain-channels/external-publishing")
	group.GET("/start-or-publish", controller.startOrPublish)
}

// startOrPublishAttempts bounds how often a message that lost a start race is published again.
const startOrPublishAttempts = 3

// patternStartOptions restarts a closed Flow ID; a retried start of this message returns its run.
func patternStartOptions(messageID string) sdk.StartFlowOptions {
	timeout := time.Hour
	requestID := "start:" + messageID
	return sdk.StartFlowOptions{
		Timeout:        &timeout,
		IDReusePolicy:  sdk.IDReuseAllowIfNotRunning,
		RequestID:      &requestID,
		AlreadyStarted: &sdk.AlreadyStartedOptions{IgnoreError: true},
	}
}

func (controller *controller) startOrPublish(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	messageID := request.Query("messageId")
	if messageID == "" {
		messageID = uuid.NewString()
	}
	message := "message " + messageID
	publishRequestID := "publish:" + messageID
	for attempt := 0; attempt < startOrPublishAttempts; attempt++ {
		var output string
		err := controller.client.InvokeRPCWithOptions(
			request.Request.Context(),
			flowID,
			controller.flow.PublishExternalChannelMessage,
			message,
			&output,
			sdk.RPCInvokeOptions{RequestID: &publishRequestID},
		)
		if err == nil {
			httputil.RespondString(request, fmt.Sprintf("Accepted message %s", messageID), nil)
			return
		}
		var inactive *sdk.FlowNotActiveOrNotFoundError
		if !errors.As(err, &inactive) {
			httputil.RespondString(request, "", err)
			return
		}
		runID, startErr := controller.client.StartFlow(
			request.Request.Context(),
			controller.flow,
			flowID,
			message,
			patternStartOptions(messageID),
		)
		var alreadyStarted *sdk.FlowAlreadyStartedError
		if errors.As(startErr, &alreadyStarted) {
			// Another caller started a run first: publish this message to that run.
			continue
		}
		httputil.RespondString(
			request,
			fmt.Sprintf("Accepted message %s as the start input of runId %s", messageID, runID),
			startErr,
		)
		return
	}
	request.String(
		http.StatusConflict,
		fmt.Sprintf("message %s is unconfirmed; send it again with the same messageId", messageID),
	)
}

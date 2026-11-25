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

package inboundeventloop

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *SupportTicketFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *SupportTicketFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/inbound-event-loop")
	group.GET("/start", controller.start)
	group.GET("/reply", controller.sendTicketReply)
	group.GET("/resolve", controller.resolveTicket)
	group.GET("/transcript", controller.getTicketTranscript)
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	timeout := time.Hour
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		"",
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) sendTicketReply(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	text, found := httputil.RequiredQuery(request, "text")
	if !found {
		return
	}
	err := controller.client.InvokeRPC(request.Request.Context(), flowID, controller.flow.SendTicketReply, text, nil)
	httputil.RespondString(request, "reply accepted", err)
}

func (controller *controller) resolveTicket(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	reason, found := httputil.RequiredQuery(request, "reason")
	if !found {
		return
	}
	err := controller.client.InvokeRPC(request.Request.Context(), flowID, controller.flow.ResolveTicket, reason, nil)
	httputil.RespondString(request, "resolution accepted", err)
}

func (controller *controller) getTicketTranscript(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var chunk TranscriptChunk
	_, err := controller.client.GetAttributes(
		request.Request.Context(),
		flowID,
		sdk.ReadAttributeMapInstance(TranscriptChunks, CurrentTranscriptChunk, &chunk),
	)
	if chunk.Replies == nil {
		chunk.Replies = []TicketReply{}
	}
	httputil.Respond(request, chunk, err)
}

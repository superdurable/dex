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

package aiagent

import (
	_ "embed"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/dex"
)

//go:embed chat.html
var chatPage []byte

type startAgentRequest struct {
	FlowID string `json:"flowId"`
}

type sendUserMessageRequest struct {
	Text string `json:"text"`
}

type controller struct {
	client *dex.Client
	flow   *AIAgentFlow
}

// RegisterRoutes serves the chat page at /ai-agent and its JSON API under /ai-agent/agents.
func RegisterRoutes(router gin.IRouter, client *dex.Client, flow *AIAgentFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/ai-agent")
	group.GET("", controller.getChatPage)
	group.POST("/agents", controller.startAgent)
	group.GET("/agents/:flowId", controller.getAgentSnapshot)
	group.POST("/agents/:flowId/messages", controller.sendUserMessage)
	group.POST("/agents/:flowId/approvals", controller.decideToolApproval)
}

func (*controller) getChatPage(request *gin.Context) {
	request.Data(http.StatusOK, "text/html; charset=utf-8", chatPage)
}

func (controller *controller) startAgent(request *gin.Context) {
	var body startAgentRequest
	if request.Request.ContentLength > 0 {
		if err := request.ShouldBindJSON(&body); err != nil {
			request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if body.FlowID == "" {
		body.FlowID = "ai-agent-" + uuid.NewString()[:8]
	}
	runID, err := controller.client.StartFlow(request.Request.Context(), controller.flow, body.FlowID, nil, dex.StartFlowOptions{})
	respond(request, gin.H{"flowId": body.FlowID, "runId": runID}, err)
}

func (controller *controller) getAgentSnapshot(request *gin.Context) {
	var snapshot AgentSnapshot
	err := controller.client.InvokeRPC(request.Request.Context(), request.Param("flowId"), controller.flow.GetAgentSnapshot, nil, &snapshot)
	respond(request, snapshot, err)
}

func (controller *controller) sendUserMessage(request *gin.Context) {
	var body sendUserMessageRequest
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var isQueued bool
	err := controller.client.InvokeRPC(request.Request.Context(), request.Param("flowId"), controller.flow.SendUserMessage, body.Text, &isQueued)
	respond(request, gin.H{"isQueued": isQueued}, err)
}

func (controller *controller) decideToolApproval(request *gin.Context) {
	var decision ToolApprovalDecision
	if err := request.ShouldBindJSON(&decision); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var isAccepted bool
	err := controller.client.InvokeRPC(request.Request.Context(), request.Param("flowId"), controller.flow.DecideToolApproval, decision, &isAccepted)
	respond(request, gin.H{"isAccepted": isAccepted}, err)
}

func respond(request *gin.Context, value any, err error) {
	var notActive *dex.FlowNotActiveOrNotFoundError
	var alreadyStarted *dex.FlowAlreadyStartedError
	switch {
	case errors.As(err, &notActive):
		request.JSON(http.StatusNotFound, gin.H{"error": "no running agent has this ID"})
	case errors.As(err, &alreadyStarted):
		request.JSON(http.StatusConflict, gin.H{"error": "an agent with this ID already exists"})
	case err != nil:
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		request.JSON(http.StatusOK, value)
	}
}

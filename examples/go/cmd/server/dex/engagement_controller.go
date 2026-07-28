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
	"context"
	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"net/http"
	"strings"
)

func descEngagement(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := engagement.EngagementWorkflow{}
		var rpcOutput engagement.EngagementDescription
		err := client.InvokeRPC(context.Background(), wfId, "", wf.Describe, nil, &rpcOutput)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, rpcOutput)
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func optOutReminder(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := engagement.EngagementWorkflow{}
		err := client.SignalWorkflow(context.Background(), wf, wfId, "", engagement.SignalChannelOptOutReminder, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func declineEngagement(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := engagement.EngagementWorkflow{}
		err := client.InvokeRPC(context.Background(), wfId, "", wf.Decline, nil, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func acceptEngagement(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := engagement.EngagementWorkflow{}
		err := client.InvokeRPC(context.Background(), wfId, "", wf.Accept, nil, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func listEngagements(c *gin.Context) {
	query := c.Query("query")
	if query != "" {
		if strings.HasPrefix(query, "'") {
			query = strings.Trim(query, "'")
		}
		resp, err := client.SearchWorkflow(context.Background(), dexpb.WorkflowSearchRequest{
			Query: query,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, resp)
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

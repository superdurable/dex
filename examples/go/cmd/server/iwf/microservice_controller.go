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

package iwf

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/superdurable/iwf/examples/go/workflows/microservices"
	"net/http"
)

func startMicroserviceWorkflow(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := microservices.OrchestrationWorkflow{}
		runId, err := client.StartWorkflow(c.Request.Context(), wf, wfId, 3600, "test initial data", nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, fmt.Sprintf("workflowId: %v runId: %v", wfId, runId))
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func signalMicroserviceWorkflow(c *gin.Context) {
	wfId := c.Query("workflowId")
	if wfId != "" {
		wf := microservices.OrchestrationWorkflow{}
		err := client.SignalWorkflow(context.Background(), wf, wfId, "", microservices.SignalChannelReady, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, struct{}{})
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

func swapDataMicroserviceWorkflow(c *gin.Context) {
	wfId := c.Query("workflowId")
	newData := c.Query("data")
	if wfId != "" {
		wf := microservices.OrchestrationWorkflow{}
		var output string
		err := client.InvokeRPC(context.Background(), wfId, "", wf.Swap, newData, &output)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
		} else {
			c.JSON(http.StatusOK, output)
		}
		return
	}
	c.JSON(http.StatusBadRequest, "must provide workflowId via URL parameter")
}

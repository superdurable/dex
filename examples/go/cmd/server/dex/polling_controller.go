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
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows/polling"
	"net/http"
	"strconv"
)

func startPollingWorkflow(c *gin.Context) {
	wfId := c.Query("workflowId")
	pollingCompletionThreshold := c.Query("pollingCompletionThreshold")

	pollingCompletionThresholdInt, err := strconv.Atoi(pollingCompletionThreshold)
	if err != nil {
		c.JSON(http.StatusBadRequest, "must provide correct pollingCompletionThreshold via URL parameter")
		return
	}

	_, err = client.StartWorkflow(c.Request.Context(), polling.PollingWorkflow{}, wfId, 0, pollingCompletionThresholdInt, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, fmt.Sprintf("workflowId: %v is started", wfId))
	return
}

func signalPollingWorkflow(c *gin.Context) {
	wfId := c.Query("workflowId")
	channel := c.Query("channel")

	err := client.SignalWorkflow(c.Request.Context(), polling.PollingWorkflow{}, wfId, "", channel, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, fmt.Sprintf("workflowId: %v is signal", wfId))
	return
}

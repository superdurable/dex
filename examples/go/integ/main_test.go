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

package integ

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	client        = dex.NewClient(workflows.GetRegistry(), nil)
	workerService = dex.NewWorkerService(workflows.GetRegistry(), nil)
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.ReleaseMode)
	closeFn := startWorkflowWorker()
	// Give the worker a moment to bind before tests start workflows.
	time.Sleep(time.Second)
	code := m.Run()
	closeFn()
	os.Exit(code)
}

func startWorkflowWorker() func() {
	router := gin.New()
	router.POST(dex.WorkflowStateWaitUntilApi, handleWaitUntil)
	router.POST(dex.WorkflowStateExecuteApi, handleExecute)
	router.POST(dex.WorkflowWorkerRPCAPI, handleRPC)

	server := &http.Server{
		Addr:    ":" + dex.DefaultWorkerPort,
		Handler: router,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("worker listen: %v", err)
		}
	}()
	fmt.Println("examples/go integ worker listening on", dex.DefaultWorkerPort)
	return func() { _ = server.Close() }
}

func handleWaitUntil(c *gin.Context) {
	var req dexpb.WorkflowStateWaitUntilRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := workerService.HandleWorkflowStateWaitUntil(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func handleExecute(c *gin.Context) {
	var req dexpb.WorkflowStateExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := workerService.HandleWorkflowStateExecute(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func handleRPC(c *gin.Context) {
	var req dexpb.WorkflowWorkerRpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := workerService.HandleWorkflowWorkerRPC(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

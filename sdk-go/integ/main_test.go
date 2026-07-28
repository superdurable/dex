// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integ

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestMain(m *testing.M) {
	fmt.Println("start running integ test")
	closeFn := startWorkflowWorker()
	code := m.Run()
	closeFn()
	fmt.Println("finished running integ test with status code", code)
	os.Exit(code)
}

func apiV1WorkflowStateStart(c *gin.Context) {
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
func apiV1WorkflowStateDecide(c *gin.Context) {
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

func apiV1WorkflowWorkerRpc(c *gin.Context) {
	var req dexpb.WorkflowWorkerRpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := workerService.HandleWorkflowWorkerRPC(c.Request.Context(), req)
	if err != nil {
		c.JSON(501, dexpb.WorkerErrorResponse{
			Detail:    dexpb.PtrString(err.Error()),
			ErrorType: dexpb.PtrString("test-error-type"),
		})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func startWorkflowWorker() (closeFunc func()) {
	router := gin.Default()
	router.POST(dex.WorkflowStateWaitUntilApi, apiV1WorkflowStateStart)
	router.POST(dex.WorkflowStateExecuteApi, apiV1WorkflowStateDecide)
	router.POST(dex.WorkflowWorkerRPCAPI, apiV1WorkflowWorkerRpc)

	wfServer := &http.Server{
		Addr:    ":" + dex.DefaultWorkerPort,
		Handler: router,
	}
	go func() {
		if err := wfServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()
	return func() { wfServer.Close() }
}

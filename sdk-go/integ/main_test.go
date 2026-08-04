// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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

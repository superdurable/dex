// Copyright (c) 2021 Cadence workflow OSS organization
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
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/urfave/cli"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// BuildCLI is the main entry point for the dex server
func BuildCLI() *cli.App {
	app := cli.NewApp()
	app.Name = "dex golang samples"
	app.Usage = "dex golang samples"
	app.Version = "beta"

	app.Commands = []cli.Command{
		{
			Name:    "start",
			Aliases: []string{""},
			Usage:   "start dex golang samples",
			Action:  start,
		},
	}
	return app
}

func start(c *cli.Context) {
	fmt.Println("start running samples")
	closeFn := startWorkflowWorker()
	// TODO improve the waiting with process signal
	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
	closeFn()
}

var client = dex.NewClient(workflows.GetRegistry(), nil)
var workerService = dex.NewWorkerService(workflows.GetRegistry(), nil)

func startWorkflowWorker() (closeFunc func()) {
	router := gin.Default()
	router.POST(dex.WorkflowStateWaitUntilApi, apiV1WorkflowStateStart)
	router.POST(dex.WorkflowStateExecuteApi, apiV1WorkflowStateDecide)
	router.POST(dex.WorkflowWorkerRPCAPI, apiV1WorkflowWorkerRpc)

	engagementInput := engagement.EngagementInput{
		EmployerId:  "test-employer-id",
		JobSeekerId: "test-jobSeeker-id",
		Notes:       "test-notes",
	}

	customer := subscription.Customer{
		FirstName: "Quanzheng",
		LastName:  "Long",
		Id:        "qlong",
		Email:     "qlong.seattle@gmail.com",
		Subscription: subscription.Subscription{
			TrialPeriod:         time.Second * 20,
			BillingPeriod:       time.Second * 10,
			MaxBillingPeriods:   10,
			BillingPeriodCharge: 100,
		},
	}

	router.GET("/subscription/start", startWorklfow(&subscription.SubscriptionWorkflow{}, customer))
	router.GET("/subscription/cancel", cancelSubscription)
	router.GET("/subscription/updateChargeAmount", updateSubscriptionChargeAmount)
	router.GET("/subscription/describe", descSubscription)

	router.GET("/engagement/start", startWorklfow(&engagement.EngagementWorkflow{}, engagementInput))
	router.GET("/engagement/describe", descEngagement)
	router.GET("/engagement/optout", optOutReminder)
	router.GET("/engagement/decline", declineEngagement)
	router.GET("/engagement/accept", acceptEngagement)
	router.GET("/engagement/list", listEngagements)

	router.GET("/microservice/start", startMicroserviceWorkflow)
	router.GET("/microservice/swap", swapDataMicroserviceWorkflow)
	router.GET("/microservice/signal", signalMicroserviceWorkflow)

	router.GET("/moneytransfer/start", startMoneyTransferWorkflow)

	router.GET("/polling/start", startPollingWorkflow)
	router.GET("/polling/complete", signalPollingWorkflow)

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

func startWorklfow(wf dex.ObjectWorkflow, input interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		wfId := "TestSample" + strconv.Itoa(int(time.Now().Unix()))
		runId, err := client.StartWorkflow(c.Request.Context(), wf, wfId, 3600, input, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, fmt.Sprintf("workflowId: %v runId: %v", wfId, runId))
		return
	}
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
	return
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
	return
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
	return
}

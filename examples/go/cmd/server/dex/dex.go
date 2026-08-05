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
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/examples/go/workflows/microservices"
	"github.com/superdurable/dex/examples/go/workflows/moneytransfer"
	"github.com/superdurable/dex/examples/go/workflows/polling"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/blobcache"
)

type sampleServer struct {
	client       *sdk.Client
	worker       *sdk.Worker
	cache        *blobcache.Cache
	httpServer   *http.Server
	workerResult chan error
	httpResult   chan error
}

func Run(ctx context.Context) error {
	server, err := newSampleServer()
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

func newSampleServer() (*sampleServer, error) {
	registry, err := sdk.NewRegistry(workflows.Flows())
	if err != nil {
		return nil, fmt.Errorf("register example flows: %w", err)
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      environmentOr("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-go-examples-blobs")),
		MaxBytes: 1 << 30,
	})
	if err != nil {
		return nil, fmt.Errorf("create blob cache: %w", err)
	}
	workerOptions := sdk.WorkerOptions{
		BindAddress:        environmentOr("DEX_WORKER_BIND_ADDRESS", "127.0.0.1:8803"),
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
	}
	if target := os.Getenv("DEX_WORKER_TARGET"); target != "" {
		workerOptions.WorkerTarget.Address = target
	}
	worker, err := sdk.NewWorker(registry, cache, workerOptions)
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	client, err := sdk.NewClient(registry, cache, sdk.ClientOptions{
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
		WorkerTarget:       worker.WorkerTarget(),
	})
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return nil, errors.Join(err, worker.Stop(stopCtx), cache.Close())
	}
	server := &sampleServer{
		client:       client,
		worker:       worker,
		cache:        cache,
		workerResult: make(chan error, 1),
		httpResult:   make(chan error, 1),
	}
	server.httpServer = &http.Server{
		Addr:    environmentOr("DEX_EXAMPLES_HTTP_ADDRESS", "127.0.0.1:8080"),
		Handler: server.router(),
	}
	return server, nil
}

func environmentOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (server *sampleServer) router() http.Handler {
	router := gin.Default()
	router.GET("/subscription/start", server.startSubscription)
	router.GET("/subscription/cancel", server.cancelSubscription)
	router.GET("/subscription/updateChargeAmount", server.updateSubscriptionChargeAmount)
	router.GET("/subscription/describe", server.describeSubscription)
	router.GET("/engagement/start", server.startEngagement)
	router.GET("/engagement/describe", server.describeEngagement)
	router.GET("/engagement/optout", server.optOutReminder)
	router.GET("/engagement/decline", server.declineEngagement)
	router.GET("/engagement/accept", server.acceptEngagement)
	router.GET("/engagement/list", server.listEngagements)
	router.GET("/microservice/start", server.startMicroservice)
	router.GET("/microservice/swap", server.swapMicroserviceData)
	router.GET("/microservice/signal", server.signalMicroservice)
	router.GET("/moneytransfer/start", server.startMoneyTransfer)
	router.GET("/polling/start", server.startPolling)
	router.GET("/polling/complete", server.completePollingTask)
	return router
}

func (server *sampleServer) Run(ctx context.Context) error {
	go func() {
		server.workerResult <- server.worker.Start()
	}()
	go func() {
		err := server.httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		server.httpResult <- err
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-server.workerResult:
		runErr = err
	case err := <-server.httpResult:
		runErr = err
	}
	return errors.Join(runErr, server.close())
}

func (server *sampleServer) close() error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := server.httpServer.Shutdown(stopCtx)
	workerErr := server.worker.Stop(stopCtx)
	clientErr := server.client.Close()
	cacheErr := server.cache.Close()
	return errors.Join(httpErr, workerErr, clientErr, cacheErr)
}

func (server *sampleServer) startSubscription(request *gin.Context) {
	flowID := newFlowID("subscription")
	customer := subscription.Customer{
		FirstName: "Quanzheng",
		LastName:  "Long",
		ID:        "qlong",
		Email:     "qlong@example.com",
		Subscription: subscription.Subscription{
			TrialPeriod:         20 * time.Second,
			BillingPeriod:       10 * time.Second,
			MaxBillingPeriods:   10,
			BillingPeriodCharge: 100,
		},
	}
	server.startFlow(request, workflows.Subscription, flowID, customer)
}

func (server *sampleServer) cancelSubscription(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := server.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		subscription.CancelSubscription,
		struct{}{},
	)
	respond(request, struct{}{}, err)
}

func (server *sampleServer) updateSubscriptionChargeAmount(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	amount, err := strconv.Atoi(request.Query("newChargeAmount"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "newChargeAmount must be an integer"})
		return
	}
	err = server.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		subscription.UpdateChargeAmount,
		amount,
	)
	respond(request, struct{}{}, err)
}

func (server *sampleServer) describeSubscription(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output subscription.Subscription
	err := server.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Subscription.Describe,
		struct{}{},
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (server *sampleServer) startEngagement(request *gin.Context) {
	flowID := newFlowID("engagement")
	input := engagement.EngagementInput{
		EmployerID:  "test-employer-id",
		JobSeekerID: "test-job-seeker-id",
		Notes:       "test-notes",
	}
	server.startFlow(request, workflows.Engagement, flowID, input)
}

func (server *sampleServer) describeEngagement(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output engagement.EngagementDescription
	err := server.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Engagement.Describe,
		struct{}{},
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (server *sampleServer) optOutReminder(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := server.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		engagement.OptOutReminder,
		struct{}{},
	)
	respond(request, struct{}{}, err)
}

func (server *sampleServer) declineEngagement(request *gin.Context) {
	server.updateEngagement(request, workflows.Engagement.Decline)
}

func (server *sampleServer) acceptEngagement(request *gin.Context) {
	server.updateEngagement(request, workflows.Engagement.Accept)
}

func (server *sampleServer) updateEngagement(
	request *gin.Context,
	rpc sdk.RPC[string, engagement.Status],
) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output engagement.Status
	err := server.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		rpc,
		request.Query("notes"),
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (server *sampleServer) listEngagements(request *gin.Context) {
	query, found := requiredQuery(request, "query")
	if !found {
		return
	}
	page, err := server.client.SearchFlows(request.Request.Context(), query, 100, "")
	respond(request, page, err)
}

func (server *sampleServer) startMicroservice(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	server.startFlow(request, workflows.Microservices, flowID, "test initial data")
}

func (server *sampleServer) swapMicroserviceData(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var output string
	err := server.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Microservices.Swap,
		request.Query("data"),
		&output,
		sdk.InvokeOptions{},
	)
	respond(request, output, err)
}

func (server *sampleServer) signalMicroservice(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := server.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		microservices.Ready,
		struct{}{},
	)
	respond(request, struct{}{}, err)
}

func (server *sampleServer) startMoneyTransfer(request *gin.Context) {
	amount, err := strconv.Atoi(request.Query("amount"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "amount must be an integer"})
		return
	}
	flowID := newFlowID("money-transfer")
	input := moneytransfer.TransferRequest{
		FromAccount: request.Query("fromAccount"),
		ToAccount:   request.Query("toAccount"),
		Amount:      amount,
		Notes:       request.Query("notes"),
	}
	server.startFlow(request, workflows.MoneyTransfer, flowID, input)
}

func (server *sampleServer) startPolling(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	maximumPolls, err := strconv.Atoi(request.Query("pollingCompletionThreshold"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "pollingCompletionThreshold must be an integer"})
		return
	}
	server.startFlow(request, workflows.Polling, flowID, maximumPolls)
}

func (server *sampleServer) completePollingTask(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var channel sdk.ChannelDef
	switch request.Query("channel") {
	case polling.TaskACompleted.ChannelName():
		channel = polling.TaskACompleted
	case polling.TaskBCompleted.ChannelName():
		channel = polling.TaskBCompleted
	default:
		request.JSON(http.StatusBadRequest, gin.H{"error": "channel must identify task A or task B"})
		return
	}
	err := server.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		channel,
		struct{}{},
	)
	respond(request, struct{}{}, err)
}

func (server *sampleServer) startFlow(
	request *gin.Context,
	flow sdk.Flow,
	flowID string,
	input any,
) {
	runID, err := server.client.StartFlow(
		request.Request.Context(),
		flow,
		flowID,
		input,
		sdk.StartFlowOptions{},
	)
	respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func requiredQuery(request *gin.Context, name string) (string, bool) {
	value := request.Query(name)
	if value == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": name + " is required"})
		return "", false
	}
	return value, true
}

func respond(request *gin.Context, value any, err error) {
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.JSON(http.StatusOK, value)
}

func newFlowID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

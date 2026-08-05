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
	newSubscriptionController(server.client).registerRoutes(router)
	newEngagementController(server.client).registerRoutes(router)
	newMicroserviceController(server.client).registerRoutes(router)
	newMoneyTransferController(server.client).registerRoutes(router)
	newPollingController(server.client).registerRoutes(router)
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

func startFlow(
	request *gin.Context,
	client *sdk.Client,
	flow sdk.Flow,
	flowID string,
	input any,
) {
	runID, err := client.StartFlow(
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

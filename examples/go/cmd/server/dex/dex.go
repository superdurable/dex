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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/datasetdeal"
	"github.com/superdurable/dex/examples/go/workflows/service"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

const defaultDatasetDealPostgresURL = "postgres://dataset_deal:dataset_deal@127.0.0.1:15432/dataset_deal?sslmode=disable"

type sampleServer struct {
	client         *sdk.Client
	worker         *sdk.Worker
	cache          *blobcache.Cache
	database       *pgxpool.Pool
	dealFlow       *datasetdeal.DealFlow
	dealRepository datasetdeal.Repository
	httpServer     *http.Server
	workerAddress  string
	workerResult   chan error
	httpResult     chan error
}

func Run(ctx context.Context) error {
	server, err := newSampleServer(ctx)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

func newSampleServer(ctx context.Context) (*sampleServer, error) {
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      environmentOr("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-go-examples-blobs")),
		MaxBytes: 1 << 30,
	})
	if err != nil {
		return nil, fmt.Errorf("create blob cache: %w", err)
	}
	database, dealRepository, err := newDatasetDealRepository(ctx)
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	dealFlow := datasetdeal.NewDealFlow(dealRepository, cache.Logger())
	var client *sdk.Client
	flows := append(
		workflows.New(service.NewMyService(), func() *sdk.Client { return client }),
		dealFlow,
	)
	registry, err := sdk.NewRegistry(flows)
	if err != nil {
		database.Close()
		return nil, errors.Join(fmt.Errorf("register example flows: %w", err), cache.Close())
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
		database.Close()
		return nil, errors.Join(err, cache.Close())
	}
	client, err = sdk.NewClient(registry, cache, sdk.ClientOptions{
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
		WorkerTarget:       worker.WorkerTarget(),
	})
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		database.Close()
		return nil, errors.Join(err, worker.Stop(stopCtx), cache.Close())
	}
	server := &sampleServer{
		client:         client,
		worker:         worker,
		cache:          cache,
		database:       database,
		dealFlow:       dealFlow,
		dealRepository: dealRepository,
		workerAddress:  workerOptions.BindAddress,
		workerResult:   make(chan error, 1),
		httpResult:     make(chan error, 1),
	}
	server.httpServer = &http.Server{
		Addr:    environmentOr("DEX_EXAMPLES_HTTP_ADDRESS", "127.0.0.1:8080"),
		Handler: server.router(),
	}
	return server, nil
}

func newDatasetDealRepository(
	ctx context.Context,
) (*pgxpool.Pool, *datasetdeal.PostgresRepository, error) {
	database, err := pgxpool.New(
		ctx,
		environmentOr("DATASET_DEAL_POSTGRES_URL", defaultDatasetDealPostgresURL),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("configure Dataset Deal PostgreSQL: %w", err)
	}
	if err := database.Ping(ctx); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("connect to Dataset Deal PostgreSQL: %w", err)
	}
	repository := datasetdeal.NewPostgresRepository(database)
	if err := repository.EnsureSchema(ctx); err != nil {
		database.Close()
		return nil, nil, err
	}
	return database, repository, nil
}

func environmentOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (server *sampleServer) router() http.Handler {
	return NewRouter(server.client, server.dealFlow, server.dealRepository)
}

// NewRouter builds the Go examples HTTP API and Dataset Deal UI.
func NewRouter(
	client *sdk.Client,
	dealFlow *datasetdeal.DealFlow,
	dealRepository datasetdeal.Repository,
) http.Handler {
	router := gin.Default()
	newSubscriptionController(client).registerRoutes(router)
	newEngagementController(client).registerRoutes(router)
	newMicroserviceController(client).registerRoutes(router)
	newMoneyTransferController(client).registerRoutes(router)
	newPollingController(client).registerRoutes(router)
	newSignupController(client).registerRoutes(router)
	newJobPostController(client).registerRoutes(router)
	newShortlistController(client).registerRoutes(router)
	newDesignPatternController(client).registerRoutes(router)
	newDatasetDealController(client, dealFlow, dealRepository).registerRoutes(router)
	return router
}

func (server *sampleServer) Run(ctx context.Context) error {
	go func() {
		server.workerResult <- server.worker.Start()
	}()
	if err := server.waitForWorker(ctx); err != nil {
		return errors.Join(err, server.close())
	}
	startCronSchedule(server.client)
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

func (server *sampleServer) waitForWorker(ctx context.Context) error {
	address, err := localWorkerAddress(server.workerAddress)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			if closeErr := connection.Close(); closeErr != nil {
				return fmt.Errorf("close Worker readiness connection: %w", closeErr)
			}
			return nil
		}
		select {
		case workerErr := <-server.workerResult:
			if workerErr == nil {
				return errors.New("Worker stopped before becoming ready")
			}
			return workerErr
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func localWorkerAddress(bindAddress string) (string, error) {
	host, port, err := net.SplitHostPort(bindAddress)
	if err != nil {
		return "", fmt.Errorf("parse Worker bind address: %w", err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port), nil
}

func (server *sampleServer) close() error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := server.httpServer.Shutdown(stopCtx)
	workerErr := server.worker.Stop(stopCtx)
	clientErr := server.client.Close()
	cacheErr := server.cache.Close()
	server.database.Close()
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

func startCronSchedule(client *sdk.Client) {
	timeout := time.Hour
	_, err := client.StartFlow(
		context.Background(),
		workflows.CronSchedule,
		"cron-schedule-sample",
		nil,
		sdk.StartFlowOptions{
			Timeout:      &timeout,
			CronSchedule: "*/1 * * * *",
		},
	)
	if err != nil {
		var duplicate *sdk.FlowAlreadyStartedError
		if errors.As(err, &duplicate) {
			return
		}
		// Temporal Schedule.Create returns no run ID; SDK surfaces that after register.
		if strings.Contains(err.Error(), "no run ID") {
			return
		}
		panic(fmt.Errorf("start cron schedule sample: %w", err))
	}
}

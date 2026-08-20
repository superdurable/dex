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
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	draininternal "github.com/superdurable/dex/examples/go/patterns/drain-channels/internal-drain"
	drainsignal "github.com/superdurable/dex/examples/go/patterns/drain-channels/signal"
	"github.com/superdurable/dex/examples/go/patterns/entity-store"
	"github.com/superdurable/dex/examples/go/patterns/interruptible"
	"github.com/superdurable/dex/examples/go/patterns/intervention"
	"github.com/superdurable/dex/examples/go/patterns/parallel"
	"github.com/superdurable/dex/examples/go/patterns/parent-child"
	patternspolling "github.com/superdurable/dex/examples/go/patterns/polling"
	"github.com/superdurable/dex/examples/go/patterns/recovery"
	"github.com/superdurable/dex/examples/go/patterns/reminders"
	"github.com/superdurable/dex/examples/go/patterns/resettable-timer"
	"github.com/superdurable/dex/examples/go/patterns/scalable-parallel"
	"github.com/superdurable/dex/examples/go/patterns/timeout"
	"github.com/superdurable/dex/examples/go/patterns/wait-for-state-completion"
	primitiveattribute "github.com/superdurable/dex/examples/go/primitives/attribute"
	primitivechannel "github.com/superdurable/dex/examples/go/primitives/channel"
	primitiveclientapis "github.com/superdurable/dex/examples/go/primitives/client-apis"
	primitivecustomretry "github.com/superdurable/dex/examples/go/primitives/custom-retry"
	primitivedurability "github.com/superdurable/dex/examples/go/primitives/durability"
	primitiveheartbeat "github.com/superdurable/dex/examples/go/primitives/heartbeat"
	primitiveoptionsoverride "github.com/superdurable/dex/examples/go/primitives/options-override"
	primitiverpc "github.com/superdurable/dex/examples/go/primitives/rpc"
	primitivestep "github.com/superdurable/dex/examples/go/primitives/step"
	primitivestepdecision "github.com/superdurable/dex/examples/go/primitives/step-decision"
	primitivesubflow "github.com/superdurable/dex/examples/go/primitives/subflow"
	primitivetimer "github.com/superdurable/dex/examples/go/primitives/timer"
	primitivewaittypes "github.com/superdurable/dex/examples/go/primitives/wait-types"
	"github.com/superdurable/dex/examples/go/products/engagement"
	"github.com/superdurable/dex/examples/go/products/job-post"
	"github.com/superdurable/dex/examples/go/products/microservices"
	"github.com/superdurable/dex/examples/go/products/money-transfer"
	"github.com/superdurable/dex/examples/go/products/order-processing"
	productspolling "github.com/superdurable/dex/examples/go/products/polling"
	"github.com/superdurable/dex/examples/go/products/shortlist-candidates"
	"github.com/superdurable/dex/examples/go/products/signup"
	"github.com/superdurable/dex/examples/go/products/subscription"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/examples/go/shared/service"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type sampleServer struct {
	client        *sdk.Client
	worker        *sdk.Worker
	cache         *blobcache.Cache
	httpServer    *http.Server
	workerAddress string
	workerResult  chan error
	httpResult    chan error
}

func Run(ctx context.Context) error {
	server, err := newSampleServer()
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

func newSampleServer() (*sampleServer, error) {
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      environmentOr("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-go-examples-blobs")),
		MaxBytes: 1 << 30,
	})
	if err != nil {
		return nil, fmt.Errorf("create blob cache: %w", err)
	}
	var client *sdk.Client
	flows := registry.New(service.NewMyService(), func() *sdk.Client { return client })
	flowRegistry, err := sdk.NewRegistry(flows)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("register example flows: %w", err), cache.Close())
	}
	workerOptions := sdk.WorkerOptions{
		BindAddress:        environmentOr("DEX_WORKER_BIND_ADDRESS", "127.0.0.1:8803"),
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
	}
	if target := os.Getenv("DEX_WORKER_TARGET"); target != "" {
		workerOptions.WorkerTarget.Address = target
	}
	worker, err := sdk.NewWorker(flowRegistry, cache, workerOptions)
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	client, err = sdk.NewClient(flowRegistry, cache, sdk.ClientOptions{
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
		WorkerTarget:       worker.WorkerTarget(),
	})
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return nil, errors.Join(err, worker.Stop(stopCtx), cache.Close())
	}
	server := &sampleServer{
		client:        client,
		worker:        worker,
		cache:         cache,
		workerAddress: workerOptions.BindAddress,
		workerResult:  make(chan error, 1),
		httpResult:    make(chan error, 1),
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
	return NewRouter(server.client)
}

// NewRouter builds the Go examples HTTP API.
func NewRouter(client *sdk.Client) http.Handler {
	router := gin.Default()
	router.Use(httputil.AllowCORS())
	subscription.RegisterRoutes(router, client, registry.Subscription)
	engagement.RegisterRoutes(router, client, registry.Engagement)
	microservices.RegisterRoutes(router, client, registry.Microservices)
	moneytransfer.RegisterRoutes(router, client, registry.MoneyTransfer)
	orderprocessing.RegisterRoutes(router, client, registry.OrderProcessing)
	productspolling.RegisterRoutes(router, client, registry.Polling)
	signup.RegisterRoutes(router, client, registry.Signup)
	jobpost.RegisterRoutes(router, client, registry.JobPost)
	shortlistcandidates.RegisterRoutes(
		router,
		client,
		registry.EmployerOptIn,
		registry.Shortlist,
	)
	patternspolling.RegisterRoutes(router, client, registry.SimplePolling, registry.BackoffPolling)
	interruptible.RegisterRoutes(router, client, registry.InterruptibleExecution)
	reminders.RegisterRoutes(router, client, registry.Reminder)
	entitystore.RegisterRoutes(router, client, registry.UserProfile)
	intervention.RegisterRoutes(router, client, registry.ManualIntervention)
	resettabletimer.RegisterRoutes(router, client, registry.ResettableTimer)
	parallel.RegisterRoutes(router, client, registry.SimpleParallel, registry.ParallelWithAwait)
	recovery.RegisterRoutes(router, client, registry.FailureRecovery)
	scalableparallel.RegisterRoutes(router, client, registry.RequestReceiver)
	parentchild.RegisterRoutes(router, client, registry.ParentChild)
	draininternal.RegisterRoutes(router, client, registry.DrainInternal)
	drainsignal.RegisterRoutes(router, client, registry.DrainSignal)
	waitforstatecompletion.RegisterRoutes(router, client, registry.WaitForStateCompletion)
	timeout.RegisterRoutes(router, client, registry.GracefulTimeout)
	primitivestep.RegisterRoutes(router, client, registry.Step, registry.StepRetry)
	primitivecustomretry.RegisterRoutes(router, client, registry.CustomRetry)
	primitivedurability.RegisterRoutes(router, client, registry.Durability)
	primitiveheartbeat.RegisterRoutes(router, client, registry.Heartbeat)
	primitiveoptionsoverride.RegisterRoutes(router, client, registry.OptionsOverride)
	primitivestepdecision.RegisterRoutes(router, client, registry.StepDecision)
	primitivewaittypes.RegisterRoutes(router, client, registry.WaitTypes)
	primitiveattribute.RegisterRoutes(router, client, registry.Attribute)
	primitivechannel.RegisterRoutes(router, client, registry.Channel)
	primitivetimer.RegisterRoutes(router, client, registry.Timer)
	primitiverpc.RegisterRoutes(router, client, registry.Rpc)
	primitivesubflow.RegisterRoutes(router, client, registry.SubFlowParent)
	primitiveclientapis.RegisterRoutes(router, client, registry.ClientApis)
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
	return errors.Join(httpErr, workerErr, clientErr, cacheErr)
}

func startCronSchedule(client *sdk.Client) {
	timeout := time.Hour
	_, err := client.StartFlow(
		context.Background(),
		registry.CronSchedule,
		"cron-schedule-sample",
		nil,
		sdk.StartFlowOptions{
			Timeout:      &timeout,
			CronSchedule: "0 * * * *",
		},
	)
	if err != nil {
		var duplicate *sdk.FlowAlreadyStartedError
		if errors.As(err, &duplicate) {
			return
		}
		if strings.Contains(err.Error(), "no run ID") {
			return
		}
		panic(fmt.Errorf("start cron schedule sample: %w", err))
	}
}

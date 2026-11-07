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

// Command ai-agent runs the AI Agent example: its Dex Worker, its Client and the chat page in one process.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	aiagent "github.com/superdurable/dex/examples/go/products/ai-agent"
	"github.com/superdurable/dex/sdk-go/dex"
)

const defaultHTTPAddress = "127.0.0.1:8090"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runAIAgentApp(ctx); err != nil {
		log.Fatal(err)
	}
}

func runAIAgentApp(ctx context.Context) error {
	toolbox, err := aiagent.NewToolboxFromEnvironment()
	if err != nil {
		return err
	}
	slackSettings, err := aiagent.LoadSlackSettings(ctx)
	if err != nil {
		return err
	}
	model := aiagent.NewModelClientFromEnvironment()
	flow := aiagent.NewAIAgentFlow(model, toolbox, slackSettings)
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return fmt.Errorf("register the AI Agent Flow: %w", err)
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      environmentOr("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-ai-agent-blobs")),
		MaxBytes: 256 << 20,
	})
	if err != nil {
		return fmt.Errorf("create the blob cache: %w", err)
	}
	connection, err := readDexConnection()
	if err != nil {
		return errors.Join(err, cache.Close())
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: connection.address,
		FlowServiceTLS:     connection.tls,
		Credential:         connection.credential,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: 16},
	})
	if err != nil {
		return errors.Join(err, cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: connection.address,
		FlowServiceTLS:     connection.tls,
		Credential:         connection.credential,
	})
	if err != nil {
		return errors.Join(err, stopWorker(worker), cache.Close())
	}
	closeAll := func() error { return errors.Join(stopWorker(worker), client.Close(), cache.Close()) }

	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	registrationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(registrationCtx); err != nil {
		return errors.Join(fmt.Errorf("register the Worker with Dex: %w", err), closeAll())
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/", func(request *gin.Context) { request.Redirect(http.StatusFound, "/ai-agent") })
	aiagent.RegisterRoutes(router, client, flow)
	httpServer := &http.Server{Handler: router}
	listener, err := net.Listen("tcp", environmentOr("DEX_AI_AGENT_HTTP_ADDRESS", defaultHTTPAddress))
	if err != nil {
		return errors.Join(fmt.Errorf("listen for HTTP: %w", err), closeAll())
	}
	httpResult := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		httpResult <- err
	}()
	log.Printf("Dex AI Agent ready at http://%s/ai-agent (model %s, Slack %s, notes file %s)",
		listener.Addr(), model.Name(), describeSlack(slackSettings), toolbox.NotesFile)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-workerResult:
	case runErr = <-httpResult:
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return errors.Join(runErr, httpServer.Shutdown(shutdownCtx), closeAll())
}

func describeSlack(slackSettings aiagent.SlackSettings) string {
	if !slackSettings.IsConfigured() {
		return "not set up: " + slackSettings.NotSetUpReason
	}
	return "channel " + slackSettings.Channel.Value.ChannelID
}

// dexConnection is the engine the app connects to: a local dexcli dev, or a Dex Cloud engine.
type dexConnection struct {
	address    string
	tls        *dex.FlowServiceTLSOptions
	credential dex.Credential
}

// readDexConnection reads the variables that a Dex Cloud engine's Connect panel exports.
func readDexConnection() (dexConnection, error) {
	connection := dexConnection{address: environmentOr("DEX_SERVER", os.Getenv("DEX_FLOW_SERVICE_ADDRESS"))}
	if os.Getenv("DEX_TLS") == "true" {
		connection.tls = &dex.FlowServiceTLSOptions{}
	}
	if apiKey := os.Getenv("DEX_API_KEY"); apiKey != "" {
		credential, err := dex.APIKeyCredential(os.Getenv("DEX_TOKEN_URL"), os.Getenv("DEX_SERVER_ID"), apiKey)
		if err != nil {
			return dexConnection{}, fmt.Errorf("create the Dex API key credential: %w", err)
		}
		connection.credential = credential
	}
	return connection, nil
}

func stopWorker(worker *dex.Worker) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return worker.Stop(stopCtx)
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

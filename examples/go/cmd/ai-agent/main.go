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
	"strconv"
	"strings"
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
	connection, err := readFlowServiceConnection()
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

// flowServiceConnection holds how the Worker and the Client reach the Dex server.
type flowServiceConnection struct {
	address    string
	tls        *dex.FlowServiceTLSOptions
	credential dex.Credential
}

// readFlowServiceConnection reads the Dex server settings, using the environment variable names of dexcli.
// It returns a plaintext connection without a credential when no TLS or API key setting is set.
func readFlowServiceConnection() (flowServiceConnection, error) {
	if readTrimmedEnvironment("DEX_CREDENTIAL") != "" || readTrimmedEnvironment("DEX_CREDENTIAL_FILE") != "" {
		return flowServiceConnection{}, errors.New("this example supports only API key credentials; unset DEX_CREDENTIAL and DEX_CREDENTIAL_FILE and set DEX_API_KEY")
	}
	tlsOptions, err := readFlowServiceTLSOptions()
	if err != nil {
		return flowServiceConnection{}, err
	}
	credential, err := readAPIKeyCredential()
	if err != nil {
		return flowServiceConnection{}, err
	}
	return flowServiceConnection{
		address:    readTrimmedEnvironment("DEX_FLOW_SERVICE_ADDRESS"),
		tls:        tlsOptions,
		credential: credential,
	}, nil
}

// readFlowServiceTLSOptions returns nil for plaintext gRPC, or the TLS options when DEX_TLS is true.
func readFlowServiceTLSOptions() (*dex.FlowServiceTLSOptions, error) {
	caFile := readTrimmedEnvironment("DEX_TLS_CA_FILE")
	serverName := readTrimmedEnvironment("DEX_TLS_SERVER_NAME")
	isTLSEnabled := false
	if value := readTrimmedEnvironment("DEX_TLS"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("DEX_TLS must be a boolean such as true or false, not %q", value)
		}
		isTLSEnabled = parsed
	}
	if !isTLSEnabled {
		if caFile != "" || serverName != "" {
			return nil, errors.New("DEX_TLS_CA_FILE and DEX_TLS_SERVER_NAME require DEX_TLS=true")
		}
		return nil, nil
	}
	tlsOptions := &dex.FlowServiceTLSOptions{ServerName: serverName}
	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read the DEX_TLS_CA_FILE certificates: %w", err)
		}
		tlsOptions.AdditionalRootCAsPEM = caPEM
	}
	return tlsOptions, nil
}

// readAPIKeyCredential returns nil when DEX_API_KEY is unset, or a credential that exchanges the key for access tokens.
func readAPIKeyCredential() (dex.Credential, error) {
	apiKey := readTrimmedEnvironment("DEX_API_KEY")
	tokenURL := readTrimmedEnvironment("DEX_TOKEN_URL")
	serverID := readTrimmedEnvironment("DEX_SERVER_ID")
	if apiKey == "" {
		if tokenURL != "" || serverID != "" {
			return nil, errors.New("DEX_TOKEN_URL and DEX_SERVER_ID require DEX_API_KEY")
		}
		return nil, nil
	}
	if tokenURL == "" {
		return nil, errors.New("an API key requires DEX_TOKEN_URL")
	}
	if serverID == "" {
		return nil, errors.New("an API key requires DEX_SERVER_ID")
	}
	credential, err := dex.APIKeyCredential(tokenURL, serverID, apiKey)
	if err != nil {
		return nil, fmt.Errorf("create the Dex API key credential from DEX_API_KEY, DEX_TOKEN_URL, and DEX_SERVER_ID: %w", err)
	}
	return credential, nil
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

// readTrimmedEnvironment returns the variable without surrounding whitespace, so a blank value counts as unset.
func readTrimmedEnvironment(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

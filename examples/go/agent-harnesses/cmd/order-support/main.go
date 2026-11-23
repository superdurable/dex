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

// Command order-support serves the order support agent on Eino over HTTP, with one Worker for its sessions.
//
// Start dexcli dev first. The routes are on 127.0.0.1:8082
// (DEX_AGENT_HARNESS_HTTP_ADDRESS); see the README for each route.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/examples/go/agent-harnesses/harnessconfig"
	"github.com/superdurable/dex/examples/go/agent-harnesses/orderdesk"
	"github.com/superdurable/dex/examples/go/agent-harnesses/ordersupport"
)

const shutdownTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (err error) {
	config, err := harnessconfig.ConfigFromEnvironment()
	if err != nil {
		return err
	}
	chatModel, err := harnessconfig.NewEinoChatModel(ctx, config.EinoModel)
	if err != nil {
		return err
	}
	// One Slack connection for the agent; without it the agent offers no Slack tool.
	slackConnection, err := ordersupport.OpenSlackProjectConnection(ctx)
	if err != nil {
		return err
	}
	slackToolSources, err := ordersupport.NewSlackToolSources(slackConnection)
	if err != nil {
		return err
	}
	orderDesk := orderdesk.NewOrderDesk(config.OrderDeskToolDelay)
	sessionFlow, err := ordersupport.NewEinoOrderSupportFlow(orderDesk, chatModel, slackToolSources)
	if err != nil {
		return err
	}
	app, err := ordersupport.StartOrderSupportApp(ctx, sessionFlow, config.FlowServiceAddress, config.BlobCacheDir)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	ordersupport.NewSessionRoutes("eino", app.Sessions, config.SessionKey).Register(mux)
	server := &http.Server{Addr: config.HTTPAddress, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.ListenAndServe() }()
	slog.Info("serving the order support agent", "address", config.HTTPAddress)

	select {
	case <-ctx.Done():
	case err = <-serverResult:
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// Stop taking requests first, then let the Worker's running Steps finish.
	shutdownErr := server.Shutdown(shutdownContext)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, shutdownErr, app.Stop(shutdownContext))
}

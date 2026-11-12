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

// Command service serves POST /orders and runs the Worker in the same process.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	quickstart "github.com/superdurable/dex/examples/go/quick-start"
	"github.com/superdurable/dex/sdk-go/dex"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	shop, err := quickstart.NewShopAPIFromEnvironment()
	if err != nil {
		return err
	}
	orderFlow := quickstart.NewOrderFlow(shop)
	registry, err := dex.NewRegistry([]dex.Flow{orderFlow})
	if err != nil {
		return err
	}
	workerCache, err := quickstart.OpenCache(".dex-blob-cache/worker")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, workerCache.Close()) }()
	clientCache, err := quickstart.OpenCache(".dex-blob-cache/client")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, clientCache.Close()) }()
	options := dex.WorkerOptions{FlowServiceAddress: quickstart.ServerAddress()}
	worker, err := dex.NewWorker(registry, workerCache, options)
	if err != nil {
		return err
	}
	client, err := dex.NewClient(registry, clientCache, dex.ClientOptions{FlowServiceAddress: quickstart.ServerAddress()})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()

	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(startupCtx); err != nil {
		return errors.Join(err, worker.Stop(context.Background()), <-workerResult)
	}

	server := &http.Server{
		Addr:              "127.0.0.1:" + quickstart.HTTPPort(),
		Handler:           quickstart.NewOrderHandler(client, orderFlow),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.ListenAndServe() }()
	fmt.Printf("Serving POST /orders on port %s (process %d).\n", quickstart.HTTPPort(), os.Getpid())

	select {
	case err = <-serverResult:
	case err = <-workerResult:
	case <-ctx.Done():
		fmt.Println("Stopping the service.")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
		err = errors.Join(err, shutdownErr)
	}
	return errors.Join(err, worker.Stop(context.Background()))
}

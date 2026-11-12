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

// Command worker runs the Worker that executes OrderFlow Steps. Stop it with Ctrl+C.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	registry, err := dex.NewRegistry([]dex.Flow{quickstart.NewOrderFlow(shop)})
	if err != nil {
		return err
	}
	cache, err := quickstart.OpenCache(".dex-blob-cache/worker")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cache.Close()) }()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: quickstart.ServerAddress(),
	})
	if err != nil {
		return err
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(startupCtx); err != nil {
		return errors.Join(err, worker.Stop(context.Background()), <-workerResult)
	}
	fmt.Printf("Worker is ready (process %d). Press Ctrl+C to stop it.\n", os.Getpid())

	select {
	case err := <-workerResult:
		return err
	case <-ctx.Done():
		fmt.Println("Stopping the Worker.")
		return errors.Join(worker.Stop(context.Background()), <-workerResult)
	}
}

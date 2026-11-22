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

package ordersupport

import (
	"context"
	"errors"
	"os"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	pullRegistrationTimeout = 30 * time.Second
	// Tool calls run as parallel Steps, so the Worker needs a slot for each one.
	maxConcurrentTasks = 16
)

// OrderSupportApp is one Registry, Worker, and Client for the order support agent's sessions.
type OrderSupportApp struct {
	// Sessions starts, drives, and reads the agent's sessions.
	Sessions *dexagents.AgentSessionClient

	cache        *blobcache.Cache
	worker       *dex.Worker
	workerResult chan error
	client       *dex.Client
}

// StartOrderSupportApp starts a Worker for sessionFlow and waits until Dex has registered it.
//
// It returns an error when the blob cache, the Worker, or the Client cannot
// start, or when the Worker does not register within 30 seconds.
func StartOrderSupportApp(
	ctx context.Context, sessionFlow *dexagents.AgentSessionFlow, flowServiceAddress string, blobCacheDir string,
) (*OrderSupportApp, error) {
	if err := os.MkdirAll(blobCacheDir, 0o755); err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: blobCacheDir, MaxBytes: 1 << 30})
	if err != nil {
		return nil, err
	}
	// Build the same session Flow in every process that starts sessions or calls their RPCs.
	registry, err := dex.NewRegistry([]dex.Flow{sessionFlow})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: maxConcurrentTasks},
	})
	if err != nil {
		return nil, errors.Join(err, client.Close(), cache.Close())
	}
	app := &OrderSupportApp{
		Sessions: dexagents.NewAgentSessionClient(client, sessionFlow),
		cache:    cache, worker: worker, workerResult: make(chan error, 1), client: client,
	}
	go func() { app.workerResult <- worker.Start() }()
	registrationContext, cancel := context.WithTimeout(ctx, pullRegistrationTimeout)
	defer cancel()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return nil, errors.Join(err, app.Stop(context.Background()))
	}
	return app, nil
}

// Stop lets running Steps finish until ctx ends, then closes the Worker, the Client, and the blob cache.
//
// Stop it after the HTTP server stops taking requests. It returns every error the shutdown met.
func (app *OrderSupportApp) Stop(ctx context.Context) error {
	stopErr := app.worker.Stop(ctx)
	var runErr error
	select {
	case runErr = <-app.workerResult:
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	return errors.Join(stopErr, runErr, app.client.Close(), app.cache.Close())
}

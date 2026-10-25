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

//go:build sdk_go_tls

package pullworkers

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// StartPullGreetingWithTLS connects a pull Worker and Client through the same TLS gateway.
func StartPullGreetingWithTLS(cache *blobcache.Cache, serverAddress string, caPath string) (*GreetingFlow, *dex.Worker, *dex.Client, <-chan error, error) {
	rootPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tlsOptions := &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: rootPEM}
	flow := &GreetingFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: serverAddress,
		FlowServiceTLS:     tlsOptions,
		Pull:               &dex.PullWorkerOptions{},
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: serverAddress,
		FlowServiceTLS:     tlsOptions,
		WorkerTarget:       worker.WorkerTarget(),
	})
	if err != nil {
		return nil, nil, nil, nil, errors.Join(err, worker.Stop(context.Background()))
	}
	workerResult := make(chan error, 1)
	go reportWorkerResult(worker, workerResult)
	startupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(startupContext); err != nil {
		return nil, nil, nil, nil, errors.Join(err, StopPullGreeting(context.Background(), worker, client, workerResult))
	}
	return flow, worker, client, workerResult, nil
}

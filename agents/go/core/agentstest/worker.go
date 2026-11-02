// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentstest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

// WorkerReadyLine is the line ServeAgentWorker prints once every pull queue registered.
const WorkerReadyLine = "dex-agents test Worker ready"

// TestBlobCacheBytes is the BlobCache size of test Workers and Clients: 64 MiB.
const TestBlobCacheBytes int64 = 64 * 1024 * 1024

const pullRegistrationTimeout = 30 * time.Second

// OpenTestBlobCache opens a BlobCache of TestBlobCacheBytes in a new directory.
//
// It returns an error when the directory cannot be created.
func OpenTestBlobCache(directory string) (*blobcache.Cache, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	return blobcache.New(&blobcache.Config{Dir: directory, MaxBytes: TestBlobCacheBytes})
}

// ServeAgentWorker serves flows with a Worker until ctx ends.
//
// It prints WorkerReadyLine to stdout after every pull queue registered, which
// a test that started this process as a KillableProcess waits for. It returns
// an error when the Registry, BlobCache, or Worker cannot start, and the
// Worker's stop error after ctx ends.
func ServeAgentWorker(ctx context.Context, flows []dex.Flow, serverAddress string, blobCacheDir string) error {
	registry, err := dex.NewRegistry(flows)
	if err != nil {
		return err
	}
	cache, err := OpenTestBlobCache(blobCacheDir)
	if err != nil {
		return err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: serverAddress})
	if err != nil {
		return err
	}
	served := make(chan error, 1)
	go func() { served <- worker.Start() }()
	registrationContext, cancel := context.WithTimeout(ctx, pullRegistrationTimeout)
	defer cancel()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return errors.Join(err, worker.Stop(context.Background()), <-served)
	}
	fmt.Println(WorkerReadyLine)
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	return errors.Join(worker.Stop(stopContext), <-served)
}

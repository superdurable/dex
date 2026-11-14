// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package demoworker runs the Worker and Client that each local demo program needs.
package demoworker

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	defaultFlowServiceAddress = "127.0.0.1:8811"
	startupTimeout            = 30 * time.Second
	blobCacheBytes            = 1 << 28
)

// FlowServiceAddress returns DEX_FLOW_SERVICE_ADDRESS, or 127.0.0.1:8811 when it is unset.
func FlowServiceAddress() string {
	if address := os.Getenv("DEX_FLOW_SERVICE_ADDRESS"); address != "" {
		return address
	}
	return defaultFlowServiceAddress
}

// Serve runs a Worker for registry until ctx ends or the Worker fails.
//
// After the Worker registered its pull queues, Serve calls startDemo once with
// a Client. blobCacheDir holds the Worker and Client BlobCaches; an empty value
// uses a temporary directory that Serve removes on return. Serve returns the
// first startup or startDemo error joined with every shutdown error, or only the
// shutdown errors after ctx ends.
func Serve(
	ctx context.Context,
	registry *dex.Registry,
	blobCacheDir string,
	startDemo func(context.Context, *dex.Client) error,
) (err error) {
	if blobCacheDir == "" {
		temporaryDir, err := os.MkdirTemp("", "dex-local-demo-blobs-")
		if err != nil {
			return err
		}
		defer removeDirectory(temporaryDir)
		blobCacheDir = temporaryDir
	}
	workerCache, err := blobcache.New(&blobcache.Config{Dir: filepath.Join(blobCacheDir, "worker"), MaxBytes: blobCacheBytes})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, workerCache.Close()) }()
	clientCache, err := blobcache.New(&blobcache.Config{Dir: filepath.Join(blobCacheDir, "client"), MaxBytes: blobCacheBytes})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, clientCache.Close()) }()

	address := FlowServiceAddress()
	worker, err := dex.NewWorker(registry, workerCache, dex.WorkerOptions{FlowServiceAddress: address})
	if err != nil {
		return err
	}
	client, err := dex.NewClient(registry, clientCache, dex.ClientOptions{FlowServiceAddress: address})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()

	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	registrationErr := worker.WaitPullRegistered(startupCtx)
	cancelStartup()
	if registrationErr != nil {
		return errors.Join(registrationErr, worker.Stop(context.Background()), <-workerResult)
	}
	if err := startDemo(ctx, client); err != nil {
		return errors.Join(err, worker.Stop(context.Background()), <-workerResult)
	}
	select {
	case err := <-workerResult:
		return errors.Join(err, worker.Stop(context.Background()))
	case <-ctx.Done():
		return errors.Join(worker.Stop(context.Background()), <-workerResult)
	}
}

func removeDirectory(directory string) {
	if err := os.RemoveAll(directory); err != nil {
		log.Printf("remove %s: %v", directory, err)
	}
}

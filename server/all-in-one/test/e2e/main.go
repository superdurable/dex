// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

type ReplicaSmokeFlow struct{ dex.FlowDefaults }

func (ReplicaSmokeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(completeReplicaSmokeStep{})}
}

func (ReplicaSmokeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type completeReplicaSmokeStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (completeReplicaSmokeStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	flow := ReplicaSmokeFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return err
	}
	cacheDirectory, err := os.MkdirTemp("", "dex-all-in-one-e2e-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(cacheDirectory); err != nil {
			log.Printf("remove cache directory: %v", err)
		}
	}()
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDirectory, MaxBytes: 64 << 20})
	if err != nil {
		return err
	}
	defer func() {
		if err := cache.Close(); err != nil {
			log.Printf("close cache: %v", err)
		}
	}()
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: "127.0.0.1:8801",
	})
	if err != nil {
		return err
	}
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := worker.Stop(stopContext); err != nil {
			log.Printf("stop worker: %v", err)
		}
	}()
	registrationContext, cancelRegistration := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRegistration()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return fmt.Errorf("register pull Worker: %w", err)
	}
	firstClient, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: "127.0.0.1:8801",
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := firstClient.Close(); err != nil {
			log.Printf("close replica 1 client: %v", err)
		}
	}()
	secondClient, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: "127.0.0.1:8802",
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := secondClient.Close(); err != nil {
			log.Printf("close replica 2 client: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	flowID := fmt.Sprintf("replica-smoke-%d", time.Now().UnixNano())
	if _, err := firstClient.StartFlow(ctx, flow, flowID, "completed", dex.StartFlowOptions{}); err != nil {
		return fmt.Errorf("start Flow through replica 1: %w", err)
	}
	result, err := secondClient.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		return fmt.Errorf("wait for Flow through replica 2: %w", err)
	}
	if len(result.Completions) != 1 {
		return fmt.Errorf("expected one completion, got %d", len(result.Completions))
	}
	var output string
	if err := result.Completions[0].Output.Decode(&output); err != nil {
		return err
	}
	if output != "completed" {
		return fmt.Errorf("unexpected Flow output %q", output)
	}
	select {
	case err := <-workerResult:
		return fmt.Errorf("worker stopped unexpectedly: %w", err)
	default:
	}
	fmt.Println("Flow started through replica 1 and completed through replica 2")
	return nil
}

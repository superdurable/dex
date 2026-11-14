// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Command many-events starts one run whose Steps loop until its history holds
// more than 1000 events, so Dex Web's Timeline shows a dense run. It keeps
// serving as a Worker until it is stopped.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/web/scripts/local-demo/demoworker"
)

// TimelineDensityFlow loops SyncBatch, with a Checkpoint every tenth round, then completes.
type TimelineDensityFlow struct {
	dex.FlowDefaults
}

// GetSteps returns the two looping Steps.
func (*TimelineDensityFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SyncBatch{}),
		dex.DefineStep(Checkpoint{}),
	}
}

// GetPersistenceSchema returns an empty schema; the round count travels as Step input.
func (*TimelineDensityFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// SyncBatch records one round and moves on.
type SyncBatch struct {
	dex.StepDefaultsNoWaitFor[int]
}

// Execute moves to the next round.
func (SyncBatch) Execute(_ dex.Context, remainingRounds int) (*dex.StepDecision, error) {
	return decideNextRound(remainingRounds), nil
}

// Checkpoint marks every tenth round, so the Timeline lane has more than one Step type.
type Checkpoint struct {
	dex.StepDefaultsNoWaitFor[int]
}

// Execute moves to the next round.
func (Checkpoint) Execute(_ dex.Context, remainingRounds int) (*dex.StepDecision, error) {
	return decideNextRound(remainingRounds), nil
}

func decideNextRound(remainingRounds int) *dex.StepDecision {
	if remainingRounds <= 0 {
		return dex.GracefulComplete("done")
	}
	if remainingRounds%10 == 0 {
		return dex.GoTo(Checkpoint{}, remainingRounds-1)
	}
	return dex.GoTo(SyncBatch{}, remainingRounds-1)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	flowID := flag.String("flow-id", fmt.Sprintf("many-events-%d", time.Now().Unix()), "Flow ID of the run to start")
	rounds := flag.Int("rounds", 1100, "Step rounds to run; each round records at least one history event")
	blobCacheDir := flag.String("blob-cache-dir", "", "BlobCache directory; empty uses a temporary directory")
	flag.Parse()
	if *rounds <= 0 {
		return fmt.Errorf("-rounds must be positive, got %d", *rounds)
	}

	flow := &TimelineDensityFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return demoworker.Serve(ctx, registry, *blobCacheDir, func(ctx context.Context, client *dex.Client) error {
		runID, err := client.StartFlow(ctx, flow, *flowID, *rounds, dex.StartFlowOptions{})
		if err != nil {
			return fmt.Errorf("start flow %s: %w", *flowID, err)
		}
		fmt.Println("FLOW", *flowID, "RUN", runID)
		return nil
	})
}

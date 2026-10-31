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
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable/dex/benchmark"
)

func runSearch(args []string) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	var config benchmark.SearchConfig
	var outputPath string
	flags.StringVar(&config.Address, "address", "127.0.0.1:8801", "Dex address")
	flags.StringVar(&config.Backend, "backend", "builtin", "builtin, postgres, or paradedb")
	flags.IntVar(&config.Runs, "runs", 100000, "runs in one fresh tenant")
	flags.IntVar(&config.Attributes, "attributes", 10, "indexed Attributes per run: 0, 3, or 10")
	flags.IntVar(&config.Concurrency, "concurrency", 128, "concurrent seed clients")
	flags.IntVar(&config.Samples, "samples", 200, "measured requests per query")
	flags.IntVar(&config.Warmup, "warmup", 10, "untimed requests per query")
	flags.IntVar(&config.DeepPage, "deep-page", 500, "page reached through opaque next-page tokens")
	flags.DurationVar(&config.Timeout, "timeout", 30*time.Second, "timeout per seed run or query")
	flags.StringVar(&config.DatabaseContainer, "database-container", "", "owned PostgreSQL container for WAL and convergence probes")
	flags.BoolVar(&config.ShouldMeasureProjectionOnly, "projection-only", false, "measure seeding without query latency")
	flags.BoolVar(&config.ShouldMeasureBackfill, "backfill", false, "defer Keyword registration, then compare backfilling and ready")
	flags.StringVar(&config.GitCommit, "git-commit", "", "checkout revision")
	flags.StringVar(&outputPath, "out", "search.json", "raw S7 result file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	result, runErr := benchmark.RunSearch(ctx, config)
	contents, encodeErr := json.MarshalIndent(result, "", "  ")
	if encodeErr != nil {
		return errors.Join(runErr, encodeErr)
	}
	return errors.Join(runErr, os.WriteFile(outputPath, append(contents, '\n'), 0o644))
}

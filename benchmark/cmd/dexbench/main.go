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
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/superdurable/dex/benchmark"
)

func main() {
	if err := execute(os.Args[1:]); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}

func execute(args []string) error {
	if len(args) == 0 { return fmt.Errorf("usage: dexbench run [flags] | report FILE...") }
	switch args[0] {
	case "run": return run(args[1:])
	case "report": return report(args[1:])
	default: return fmt.Errorf("unknown command %q", args[0])
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var config benchmark.Config
	var scenarios string
	var outputDirectory string
	var isShort bool
	flags.StringVar(&config.Address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.StringVar(&config.Target, "target", "zd", "target name")
	flags.StringVar(&config.Engine, "engine", "zd", "server engine")
	flags.StringVar(&scenarios, "scenarios", "start-complete,sequential-steps,async-steps,rpc,channel-wake", "comma-separated scenarios")
	flags.Float64Var(&config.DurationSeconds, "duration", 60, "measurement seconds per scenario")
	flags.Float64Var(&config.WarmupSeconds, "warmup", 10, "warmup seconds per scenario")
	flags.Float64Var(&config.TimeoutSeconds, "timeout", 30, "timeout seconds per operation")
	flags.IntVar(&config.Concurrency, "concurrency", 16, "concurrent clients")
	flags.IntVar(&config.WorkerPool, "workers", 16, "pull Worker task pool size")
	flags.IntVar(&config.Steps, "steps", 10, "Steps per chain Flow")
	flags.IntVar(&config.ServerPID, "server-pid", 0, "server PID for CPU and RSS")
	flags.StringVar(&config.ServerProcess, "server-process", "", "server executable name for CPU and RSS")
	flags.StringVar(&config.MetricsURL, "metrics-url", "", "server-zd /metrics URL")
	flags.StringVar(&outputDirectory, "out", "results", "result directory")
	flags.BoolVar(&isShort, "short", false, "five-second low-concurrency smoke mode")
	if err := flags.Parse(args); err != nil { return err }
	if isShort { config.DurationSeconds = 5; config.WarmupSeconds = 1; config.Concurrency = 2; config.WorkerPool = 2; config.Steps = 3 }
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil { return err }
	for _, requested := range strings.Split(scenarios, ",") {
		selected := []string{strings.TrimSpace(requested)}
		if selected[0] == "rpc" { selected = []string{"rpc-plain", "rpc-locking"} }
		for _, scenario := range selected {
			config.Scenario = scenario
			if !isKnownScenario(scenario) { return fmt.Errorf("unknown scenario %q", scenario) }
			runner, err := benchmark.NewRunner(config)
			if err != nil { return err }
			result, runErr := runner.Run(context.Background())
			closeErr := runner.Close()
			path := filepath.Join(outputDirectory, fmt.Sprintf("%s-%s-%s.json", config.Target, scenario, time.Now().UTC().Format("20060102T150405Z")))
			writeErr := benchmark.WriteResult(path, result)
			fmt.Fprintln(os.Stderr, path)
			if combined := errors.Join(runErr, closeErr, writeErr); combined != nil { return fmt.Errorf("scenario %s: %w", scenario, combined) }
		}
	}
	return nil
}

func report(paths []string) error {
	if len(paths) == 0 { return fmt.Errorf("usage: dexbench report FILE...") }
	results := make([]benchmark.Result, 0, len(paths))
	for _, path := range paths {
		result, err := benchmark.ReadResult(path)
		if err != nil { return err }
		results = append(results, result)
	}
	fmt.Print(benchmark.RenderReport(results))
	return nil
}

func isKnownScenario(scenario string) bool {
	switch scenario {
	case "start-complete", "sequential-steps", "async-steps", "rpc-plain", "rpc-locking", "channel-wake": return true
	default: return false
	}
}


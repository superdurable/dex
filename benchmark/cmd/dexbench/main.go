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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/superdurable/dex/benchmark"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: dexbench run [flags] | report FILE...")
	}
	switch args[0] {
	case "run":
		return run(args[1:])
	case "report":
		return report(args[1:])
	case "seed-recovery":
		return seedRecovery(args[1:])
	case "record-recovery":
		return recordRecovery(args[1:])
	case "probe":
		return probe(args[1:])
	case "wait-ready":
		return waitReady(args[1:])
	case "variants":
		return printVariants(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func probe(args []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	var address string
	flags.StringVar(&address, "address", "127.0.0.1:8801", "Dex FlowService address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, closeErr)
		}
	}()
	return checkHealth(connection, time.Second)
}

func checkHealth(connection *grpc.ClientConn, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{Service: "dex.FlowService"})
	if err != nil {
		return err
	}
	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("FlowService health is %s", response.GetStatus())
	}
	return nil
}

// waitReady dials fresh connections so gRPC reconnect backoff cannot quantize readiness time.
func waitReady(args []string) error {
	flags := flag.NewFlagSet("wait-ready", flag.ContinueOnError)
	var address string
	var interval, timeout time.Duration
	var processID int
	var startedUnixNano int64
	flags.StringVar(&address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.DurationVar(&interval, "interval", 10*time.Millisecond, "poll interval")
	flags.DurationVar(&timeout, "timeout", 5*time.Minute, "maximum wait")
	flags.IntVar(&processID, "pid", 0, "server process that must stay alive while waiting")
	flags.Int64Var(&startedUnixNano, "started-unix-nano", 0, "when set, print seconds from this time until ready")
	if err := flags.Parse(args); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		if isReady(address, interval) {
			if startedUnixNano > 0 {
				fmt.Printf("%.6f\n", time.Since(time.Unix(0, startedUnixNano)).Seconds())
			}
			return nil
		}
		if processID > 0 && syscall.Kill(processID, 0) != nil {
			return fmt.Errorf("server process %d exited during startup", processID)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("FlowService at %s was not ready within %s", address, timeout)
		}
		time.Sleep(interval)
	}
}

func isReady(address string, interval time.Duration) bool {
	dialTimeout := max(interval, 50*time.Millisecond)
	tcpConnection, err := net.DialTimeout("tcp", address, dialTimeout)
	if err != nil {
		return false
	}
	if err := tcpConnection.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, closeErr)
		}
	}()
	return checkHealth(connection, time.Second) == nil
}

func printVariants(args []string) error {
	flags := flag.NewFlagSet("variants", flag.ContinueOnError)
	var scenarios, attributeStoreName string
	flags.StringVar(&scenarios, "scenarios", "", "comma-separated scenarios")
	flags.StringVar(&attributeStoreName, "attribute-store-name", "", "PostgreSQL Attribute Store name for synchronized writes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	variants, err := expandScenarios(scenarios, attributeStoreName)
	if err != nil {
		return err
	}
	for _, variant := range variants {
		fmt.Println(variant)
	}
	return nil
}

func expandScenarios(scenarios, attributeStoreName string) ([]string, error) {
	var variants []string
	for _, requested := range strings.Split(scenarios, ",") {
		selected := []string{strings.TrimSpace(requested)}
		switch selected[0] {
		case "":
			continue
		case "rpc":
			selected = []string{"rpc-plain", "rpc-locking"}
		case "attribute-read":
			selected = []string{"attribute-read-rpc", "attribute-read-direct"}
		case "attribute-write":
			selected = []string{"attribute-write-local"}
			if attributeStoreName != "" {
				selected = append(selected, "attribute-write-sync")
			}
		}
		for _, scenario := range selected {
			if !isKnownScenario(scenario) {
				return nil, fmt.Errorf("unknown scenario %q", scenario)
			}
			variants = append(variants, scenario)
		}
	}
	return variants, nil
}

func seedRecovery(args []string) error {
	flags := flag.NewFlagSet("seed-recovery", flag.ContinueOnError)
	var config benchmark.Config
	var sizeMB int
	flags.StringVar(&config.Address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.IntVar(&sizeMB, "size-mb", 100, "target command payload size in MiB")
	flags.Float64Var(&config.TimeoutSeconds, "timeout", 30, "write timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if sizeMB < 1 {
		return fmt.Errorf("size-mb must be positive")
	}
	config.Scenario = "attribute-write-local"
	config.Target = "zd"
	config.Engine = "zd"
	config.Concurrency = 1
	config.WorkerPool = 1
	config.Steps = 1
	config.AttributeBytes = 32
	config.DurationSeconds = 1
	runner, err := benchmark.NewRunner(config)
	if err != nil {
		return err
	}
	submittedBytes, seedErr := runner.SeedRecovery(context.Background(), uint64(sizeMB)<<20)
	closeErr := runner.Close()
	if seedErr != nil || closeErr != nil {
		return errors.Join(seedErr, closeErr)
	}
	fmt.Printf("submitted_bytes=%d\n", submittedBytes)
	return nil
}

func recordRecovery(args []string) error {
	flags := flag.NewFlagSet("record-recovery", flag.ContinueOnError)
	var config benchmark.Config
	var outputPath string
	var elapsedSeconds float64
	flags.IntVar(&config.RecoveryTargetMB, "size-mb", 100, "target command payload size in MiB")
	flags.Float64Var(&elapsedSeconds, "elapsed-seconds", 0, "restart to serving seconds")
	flags.StringVar(&config.MetricsURL, "metrics-url", "http://127.0.0.1:9901/metrics", "server-zd metrics URL")
	flags.StringVar(&config.Mode, "mode", "quick", "quick or prod")
	flags.StringVar(&config.Region, "region", os.Getenv("AWS_REGION"), "server region")
	flags.StringVar(&config.InstanceType, "instance-type", os.Getenv("DEXBENCH_INSTANCE_TYPE"), "host instance type")
	flags.StringVar(&config.GitCommit, "git-commit", os.Getenv("DEXBENCH_GIT_COMMIT"), "SDK checkout commit")
	flags.StringVar(&outputPath, "out", "", "JSON result path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if config.RecoveryTargetMB < 1 || elapsedSeconds <= 0 || outputPath == "" {
		return fmt.Errorf("positive size-mb, elapsed-seconds, and out are required")
	}
	config.Target = "zd"
	config.Engine = "zd"
	config.Scenario = "recovery"
	config.Concurrency = 1
	result, err := benchmark.RecoveryResult(context.Background(), config, elapsedSeconds)
	if err != nil {
		return err
	}
	return benchmark.WriteResult(outputPath, result)
}

func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var config benchmark.Config
	var scenarios string
	var outputDirectory string
	var isShort bool
	var concurrencyValues string
	var resourcePIDs string
	var resourceCgroups string
	flags.StringVar(&config.Address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.StringVar(&config.Target, "target", "zd", "target name")
	flags.StringVar(&config.Engine, "engine", "zd", "server engine")
	flags.StringVar(&scenarios, "scenarios", "start-complete,sequential-steps,async-steps,rpc,attribute-read,channel-wake,attribute-write", "comma-separated scenarios")
	flags.Float64Var(&config.DurationSeconds, "duration", 180, "measurement seconds per scenario")
	flags.Float64Var(&config.WarmupSeconds, "warmup", 30, "warmup seconds per scenario")
	flags.Float64Var(&config.TimeoutSeconds, "timeout", 30, "timeout seconds per operation")
	flags.StringVar(&concurrencyValues, "concurrency", "1,8,32,128", "comma-separated concurrent client counts")
	flags.IntVar(&config.WorkerPool, "workers", 0, "pull Worker task pool size; defaults to concurrency")
	flags.IntVar(&config.Steps, "steps", 10, "Steps per chain Flow")
	flags.IntVar(&config.AttributeBytes, "attribute-bytes", 16384, "bytes per read Attribute")
	flags.StringVar(&config.AttributeStoreName, "attribute-store-name", "", "PostgreSQL Attribute Store name for synchronized writes")
	flags.StringVar(&config.Mode, "mode", "quick", "quick or prod")
	flags.StringVar(&config.Region, "region", os.Getenv("AWS_REGION"), "server region")
	flags.StringVar(&config.InstanceType, "instance-type", os.Getenv("DEXBENCH_INSTANCE_TYPE"), "host instance type")
	flags.StringVar(&config.GitCommit, "git-commit", os.Getenv("DEXBENCH_GIT_COMMIT"), "SDK checkout commit")
	flags.IntVar(&config.ServerPID, "server-pid", 0, "root server PID; its whole process tree is sampled for CPU and RSS")
	flags.StringVar(&resourcePIDs, "resource-pids", "", "additional comma-separated root process IDs; each root's process tree is sampled")
	flags.StringVar(&resourceCgroups, "resource-cgroups", "", "comma-separated cgroup v2 directories to sample, such as Docker container cgroups")
	flags.StringVar(&config.MetricsURL, "metrics-url", "", "server-zd /metrics URL")
	flags.StringVar(&outputDirectory, "out", "results", "result directory")
	flags.BoolVar(&isShort, "short", false, "ten-second low-concurrency smoke mode")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if isShort {
		config.DurationSeconds = 10
		config.WarmupSeconds = 1
		concurrencyValues = "2"
		config.Steps = 3
	}
	for _, value := range strings.Split(resourcePIDs, ",") {
		if strings.TrimSpace(value) == "" {
			continue
		}
		processID, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || processID < 1 {
			return fmt.Errorf("invalid resource PID %q", value)
		}
		config.ResourcePIDs = append(config.ResourcePIDs, processID)
	}
	for _, value := range strings.Split(resourceCgroups, ",") {
		if directory := strings.TrimSpace(value); directory != "" {
			config.ResourceCgroups = append(config.ResourceCgroups, directory)
		}
	}
	requestedWorkerPool := config.WorkerPool
	variants, err := expandScenarios(scenarios, config.AttributeStoreName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	for _, scenario := range variants {
		for _, requestedConcurrency := range strings.Split(concurrencyValues, ",") {
			concurrency, err := strconv.Atoi(strings.TrimSpace(requestedConcurrency))
			if err != nil || concurrency < 1 {
				return fmt.Errorf("invalid concurrency %q", requestedConcurrency)
			}
			config.Concurrency = concurrency
			config.Scenario = scenario
			config.WorkerPool = requestedWorkerPool
			if config.WorkerPool == 0 {
				config.WorkerPool = concurrency
			}
			runner, err := benchmark.NewRunner(config)
			if err != nil {
				return err
			}
			result, runErr := runner.Run(context.Background())
			closeErr := runner.Close()
			path := filepath.Join(outputDirectory, fmt.Sprintf("%s-%s-c%d-%s.json", config.Target, scenario, concurrency, time.Now().UTC().Format("20060102T150405Z")))
			writeErr := benchmark.WriteResult(path, result)
			fmt.Fprintln(os.Stderr, path)
			if combined := errors.Join(runErr, closeErr, writeErr); combined != nil {
				return fmt.Errorf("scenario %s: %w", scenario, combined)
			}
		}
	}
	return nil
}

func report(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: dexbench report FILE...")
	}
	results := make([]benchmark.Result, 0, len(paths))
	for _, path := range paths {
		result, err := benchmark.ReadResult(path)
		if err != nil {
			return err
		}
		results = append(results, result)
	}
	fmt.Print(benchmark.RenderReport(results))
	return nil
}

func isKnownScenario(scenario string) bool {
	switch scenario {
	case "start-complete", "sequential-steps", "async-steps", "rpc-plain", "rpc-locking", "channel-wake", "attribute-read-rpc", "attribute-read-direct", "attribute-write-local", "attribute-write-sync":
		return true
	default:
		return false
	}
}

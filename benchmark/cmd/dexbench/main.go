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
	"os/signal"
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
		return fmt.Errorf("usage: dexbench run [flags] | report FILE... | chart FILE... | sample --pid PID --out FILE | merge-server --result FILE --samples FILE")
	}
	switch args[0] {
	case "search":
		return runSearch(args[1:])
	case "run":
		return run(args[1:])
	case "report":
		return report(args[1:])
	case "chart":
		return chart(args[1:])
	case "probe":
		return probe(args[1:])
	case "wait-ready":
		return waitReady(args[1:])
	case "variants":
		return printVariants(args[1:])
	case "sample":
		return sampleServer(args[1:])
	case "merge-server":
		return mergeServer(args[1:])
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
	flags.StringVar(&address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.DurationVar(&interval, "interval", 10*time.Millisecond, "poll interval")
	flags.DurationVar(&timeout, "timeout", 5*time.Minute, "maximum wait")
	flags.IntVar(&processID, "pid", 0, "server process that must stay alive while waiting")
	if err := flags.Parse(args); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		if isReady(address, interval) {
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
	var scenarios string
	flags.StringVar(&scenarios, "scenarios", "", "comma-separated scenarios")
	if err := flags.Parse(args); err != nil {
		return err
	}
	variants, err := expandScenarios(scenarios)
	if err != nil {
		return err
	}
	for _, variant := range variants {
		fmt.Println(variant)
	}
	return nil
}

func expandScenarios(scenarios string) ([]string, error) {
	var variants []string
	for _, requested := range strings.Split(scenarios, ",") {
		selected := []string{strings.TrimSpace(requested)}
		switch selected[0] {
		case "":
			continue
		case "rpc":
			selected = []string{"rpc-plain", "rpc-locking"}
		case "attribute-read":
			selected = []string{"attribute-read-direct"}
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

func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var config benchmark.Config
	var scenarios string
	var outputDirectory string
	var isShort bool
	var concurrencyValues string
	var resourcePIDs string
	var serverTunables string
	flags.StringVar(&config.Address, "address", "127.0.0.1:8801", "Dex FlowService address")
	flags.StringVar(&config.Target, "target", "zd", "target name")
	flags.StringVar(&config.Engine, "engine", "zd", "server engine")
	flags.StringVar(&scenarios, "scenarios", "start-complete,sequential-steps,sequential-steps-async,rpc,attribute-read,channel-wake", "comma-separated scenarios")
	flags.Float64Var(&config.DurationSeconds, "duration", 180, "measurement seconds per scenario")
	flags.Float64Var(&config.WarmupSeconds, "warmup", 30, "warmup seconds per scenario")
	flags.Float64Var(&config.TimeoutSeconds, "timeout", 30, "timeout seconds per operation")
	flags.StringVar(&concurrencyValues, "concurrency", "1,8,32,128", "comma-separated concurrent client counts")
	flags.IntVar(&config.WorkerPool, "workers", 0, "pull Worker task pool size; defaults to concurrency")
	flags.IntVar(&config.Steps, "steps", 10, "Steps per chain Flow")
	flags.IntVar(&config.Connections, "connections", 1, "Client and Worker pairs, each with its own gRPC connections; clients are spread round-robin")
	flags.IntVar(&config.Trial, "trial", 0, "trial number of a repeated point; 0 when not repeated")
	flags.StringVar(&serverTunables, "server-tunables", "", "semicolon-separated KEY=VALUE server settings to record in the result")
	flags.IntVar(&config.AttributeBytes, "attribute-bytes", 16384, "bytes per read Attribute")
	flags.StringVar(&config.Mode, "mode", "quick", "run mode label")
	flags.StringVar(&config.GitCommit, "git-commit", os.Getenv("DEXBENCH_GIT_COMMIT"), "SDK checkout commit")
	flags.IntVar(&config.ServerPID, "server-pid", 0, "root server PID; its whole process tree is sampled for CPU and RSS")
	flags.StringVar(&resourcePIDs, "resource-pids", "", "additional comma-separated root process IDs; each root's process tree is sampled")
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
	if config.Connections < 1 {
		return fmt.Errorf("connections must be positive")
	}
	tunables, err := parseServerTunables(serverTunables)
	if err != nil {
		return err
	}
	config.ServerTunables = tunables
	requestedWorkerPool := config.WorkerPool
	variants, err := expandScenarios(scenarios)
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
	results, err := readResults(paths)
	if err != nil {
		return err
	}
	fmt.Print(benchmark.RenderReport(results))
	return nil
}

func chart(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: dexbench chart FILE...")
	}
	results, err := readResults(paths)
	if err != nil {
		return err
	}
	fmt.Print(benchmark.RenderChartHTML(results))
	return nil
}

func readResults(paths []string) ([]benchmark.Result, error) {
	results := make([]benchmark.Result, 0, len(paths))
	for _, path := range paths {
		result, err := benchmark.ReadResult(path)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func isKnownScenario(scenario string) bool {
	switch scenario {
	case "start-complete", "sequential-steps", "sequential-steps-async", "rpc-plain", "rpc-locking", "channel-wake", "attribute-read-direct":
		return true
	default:
		return false
	}
}

func parseServerTunables(value string) (map[string]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	tunables := map[string]string{}
	for _, pair := range strings.Split(value, ";") {
		key, setting, hasSetting := strings.Cut(strings.TrimSpace(pair), "=")
		if !hasSetting || key == "" {
			return nil, fmt.Errorf("server tunable %q is not KEY=VALUE", pair)
		}
		tunables[key] = setting
	}
	return tunables, nil
}

// sampleServer samples a server process tree until SIGINT or SIGTERM, then writes the samples.
func sampleServer(args []string) error {
	flags := flag.NewFlagSet("sample", flag.ContinueOnError)
	var processID int
	var outputPath string
	flags.IntVar(&processID, "pid", 0, "root server PID; its whole process tree is sampled")
	flags.StringVar(&outputPath, "out", "", "JSON samples path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if processID < 1 || outputPath == "" {
		return fmt.Errorf("positive pid and out are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return benchmark.WriteServerSamples(outputPath, benchmark.SampleServerProcess(ctx, processID))
}

// mergeServer replaces a result's resources with the server-host samples from its measurement window.
func mergeServer(args []string) error {
	flags := flag.NewFlagSet("merge-server", flag.ContinueOnError)
	var resultPath, samplesPath string
	flags.StringVar(&resultPath, "result", "", "result JSON to update in place")
	flags.StringVar(&samplesPath, "samples", "", "server samples JSON written by dexbench sample")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if resultPath == "" || samplesPath == "" {
		return fmt.Errorf("result and samples are required")
	}
	result, err := benchmark.ReadResult(resultPath)
	if err != nil {
		return err
	}
	samples, err := benchmark.ReadServerSamples(samplesPath)
	if err != nil {
		return err
	}
	if err := benchmark.MergeServerSamples(&result, samples); err != nil {
		return fmt.Errorf("merge %s: %w", samplesPath, err)
	}
	return benchmark.WriteResult(resultPath, result)
}

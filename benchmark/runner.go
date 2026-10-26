// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type Runner struct {
	config        Config
	client        *dex.Client
	connection    *grpc.ClientConn
	service       dexpb.FlowServiceClient
	worker        *dex.Worker
	workerResult  chan error
	cache         *blobcache.Cache
	completion    completionFlow
	chain         *chainFlow
	rpc           rpcFlow
	attribute     attributeFlow
	stepLatencies latencyCollector
	nextFlowID    atomic.Int64
	runPrefix     string
}

func NewRunner(config Config) (*Runner, error) {
	if config.Address == "" || config.Concurrency < 1 || config.WorkerPool < 1 || config.Steps < 1 || config.DurationSeconds <= 0 || config.TimeoutSeconds <= 0 {
		return nil, fmt.Errorf("address, positive concurrency, worker pool, steps, duration, and timeout are required")
	}
	runner := &Runner{config: config, workerResult: make(chan error, 1), runPrefix: fmt.Sprintf("bench-%d", time.Now().UnixNano())}
	runner.chain = &chainFlow{latencies: &runner.stepLatencies, isAsync: config.Scenario == "async-steps"}
	registry, err := dex.NewRegistry([]dex.Flow{runner.completion, runner.chain, runner.rpc, runner.attribute})
	if err != nil {
		return nil, err
	}
	cacheDir, err := os.MkdirTemp("", "dexbench-cache-")
	if err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDir, MaxBytes: 256 << 20, Logger: slog.Default()})
	if err != nil {
		os.RemoveAll(cacheDir)
		return nil, err
	}
	runner.cache = cache
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: config.Address,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: config.WorkerPool, ReservedRPCTasks: ptr.Any(0)},
	})
	if err != nil {
		runner.closeCache(cacheDir)
		return nil, err
	}
	runner.worker = worker
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: config.Address, WorkerTarget: worker.WorkerTarget()})
	if err != nil {
		runner.stopWorker()
		runner.closeCache(cacheDir)
		return nil, err
	}
	runner.client = client
	connection, err := grpc.NewClient(config.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		runner.Close()
		return nil, fmt.Errorf("connect FlowService: %w", err)
	}
	runner.connection = connection
	runner.service = dexpb.NewFlowServiceClient(connection)
	go func() { runner.workerResult <- worker.Start() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(ctx); err != nil {
		runner.Close()
		return nil, fmt.Errorf("pull Worker registration: %w", err)
	}
	return runner, nil
}

func (runner *Runner) Run(ctx context.Context) (Result, error) {
	config := runner.config
	result := Result{SchemaVersion: 1, StartedAt: time.Now().UTC(), Config: config, Machine: machineInfo(), Errors: map[string]int64{}, SDKVersion: sdkVersion(), ServerVersion: os.Getenv("DEXBENCH_SERVER_VERSION")}
	result.Machine.InstanceType = config.InstanceType
	result.Machine.Region = config.Region
	if result.ServerVersion == "" {
		result.ServerVersion = "unknown"
	}
	pid, err := serverPID(config)
	if err != nil {
		return result, err
	}
	if pid != 0 {
		result.Config.ServerPID = pid
	}
	if err := runner.runWindow(ctx, time.Duration(config.WarmupSeconds*float64(time.Second)), false, &result); err != nil {
		return result, fmt.Errorf("warmup: %w", err)
	}
	runner.stepLatencies.take()
	metricsContext, cancelMetrics := context.WithTimeout(ctx, 5*time.Second)
	beforeMetrics, err := scrapeMetrics(metricsContext, config.MetricsURL)
	cancelMetrics()
	if err != nil {
		return result, fmt.Errorf("scrape metrics before run: %w", err)
	}
	sampleContext, stopSampling := context.WithCancel(ctx)
	sampleResult := make(chan []ResourceSample, 1)
	processIDs := append([]int(nil), config.ResourcePIDs...)
	if pid != 0 {
		processIDs = append(processIDs, pid)
	}
	if len(processIDs) != 0 {
		go sampleResources(sampleContext, processIDs, sampleResult)
	} else {
		sampleResult <- nil
	}
	start := time.Now()
	runErr := runner.runWindow(ctx, time.Duration(config.DurationSeconds*float64(time.Second)), true, &result)
	actualDuration := time.Since(start)
	stopSampling()
	result.Resources = <-sampleResult
	metricsContext, cancelMetrics = context.WithTimeout(ctx, 5*time.Second)
	afterMetrics, metricsErr := scrapeMetrics(metricsContext, config.MetricsURL)
	cancelMetrics()
	if metricsErr != nil {
		return result, fmt.Errorf("scrape metrics after run: %w", metricsErr)
	}
	result.ObjectStore = metricDifference(beforeMetrics, afterMetrics, actualDuration)
	stepSamples := runner.stepLatencies.take()
	result.StepSamples = len(stepSamples)
	result.StepLatency = calculatePercentiles(stepSamples)
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (runner *Runner) runWindow(ctx context.Context, duration time.Duration, isMeasured bool, result *Result) error {
	if duration <= 0 {
		return nil
	}
	var deadline time.Time
	startWindow := make(chan struct{})
	var readyWorkers sync.WaitGroup
	readyWorkers.Add(runner.config.Concurrency)
	var waitGroup sync.WaitGroup
	var mutex sync.Mutex
	latencies := make([]time.Duration, 0)
	var successes int64
	var attempts int64
	for workerIndex := 0; workerIndex < runner.config.Concurrency; workerIndex++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			isReady := false
			defer func() {
				if !isReady {
					readyWorkers.Done()
				}
			}()
			var rpcFlowID string
			if strings.HasPrefix(runner.config.Scenario, "rpc-") || strings.HasPrefix(runner.config.Scenario, "attribute-") {
				callContext, cancel := context.WithTimeout(ctx, time.Duration(runner.config.TimeoutSeconds*float64(time.Second)))
				rpcFlowID = runner.newFlowID()
				err := runner.startPersistentFlow(callContext, rpcFlowID)
				cancel()
				if err != nil {
					mutex.Lock()
					result.Errors["setup: "+err.Error()]++
					mutex.Unlock()
					return
				}
				defer func() {
					stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := runner.client.StopFlow(stopContext, rpcFlowID, dex.StopOptions{}); err != nil {
						mutex.Lock()
						result.Errors["cleanup: "+err.Error()]++
						mutex.Unlock()
					}
					stopCancel()
				}()
			}
			isReady = true
			readyWorkers.Done()
			<-startWindow
			for time.Now().Before(deadline) && ctx.Err() == nil {
				operationContext, cancel := context.WithTimeout(ctx, time.Duration(runner.config.TimeoutSeconds*float64(time.Second)))
				started := time.Now()
				latency, err := runner.runOperation(operationContext, rpcFlowID)
				cancel()
				if isMeasured {
					mutex.Lock()
					attempts++
					if err != nil {
						result.Errors[err.Error()]++
					} else {
						successes++
						if latency == 0 {
							latency = time.Since(started)
						}
						latencies = append(latencies, latency)
					}
					mutex.Unlock()
				} else if err != nil {
					mutex.Lock()
					result.Errors["warmup: "+err.Error()]++
					mutex.Unlock()
				}
			}
		}(workerIndex)
	}
	readyWorkers.Wait()
	deadline = time.Now().Add(duration)
	close(startWindow)
	waitGroup.Wait()
	if isMeasured {
		result.Attempts = attempts
		result.Successes = successes
		result.Latency = calculatePercentiles(latencies)
		result.ThroughputUnit = "flows"
		factor := int64(1)
		if runner.config.Scenario == "sequential-steps" || runner.config.Scenario == "async-steps" {
			result.ThroughputUnit = "steps"
			factor = int64(runner.config.Steps)
		}
		if strings.HasPrefix(runner.config.Scenario, "rpc-") {
			result.ThroughputUnit = "rpcs"
		}
		if strings.HasPrefix(runner.config.Scenario, "attribute-read") {
			result.ThroughputUnit = "reads"
		}
		if strings.HasPrefix(runner.config.Scenario, "attribute-write") {
			result.ThroughputUnit = "writes"
		}
		if runner.config.Scenario == "channel-wake" {
			result.ThroughputUnit = "wakes"
		}
		result.ThroughputPerSecond = float64(successes*factor) / duration.Seconds()
	}
	return ctx.Err()
}

func (runner *Runner) startPersistentFlow(ctx context.Context, flowID string) error {
	if strings.HasPrefix(runner.config.Scenario, "rpc-") {
		initialCounter, err := dex.InitialAttribute(benchmarkRPCCounter, int64(0))
		if err != nil {
			return err
		}
		_, err = runner.client.StartFlow(ctx, runner.rpc, flowID, nil, dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{initialCounter}})
		return err
	}
	initialAttributes := make([]dex.InitialAttributeDef, 0, len(benchmarkReadAttributes))
	for index, attribute := range benchmarkReadAttributes {
		value := fmt.Sprintf("%02d-%s", index, strings.Repeat("x", runner.config.AttributeBytes))
		initialAttribute, err := dex.InitialAttribute(attribute, value)
		if err != nil {
			return err
		}
		initialAttributes = append(initialAttributes, initialAttribute)
	}
	options := dex.StartFlowOptions{Attributes: initialAttributes}
	if runner.config.Scenario == "attribute-write-sync" {
		if runner.config.AttributeStoreName == "" {
			return fmt.Errorf("attribute store name required for synchronized writes")
		}
		options.ConfigOverride = &dex.FlowConfig{AttributeStoreNames: []string{runner.config.AttributeStoreName}}
	}
	_, err := runner.client.StartFlow(ctx, runner.attribute, flowID, nil, options)
	return err
}

func (runner *Runner) runOperation(ctx context.Context, rpcFlowID string) (time.Duration, error) {
	flowID := runner.newFlowID()
	switch runner.config.Scenario {
	case "start-complete":
		if _, err := runner.client.StartFlow(ctx, runner.completion, flowID, nil, dex.StartFlowOptions{}); err != nil {
			return 0, err
		}
		result, err := runner.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
		if err != nil {
			return 0, err
		}
		if result.Status != dex.FlowCompleted {
			return 0, fmt.Errorf("Flow status %v", result.Status)
		}
	case "sequential-steps", "async-steps":
		input := chainInput{Remaining: runner.config.Steps}
		if _, err := runner.client.StartFlow(ctx, runner.chain, flowID, input, dex.StartFlowOptions{}); err != nil {
			return 0, err
		}
		result, err := runner.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
		if err != nil {
			return 0, err
		}
		if result.Status != dex.FlowCompleted {
			return 0, fmt.Errorf("Flow status %v", result.Status)
		}
	case "rpc-plain":
		var output int64
		return 0, runner.client.InvokeRPC(ctx, rpcFlowID, runner.rpc.GetCounter, nil, &output)
	case "rpc-locking":
		var output int64
		return 0, runner.client.InvokeRPC(ctx, rpcFlowID, runner.rpc.IncrementCounter, nil, &output)
	case "channel-wake":
		initialCounter, err := dex.InitialAttribute(benchmarkRPCCounter, int64(0))
		if err != nil {
			return 0, err
		}
		if _, err := runner.client.StartFlow(ctx, runner.rpc, flowID, nil, dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{initialCounter}}); err != nil {
			return 0, err
		}
		publishedAt := time.Now().UnixNano()
		if err := runner.client.InvokeRPC(ctx, flowID, runner.rpc.PublishWake, publishedAt, nil); err != nil {
			return 0, err
		}
		result, err := runner.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
		if err != nil {
			return 0, err
		}
		if result.Status != dex.FlowCompleted {
			return 0, fmt.Errorf("Flow status %v", result.Status)
		}
		var elapsedNanoseconds int64
		if err := result.DecodeSingleOutput(&elapsedNanoseconds); err != nil {
			return 0, err
		}
		return time.Duration(elapsedNanoseconds), nil
	case "attribute-read-rpc":
		var bytesRead int64
		return 0, runner.client.InvokeRPC(ctx, rpcFlowID, runner.attribute.ReadAttributes, nil, &bytesRead)
	case "attribute-read-direct":
		response, err := runner.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: rpcFlowID, AllKeys: true})
		if err != nil {
			return 0, err
		}
		if len(response.GetAttributes()) != len(benchmarkReadAttributes) {
			return 0, fmt.Errorf("read %d attributes, expected %d", len(response.GetAttributes()), len(benchmarkReadAttributes))
		}
	case "attribute-write-local", "attribute-write-sync":
		value := &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: time.Now().UnixNano()}}
		write := &dexpb.AttributeWrite{Key: benchmarkWriteAttribute.AttributeName(), Value: value}
		if runner.config.Scenario == "attribute-write-sync" {
			write.SyncConfig = &dexpb.AttributeSyncConfig{Enabled: true}
		}
		_, err := runner.service.SetAttributes(ctx, &dexpb.SetAttributesRequest{FlowId: rpcFlowID, Attributes: []*dexpb.AttributeWrite{write}, RequestId: runner.newFlowID()})
		return 0, err
	default:
		return 0, fmt.Errorf("unknown scenario %s", runner.config.Scenario)
	}
	return 0, nil
}

func (runner *Runner) SeedRecovery(ctx context.Context, targetBytes uint64) (uint64, error) {
	flowID := runner.newFlowID()
	if err := runner.startPersistentFlow(ctx, flowID); err != nil {
		return 0, err
	}
	var submittedBytes uint64
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	for submittedBytes < targetBytes {
		writes := make([]*dexpb.AttributeWrite, 512)
		for index := range writes {
			payload := make([]byte, 72)
			for offset := range payload {
				payload[offset] = byte('a' + random.Intn(26))
			}
			writes[index] = &dexpb.AttributeWrite{
				Key:   fmt.Sprintf("recovery-%03d", index),
				Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: string(payload)}},
			}
		}
		request := &dexpb.SetAttributesRequest{FlowId: flowID, Attributes: writes, RequestId: runner.newFlowID()}
		operationContext, cancel := context.WithTimeout(ctx, time.Duration(runner.config.TimeoutSeconds*float64(time.Second)))
		_, err := runner.service.SetAttributes(operationContext, request)
		cancel()
		if err != nil {
			return submittedBytes, fmt.Errorf("seed recovery after %d bytes: %w", submittedBytes, err)
		}
		submittedBytes += uint64(proto.Size(request))
	}
	return submittedBytes, nil
}

func (runner *Runner) Close() error {
	var combined error
	if runner.connection != nil {
		combined = errors.Join(combined, runner.connection.Close())
	}
	if runner.client != nil {
		combined = errors.Join(combined, runner.client.Close())
	}
	if runner.worker != nil {
		stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		combined = errors.Join(combined, runner.worker.Stop(stopContext))
		cancel()
		select {
		case err := <-runner.workerResult:
			combined = errors.Join(combined, err)
		case <-time.After(30 * time.Second):
			combined = errors.Join(combined, fmt.Errorf("Worker did not stop in 30 seconds"))
		}
	}
	if runner.cache != nil {
		combined = errors.Join(combined, runner.cache.Close())
	}
	return combined
}

func (runner *Runner) stopWorker() {
	if runner.worker == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.worker.Stop(ctx); err != nil {
		slog.Error("stop Worker", "error", err)
	}
}

func (runner *Runner) closeCache(directory string) {
	if err := runner.cache.Close(); err != nil {
		slog.Error("close cache", "error", err)
	}
	if err := os.RemoveAll(directory); err != nil {
		slog.Error("remove cache", "error", err)
	}
}

func (runner *Runner) newFlowID() string {
	return fmt.Sprintf("%s-%d", runner.runPrefix, runner.nextFlowID.Add(1))
}

func sampleResources(ctx context.Context, processIDs []int, output chan<- []ResourceSample) {
	var samples []ResourceSample
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			output <- samples
			return
		case <-ticker.C:
			combined := ResourceSample{At: time.Now().UTC()}
			for _, processID := range processIDs {
				sample, err := sampleProcess(processID)
				if err != nil {
					slog.Warn("sample server process", "pid", processID, "error", err)
					continue
				}
				combined.CPUPercent += sample.CPUPercent
				combined.RSSBytes += sample.RSSBytes
			}
			samples = append(samples, combined)
		}
	}
}

func sdkVersion() string {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dependency := range build.Deps {
		if dependency.Path == "github.com/superdurable/dex/sdk-go" {
			if dependency.Replace != nil {
				return "in-repo (" + dependency.Replace.Path + ")"
			}
			return dependency.Version
		}
	}
	return "unknown"
}

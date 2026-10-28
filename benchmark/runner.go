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
	config               Config
	client               *dex.Client
	connection           *grpc.ClientConn
	service              dexpb.FlowServiceClient
	worker               *dex.Worker
	workerResult         chan error
	cache                *blobcache.Cache
	cacheDir             string
	completion           completionFlow
	chain                *chainFlow
	rpc                  rpcFlow
	attribute            *attributeFlow
	wake                 *wakeFlow
	stepLatencies        latencyCollector
	nextFlowID           atomic.Int64
	nextWakeSequence     atomic.Int64
	blobsLoaded          atomic.Int64
	flowStatePolls       atomic.Int64
	runPrefix            string
	expectedReadValues   map[string]string
	expectedBytesPerRead int64
}

func NewRunner(config Config) (*Runner, error) {
	if config.Address == "" || config.Concurrency < 1 || config.WorkerPool < 1 || config.Steps < 1 || config.DurationSeconds <= 0 || config.TimeoutSeconds <= 0 {
		return nil, fmt.Errorf("address, positive concurrency, worker pool, steps, duration, and timeout are required")
	}
	runner := &Runner{config: config, workerResult: make(chan error, 1), runPrefix: fmt.Sprintf("bench-%d", time.Now().UnixNano())}
	runner.chain = &chainFlow{latencies: &runner.stepLatencies}
	runner.wake = &wakeFlow{signals: newWakeSignals(config.Concurrency)}
	runner.expectedReadValues = make(map[string]string, len(benchmarkReadAttributes))
	for index, attribute := range benchmarkReadAttributes {
		value := benchmarkReadValue(index, config.AttributeBytes)
		runner.expectedReadValues[attribute.AttributeName()] = value
		runner.expectedBytesPerRead += int64(len(value))
	}
	runner.attribute = &attributeFlow{expectedValues: runner.expectedReadValues}
	registry, err := dex.NewRegistry([]dex.Flow{runner.completion, runner.chain, runner.rpc, runner.attribute, runner.wake})
	if err != nil {
		return nil, err
	}
	cacheDir, err := os.MkdirTemp("", "dexbench-cache-")
	if err != nil {
		return nil, err
	}
	runner.cacheDir = cacheDir
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDir, MaxBytes: 256 << 20, Logger: slog.Default()})
	if err != nil {
		removeDirectory(cacheDir)
		return nil, err
	}
	runner.cache = cache
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: config.Address,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: config.WorkerPool, ReservedRPCTasks: ptr.Any(0)},
	})
	if err != nil {
		runner.closeCache()
		return nil, err
	}
	runner.worker = worker
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: config.Address, WorkerTarget: worker.WorkerTarget()})
	if err != nil {
		runner.stopWorker()
		runner.closeCache()
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

func benchmarkReadValue(index, size int) string {
	return fmt.Sprintf("%02d-%s", index, strings.Repeat("x", size))
}

func (runner *Runner) Run(ctx context.Context) (Result, error) {
	config := runner.config
	result := Result{SchemaVersion: 2, StartedAt: time.Now().UTC(), Config: config, Machine: machineInfo(), Errors: map[string]int64{}, UnmeasuredErrors: map[string]int64{}, SDKVersion: sdkVersion(), ServerVersion: os.Getenv("DEXBENCH_SERVER_VERSION")}
	if result.ServerVersion == "" {
		result.ServerVersion = "unknown"
	}
	persistentFlowIDs, err := runner.startPersistentFlows(ctx)
	defer runner.stopPersistentFlows(persistentFlowIDs, &result)
	if err != nil {
		return result, fmt.Errorf("setup: %w", err)
	}
	if err := runner.runWindow(ctx, time.Duration(config.WarmupSeconds*float64(time.Second)), false, persistentFlowIDs, &result); err != nil {
		return result, fmt.Errorf("warmup: %w", err)
	}
	runner.stepLatencies.take()
	runner.blobsLoaded.Store(0)
	runner.flowStatePolls.Store(0)
	metricsContext, cancelMetrics := context.WithTimeout(ctx, 5*time.Second)
	beforeMetrics, err := scrapeMetrics(metricsContext, config.MetricsURL)
	cancelMetrics()
	if err != nil {
		return result, fmt.Errorf("scrape metrics before run: %w", err)
	}
	sampleContext, stopSampling := context.WithCancel(ctx)
	sampleResult := make(chan []ResourceSample, 1)
	rootPIDs := append([]int(nil), config.ResourcePIDs...)
	if config.ServerPID > 0 {
		rootPIDs = append(rootPIDs, config.ServerPID)
	}
	if len(rootPIDs) != 0 || len(config.ResourceCgroups) != 0 {
		result.CPUMethod = resourceSamplingMethod()
		go sampleResources(sampleContext, newResourceSampler(rootPIDs, config.ResourceCgroups), sampleResult)
	} else {
		sampleResult <- nil
	}
	start := time.Now()
	runErr := runner.runWindow(ctx, time.Duration(config.DurationSeconds*float64(time.Second)), true, persistentFlowIDs, &result)
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
	result.BlobsLoaded = runner.blobsLoaded.Load()
	if config.Scenario == "channel-wake" && result.Attempts > 0 {
		result.FlowStatePollsPerWake = float64(runner.flowStatePolls.Load()) / float64(result.Attempts)
	}
	if strings.HasPrefix(config.Scenario, "attribute-read") {
		result.VerifiedBytes = runner.expectedBytesPerRead
	}
	return result, runErr
}

func (runner *Runner) usesPersistentFlows() bool {
	return strings.HasPrefix(runner.config.Scenario, "rpc-") || strings.HasPrefix(runner.config.Scenario, "attribute-")
}

// startPersistentFlows starts one Flow per client before warmup, so warmup and measurement share them.
func (runner *Runner) startPersistentFlows(ctx context.Context) ([]string, error) {
	flowIDs := make([]string, runner.config.Concurrency)
	if !runner.usesPersistentFlows() {
		return flowIDs, nil
	}
	for index := range flowIDs {
		callContext, cancel := context.WithTimeout(ctx, runner.operationTimeout())
		flowID := runner.newFlowID()
		err := runner.startPersistentFlow(callContext, flowID)
		cancel()
		if err != nil {
			return flowIDs, fmt.Errorf("start persistent Flow for client %d: %w", index, err)
		}
		flowIDs[index] = flowID
	}
	return flowIDs, nil
}

func (runner *Runner) stopPersistentFlows(flowIDs []string, result *Result) {
	for _, flowID := range flowIDs {
		if flowID == "" {
			continue
		}
		stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := runner.client.StopFlow(stopContext, flowID, dex.StopOptions{}); err != nil {
			result.UnmeasuredErrors["cleanup: "+err.Error()]++
		}
		cancel()
	}
}

func (runner *Runner) operationTimeout() time.Duration {
	return time.Duration(runner.config.TimeoutSeconds * float64(time.Second))
}

func (runner *Runner) runWindow(ctx context.Context, duration time.Duration, isMeasured bool, flowIDs []string, result *Result) error {
	if duration <= 0 {
		return nil
	}
	var waitGroup sync.WaitGroup
	var mutex sync.Mutex
	latencies := make([]time.Duration, 0)
	var successes int64
	var attempts int64
	started := time.Now()
	deadline := started.Add(duration)
	for clientIndex := 0; clientIndex < runner.config.Concurrency; clientIndex++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			for time.Now().Before(deadline) && ctx.Err() == nil {
				operationContext, cancel := context.WithTimeout(ctx, runner.operationTimeout())
				operationStarted := time.Now()
				latency, err := runner.runOperation(operationContext, index, flowIDs[index])
				cancel()
				if latency == 0 {
					latency = time.Since(operationStarted)
				}
				mutex.Lock()
				switch {
				case !isMeasured && err != nil:
					result.UnmeasuredErrors["warmup: "+err.Error()]++
				case isMeasured && err != nil:
					attempts++
					result.Errors[err.Error()]++
				case isMeasured:
					attempts++
					successes++
					latencies = append(latencies, latency)
				}
				mutex.Unlock()
			}
		}(clientIndex)
	}
	waitGroup.Wait()
	elapsed := time.Since(started)
	if isMeasured {
		result.Attempts = attempts
		result.Successes = successes
		result.Latency = calculatePercentiles(latencies)
		result.MeasuredSeconds = elapsed.Seconds()
		result.ThroughputUnit = throughputUnit(runner.config.Scenario)
		factor := int64(1)
		if runner.config.Scenario == "sequential-steps" {
			factor = int64(runner.config.Steps)
		}
		// Operations that finish after the deadline still count, so divide by elapsed time.
		result.ThroughputPerSecond = float64(successes*factor) / elapsed.Seconds()
	}
	return ctx.Err()
}

func throughputUnit(scenario string) string {
	switch {
	case scenario == "sequential-steps":
		return "steps"
	case strings.HasPrefix(scenario, "rpc-"):
		return "rpcs"
	case strings.HasPrefix(scenario, "attribute-read"):
		return "reads"
	case strings.HasPrefix(scenario, "attribute-write"):
		return "writes"
	case scenario == "channel-wake":
		return "wakes"
	default:
		return "flows"
	}
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
		value := benchmarkReadValue(index, runner.config.AttributeBytes)
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

func (runner *Runner) runOperation(ctx context.Context, clientIndex int, persistentFlowID string) (time.Duration, error) {
	switch runner.config.Scenario {
	case "start-complete":
		flowID := runner.newFlowID()
		if _, err := runner.client.StartFlow(ctx, runner.completion, flowID, nil, dex.StartFlowOptions{}); err != nil {
			return 0, err
		}
		return 0, runner.waitForCompletion(ctx, flowID)
	case "sequential-steps":
		flowID := runner.newFlowID()
		input := chainInput{Remaining: runner.config.Steps}
		if _, err := runner.client.StartFlow(ctx, runner.chain, flowID, input, dex.StartFlowOptions{}); err != nil {
			return 0, err
		}
		return 0, runner.waitForCompletion(ctx, flowID)
	case "rpc-plain":
		var output int64
		return 0, runner.client.InvokeRPC(ctx, persistentFlowID, runner.rpc.GetCounter, nil, &output)
	case "rpc-locking":
		var output int64
		return 0, runner.client.InvokeRPC(ctx, persistentFlowID, runner.rpc.IncrementCounter, nil, &output)
	case "channel-wake":
		return runner.wakeWaitingFlow(ctx, clientIndex)
	case "attribute-read-rpc":
		var output attributeReadOutput
		if err := runner.client.InvokeRPC(ctx, persistentFlowID, runner.attribute.ReadAttributes, nil, &output); err != nil {
			return 0, err
		}
		if output.VerifiedValues != len(runner.expectedReadValues) || output.VerifiedBytes != runner.expectedBytesPerRead {
			return 0, fmt.Errorf("RPC verified %d values and %d bytes, expected %d and %d", output.VerifiedValues, output.VerifiedBytes, len(runner.expectedReadValues), runner.expectedBytesPerRead)
		}
		return 0, nil
	case "attribute-read-direct":
		return 0, runner.readAttributesDirect(ctx, persistentFlowID)
	case "attribute-write-local", "attribute-write-sync":
		value := &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: time.Now().UnixNano()}}
		write := &dexpb.AttributeWrite{Key: benchmarkWriteAttribute.AttributeName(), Value: value}
		if runner.config.Scenario == "attribute-write-sync" {
			write.SyncConfig = &dexpb.AttributeSyncConfig{Enabled: true}
		}
		_, err := runner.service.SetAttributes(ctx, &dexpb.SetAttributesRequest{FlowId: persistentFlowID, Attributes: []*dexpb.AttributeWrite{write}, RequestId: runner.newFlowID()})
		return 0, err
	default:
		return 0, fmt.Errorf("unknown scenario %s", runner.config.Scenario)
	}
}

func (runner *Runner) waitForCompletion(ctx context.Context, flowID string) error {
	result, err := runner.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	if err != nil {
		return err
	}
	if result.Status != dex.FlowCompleted {
		return fmt.Errorf("Flow status %v", result.Status)
	}
	return nil
}

// wakeWaitingFlow times PublishToChannel until the waiting Step's Execute starts.
// Starting the Flow and confirming it waits are not timed.
func (runner *Runner) wakeWaitingFlow(ctx context.Context, clientIndex int) (time.Duration, error) {
	flowID := runner.newFlowID()
	sequence := runner.nextWakeSequence.Add(1)
	if _, err := runner.client.StartFlow(ctx, runner.wake, flowID, wakeInput{Client: clientIndex, Sequence: sequence}, dex.StartFlowOptions{}); err != nil {
		return 0, err
	}
	if err := runner.waitForWaitForReturn(ctx, clientIndex, sequence); err != nil {
		return 0, err
	}
	if err := runner.waitUntilStepWaiting(ctx, flowID); err != nil {
		return 0, err
	}
	message := &dexpb.ChannelMessage{ChannelName: benchmarkWakeChannel.ChannelName(), Value: &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: sequence}}}
	publishedAt := time.Now()
	if _, err := runner.service.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{FlowId: flowID, Messages: []*dexpb.ChannelMessage{message}}); err != nil {
		return 0, err
	}
	signals := runner.wake.signals.channels[clientIndex]
	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("wait for woken Step: %w", ctx.Err())
		case signal := <-signals:
			if signal.sequence != sequence {
				continue
			}
			latency := signal.startedAt.Sub(publishedAt)
			if err := runner.waitForCompletion(ctx, flowID); err != nil {
				return 0, err
			}
			return latency, nil
		}
	}
}

// waitForWaitForReturn keeps GetFlowState polls, which are Temporal queries on the Go Server, near one per wake.
func (runner *Runner) waitForWaitForReturn(ctx context.Context, clientIndex int, sequence int64) error {
	waited := runner.wake.signals.waitedChannels[clientIndex]
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for WaitFor: %w", ctx.Err())
		case waitedSequence := <-waited:
			if waitedSequence == sequence {
				return nil
			}
		}
	}
}

func (runner *Runner) waitUntilStepWaiting(ctx context.Context, flowID string) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		runner.flowStatePolls.Add(1)
		state, err := runner.service.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		if err != nil {
			return fmt.Errorf("get Flow state: %w", err)
		}
		for _, execution := range state.GetActiveStepExecutions() {
			if execution.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for waiting Step: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// readAttributesDirect reads like an uncached client: GetAttributes, then one LoadBlobs for every reference.
func (runner *Runner) readAttributesDirect(ctx context.Context, flowID string) error {
	response, err := runner.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: flowID, AllKeys: true})
	if err != nil {
		return err
	}
	values := make(map[string]string, len(runner.expectedReadValues))
	pendingKeys := map[string]string{}
	var entries []*dexpb.LoadBlobRequestEntry
	for _, attribute := range response.GetAttributes() {
		if _, isRead := runner.expectedReadValues[attribute.GetKey()]; !isRead {
			continue
		}
		switch kind := attribute.GetValue().GetKind().(type) {
		case *dexpb.Value_StringValue:
			values[attribute.GetKey()] = kind.StringValue
		case *dexpb.Value_InternalBlobIdForStringValue:
			pendingKeys[kind.InternalBlobIdForStringValue] = attribute.GetKey()
			entries = append(entries, &dexpb.LoadBlobRequestEntry{FlowId: flowID, BlobValue: attribute.GetValue()})
		default:
			return fmt.Errorf("Attribute %s has unexpected value kind %T", attribute.GetKey(), kind)
		}
	}
	if len(entries) != 0 {
		loaded, err := runner.service.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Entries: entries})
		if err != nil {
			return fmt.Errorf("load blobs: %w", err)
		}
		for blobID, key := range pendingKeys {
			value, found := loaded.GetValues()[blobID]
			if !found {
				return fmt.Errorf("LoadBlobs omitted blob for Attribute %s", key)
			}
			stringValue, isString := value.GetKind().(*dexpb.Value_StringValue)
			if !isString {
				return fmt.Errorf("LoadBlobs returned %T for Attribute %s", value.GetKind(), key)
			}
			values[key] = stringValue.StringValue
		}
		runner.blobsLoaded.Add(int64(len(entries)))
	}
	var bytesRead int64
	for key, expected := range runner.expectedReadValues {
		if values[key] != expected {
			return fmt.Errorf("Attribute %s returned %d bytes that differ from the %d bytes written", key, len(values[key]), len(expected))
		}
		bytesRead += int64(len(values[key]))
	}
	if bytesRead != runner.expectedBytesPerRead {
		return fmt.Errorf("direct read %d bytes, expected %d", bytesRead, runner.expectedBytesPerRead)
	}
	return nil
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
	if runner.cacheDir != "" {
		combined = errors.Join(combined, os.RemoveAll(runner.cacheDir))
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

func (runner *Runner) closeCache() {
	if err := runner.cache.Close(); err != nil {
		slog.Error("close cache", "error", err)
	}
	removeDirectory(runner.cacheDir)
}

func removeDirectory(directory string) {
	if err := os.RemoveAll(directory); err != nil {
		slog.Error("remove directory", "directory", directory, "error", err)
	}
}

func (runner *Runner) newFlowID() string {
	return fmt.Sprintf("%s-%d", runner.runPrefix, runner.nextFlowID.Add(1))
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

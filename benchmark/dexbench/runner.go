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
	"math"
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
)

type Runner struct {
	config        Config
	connections   []*benchmarkConnection
	cache         *blobcache.Cache
	cacheDir      string
	completion    completionFlow
	chain         *chainFlow
	asyncChain    *asyncChainFlow
	rpc           rpcFlow
	attribute     *attributeFlow
	wake          *wakeFlow
	stepLatencies latencyCollector
	nextFlowID    atomic.Int64
	// wakeSequences holds the sequence each client publishes next; only that client's goroutine touches its entry.
	wakeSequences []int64
	// wakeNeedsStatePoll marks clients whose last wake failed; their WaitFor notice may already be consumed.
	wakeNeedsStatePoll   []bool
	blobsLoaded          atomic.Int64
	flowStatePolls       atomic.Int64
	runPrefix            string
	expectedReadValues   map[string]string
	expectedBytesPerRead int64
}

// benchmarkConnection is one Client and Worker pair, each with its own gRPC connections to the server.
type benchmarkConnection struct {
	client         *dex.Client
	grpcConnection *grpc.ClientConn
	service        dexpb.FlowServiceClient
	worker         *dex.Worker
	workerResult   chan error
}

func NewRunner(config Config) (*Runner, error) {
	if config.Address == "" || config.Concurrency < 1 || config.WorkerPool < 1 || config.Steps < 1 || config.DurationSeconds <= 0 || config.TimeoutSeconds <= 0 {
		return nil, fmt.Errorf("address, positive concurrency, worker pool, steps, duration, and timeout are required")
	}
	if config.Connections < 1 {
		config.Connections = 1
	}
	runner := &Runner{config: config, runPrefix: fmt.Sprintf("bench-%d", time.Now().UnixNano())}
	runner.chain = &chainFlow{latencies: &runner.stepLatencies}
	runner.asyncChain = &asyncChainFlow{latencies: &runner.stepLatencies}
	runner.wake = &wakeFlow{signals: newWakeSignals(config.Concurrency)}
	runner.wakeSequences = make([]int64, config.Concurrency)
	runner.wakeNeedsStatePoll = make([]bool, config.Concurrency)
	runner.expectedReadValues = make(map[string]string, len(benchmarkReadAttributes))
	for index, attribute := range benchmarkReadAttributes {
		value := benchmarkReadValue(index, config.AttributeBytes)
		runner.expectedReadValues[attribute.AttributeName()] = value
		runner.expectedBytesPerRead += int64(len(value))
	}
	runner.attribute = &attributeFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{runner.completion, runner.chain, runner.asyncChain, runner.rpc, runner.attribute, runner.wake})
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
	// Worker task slots are split across connections, rounded up so none is left without slots.
	tasksPerWorker := (config.WorkerPool + config.Connections - 1) / config.Connections
	runner.config.WorkerPool = tasksPerWorker * config.Connections
	for index := 0; index < config.Connections; index++ {
		connection, err := runner.openConnection(registry, tasksPerWorker)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("open connection %d: %w", index, err), runner.Close())
		}
		runner.connections = append(runner.connections, connection)
	}
	return runner, nil
}

func (runner *Runner) openConnection(registry *dex.Registry, tasksPerWorker int) (*benchmarkConnection, error) {
	worker, err := dex.NewWorker(registry, runner.cache, dex.WorkerOptions{
		FlowServiceAddress: runner.config.Address,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: tasksPerWorker, ReservedRPCTasks: ptr.Any(0)},
	})
	if err != nil {
		return nil, err
	}
	client, err := dex.NewClient(registry, runner.cache, dex.ClientOptions{FlowServiceAddress: runner.config.Address})
	if err != nil {
		stopWorker(worker)
		return nil, err
	}
	grpcConnection, err := grpc.NewClient(runner.config.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		stopWorker(worker)
		return nil, errors.Join(fmt.Errorf("connect FlowService: %w", err), client.Close())
	}
	connection := &benchmarkConnection{
		client:         client,
		grpcConnection: grpcConnection,
		service:        dexpb.NewFlowServiceClient(grpcConnection),
		worker:         worker,
		workerResult:   make(chan error, 1),
	}
	go func() { connection.workerResult <- worker.Start() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("pull Worker registration: %w", err), connection.close())
	}
	return connection, nil
}

// connectionFor spreads clients round-robin across connections.
func (runner *Runner) connectionFor(clientIndex int) *benchmarkConnection {
	return runner.connections[clientIndex%len(runner.connections)]
}

func benchmarkReadValue(index, size int) string {
	return fmt.Sprintf("%02d-%s", index, strings.Repeat("x", size))
}

// Run measures one point. It fails when a persistent Flow cannot be confirmed stopped, so no live run leaks into the next point.
func (runner *Runner) Run(ctx context.Context) (result Result, err error) {
	config := runner.config
	result = Result{SchemaVersion: 2, StartedAt: time.Now().UTC(), Config: config, Machine: machineInfo(), Errors: map[string]int64{}, UnmeasuredErrors: map[string]int64{}, SDKVersion: sdkVersion(), ServerVersion: os.Getenv("DEXBENCH_SERVER_VERSION")}
	if result.ServerVersion == "" {
		result.ServerVersion = "unknown"
	}
	persistentFlowIDs, err := runner.startPersistentFlows(ctx)
	defer func() {
		if cleanupErr := runner.stopPersistentFlows(persistentFlowIDs, &result); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
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
	result.CPUMethod = resourceSamplingMethod()
	if len(rootPIDs) != 0 {
		go sampleResources(sampleContext, newResourceSampler(rootPIDs), sampleResult)
	} else {
		sampleResult <- nil
	}
	loadHostResult := make(chan []ResourceSample, 1)
	go sampleResources(sampleContext, newResourceSampler([]int{os.Getpid()}), loadHostResult)
	start := time.Now()
	runErr := runner.runWindow(ctx, time.Duration(config.DurationSeconds*float64(time.Second)), true, persistentFlowIDs, &result)
	actualDuration := time.Since(start)
	stopSampling()
	result.MeasurementStartedAt = start.UTC()
	result.MeasurementEndedAt = start.Add(actualDuration).UTC()
	result.Resources = <-sampleResult
	result.LoadHostResources = <-loadHostResult
	metricsContext, cancelMetrics = context.WithTimeout(ctx, 5*time.Second)
	afterMetrics, metricsErr := scrapeMetrics(metricsContext, config.MetricsURL)
	cancelMetrics()
	if metricsErr != nil {
		return result, fmt.Errorf("scrape metrics after run: %w", metricsErr)
	}
	result.ObjectStore = metricDifference(beforeMetrics, afterMetrics, actualDuration)
	if !isSequentialStepsScenario(config.Scenario) {
		stepSamples := runner.stepLatencies.take()
		result.StepSamples = len(stepSamples)
		result.StepLatency = calculatePercentiles(stepSamples)
	}
	result.BlobsLoaded = runner.blobsLoaded.Load()
	if config.Scenario == "channel-wake" && result.Attempts > 0 {
		result.FlowStatePollsPerWake = float64(runner.flowStatePolls.Load()) / float64(result.Attempts)
	}
	if strings.HasPrefix(config.Scenario, "attribute-read") {
		result.VerifiedBytes = runner.expectedBytesPerRead
	}
	return result, runErr
}

// usesPersistentFlows reports whether each client works on one long-lived run; only start-complete starts a run per operation.
func (runner *Runner) usesPersistentFlows() bool {
	return runner.config.Scenario != "start-complete"
}

// persistentFlowSetupParallelism bounds the concurrent StartFlow and StopFlow calls around a point.
const persistentFlowSetupParallelism = 64

// startPersistentFlows starts one Flow per client before warmup, so warmup and measurement share them.
func (runner *Runner) startPersistentFlows(ctx context.Context) ([]string, error) {
	flowIDs := make([]string, runner.config.Concurrency)
	if !runner.usesPersistentFlows() {
		return flowIDs, nil
	}
	errs := make([]error, len(flowIDs))
	runner.forEachClientInParallel(len(flowIDs), func(index int) {
		callContext, cancel := context.WithTimeout(ctx, runner.operationTimeout())
		defer cancel()
		flowID := runner.newFlowID()
		// IDs are client-chosen, so cleanup also stops a Flow whose StartFlow response was lost.
		flowIDs[index] = flowID
		if err := runner.startPersistentFlow(callContext, index, flowID); err != nil {
			errs[index] = fmt.Errorf("start persistent Flow for client %d: %w", index, err)
		}
	})
	return flowIDs, errors.Join(errs...)
}

// persistentFlowStopAttempts is how many times cleanup tries to stop one Flow before the point fails.
const persistentFlowStopAttempts = 3

// stopPersistentFlows stops every persistent Flow and returns an error when any of them may still be running.
func (runner *Runner) stopPersistentFlows(flowIDs []string, result *Result) error {
	var mutex sync.Mutex
	errs := make([]error, len(flowIDs))
	runner.forEachClientInParallel(len(flowIDs), func(index int) {
		if flowIDs[index] == "" {
			return
		}
		err := runner.stopPersistentFlow(index, flowIDs[index])
		if err == nil {
			return
		}
		errs[index] = fmt.Errorf("stop persistent Flow %s: %w", flowIDs[index], err)
		mutex.Lock()
		result.UnmeasuredErrors["cleanup: "+err.Error()]++
		mutex.Unlock()
	})
	return errors.Join(errs...)
}

// stopPersistentFlow stops one Flow and waits until it is closed, so it cannot overlap the next point.
func (runner *Runner) stopPersistentFlow(clientIndex int, flowID string) error {
	return stopAndConfirmFlow(runner.connectionFor(clientIndex).client, flowID, persistentFlowStopTimeout, persistentFlowCloseWait)
}

// persistentFlowStopper is the part of the Client that cleanup needs.
type persistentFlowStopper interface {
	StopFlow(ctx context.Context, flowID string, options dex.StopOptions) error
	WaitForFlow(ctx context.Context, flowID string, options dex.WaitForFlowOptions) (dex.FlowResult, error)
}

const (
	// persistentFlowStopTimeout bounds one StopFlow call.
	persistentFlowStopTimeout = 10 * time.Second
	// persistentFlowStopRetryDelay grows linearly between StopFlow retries.
	persistentFlowStopRetryDelay = 200 * time.Millisecond
	// persistentFlowCloseWait bounds the wait for a stopped Flow to reach a terminal state.
	persistentFlowCloseWait = 30 * time.Second
)

// stopAndConfirmFlow retries StopFlow, then waits until the Flow is terminal.
// A Flow that is already closed or was never created counts as stopped.
// It returns an error when the Flow may still be running.
func stopAndConfirmFlow(stopper persistentFlowStopper, flowID string, stopTimeout, closeWait time.Duration) error {
	var err error
	for attempt := range persistentFlowStopAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * persistentFlowStopRetryDelay)
		}
		stopContext, cancel := context.WithTimeout(context.Background(), stopTimeout)
		err = stopper.StopFlow(stopContext, flowID, dex.StopOptions{})
		cancel()
		var notActive *dex.FlowNotActiveOrNotFoundError
		if errors.As(err, &notActive) {
			return nil
		}
		var closing *dex.FlowClosingError
		if err == nil || errors.As(err, &closing) {
			// StopFlow returns once the stop is accepted, before the Flow closes.
			return waitForFlowClosed(stopper, flowID, closeWait)
		}
	}
	return err
}

// waitForFlowClosed polls WaitForFlow until the Flow is terminal or closeWait passes.
func waitForFlowClosed(stopper persistentFlowStopper, flowID string, closeWait time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	var lastErr error
	for {
		result, err := stopper.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
		var notActive *dex.FlowNotActiveOrNotFoundError
		var notFound *dex.FlowNotFoundError
		switch {
		case err == nil && result.IsTerminal():
			return nil
		case errors.As(err, &notActive) || errors.As(err, &notFound):
			return nil
		case err != nil:
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Flow %s was not closed within %v after its stop was accepted: %w", flowID, closeWait, errors.Join(ctx.Err(), lastErr))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// forEachClientInParallel calls work for every client index with bounded parallelism and returns when all finish.
func (runner *Runner) forEachClientInParallel(clients int, work func(index int)) {
	indexes := make(chan int)
	var waitGroup sync.WaitGroup
	for range min(clients, persistentFlowSetupParallelism) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range indexes {
				work(index)
			}
		}()
	}
	for index := range clients {
		indexes <- index
	}
	close(indexes)
	waitGroup.Wait()
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
	if isSequentialStepsScenario(runner.config.Scenario) {
		return runner.waitForChainWindow(ctx, started, deadline, isMeasured, result)
	}
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
		// Operations that finish after the deadline still count, so divide by elapsed time.
		result.ThroughputPerSecond = float64(successes) / elapsed.Seconds()
	}
	return ctx.Err()
}

// waitForChainWindow lets the long-lived chain runs execute Steps until the deadline; clients send nothing.
// A measured window counts only the Steps the Worker starts between its two boundary snapshots.
func (runner *Runner) waitForChainWindow(ctx context.Context, started, deadline time.Time, isMeasured bool, result *Result) error {
	runner.stepLatencies.take()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	stepSamples := runner.stepLatencies.take()
	elapsed := time.Since(started)
	if isMeasured {
		// One operation of a long-lived chain is one Step, measured from the previous Step's start.
		result.MeasuredSeconds = elapsed.Seconds()
		result.ThroughputUnit = throughputUnit(runner.config.Scenario)
		result.StepSamples = len(stepSamples)
		result.StepLatency = calculatePercentiles(stepSamples)
		result.Latency = result.StepLatency
		result.Attempts = int64(len(stepSamples))
		result.Successes = result.Attempts
		result.ThroughputPerSecond = float64(len(stepSamples)) / elapsed.Seconds()
	}
	return ctx.Err()
}

func throughputUnit(scenario string) string {
	switch {
	case isSequentialStepsScenario(scenario):
		return "steps"
	case strings.HasPrefix(scenario, "rpc-"):
		return "rpcs"
	case strings.HasPrefix(scenario, "attribute-read"):
		return "reads"
	case scenario == "channel-wake":
		return "wakes"
	default:
		return "flows"
	}
}

func isSequentialStepsScenario(scenario string) bool {
	return scenario == "sequential-steps" || scenario == "sequential-steps-async"
}

func (runner *Runner) startPersistentFlow(ctx context.Context, clientIndex int, flowID string) error {
	connection := runner.connectionFor(clientIndex)
	switch runner.config.Scenario {
	case "sequential-steps":
		_, err := connection.client.StartFlow(ctx, runner.chain, flowID, chainInput{Remaining: math.MaxInt}, dex.StartFlowOptions{})
		return err
	case "sequential-steps-async":
		_, err := connection.client.StartFlow(ctx, runner.asyncChain, flowID, chainInput{Remaining: math.MaxInt}, dex.StartFlowOptions{})
		return err
	case "channel-wake":
		runner.wakeSequences[clientIndex] = 1
		_, err := connection.client.StartFlow(ctx, runner.wake, flowID, wakeInput{Client: clientIndex}, dex.StartFlowOptions{})
		return err
	}
	if strings.HasPrefix(runner.config.Scenario, "rpc-") {
		initialCounter, err := dex.InitialAttribute(benchmarkRPCCounter, int64(0))
		if err != nil {
			return err
		}
		_, err = connection.client.StartFlow(ctx, runner.rpc, flowID, nil, dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{initialCounter}})
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
	_, err := connection.client.StartFlow(ctx, runner.attribute, flowID, nil, dex.StartFlowOptions{Attributes: initialAttributes})
	return err
}

func (runner *Runner) runOperation(ctx context.Context, clientIndex int, persistentFlowID string) (time.Duration, error) {
	connection := runner.connectionFor(clientIndex)
	switch runner.config.Scenario {
	case "start-complete":
		flowID := runner.newFlowID()
		// Short runs keep their history local, so closing one uploads no history segment.
		options := dex.StartFlowOptions{ConfigOverride: &dex.FlowConfig{OffloadHistory: ptr.Any(false)}}
		if _, err := connection.client.StartFlow(ctx, runner.completion, flowID, nil, options); err != nil {
			return 0, err
		}
		return 0, runner.waitForCompletion(ctx, connection, flowID)
	case "rpc-plain":
		var output int64
		return 0, connection.client.InvokeRPC(ctx, persistentFlowID, runner.rpc.GetCounter, nil, &output)
	case "rpc-locking":
		var output int64
		return 0, connection.client.InvokeRPC(ctx, persistentFlowID, runner.rpc.IncrementCounter, nil, &output)
	case "channel-wake":
		return runner.wakeWaitingFlow(ctx, connection, clientIndex, persistentFlowID)
	case "attribute-read-direct":
		return 0, runner.readAttributesDirect(ctx, connection, persistentFlowID)
	default:
		return 0, fmt.Errorf("unknown scenario %s", runner.config.Scenario)
	}
}

func (runner *Runner) waitForCompletion(ctx context.Context, connection *benchmarkConnection, flowID string) error {
	result, err := connection.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	if err != nil {
		return err
	}
	if result.Status != dex.FlowCompleted {
		return fmt.Errorf("Flow status %v", result.Status)
	}
	return nil
}

// wakeWaitingFlow times PublishToChannel until the waiting Step's Execute starts.
// Confirming that the client's long-lived run waits again is not timed.
// Every attempt publishes a new sequence, so a failed or timed-out attempt never leaves the client waiting for a stale wake.
func (runner *Runner) wakeWaitingFlow(ctx context.Context, connection *benchmarkConnection, clientIndex int, flowID string) (time.Duration, error) {
	latency, err := runner.doWakeWaitingFlow(ctx, connection, clientIndex, flowID)
	// After a failure the notice of the current wait may be consumed, so the next attempt polls state at once.
	runner.wakeNeedsStatePoll[clientIndex] = err != nil
	return latency, err
}

func (runner *Runner) doWakeWaitingFlow(ctx context.Context, connection *benchmarkConnection, clientIndex int, flowID string) (time.Duration, error) {
	sequence := runner.wakeSequences[clientIndex]
	runner.wakeSequences[clientIndex] = sequence + 1
	if !runner.wakeNeedsStatePoll[clientIndex] {
		if err := waitForWakeNotice(ctx, runner.wake.signals.waitedChannels[clientIndex], wakeNoticeWait); err != nil {
			return 0, err
		}
	}
	if err := runner.waitUntilStepWaiting(ctx, connection, flowID); err != nil {
		return 0, err
	}
	// The worker sends a notice before the server records WAITING, so every queued notice is for this wait or older.
	drainWakeNotices(runner.wake.signals.waitedChannels[clientIndex])
	message := &dexpb.ChannelMessage{ChannelName: benchmarkWakeChannel.ChannelName(), Value: &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: sequence}}}
	publishedAt := time.Now()
	if _, err := connection.service.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{FlowId: flowID, Messages: []*dexpb.ChannelMessage{message}}); err != nil {
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
			return signal.startedAt.Sub(publishedAt), nil
		}
	}
}

// wakeNoticeWait bounds the wait for a WaitFor notice; after an error the notice may already be consumed.
const wakeNoticeWait = 2 * time.Second

// waitForWakeNotice keeps GetFlowState polls, which are server state reads, near one per wake.
// It waits at most maxWait and at most a quarter of the time left before ctx's deadline, so a missing notice
// still leaves time to poll and publish. waitUntilStepWaiting confirms the wait either way.
func waitForWakeNotice(ctx context.Context, notices <-chan struct{}, maxWait time.Duration) error {
	wait := maxWait
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		wait = min(wait, time.Until(deadline)/4)
	}
	timer := time.NewTimer(max(wait, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for WaitFor: %w", ctx.Err())
	case <-notices:
	case <-timer.C:
	}
	return nil
}

// drainWakeNotices discards queued WaitFor notices without blocking, keeping one notice per wake.
func drainWakeNotices(notices <-chan struct{}) {
	for {
		select {
		case <-notices:
		default:
			return
		}
	}
}

func (runner *Runner) waitUntilStepWaiting(ctx context.Context, connection *benchmarkConnection, flowID string) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		runner.flowStatePolls.Add(1)
		state, err := connection.service.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
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

// readAttributesDirect reads like an uncached client: GetAttributes, then ordered LoadBlobs batches for every reference.
func (runner *Runner) readAttributesDirect(ctx context.Context, connection *benchmarkConnection, flowID string) error {
	response, err := connection.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: flowID, AllKeys: true})
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
	for start := 0; start < len(entries); start += 1000 {
		batch := entries[start:min(start+1000, len(entries))]
		loaded, err := connection.service.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Entries: batch})
		if err != nil {
			return fmt.Errorf("load blobs: %w", err)
		}
		for _, entry := range batch {
			blobID := entry.GetBlobValue().GetInternalBlobIdForStringValue()
			key := pendingKeys[blobID]
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
		runner.blobsLoaded.Add(int64(len(batch)))
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

func (runner *Runner) Close() error {
	var combined error
	for _, connection := range runner.connections {
		combined = errors.Join(combined, connection.close())
	}
	runner.connections = nil
	if runner.cache != nil {
		combined = errors.Join(combined, runner.cache.Close())
	}
	if runner.cacheDir != "" {
		combined = errors.Join(combined, os.RemoveAll(runner.cacheDir))
	}
	return combined
}

func (connection *benchmarkConnection) close() error {
	combined := errors.Join(connection.grpcConnection.Close(), connection.client.Close())
	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	combined = errors.Join(combined, connection.worker.Stop(stopContext))
	cancel()
	select {
	case err := <-connection.workerResult:
		combined = errors.Join(combined, err)
	case <-time.After(30 * time.Second):
		combined = errors.Join(combined, fmt.Errorf("Worker did not stop in 30 seconds"))
	}
	return combined
}

func stopWorker(worker *dex.Worker) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := worker.Stop(ctx); err != nil {
		slog.Error("stop Worker", "error", err)
	}
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

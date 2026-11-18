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
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Phase names shared by the slot stream scenarios and their summaries.
const (
	slotPhaseBaseline = "baseline"
	slotPhaseOpen     = "open"
	slotPhaseSettle   = "settle"
	slotPhaseIdle     = "idle"
	slotPhaseProbe    = "probe"
	slotPhaseRestart  = "restart"
	slotPhaseEnqueue  = "enqueue"
	slotPhaseDrain    = "drain"
)

const (
	// defaultIdleSlotsPerWorker is matching.maxConcurrentTasksCap's default, the most slots one Worker may hold.
	defaultIdleSlotsPerWorker = 256
	// defaultCeilingSlotsPerWorker is the design's adaptive ceiling E: 100 slots minus one reserved RPC slot.
	defaultCeilingSlotsPerWorker = 99
	defaultFairnessWorkers       = 8
	// maxPerWorkerSampleWorkers bounds the per-second per-Worker busy series, which grows with Workers.
	maxPerWorkerSampleWorkers = 64
	// workerOpenParallelism bounds concurrent Worker starts.
	workerOpenParallelism = 32
	// startQueueCapacity bounds scheduled StartFlow calls; an open-loop start beyond it is counted as skipped.
	startQueueCapacity = 1 << 16
	// syncMatchAllowance exceeds matching.syncMatchWait (200 ms), after which an unmatched Step is enqueued.
	syncMatchAllowance = time.Second
	// restartDownTimeout bounds the wait, after the restart command returns, for a sign of the new server.
	restartDownTimeout = time.Minute
)

// errAdaptiveSlotsUnsupported keeps the adaptive path off until the Go SDK has adaptive slot streams.
var errAdaptiveSlotsUnsupported = errors.New("adaptive slot streams need PullWorkerOptions.MinOpenSlots, which this Go SDK does not have; " +
	"set it in slotWorkerPullOptions once the SDK supports it")

type slotScenarioKind int

const (
	slotScenarioIdle slotScenarioKind = iota + 1
	slotScenarioBurst
	slotScenarioFairness
	slotScenarioPeriodic
	slotScenarioBacklog
)

// slotScenario is what a variant name fixes; zero fields fall back to SlotStreamConfig and scenario defaults.
type slotScenario struct {
	kind            slotScenarioKind
	flowTypes       int
	handlerDuration time.Duration
	durability      string
}

// SlotStreamVariants returns the variants a slot stream scenario name runs, or nil when name is not one.
func SlotStreamVariants(name string) []string {
	switch name {
	case "idle-slots":
		return []string{"idle-slots-f1", "idle-slots-f16"}
	case "burst":
		return []string{"burst-async", "burst-sync"}
	case "fairness", "periodic":
		return []string{name}
	case "backlog-one-partition":
		return []string{"backlog-one-partition-10ms", "backlog-one-partition-2s"}
	}
	return nil
}

// IsSlotStreamScenario reports whether variant runs through SlotStreamRunner.
func IsSlotStreamScenario(variant string) bool {
	_, err := parseSlotScenario(variant)
	return err == nil
}

func parseSlotScenario(variant string) (slotScenario, error) {
	switch variant {
	case "idle-slots-f1":
		return slotScenario{kind: slotScenarioIdle, flowTypes: 1}, nil
	case "idle-slots-f16":
		return slotScenario{kind: slotScenarioIdle, flowTypes: 16}, nil
	case "burst-async":
		return slotScenario{kind: slotScenarioBurst, durability: "async"}, nil
	case "burst-sync":
		return slotScenario{kind: slotScenarioBurst, durability: "sync"}, nil
	case "fairness":
		return slotScenario{kind: slotScenarioFairness}, nil
	case "periodic":
		return slotScenario{kind: slotScenarioPeriodic}, nil
	case "backlog-one-partition-10ms":
		return slotScenario{kind: slotScenarioBacklog, handlerDuration: 10 * time.Millisecond}, nil
	case "backlog-one-partition-2s":
		return slotScenario{kind: slotScenarioBacklog, handlerDuration: 2 * time.Second}, nil
	}
	return slotScenario{}, fmt.Errorf("unknown slot stream scenario %q", variant)
}

// slotStreamSettings are the resolved settings of one point.
type slotStreamSettings struct {
	scenario        slotScenario
	workerSlots     []int
	flowTypes       int
	handlerDuration time.Duration
	durability      dex.StepDurability
}

func (settings slotStreamSettings) totalSlots() int {
	total := 0
	for _, slots := range settings.workerSlots {
		total += slots
	}
	return total
}

// resolveSlotStreamSettings applies scenario defaults, writes them back into config for the record, and validates.
func resolveSlotStreamSettings(config *Config) (slotStreamSettings, error) {
	scenario, err := parseSlotScenario(config.Scenario)
	if err != nil {
		return slotStreamSettings{}, err
	}
	if config.SlotStreams == nil {
		config.SlotStreams = &SlotStreamConfig{}
	}
	options := config.SlotStreams
	if options.IsAdaptiveSlots || options.MinOpenSlots > 0 {
		return slotStreamSettings{}, errAdaptiveSlotsUnsupported
	}
	if config.Address == "" || config.Concurrency < 1 || config.DurationSeconds <= 0 || config.TimeoutSeconds <= 0 {
		return slotStreamSettings{}, errors.New("address, positive concurrency, duration, and timeout are required")
	}
	if options.StartParallelism < 1 || options.ClientConnections < 1 {
		return slotStreamSettings{}, errors.New("start parallelism and client connections must be positive")
	}
	settings := slotStreamSettings{scenario: scenario}
	switch scenario.kind {
	case slotScenarioIdle:
		if config.MetricsURL == "" {
			return slotStreamSettings{}, errors.New("idle-slots needs --metrics-url to count registered streams")
		}
		settings.workerSlots = splitSlots(config.Concurrency, defaultedInt(options.SlotsPerWorker, defaultIdleSlotsPerWorker))
	case slotScenarioBurst, slotScenarioPeriodic:
		settings.workerSlots = splitSlotsEvenly(config.Concurrency, options.Workers, defaultedInt(options.SlotsPerWorker, defaultCeilingSlotsPerWorker))
	case slotScenarioFairness:
		settings.workerSlots = splitSlotsEvenly(config.Concurrency, defaultedInt(options.Workers, defaultFairnessWorkers), 0)
	case slotScenarioBacklog:
		settings.workerSlots = []int{defaultedInt(options.SlotsPerWorker, defaultCeilingSlotsPerWorker)}
	}
	settings.flowTypes = defaultedInt(scenario.flowTypes, defaultedInt(options.FlowTypes, 1))
	settings.handlerDuration = scenario.handlerDuration
	if settings.handlerDuration == 0 {
		settings.handlerDuration = time.Duration(options.HandlerMilliseconds * float64(time.Millisecond))
	}
	if settings.handlerDuration == 0 {
		settings.handlerDuration = defaultSlotHandlerDuration(scenario.kind)
	}
	durability := scenario.durability
	if durability == "" {
		durability = defaultedString(options.StepDurability, "async")
	}
	switch durability {
	case "async":
		settings.durability = dex.StepDurabilityAsync
	case "sync":
		settings.durability = dex.StepDurabilitySync
	default:
		return slotStreamSettings{}, fmt.Errorf("step durability %q is not async or sync", durability)
	}
	if options.LoadWave != "square" && options.LoadWave != "sine" {
		return slotStreamSettings{}, fmt.Errorf("load wave %q is not square or sine", options.LoadWave)
	}
	options.Workers = len(settings.workerSlots)
	options.SlotsPerWorker = settings.workerSlots[0]
	options.FlowTypes = settings.flowTypes
	options.HandlerMilliseconds = float64(settings.handlerDuration) / float64(time.Millisecond)
	options.StepDurability = durability
	return settings, nil
}

func defaultSlotHandlerDuration(kind slotScenarioKind) time.Duration {
	switch kind {
	case slotScenarioIdle:
		return 10 * time.Millisecond
	case slotScenarioBurst:
		return time.Second
	default:
		return 100 * time.Millisecond
	}
}

// splitSlots gives each Worker perWorker slots and the last one the remainder, so the total is exactly total.
func splitSlots(total, perWorker int) []int {
	var slots []int
	for remaining := total; remaining > 0; remaining -= perWorker {
		slots = append(slots, min(perWorker, remaining))
	}
	return slots
}

// splitSlotsEvenly splits exactly total slots over workers, the first total % workers Workers getting one more.
// With workers 0 it derives the count from perWorker; it never uses more Workers than slots.
func splitSlotsEvenly(total, workers, perWorker int) []int {
	if workers < 1 {
		workers = (total + perWorker - 1) / perWorker
	}
	workers = min(workers, total)
	slots := make([]int, workers)
	for index := range slots {
		slots[index] = total / workers
		if index < total%workers {
			slots[index]++
		}
	}
	return slots
}

func defaultedInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultedString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// SlotStreamRunner runs one point of a slot stream scenario: open-loop probe Flows against pull Workers with fixed slots.
type SlotStreamRunner struct {
	config          Config
	settings        slotStreamSettings
	cache           *blobcache.Cache
	cacheDir        string
	recorder        *slotProbeRecorder
	clients         []*dex.Client
	clientFlows     []dex.Flow
	workers         []*slotWorker
	flowIDGenerator *partitionFlowIDGenerator
	runPrefix       string
	flowIDMutex     sync.Mutex
	flowIDCounter   int64
	// startJobs carries the planned start time of each queued probe Flow.
	startJobs chan time.Time
	startPool sync.WaitGroup
	isClosing atomic.Bool
	nextStart atomic.Int64
	// plannedStarts counts every due start, including skipped ones; pendingStarts those not yet returned.
	plannedStarts    atomic.Int64
	pendingStarts    atomic.Int64
	successfulStarts atomic.Int64
	failedStarts     atomic.Int64
	startsSkipped    atomic.Int64
	lateSubmissions  atomic.Int64
	maxSchedulerLag  atomic.Int64
	errorsMutex      sync.Mutex
	startErrors      map[string]int64
	unmeasuredErrors map[string]int64
	offeredRateBits  atomic.Uint64
	phaseMutex       sync.Mutex
	phaseName        string
	phaseStartedAt   time.Time
	phasePlanned     int64
	phases           []SlotStreamPhase
	// phaseBuckets holds each phase's recorder bucket, parallel to phases.
	phaseBuckets    []int
	samplesMutex    sync.Mutex
	samples         []SlotStreamSample
	baselineStreams float64
	// metricsBeforeRestart replaces the final scrape, because a restart resets the server counters.
	metricsBeforeRestart *slotServerMetrics
	idle                 *IdleStreamCost
	restart              *StreamRestart
	backlog              *BacklogDrain
}

// slotWorker is one pull Worker and the result of its Start.
type slotWorker struct {
	worker *dex.Worker
	result chan error
}

func NewSlotStreamRunner(config Config) (*SlotStreamRunner, error) {
	settings, err := resolveSlotStreamSettings(&config)
	if err != nil {
		return nil, err
	}
	runner := &SlotStreamRunner{
		config:           config,
		settings:         settings,
		recorder:         newSlotProbeRecorder(len(settings.workerSlots)),
		runPrefix:        fmt.Sprintf("slots-%d", time.Now().UnixNano()),
		startJobs:        make(chan time.Time, startQueueCapacity),
		startErrors:      map[string]int64{},
		unmeasuredErrors: map[string]int64{},
	}
	if key := config.SlotStreams.PartitionHashKey; key != "" {
		runner.flowIDGenerator, err = newPartitionFlowIDGenerator(key, config.SlotStreams.PartitionCount, config.SlotStreams.PinnedPartition)
		if err != nil {
			return nil, err
		}
	}
	cacheDir, err := os.MkdirTemp("", "dexbench-slot-cache-")
	if err != nil {
		return nil, err
	}
	runner.cacheDir = cacheDir
	runner.cache, err = blobcache.New(&blobcache.Config{Dir: cacheDir, MaxBytes: 64 << 20, Logger: slog.Default()})
	if err != nil {
		removeDirectory(cacheDir)
		return nil, err
	}
	runner.clientFlows = runner.probeFlows(-1, nil)
	clientRegistry, err := dex.NewRegistry(runner.clientFlows)
	if err != nil {
		return nil, errors.Join(err, runner.Close())
	}
	for index := 0; index < config.SlotStreams.ClientConnections; index++ {
		client, err := dex.NewClient(clientRegistry, runner.cache, dex.ClientOptions{FlowServiceAddress: config.Address})
		if err != nil {
			return nil, errors.Join(fmt.Errorf("open Client %d: %w", index, err), runner.Close())
		}
		runner.clients = append(runner.clients, client)
	}
	for range config.SlotStreams.StartParallelism {
		runner.startPool.Add(1)
		go runner.runStartFlowCalls()
	}
	return runner, nil
}

// probeFlows returns one probe Flow per Flow type for workerIndex; the client registry passes -1 and no recorder.
func (runner *SlotStreamRunner) probeFlows(workerIndex int, recorder *slotProbeRecorder) []dex.Flow {
	flows := make([]dex.Flow, runner.settings.flowTypes)
	for index := range flows {
		flows[index] = newSlotProbeFlow(index, workerIndex, runner.settings.handlerDuration, runner.settings.durability, recorder)
	}
	return flows
}

// Run measures the point. The returned result is complete even when err is not nil.
func (runner *SlotStreamRunner) Run(ctx context.Context) (result Result, err error) {
	config := runner.config
	result = Result{SchemaVersion: 2, StartedAt: time.Now().UTC(), Config: config, Machine: machineInfo(), Errors: map[string]int64{},
		UnmeasuredErrors: map[string]int64{}, SDKVersion: sdkVersion(), ServerVersion: defaultedString(os.Getenv("DEXBENCH_SERVER_VERSION"), "unknown")}
	var metricsBefore slotServerMetrics
	if config.MetricsURL != "" {
		metricsBefore, err = runner.scrapeSlotMetricsOnce(ctx)
		if err != nil {
			return result, fmt.Errorf("scrape metrics before run: %w", err)
		}
		runner.baselineStreams = metricsBefore.workerStreams
	}
	samplingContext, stopSampling := context.WithCancel(ctx)
	serverResources := make(chan []ResourceSample, 1)
	rootPIDs := append([]int(nil), config.ResourcePIDs...)
	if config.ServerPID > 0 {
		rootPIDs = append(rootPIDs, config.ServerPID)
	}
	result.CPUMethod = resourceSamplingMethod()
	if len(rootPIDs) != 0 {
		go sampleResources(samplingContext, newResourceSampler(rootPIDs), serverResources)
	} else {
		serverResources <- nil
	}
	loadHostResources := make(chan []ResourceSample, 1)
	go sampleResources(samplingContext, newResourceSampler([]int{os.Getpid()}), loadHostResources)
	timeSeriesDone := make(chan struct{})
	go func() {
		defer close(timeSeriesDone)
		runner.sampleSlotTimeSeries(samplingContext)
	}()
	started := time.Now()
	runErr := runner.runScenario(ctx)
	elapsed := time.Since(started)
	stopSampling()
	<-timeSeriesDone
	result.Resources = <-serverResources
	result.LoadHostResources = <-loadHostResources
	result.MeasurementStartedAt = started.UTC()
	result.MeasurementEndedAt = started.Add(elapsed).UTC()
	result.MeasuredSeconds = elapsed.Seconds()
	runner.fillResult(&result, elapsed)
	if config.MetricsURL != "" {
		var metricsAfter slotServerMetrics
		var scrapeErr error
		if runner.metricsBeforeRestart != nil {
			metricsAfter = *runner.metricsBeforeRestart
		} else {
			metricsAfter, scrapeErr = runner.scrapeSlotMetricsOnce(ctx)
		}
		if scrapeErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("scrape metrics after run: %w", scrapeErr))
		} else {
			result.SlotStreams.ServerMetricsDelta = slotMetricsDelta(metricsBefore, metricsAfter)
		}
	}
	summarizeIdleStreamCost(&result)
	return result, runErr
}

func (runner *SlotStreamRunner) runScenario(ctx context.Context) error {
	switch runner.settings.scenario.kind {
	case slotScenarioIdle:
		return runner.runIdleSlots(ctx)
	case slotScenarioBurst:
		return runner.runBurst(ctx)
	case slotScenarioFairness:
		return runner.runFairness(ctx)
	case slotScenarioPeriodic:
		return runner.runPeriodic(ctx)
	case slotScenarioBacklog:
		return runner.runBacklog(ctx)
	}
	return fmt.Errorf("slot scenario kind %d has no runner", runner.settings.scenario.kind)
}

// runIdleSlots measures idle stream cost, a light probe's dispatch latency, and re-registration after a restart.
func (runner *SlotStreamRunner) runIdleSlots(ctx context.Context) error {
	options := runner.config.SlotStreams
	if err := runner.runPhase(slotPhaseBaseline, func() error { return sleepContext(ctx, seconds(options.BaselineSeconds)) }); err != nil {
		return err
	}
	runner.idle = &IdleStreamCost{Streams: runner.settings.totalSlots(),
		DerivedPingsPerSecond: float64(runner.settings.totalSlots()) / options.PingIntervalSeconds}
	openStarted := time.Now()
	if err := runner.runPhase(slotPhaseOpen, func() error { return runner.openWorkersAndWaitForStreams(ctx) }); err != nil {
		return err
	}
	runner.idle.OpenSeconds = time.Since(openStarted).Seconds()
	if err := runner.runPhase(slotPhaseSettle, func() error { return sleepContext(ctx, seconds(runner.config.WarmupSeconds)) }); err != nil {
		return err
	}
	if err := runner.runPhase(slotPhaseIdle, func() error { return sleepContext(ctx, seconds(runner.config.DurationSeconds)) }); err != nil {
		return err
	}
	registered, err := runner.scrapeSlotMetricsOnce(ctx)
	if err != nil {
		return fmt.Errorf("scrape streams after the idle phase: %w", err)
	}
	runner.idle.RegisteredStreams = registered.workerStreams
	probeRate := options.ProbeRatePerSecond
	if err := runner.runPhase(slotPhaseProbe, func() error {
		if err := runner.applyOpenLoopLoad(ctx, seconds(options.ProbeSeconds), func(time.Duration) float64 { return probeRate }); err != nil {
			return err
		}
		return runner.waitForDrain(ctx)
	}); err != nil {
		return err
	}
	if options.RestartCommand == "" {
		return nil
	}
	metricsBeforeRestart, err := runner.scrapeSlotMetricsOnce(ctx)
	if err != nil {
		return fmt.Errorf("scrape metrics before restart: %w", err)
	}
	runner.metricsBeforeRestart = &metricsBeforeRestart
	return runner.runPhase(slotPhaseRestart, func() error { return runner.restartServerAndWaitForStreams(ctx, metricsBeforeRestart) })
}

// runBurst steps from zero load to each burst level of in-flight Steps per Worker.
func (runner *SlotStreamRunner) runBurst(ctx context.Context) error {
	if err := runner.openAndSettle(ctx); err != nil {
		return err
	}
	options := runner.config.SlotStreams
	workers := float64(len(runner.settings.workerSlots))
	for _, level := range options.BurstLevels {
		if err := runner.runPhase(fmt.Sprintf("gap-before-%d", level), func() error {
			return sleepContext(ctx, seconds(options.IdleGapSeconds))
		}); err != nil {
			return err
		}
		// By Little's law this rate keeps level Steps per Worker in flight.
		rate := float64(level) * workers / runner.settings.handlerDuration.Seconds()
		if err := runner.runPhase(fmt.Sprintf("burst-%d-per-worker", level), func() error {
			return runner.applyOpenLoopLoad(ctx, seconds(runner.config.DurationSeconds), func(time.Duration) float64 { return rate })
		}); err != nil {
			return err
		}
	}
	return runner.runPhase(slotPhaseDrain, func() error { return runner.waitForDrain(ctx) })
}

// runFairness holds each load level, as a share of total slot capacity, on several Workers of one task queue.
func (runner *SlotStreamRunner) runFairness(ctx context.Context) error {
	if err := runner.openAndSettle(ctx); err != nil {
		return err
	}
	capacityRate := float64(runner.settings.totalSlots()) / runner.settings.handlerDuration.Seconds()
	for _, percent := range runner.config.SlotStreams.LoadPercents {
		rate := capacityRate * percent / 100
		if err := runner.runPhase(fmt.Sprintf("load-%gpct", percent), func() error {
			return runner.applyOpenLoopLoad(ctx, seconds(runner.config.DurationSeconds), func(time.Duration) float64 { return rate })
		}); err != nil {
			return err
		}
	}
	return runner.runPhase(slotPhaseDrain, func() error { return runner.waitForDrain(ctx) })
}

// runPeriodic applies a square or sine wave between two shares of total slot capacity.
func (runner *SlotStreamRunner) runPeriodic(ctx context.Context) error {
	if err := runner.openAndSettle(ctx); err != nil {
		return err
	}
	options := runner.config.SlotStreams
	capacityRate := float64(runner.settings.totalSlots()) / runner.settings.handlerDuration.Seconds()
	low, high := capacityRate*options.LoadLowPercent/100, capacityRate*options.LoadHighPercent/100
	period := seconds(options.LoadPeriodSeconds)
	wave := squareWaveRate
	if options.LoadWave == "sine" {
		wave = sineWaveRate
	}
	if err := runner.runPhase("periodic", func() error {
		return runner.applyOpenLoopLoad(ctx, seconds(runner.config.DurationSeconds), func(elapsed time.Duration) float64 {
			return wave(elapsed, period, low, high)
		})
	}); err != nil {
		return err
	}
	return runner.runPhase(slotPhaseDrain, func() error { return runner.waitForDrain(ctx) })
}

// runBacklog queues concurrency Steps with no Worker connected, then times one Worker draining them.
func (runner *SlotStreamRunner) runBacklog(ctx context.Context) error {
	queued := int64(runner.config.Concurrency)
	runner.backlog = &BacklogDrain{QueuedSteps: queued}
	if err := runner.runPhase(slotPhaseEnqueue, func() error {
		plannedAt := time.Now()
		for range queued {
			runner.submitStart(plannedAt, plannedAt)
		}
		if err := runner.waitForStartsReturned(ctx); err != nil {
			return err
		}
		if err := sleepContext(ctx, syncMatchAllowance); err != nil {
			return err
		}
		return runner.waitForQueuedSteps(ctx, float64(runner.successfulStarts.Load()))
	}); err != nil {
		return err
	}
	runner.recorder.resetStartBounds()
	drainStarted := time.Now()
	if err := runner.runPhase(slotPhaseDrain, func() error {
		if err := runner.openWorkers(ctx); err != nil {
			return err
		}
		return runner.waitForDrain(ctx)
	}); err != nil {
		return err
	}
	runner.backlog.DrainSeconds = time.Since(drainStarted).Seconds()
	drainWorkerSteps, _ := runner.recorder.phaseSteps(runner.phaseBuckets[len(runner.phaseBuckets)-1])
	var drainSteps int64
	for _, steps := range drainWorkerSteps {
		drainSteps += steps
	}
	firstStart, lastStart := runner.recorder.firstStartUnixNano.Load(), runner.recorder.lastStartUnixNano.Load()
	if firstStart != 0 {
		runner.backlog.FirstStartSeconds = time.Unix(0, firstStart).Sub(drainStarted).Seconds()
		// The first start opens the span, so the rate counts the starts after it.
		if span := time.Duration(lastStart - firstStart).Seconds(); span > 0 && drainSteps > 1 {
			runner.backlog.StartsPerSecond = float64(drainSteps-1) / span
		}
	}
	return nil
}

func (runner *SlotStreamRunner) openAndSettle(ctx context.Context) error {
	if err := runner.runPhase(slotPhaseOpen, func() error { return runner.openWorkersAndWaitForStreams(ctx) }); err != nil {
		return err
	}
	return runner.runPhase(slotPhaseSettle, func() error { return sleepContext(ctx, seconds(runner.config.WarmupSeconds)) })
}

// runPhase records a named phase around work; phases are contiguous, so every Step start belongs to one.
func (runner *SlotStreamRunner) runPhase(name string, work func() error) error {
	runner.beginPhase(name)
	err := work()
	runner.endPhase()
	if err != nil {
		return fmt.Errorf("phase %s: %w", name, err)
	}
	return nil
}

func (runner *SlotStreamRunner) beginPhase(name string) {
	runner.phaseMutex.Lock()
	defer runner.phaseMutex.Unlock()
	runner.phaseName = name
	runner.phaseStartedAt = time.Now()
	runner.phasePlanned = runner.plannedStarts.Load()
	runner.phaseBuckets = append(runner.phaseBuckets, runner.recorder.beginPhase())
}

// endPhase records the phase window; fillPhaseStepStats adds its Step counts and latencies at the end.
// Starts until the next beginPhase still land in this phase's bucket.
func (runner *SlotStreamRunner) endPhase() {
	runner.phaseMutex.Lock()
	defer runner.phaseMutex.Unlock()
	ended := time.Now()
	runner.phases = append(runner.phases, SlotStreamPhase{
		Name:                 runner.phaseName,
		StartedAt:            runner.phaseStartedAt.UTC(),
		EndedAt:              ended.UTC(),
		OfferedRatePerSecond: float64(runner.plannedStarts.Load()-runner.phasePlanned) / ended.Sub(runner.phaseStartedAt).Seconds(),
	})
	runner.phaseName = ""
}

// fillPhaseStepStats sets a phase's Step counts, shares, and latencies from its recorder bucket.
func fillPhaseStepStats(phase *SlotStreamPhase, counts []int64, latencies []time.Duration) {
	phase.PerWorkerSteps = counts
	for _, count := range counts {
		phase.StepsStarted += count
	}
	phase.ScheduleToStart = calculatePercentiles(latencies)
	if len(counts) > 1 {
		phase.PerWorkerShare = workerShares(counts)
		phase.JainFairnessIndex = jainFairnessIndex(counts)
	}
}

func (runner *SlotStreamRunner) currentPhaseName() string {
	runner.phaseMutex.Lock()
	defer runner.phaseMutex.Unlock()
	return runner.phaseName
}

// openWorkersAndWaitForStreams starts every Worker, then waits until the server counts all their streams.
func (runner *SlotStreamRunner) openWorkersAndWaitForStreams(ctx context.Context) error {
	if err := runner.openWorkers(ctx); err != nil {
		return err
	}
	if runner.config.MetricsURL == "" {
		return nil
	}
	expected := runner.baselineStreams + float64(runner.settings.totalSlots())
	deadline := time.Now().Add(seconds(runner.config.SlotStreams.OpenTimeoutSeconds))
	for {
		metrics, err := runner.scrapeSlotMetricsOnce(ctx)
		if err == nil && metrics.workerStreams >= expected {
			return nil
		}
		if time.Now().After(deadline) {
			return errorWithCause(fmt.Sprintf("server counted %.0f of %.0f streams after %.0f seconds",
				metrics.workerStreams, expected, runner.config.SlotStreams.OpenTimeoutSeconds), err)
		}
		if err := sleepContext(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

// openWorkers starts one pull Worker per entry of workerSlots and waits until each has registered.
func (runner *SlotStreamRunner) openWorkers(ctx context.Context) error {
	runner.workers = make([]*slotWorker, len(runner.settings.workerSlots))
	errs := make([]error, len(runner.workers))
	forEachInParallel(len(runner.workers), workerOpenParallelism, func(index int) {
		errs[index] = runner.openWorker(ctx, index)
	})
	return errors.Join(errs...)
}

func (runner *SlotStreamRunner) openWorker(ctx context.Context, index int) error {
	registry, err := dex.NewRegistry(runner.probeFlows(index, runner.recorder))
	if err != nil {
		return fmt.Errorf("Worker %d registry: %w", index, err)
	}
	worker, err := dex.NewWorker(registry, runner.cache, dex.WorkerOptions{
		FlowServiceAddress: runner.config.Address,
		Pull:               slotWorkerPullOptions(runner.settings.workerSlots[index]),
	})
	if err != nil {
		return fmt.Errorf("Worker %d: %w", index, err)
	}
	opened := &slotWorker{worker: worker, result: make(chan error, 1)}
	runner.workers[index] = opened
	go func() { opened.result <- worker.Start() }()
	registrationContext, cancel := context.WithTimeout(ctx, seconds(runner.config.SlotStreams.OpenTimeoutSeconds))
	defer cancel()
	if err := worker.WaitPullRegistered(registrationContext); err != nil {
		return fmt.Errorf("Worker %d registration: %w", index, err)
	}
	return nil
}

// slotWorkerPullOptions builds fixed-slot pull options with every slot a Step-and-RPC stream.
// Adaptive slot streams set PullWorkerOptions.MinOpenSlots here once the SDK has it.
func slotWorkerPullOptions(slots int) *dex.PullWorkerOptions {
	return &dex.PullWorkerOptions{MaxConcurrentTasks: slots, ReservedRPCTasks: ptr.Any(0)}
}

// applyOpenLoopLoad starts Flows at rate per second of planned time for duration, whether or not earlier starts finished.
// Each start keeps its planned time, so a late scheduler shows up as latency, never as a later plan.
func (runner *SlotStreamRunner) applyOpenLoopLoad(ctx context.Context, duration time.Duration, rate func(time.Duration) float64) error {
	defer runner.offeredRateBits.Store(0)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	schedule := &openLoopSchedule{}
	for {
		now := time.Now()
		elapsed := min(now.Sub(started), duration)
		runner.offeredRateBits.Store(math.Float64bits(rate(elapsed)))
		for _, plannedOffset := range schedule.advanceTo(elapsed, rate) {
			runner.submitStart(started.Add(plannedOffset), now)
		}
		if elapsed >= duration {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// lateSubmissionThreshold is how far behind its plan a start may be queued before it counts as late.
const lateSubmissionThreshold = 10 * time.Millisecond

// submitStart queues one probe Flow start without blocking; a full queue counts the start as skipped.
func (runner *SlotStreamRunner) submitStart(plannedAt, submittedAt time.Time) {
	runner.plannedStarts.Add(1)
	lag := submittedAt.Sub(plannedAt)
	if lag > lateSubmissionThreshold {
		runner.lateSubmissions.Add(1)
	}
	for {
		previous := runner.maxSchedulerLag.Load()
		if int64(lag) <= previous || runner.maxSchedulerLag.CompareAndSwap(previous, int64(lag)) {
			break
		}
	}
	runner.pendingStarts.Add(1)
	select {
	case runner.startJobs <- plannedAt:
	default:
		runner.pendingStarts.Add(-1)
		runner.startsSkipped.Add(1)
	}
}

func (runner *SlotStreamRunner) runStartFlowCalls() {
	defer runner.startPool.Done()
	for plannedAt := range runner.startJobs {
		if !runner.isClosing.Load() {
			runner.startProbeFlow(plannedAt)
		}
		runner.pendingStarts.Add(-1)
	}
}

func (runner *SlotStreamRunner) startProbeFlow(scheduledAt time.Time) {
	sequence := runner.nextStart.Add(1)
	client := runner.clients[sequence%int64(len(runner.clients))]
	flow := runner.clientFlows[sequence%int64(len(runner.clientFlows))]
	ctx, cancel := context.WithTimeout(context.Background(), seconds(runner.config.TimeoutSeconds))
	defer cancel()
	// Each probe Flow is one short Step, so it keeps its history local.
	options := dex.StartFlowOptions{ConfigOverride: &dex.FlowConfig{OffloadHistory: ptr.Any(false)}}
	if _, err := client.StartFlow(ctx, flow, runner.newFlowID(), slotProbeInput{ScheduledAtUnixNano: scheduledAt.UnixNano()}, options); err != nil {
		runner.failedStarts.Add(1)
		runner.errorsMutex.Lock()
		runner.startErrors["StartFlow: "+err.Error()]++
		runner.errorsMutex.Unlock()
		return
	}
	runner.successfulStarts.Add(1)
}

func (runner *SlotStreamRunner) recordUnmeasuredError(message string) {
	runner.errorsMutex.Lock()
	runner.unmeasuredErrors[message]++
	runner.errorsMutex.Unlock()
}

// newFlowID returns a unique Flow ID, in the pinned partition when a partition hash key is set.
func (runner *SlotStreamRunner) newFlowID() string {
	runner.flowIDMutex.Lock()
	defer runner.flowIDMutex.Unlock()
	if runner.flowIDGenerator != nil {
		return runner.flowIDGenerator.nextFlowID(runner.runPrefix, &runner.flowIDCounter)
	}
	runner.flowIDCounter++
	return fmt.Sprintf("%s-%d", runner.runPrefix, runner.flowIDCounter)
}

// waitForStartsReturned waits until every queued StartFlow call has returned.
func (runner *SlotStreamRunner) waitForStartsReturned(ctx context.Context) error {
	deadline := time.Now().Add(seconds(runner.config.SlotStreams.DrainTimeoutSeconds))
	for runner.pendingStarts.Load() > 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("%d StartFlow calls still pending", runner.pendingStarts.Load())
		}
		if err := sleepContext(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// waitForDrain waits until every started probe Flow's Step has finished.
func (runner *SlotStreamRunner) waitForDrain(ctx context.Context) error {
	if err := runner.waitForStartsReturned(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(seconds(runner.config.SlotStreams.DrainTimeoutSeconds))
	for {
		_, finished, _ := runner.recorder.totals()
		remaining := runner.successfulStarts.Load() - finished
		if remaining <= 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d started Steps did not finish within %.0f seconds", remaining, runner.config.SlotStreams.DrainTimeoutSeconds)
		}
		if err := sleepContext(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
}

// waitForQueuedSteps waits until the task queue holds expected entries; without metrics it returns at once.
func (runner *SlotStreamRunner) waitForQueuedSteps(ctx context.Context, expected float64) error {
	if runner.config.MetricsURL == "" {
		return nil
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		metrics, err := runner.scrapeSlotMetricsOnce(ctx)
		if err == nil {
			runner.backlog.ObservedQueueEntries = metrics.taskQueueEntries
			if metrics.taskQueueEntries >= expected {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errorWithCause(fmt.Sprintf("task queue holds %.0f of %.0f Steps after 30 seconds", runner.backlog.ObservedQueueEntries, expected), err)
		}
		if err := sleepContext(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

// restartServerAndWaitForStreams times readiness and the return of every stream, watching while the restart command runs.
// before is the last scrape of the old server; a new process start time or a counter reset identifies the new one.
func (runner *SlotStreamRunner) restartServerAndWaitForStreams(ctx context.Context, before slotServerMetrics) error {
	options := runner.config.SlotStreams
	expected := float64(runner.settings.totalSlots())
	runner.restart = &StreamRestart{ExpectedStreams: expected}
	commandStarted := time.Now()
	commandResult := make(chan error, 1)
	go func() {
		output, err := exec.CommandContext(ctx, "bash", "-c", options.RestartCommand).CombinedOutput()
		if err != nil {
			err = fmt.Errorf("restart command: %w: %s", err, strings.TrimSpace(string(output)))
		}
		commandResult <- err
	}()
	var commandEnded time.Time
	isRestartObserved, isReady := false, false
	deadline := commandStarted.Add(seconds(options.OpenTimeoutSeconds))
	for {
		if commandEnded.IsZero() {
			select {
			case err := <-commandResult:
				if err != nil {
					return err
				}
				commandEnded = time.Now()
				runner.restart.CommandSeconds = commandEnded.Sub(commandStarted).Seconds()
			default:
			}
		}
		metrics, scrapeErr := runner.scrapeSlotMetricsOnce(ctx)
		if isRestartedServer(before, metrics, scrapeErr) || metrics.workerStreams < expected {
			isRestartObserved = true
		}
		if isRestartObserved && !isReady && isFlowServiceServing(runner.config.Address) {
			isReady = true
			runner.restart.ReadySeconds = time.Since(commandStarted).Seconds()
		}
		if isRestartObserved && isReady && scrapeErr == nil && metrics.workerStreams >= expected {
			runner.restart.ReregisteredSeconds = time.Since(commandStarted).Seconds()
			if commandEnded.IsZero() {
				// The streams are back, so the command has started the new server; its result still matters.
				if err := <-commandResult; err != nil {
					return err
				}
				runner.restart.CommandSeconds = time.Since(commandStarted).Seconds()
			}
			return nil
		}
		if !isRestartObserved && !commandEnded.IsZero() && time.Since(commandEnded) > restartDownTimeout {
			return fmt.Errorf("no sign of a new server process %v after the restart command returned", restartDownTimeout)
		}
		if time.Now().After(deadline) {
			return errorWithCause(fmt.Sprintf("server counted %.0f of %.0f streams %.0f seconds after the restart command (ready %v)",
				metrics.workerStreams, expected, options.OpenTimeoutSeconds, isReady), scrapeErr)
		}
		if err := sleepContext(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

// isFlowServiceServing dials a fresh connection, so gRPC reconnect backoff cannot delay the readiness time.
func isFlowServiceServing(address string) bool {
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			slog.Warn("close health connection", "error", closeErr)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{Service: "dex.FlowService"})
	return err == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

func (runner *SlotStreamRunner) scrapeSlotMetricsOnce(ctx context.Context) (slotServerMetrics, error) {
	scrapeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return scrapeSlotMetrics(scrapeContext, runner.config.MetricsURL)
}

// sampleSlotTimeSeries appends one SlotStreamSample per second until ctx ends.
func (runner *SlotStreamRunner) sampleSlotTimeSeries(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previousStarted, previousFinished int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		started, finished, busy := runner.recorder.totals()
		sample := SlotStreamSample{
			At:                   time.Now().UTC(),
			Phase:                runner.currentPhaseName(),
			OfferedRatePerSecond: math.Float64frombits(runner.offeredRateBits.Load()),
			StepsStarted:         started - previousStarted,
			StepsFinished:        finished - previousFinished,
			BusyHandlers:         busy,
			ScheduleToStart:      calculatePercentiles(runner.recorder.takeIntervalLatencies()),
		}
		previousStarted, previousFinished = started, finished
		if len(runner.settings.workerSlots) <= maxPerWorkerSampleWorkers {
			sample.PerWorkerBusy = runner.recorder.busyPerWorker()
		}
		if runner.config.MetricsURL != "" {
			scrapeContext, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
			metrics, err := scrapeSlotMetrics(scrapeContext, runner.config.MetricsURL)
			cancel()
			if err != nil {
				// Expected while a restart is in progress; the sample keeps IsServerMetricsAvailable false.
				runner.recordUnmeasuredError("metrics scrape: " + err.Error())
			} else {
				sample.IsServerMetricsAvailable = true
				sample.ServerWorkerStreams = metrics.workerStreams
				sample.ServerTaskQueueEntries = metrics.taskQueueEntries
				sample.ServerStreamsClosed = metrics.streamsClosed
				sample.ServerNotDelivered = metrics.notDelivered
			}
		}
		runner.samplesMutex.Lock()
		runner.samples = append(runner.samples, sample)
		runner.samplesMutex.Unlock()
	}
}

// fillResult copies the counters, phases, and samples into result.
func (runner *SlotStreamRunner) fillResult(result *Result, elapsed time.Duration) {
	stepsStarted, _, _ := runner.recorder.totals()
	loss := runner.requestLoss(stepsStarted)
	// Every planned start is an attempt, so lost requests show as attempts without success.
	result.Attempts = loss.Planned
	result.Successes = stepsStarted
	result.ThroughputUnit = "steps"
	result.ThroughputPerSecond = float64(stepsStarted) / elapsed.Seconds()
	if loss.Skipped > 0 {
		result.Errors["start skipped: the start queue was full"] += loss.Skipped
	}
	if loss.StepsNotStarted > 0 {
		result.Errors["Step not started before the point ended"] += loss.StepsNotStarted
	}
	runner.errorsMutex.Lock()
	for message, count := range runner.startErrors {
		result.Errors[message] += count
	}
	for message, count := range runner.unmeasuredErrors {
		result.UnmeasuredErrors[message] += count
	}
	runner.errorsMutex.Unlock()
	runner.samplesMutex.Lock()
	samples := append([]SlotStreamSample(nil), runner.samples...)
	runner.samplesMutex.Unlock()
	phases := append([]SlotStreamPhase(nil), runner.phases...)
	var allLatencies []time.Duration
	for index := range phases {
		counts, latencies := runner.recorder.phaseSteps(runner.phaseBuckets[index])
		fillPhaseStepStats(&phases[index], counts, latencies)
		fillPhaseSampleStats(&phases[index], samples)
		allLatencies = append(allLatencies, latencies...)
	}
	result.Latency = calculatePercentiles(allLatencies)
	reasons := unusableReasons(loss)
	slotResult := &SlotStreamResult{
		Workers:          len(runner.settings.workerSlots),
		SlotsPerWorker:   runner.settings.workerSlots,
		TotalSlots:       runner.settings.totalSlots(),
		PartitionPinning: runner.partitionPinningLabel(),
		BaselineStreams:  runner.baselineStreams,
		IsUsable:         len(reasons) == 0,
		UnusableReasons:  reasons,
		RequestLoss:      loss,
		Phases:           phases,
		Samples:          samples,
		Idle:             runner.idle,
		Restart:          runner.restart,
		Backlog:          runner.backlog,
	}
	if runner.backlog != nil {
		if drain, isFound := findSlotPhase(phases, slotPhaseDrain); isFound {
			runner.backlog.MaxBusyHandlers = drain.MaxBusyHandlers
			runner.backlog.MaxServerStreams = drain.MaxServerStreams
		}
	}
	for index, opened := range runner.workers {
		if opened == nil {
			continue
		}
		slotResult.WorkerRegistrations = append(slotResult.WorkerRegistrations, workerRegistrationSnapshot(index, opened.worker))
	}
	result.SlotStreams = slotResult
}

func (runner *SlotStreamRunner) requestLoss(stepsStarted int64) SlotRequestLoss {
	flowsStarted := runner.successfulStarts.Load()
	return SlotRequestLoss{
		Planned:           runner.plannedStarts.Load(),
		Skipped:           runner.startsSkipped.Load(),
		StartFailed:       runner.failedStarts.Load(),
		FlowsStarted:      flowsStarted,
		StepsStarted:      stepsStarted,
		StepsNotStarted:   max(flowsStarted-stepsStarted, 0),
		LateSubmissions:   runner.lateSubmissions.Load(),
		MaxSchedulerLagMs: float64(runner.maxSchedulerLag.Load()) / float64(time.Millisecond),
	}
}

// maxUsableSchedulerLag is the scheduler lag above which the load generator, not the server, shaped the load.
const maxUsableSchedulerLag = 100 * time.Millisecond

// unusableReasons returns why a point's numbers cannot be trusted, or nil when every planned request was measured.
// Lost requests have no latency, so the distribution of such a point is incomplete.
func unusableReasons(loss SlotRequestLoss) []string {
	var reasons []string
	if loss.Skipped > 0 {
		reasons = append(reasons, fmt.Sprintf("%d of %d planned starts were skipped because the start queue was full", loss.Skipped, loss.Planned))
	}
	if loss.StartFailed > 0 {
		reasons = append(reasons, fmt.Sprintf("%d StartFlow calls failed", loss.StartFailed))
	}
	if loss.StepsNotStarted > 0 {
		reasons = append(reasons, fmt.Sprintf("%d started Flows never ran their Step", loss.StepsNotStarted))
	}
	if lag := time.Duration(loss.MaxSchedulerLagMs * float64(time.Millisecond)); lag > maxUsableSchedulerLag {
		reasons = append(reasons, fmt.Sprintf("the scheduler fell %v behind its plan", lag.Round(time.Millisecond)))
	}
	return reasons
}

// fillPhaseSampleStats sets the stream range and busy peak of the samples taken during phase.
func fillPhaseSampleStats(phase *SlotStreamPhase, samples []SlotStreamSample) {
	minimum, maximum := math.Inf(1), math.Inf(-1)
	for _, sample := range samples {
		if sample.At.Before(phase.StartedAt) || sample.At.After(phase.EndedAt) {
			continue
		}
		phase.MaxBusyHandlers = max(phase.MaxBusyHandlers, sample.BusyHandlers)
		if sample.IsServerMetricsAvailable {
			minimum = math.Min(minimum, sample.ServerWorkerStreams)
			maximum = math.Max(maximum, sample.ServerWorkerStreams)
		}
	}
	if !math.IsInf(minimum, 1) {
		phase.MinServerStreams, phase.MaxServerStreams = minimum, maximum
	}
}

func (runner *SlotStreamRunner) partitionPinningLabel() string {
	options := runner.config.SlotStreams
	switch {
	case runner.flowIDGenerator != nil:
		return fmt.Sprintf("Flow IDs hashed into partition %d of %d", options.PinnedPartition, options.PartitionCount)
	case runner.settings.scenario.kind == slotScenarioBacklog:
		return "one partition only if the server runs with cluster.partitionCount 1; dexbench does not verify it"
	default:
		return ""
	}
}

func workerRegistrationSnapshot(index int, worker *dex.Worker) SlotWorkerRegistration {
	state := worker.PullRegistrationState()
	snapshot := SlotWorkerRegistration{Worker: index, IsReady: state.IsReady}
	for _, queue := range state.Queues {
		entry := SlotWorkerQueueRegistration{Queue: queue.Queue, HasRegisteredStream: queue.HasRegisteredStream}
		if queue.LastPermanentError != nil {
			entry.LastPermanentError = queue.LastPermanentError.Error()
		}
		snapshot.Queues = append(snapshot.Queues, entry)
	}
	return snapshot
}

// Close stops the StartFlow pool, every Worker, and every Client.
func (runner *SlotStreamRunner) Close() error {
	runner.isClosing.Store(true)
	close(runner.startJobs)
	runner.startPool.Wait()
	errs := make([]error, len(runner.workers))
	forEachInParallel(len(runner.workers), workerOpenParallelism, func(index int) {
		if runner.workers[index] != nil {
			errs[index] = runner.workers[index].stop()
		}
	})
	combined := errors.Join(errs...)
	for _, client := range runner.clients {
		combined = errors.Join(combined, client.Close())
	}
	if runner.cache != nil {
		combined = errors.Join(combined, runner.cache.Close())
	}
	if runner.cacheDir != "" {
		combined = errors.Join(combined, os.RemoveAll(runner.cacheDir))
	}
	return combined
}

func (opened *slotWorker) stop() error {
	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	combined := opened.worker.Stop(stopContext)
	select {
	case err := <-opened.result:
		return errors.Join(combined, err)
	case <-time.After(30 * time.Second):
		return errors.Join(combined, errors.New("Worker did not stop in 30 seconds"))
	}
}

// forEachInParallel calls work for every index in 0..count with at most parallelism concurrent calls.
func forEachInParallel(count, parallelism int, work func(index int)) {
	indexes := make(chan int)
	var waitGroup sync.WaitGroup
	for range min(count, parallelism) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range indexes {
				work(index)
			}
		}()
	}
	for index := range count {
		indexes <- index
	}
	close(indexes)
	waitGroup.Wait()
}

// errorWithCause returns message as an error, wrapping cause when there is one.
func errorWithCause(message string, cause error) error {
	if cause == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, cause)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

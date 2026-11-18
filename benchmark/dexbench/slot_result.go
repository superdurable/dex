// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"time"
)

// SlotStreamConfig holds the slot stream scenario settings; "scenario default" fields resolve zero per scenario.
type SlotStreamConfig struct {
	// SlotsPerWorker is the MaxConcurrentTasks of each pull Worker; scenario default.
	SlotsPerWorker int `json:"slots_per_worker"`
	// Workers is the pull Worker count; scenario default, derived from the concurrency and SlotsPerWorker.
	Workers int `json:"workers"`
	// FlowTypes is the number of registered Flow types; each stream joins 1 + FlowTypes queues.
	FlowTypes int `json:"flow_types"`
	// HandlerMilliseconds is how long each probe Step holds its slot; scenario default.
	HandlerMilliseconds float64 `json:"handler_ms"`
	// StepDurability is "async" or "sync".
	StepDurability string `json:"step_durability"`
	// BaselineSeconds is the idle-slots observation before any Worker opens.
	BaselineSeconds float64 `json:"baseline_seconds,omitempty"`
	// ProbeRatePerSecond and ProbeSeconds define the light idle-slots load.
	ProbeRatePerSecond float64 `json:"probe_rate_per_second,omitempty"`
	ProbeSeconds       float64 `json:"probe_seconds,omitempty"`
	// IdleGapSeconds is the zero-load gap before each burst level.
	IdleGapSeconds float64 `json:"idle_gap_seconds,omitempty"`
	// BurstLevels are the in-flight Steps per Worker of each burst level.
	BurstLevels []int `json:"burst_levels,omitempty"`
	// LoadPercents are the fairness load levels as a share of total slot capacity.
	LoadPercents []float64 `json:"load_percents,omitempty"`
	// LoadWave is "square" or "sine"; LoadPeriodSeconds, LoadLowPercent, and LoadHighPercent shape it.
	LoadWave          string  `json:"load_wave,omitempty"`
	LoadPeriodSeconds float64 `json:"load_period_seconds,omitempty"`
	LoadLowPercent    float64 `json:"load_low_percent,omitempty"`
	LoadHighPercent   float64 `json:"load_high_percent,omitempty"`
	// RestartCommand is run with bash -c to restart the server after the idle-slots probe; empty skips the restart.
	RestartCommand string `json:"restart_command,omitempty"`
	// ServerPIDFile holds the server PID that a restart changes; empty uses process_start_time_seconds instead.
	ServerPIDFile string `json:"server_pid_file,omitempty"`
	// PingIntervalSeconds is the server's matching.pingInterval, used only for the derived Ping rate.
	PingIntervalSeconds float64 `json:"ping_interval_seconds,omitempty"`
	// PartitionHashKey is the cluster's 32-hex-digit partition hash key. It is never written to results.
	PartitionHashKey string `json:"-"`
	// PartitionCount and PinnedPartition choose the partition when PartitionHashKey is set.
	PartitionCount  int `json:"partition_count,omitempty"`
	PinnedPartition int `json:"pinned_partition,omitempty"`
	// StartParallelism bounds concurrent StartFlow calls; ClientConnections is the Client count.
	StartParallelism  int `json:"start_parallelism"`
	ClientConnections int `json:"client_connections"`
	// OpenTimeoutSeconds bounds stream registration; DrainTimeoutSeconds bounds the wait for started Steps.
	OpenTimeoutSeconds  float64 `json:"open_timeout_seconds"`
	DrainTimeoutSeconds float64 `json:"drain_timeout_seconds"`
	// IsAdaptiveSlots and MinOpenSlots select adaptive slot streams once the SDK supports them.
	IsAdaptiveSlots bool `json:"is_adaptive_slots,omitempty"`
	MinOpenSlots    int  `json:"min_open_slots,omitempty"`
}

// SlotStreamResult is the scenario-specific part of a slot stream point.
type SlotStreamResult struct {
	Workers          int     `json:"workers"`
	SlotsPerWorker   []int   `json:"slots_per_worker"`
	TotalSlots       int     `json:"total_slots"`
	PartitionPinning string  `json:"partition_pinning,omitempty"`
	BaselineStreams  float64 `json:"baseline_server_streams"`
	// IsUsable is false when requests were lost or the load generator fell behind; UnusableReasons says why.
	IsUsable        bool            `json:"is_usable"`
	UnusableReasons []string        `json:"unusable_reasons,omitempty"`
	RequestLoss     SlotRequestLoss `json:"request_loss"`
	// StepsOutsidePhases started between two phases or after the last one, so no phase table lists them.
	StepsOutsidePhases int64                   `json:"steps_outside_phases"`
	Phases             []SlotStreamPhase       `json:"phases"`
	Samples            []SlotStreamSample      `json:"samples"`
	Idle               *IdleStreamCost         `json:"idle,omitempty"`
	Restart            *StreamRestart          `json:"restart,omitempty"`
	Backlog            *BacklogDrain           `json:"backlog,omitempty"`
	ServerMetricsDelta *SlotServerMetricsDelta `json:"server_metrics_delta,omitempty"`
	// WorkerRegistrations is the SDK registration snapshot of every Worker at the end of the point.
	WorkerRegistrations []SlotWorkerRegistration `json:"worker_registrations"`
}

// SlotRequestLoss accounts for every planned probe start of a point.
type SlotRequestLoss struct {
	// Planned counts every start the schedule made due.
	Planned int64 `json:"planned"`
	// Skipped starts were dropped because the start queue was full; they never reached the server.
	Skipped int64 `json:"skipped"`
	// StartFailed starts returned a StartFlow error.
	StartFailed  int64 `json:"start_failed"`
	FlowsStarted int64 `json:"flows_started"`
	StepsStarted int64 `json:"steps_started"`
	// StepsNotStarted are started Flows whose Step never ran before the point ended.
	StepsNotStarted int64 `json:"steps_not_started"`
	// LateSubmissions were queued more than lateSubmissionThreshold after their planned time.
	LateSubmissions   int64   `json:"late_submissions"`
	MaxSchedulerLagMs float64 `json:"max_scheduler_lag_ms"`
}

// SlotStreamPhase summarizes one load phase.
type SlotStreamPhase struct {
	Name                 string      `json:"name"`
	StartedAt            time.Time   `json:"started_at"`
	EndedAt              time.Time   `json:"ended_at"`
	OfferedRatePerSecond float64     `json:"offered_rate_per_second"`
	StepsStarted         int64       `json:"steps_started"`
	ScheduleToStart      Percentiles `json:"schedule_to_start"`
	// PerWorkerSteps and PerWorkerShare are the Steps each Worker started during the phase.
	PerWorkerSteps    []int64   `json:"per_worker_steps"`
	PerWorkerShare    []float64 `json:"per_worker_share,omitempty"`
	JainFairnessIndex float64   `json:"jain_fairness_index,omitempty"`
	MinServerStreams  float64   `json:"min_server_streams"`
	MaxServerStreams  float64   `json:"max_server_streams"`
	MaxBusyHandlers   int64     `json:"max_busy_handlers"`
}

// SlotStreamSample covers one second of a slot stream point.
type SlotStreamSample struct {
	At                   time.Time   `json:"at"`
	Phase                string      `json:"phase"`
	OfferedRatePerSecond float64     `json:"offered_rate_per_second"`
	StepsStarted         int64       `json:"steps_started"`
	StepsFinished        int64       `json:"steps_finished"`
	BusyHandlers         int64       `json:"busy_handlers"`
	ScheduleToStart      Percentiles `json:"schedule_to_start"`
	// PerWorkerBusy is recorded only for points with at most maxPerWorkerSampleWorkers Workers.
	PerWorkerBusy []int64 `json:"per_worker_busy,omitempty"`
	// IsServerMetricsAvailable is false when /metrics could not be scraped in this second.
	IsServerMetricsAvailable bool    `json:"is_server_metrics_available"`
	ServerWorkerStreams      float64 `json:"server_worker_streams"`
	ServerTaskQueueEntries   float64 `json:"server_task_queue_entries"`
	ServerStreamsClosed      float64 `json:"server_streams_closed_total"`
	ServerNotDelivered       float64 `json:"server_not_delivered_total"`
}

// IdleStreamCost is the server cost of idle slot streams, from the baseline and idle phases.
type IdleStreamCost struct {
	Streams               int     `json:"streams"`
	RegisteredStreams     float64 `json:"registered_server_streams"`
	OpenSeconds           float64 `json:"open_seconds"`
	BaselineRSSBytes      float64 `json:"baseline_rss_bytes"`
	IdleRSSBytes          float64 `json:"idle_rss_bytes"`
	RSSBytesPerStream     float64 `json:"rss_bytes_per_stream"`
	BaselineCPUPercent    float64 `json:"baseline_cpu_percent"`
	IdleCPUPercent        float64 `json:"idle_cpu_percent"`
	CPUPercentPer1000     float64 `json:"cpu_percent_per_1000_streams"`
	ResourceSamples       int     `json:"resource_samples"`
	DerivedPingsPerSecond float64 `json:"derived_pings_per_second"`
}

// StreamRestart times the return of every stream after a server restart.
type StreamRestart struct {
	CommandSeconds float64 `json:"command_seconds"`
	// ReadySeconds and ReregisteredSeconds run from the start of the restart command.
	ReadySeconds        float64 `json:"ready_seconds"`
	ReregisteredSeconds float64 `json:"reregistered_seconds"`
	ExpectedStreams     float64 `json:"expected_streams"`
}

// BacklogDrain describes how one Worker drained a queued backlog.
type BacklogDrain struct {
	QueuedSteps          int64   `json:"queued_steps"`
	ObservedQueueEntries float64 `json:"observed_queue_entries"`
	DrainSeconds         float64 `json:"drain_seconds"`
	FirstStartSeconds    float64 `json:"first_start_seconds"`
	StartsPerSecond      float64 `json:"starts_per_second"`
	MaxBusyHandlers      int64   `json:"max_busy_handlers"`
	MaxServerStreams     float64 `json:"max_server_streams"`
}

// SlotServerMetricsDelta is the change of server counters over the point.
type SlotServerMetricsDelta struct {
	ScheduleToStartBuckets map[string]float64 `json:"schedule_to_start_buckets"`
	ScheduleToStartCount   float64            `json:"schedule_to_start_count"`
	StreamsClosed          float64            `json:"streams_closed"`
	NotDelivered           float64            `json:"not_delivered"`
}

// SlotWorkerRegistration is one Worker's PullRegistrationState.
type SlotWorkerRegistration struct {
	Worker  int                           `json:"worker"`
	IsReady bool                          `json:"is_ready"`
	Queues  []SlotWorkerQueueRegistration `json:"queues"`
}

type SlotWorkerQueueRegistration struct {
	Queue               string `json:"queue"`
	HasRegisteredStream bool   `json:"has_registered_stream"`
	LastPermanentError  string `json:"last_permanent_error,omitempty"`
}

// summarizeIdleStreamCost fills the idle resource fields from result.Resources; merging server samples reruns it.
func summarizeIdleStreamCost(result *Result) {
	if result.SlotStreams == nil || result.SlotStreams.Idle == nil {
		return
	}
	idle := result.SlotStreams.Idle
	baseline, isBaselineFound := findSlotPhase(result.SlotStreams.Phases, slotPhaseBaseline)
	idlePhase, isIdleFound := findSlotPhase(result.SlotStreams.Phases, slotPhaseIdle)
	if !isBaselineFound || !isIdleFound {
		return
	}
	baselineRSS, baselineCPU, baselineCount := meanResourcesInWindow(result.Resources, baseline.StartedAt, baseline.EndedAt)
	idleRSS, idleCPU, idleCount := meanResourcesInWindow(result.Resources, idlePhase.StartedAt, idlePhase.EndedAt)
	idle.ResourceSamples = baselineCount + idleCount
	if baselineCount == 0 || idleCount == 0 || idle.Streams == 0 {
		return
	}
	idle.BaselineRSSBytes, idle.IdleRSSBytes = baselineRSS, idleRSS
	idle.BaselineCPUPercent, idle.IdleCPUPercent = baselineCPU, idleCPU
	idle.RSSBytesPerStream = (idleRSS - baselineRSS) / float64(idle.Streams)
	idle.CPUPercentPer1000 = (idleCPU - baselineCPU) / float64(idle.Streams) * 1000
}

func findSlotPhase(phases []SlotStreamPhase, name string) (SlotStreamPhase, bool) {
	for _, phase := range phases {
		if phase.Name == name {
			return phase, true
		}
	}
	return SlotStreamPhase{}, false
}

// meanResourcesInWindow returns mean RSS bytes, mean CPU %, and the count of samples whose interval midpoint lies in the window.
func meanResourcesInWindow(samples []ResourceSample, start, end time.Time) (float64, float64, int) {
	var rssTotal, cpuTotal float64
	count := 0
	for _, sample := range samples {
		midpoint := sample.At.Add(-time.Duration(sample.IntervalSeconds * float64(time.Second) / 2))
		if midpoint.Before(start) || midpoint.After(end) {
			continue
		}
		rssTotal += float64(sample.RSSBytes)
		cpuTotal += sample.CPUPercent
		count++
	}
	if count == 0 {
		return 0, 0, 0
	}
	return rssTotal / float64(count), cpuTotal / float64(count), count
}

// jainFairnessIndex returns (sum x)^2 / (n * sum x^2): 1 when every Worker got the same share, 1/n when one got all.
// It returns 0 when no Worker started a Step.
func jainFairnessIndex(counts []int64) float64 {
	var sum, sumOfSquares float64
	for _, count := range counts {
		sum += float64(count)
		sumOfSquares += float64(count) * float64(count)
	}
	if sumOfSquares == 0 {
		return 0
	}
	return sum * sum / (float64(len(counts)) * sumOfSquares)
}

// workerShares returns each Worker's fraction of the started Steps, or nil when none started.
func workerShares(counts []int64) []float64 {
	var total int64
	for _, count := range counts {
		total += count
	}
	if total == 0 {
		return nil
	}
	shares := make([]float64, len(counts))
	for index, count := range counts {
		shares[index] = float64(count) / float64(total)
	}
	return shares
}

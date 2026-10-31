// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Percentiles struct {
	P50 float64 `json:"p50_ms"`
	P90 float64 `json:"p90_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

type Config struct {
	Address         string  `json:"address"`
	Target          string  `json:"target"`
	Engine          string  `json:"engine"`
	Scenario        string  `json:"scenario"`
	DurationSeconds float64 `json:"duration_seconds"`
	WarmupSeconds   float64 `json:"warmup_seconds"`
	Concurrency     int     `json:"concurrency"`
	WorkerPool      int     `json:"worker_pool"`
	Steps           int     `json:"steps"`
	TimeoutSeconds  float64 `json:"timeout_seconds"`
	ServerPID       int     `json:"server_pid,omitempty"`
	ResourcePIDs    []int   `json:"resource_pids,omitempty"`
	MetricsURL      string  `json:"metrics_url,omitempty"`
	AttributeBytes  int     `json:"attribute_bytes"`
	Mode            string  `json:"mode"`
	GitCommit       string  `json:"git_commit,omitempty"`
}

type MachineInfo struct {
	CPUModel      string `json:"cpu_model"`
	Cores         int    `json:"cores"`
	RAMBytes      uint64 `json:"ram_bytes"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	FreeDiskBytes uint64 `json:"free_disk_bytes"`
}

// ResourceSample covers one sampling interval. CPUPercent is 100 per fully used core.
type ResourceSample struct {
	At              time.Time `json:"at"`
	IntervalSeconds float64   `json:"interval_seconds"`
	CPUPercent      float64   `json:"cpu_percent"`
	RSSBytes        uint64    `json:"rss_bytes"`
	ProcessCount    int       `json:"process_count"`
}

type ObjectStoreMetrics struct {
	RequestsPerMinute            map[string]float64 `json:"requests_per_minute,omitempty"`
	RequestsPerMinuteByOperation map[string]float64 `json:"requests_per_minute_by_operation,omitempty"`
	RequestErrors                int64              `json:"request_errors"`
	CommitLatencyBuckets         map[string]float64 `json:"commit_latency_buckets,omitempty"`
	CommitLatencyCount           int64              `json:"commit_latency_count"`
	CommitLatencySumSeconds      float64            `json:"commit_latency_sum_seconds"`
}

type Result struct {
	SchemaVersion         int                 `json:"schema_version"`
	StartedAt             time.Time           `json:"started_at"`
	Config                Config              `json:"config"`
	ServerVersion         string              `json:"server_version"`
	SDKVersion            string              `json:"sdk_version"`
	Machine               MachineInfo         `json:"machine"`
	Attempts              int64               `json:"attempts"`
	Successes             int64               `json:"successes"`
	Errors                map[string]int64    `json:"errors"`
	UnmeasuredErrors      map[string]int64    `json:"unmeasured_errors,omitempty"`
	MeasuredSeconds       float64             `json:"measured_seconds"`
	VerifiedBytes         int64               `json:"verified_bytes_per_read,omitempty"`
	BlobsLoaded           int64               `json:"blobs_loaded,omitempty"`
	FlowStatePollsPerWake float64             `json:"flow_state_polls_per_wake,omitempty"`
	CPUMethod             string              `json:"cpu_method,omitempty"`
	ThroughputPerSecond   float64             `json:"throughput_per_second"`
	ThroughputUnit        string              `json:"throughput_unit"`
	Latency               Percentiles         `json:"latency"`
	StepLatency           Percentiles         `json:"step_latency,omitempty"`
	StepSamples           int                 `json:"step_samples,omitempty"`
	Resources             []ResourceSample    `json:"resources,omitempty"`
	ObjectStore           *ObjectStoreMetrics `json:"object_store,omitempty"`
}

type latencyCollector struct {
	mu     sync.Mutex
	values []time.Duration
}

func (collector *latencyCollector) add(value time.Duration) {
	collector.mu.Lock()
	collector.values = append(collector.values, value)
	collector.mu.Unlock()
}

func (collector *latencyCollector) take() []time.Duration {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	values := collector.values
	collector.values = nil
	return values
}

// recordChainStep records the gap since the previous Step and returns the next input, or true when the chain ends.
func (collector *latencyCollector) recordChainStep(input chainInput) (chainInput, bool) {
	now := time.Now()
	if input.PreviousAtUnixNano != 0 {
		collector.add(time.Duration(now.UnixNano() - input.PreviousAtUnixNano))
	}
	if input.Remaining <= 1 {
		return chainInput{}, true
	}
	return chainInput{Remaining: input.Remaining - 1, PreviousAtUnixNano: now.UnixNano()}, false
}

func calculatePercentiles(values []time.Duration) Percentiles {
	if len(values) == 0 {
		return Percentiles{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	selectValue := func(quantile float64) float64 {
		index := int(math.Ceil(quantile*float64(len(values)))) - 1
		if index < 0 {
			index = 0
		}
		return float64(values[index]) / float64(time.Millisecond)
	}
	return Percentiles{P50: selectValue(0.5), P90: selectValue(0.9), P99: selectValue(0.99), Max: selectValue(1)}
}

func WriteResult(path string, result Result) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create result %s: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return errors.Join(fmt.Errorf("encode result %s: %w", path, err), file.Close())
	}
	return file.Close()
}

func ReadResult(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read result %s: %w", path, err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, fmt.Errorf("decode result %s: %w", path, err)
	}
	if result.SchemaVersion != 2 {
		return Result{}, fmt.Errorf("unsupported result schema %d in %s", result.SchemaVersion, path)
	}
	return result, nil
}

func RenderReport(results []Result) string {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Config.Scenario == results[j].Config.Scenario {
			if results[i].Config.Concurrency == results[j].Config.Concurrency {
				return results[i].Config.Target < results[j].Config.Target
			}
			return results[i].Config.Concurrency < results[j].Config.Concurrency
		}
		return results[i].Config.Scenario < results[j].Config.Scenario
	})
	var output strings.Builder
	output.WriteString("Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. ")
	output.WriteString("Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. ")
	output.WriteString("channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.\n\n")
	var previousScenario string
	for _, result := range results {
		isStepScenario := isSequentialStepsScenario(result.Config.Scenario)
		if result.Config.Scenario != previousScenario {
			if previousScenario != "" {
				output.WriteString("\n")
			}
			fmt.Fprintf(&output, "## %s\n\n", result.Config.Scenario)
			switch {
			case isStepScenario:
				output.WriteString("| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |\n")
				output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
			default:
				output.WriteString("| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |\n")
				output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
			}
			previousScenario = result.Config.Scenario
		}
		var errorCount int64
		for _, count := range result.Errors {
			errorCount += count
		}
		cpuMean, rssMax := summarizeResources(result.Resources)
		latency := result.Latency
		if isStepScenario {
			fmt.Fprintf(&output, "| %d | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.1f | %.1f |\n",
				result.Config.Concurrency, result.Config.Target, result.Successes, errorCount, result.ThroughputPerSecond,
				result.ThroughputUnit, latency.P50, latency.P99, result.StepLatency.P50, result.StepLatency.P90,
				result.StepLatency.P99, result.StepLatency.Max, cpuMean, rssMax)
		} else {
			fmt.Fprintf(&output, "| %d | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f | %.1f | %.1f |\n",
				result.Config.Concurrency, result.Config.Target, result.Successes, errorCount, result.ThroughputPerSecond,
				result.ThroughputUnit, latency.P50, latency.P90, latency.P99, latency.Max, cpuMean, rssMax)
		}
	}
	return output.String()
}

func summarizeResources(samples []ResourceSample) (float64, float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	var cpuTotal float64
	var rssMax uint64
	for _, sample := range samples {
		cpuTotal += sample.CPUPercent
		rssMax = max(rssMax, sample.RSSBytes)
	}
	return cpuTotal / float64(len(samples)), float64(rssMax) / float64(1<<20)
}

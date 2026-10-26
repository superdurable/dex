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
	Address            string  `json:"address"`
	Target             string  `json:"target"`
	Engine             string  `json:"engine"`
	Scenario           string  `json:"scenario"`
	DurationSeconds    float64 `json:"duration_seconds"`
	WarmupSeconds      float64 `json:"warmup_seconds"`
	Concurrency        int     `json:"concurrency"`
	WorkerPool         int     `json:"worker_pool"`
	Steps              int     `json:"steps"`
	TimeoutSeconds     float64 `json:"timeout_seconds"`
	ServerPID          int     `json:"server_pid,omitempty"`
	ServerProcess      string  `json:"server_process,omitempty"`
	ResourcePIDs       []int   `json:"resource_pids,omitempty"`
	MetricsURL         string  `json:"metrics_url,omitempty"`
	AttributeStoreName string  `json:"attribute_store_name,omitempty"`
	AttributeBytes     int     `json:"attribute_bytes"`
	Mode               string  `json:"mode"`
	Region             string  `json:"region,omitempty"`
	InstanceType       string  `json:"instance_type,omitempty"`
	GitCommit          string  `json:"git_commit,omitempty"`
	RecoveryTargetMB   int     `json:"recovery_target_mb,omitempty"`
}

type MachineInfo struct {
	CPUModel      string `json:"cpu_model"`
	Cores         int    `json:"cores"`
	RAMBytes      uint64 `json:"ram_bytes"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	FreeDiskBytes uint64 `json:"free_disk_bytes"`
	InstanceType  string `json:"instance_type,omitempty"`
	Region        string `json:"region,omitempty"`
}

type ResourceSample struct {
	At         time.Time `json:"at"`
	CPUPercent float64   `json:"cpu_percent"`
	RSSBytes   uint64    `json:"rss_bytes"`
}

type ObjectStoreMetrics struct {
	RequestsPerMinute       map[string]float64 `json:"requests_per_minute,omitempty"`
	RequestErrors           int64              `json:"request_errors"`
	CommitLatencyBuckets    map[string]float64 `json:"commit_latency_buckets,omitempty"`
	CommitLatencyCount      int64              `json:"commit_latency_count"`
	CommitLatencySumSeconds float64            `json:"commit_latency_sum_seconds"`
}

type Result struct {
	SchemaVersion       int                 `json:"schema_version"`
	StartedAt           time.Time           `json:"started_at"`
	Config              Config              `json:"config"`
	ServerVersion       string              `json:"server_version"`
	SDKVersion          string              `json:"sdk_version"`
	Machine             MachineInfo         `json:"machine"`
	Attempts            int64               `json:"attempts"`
	Successes           int64               `json:"successes"`
	Errors              map[string]int64    `json:"errors"`
	ThroughputPerSecond float64             `json:"throughput_per_second"`
	ThroughputUnit      string              `json:"throughput_unit"`
	Latency             Percentiles         `json:"latency"`
	StepLatency         Percentiles         `json:"step_latency,omitempty"`
	StepSamples         int                 `json:"step_samples,omitempty"`
	Resources           []ResourceSample    `json:"resources,omitempty"`
	ObjectStore         *ObjectStoreMetrics `json:"object_store,omitempty"`
	RecoveryLogBytes    uint64              `json:"recovery_log_bytes,omitempty"`
	RecoverySeconds     float64             `json:"recovery_seconds,omitempty"`
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
	if result.SchemaVersion != 1 {
		return Result{}, fmt.Errorf("unsupported result schema %d in %s", result.SchemaVersion, path)
	}
	return result, nil
}

func RenderReport(results []Result) string {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Config.Scenario == results[j].Config.Scenario {
			if results[i].Config.Concurrency == results[j].Config.Concurrency {
				if results[i].Config.Scenario == "recovery" {
					return results[i].Config.RecoveryTargetMB < results[j].Config.RecoveryTargetMB
				}
				return results[i].Config.Target < results[j].Config.Target
			}
			return results[i].Config.Concurrency < results[j].Config.Concurrency
		}
		return results[i].Config.Scenario < results[j].Config.Scenario
	})
	var output strings.Builder
	var previousScenario string
	var previousConcurrency int
	var temporal *Result
	for _, result := range results {
		if result.Config.Scenario != previousScenario {
			fmt.Fprintf(&output, "## %s\n\n", result.Config.Scenario)
			if result.Config.Scenario == "recovery" {
				output.WriteString("| Target log MiB | Replayed MiB | Target | Recovery s | Errors |\n")
				output.WriteString("| ---: | ---: | --- | ---: | ---: |\n")
			} else {
				output.WriteString("| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |\n")
				output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
			}
			previousScenario = result.Config.Scenario
			previousConcurrency = -1
		}
		if result.Config.Concurrency != previousConcurrency {
			temporal = nil
			previousConcurrency = result.Config.Concurrency
		}
		var errorCount int64
		for _, count := range result.Errors {
			errorCount += count
		}
		if result.Config.Scenario == "recovery" {
			fmt.Fprintf(&output, "| %d | %.2f | %s | %.2f | %d |\n", result.Config.RecoveryTargetMB,
				float64(result.RecoveryLogBytes)/float64(1<<20), result.Config.Target, result.RecoverySeconds, errorCount)
			continue
		}
		fmt.Fprintf(&output, "| %d | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f |\n",
			result.Config.Concurrency, result.Config.Target, result.Successes, errorCount, result.ThroughputPerSecond,
			result.ThroughputUnit, result.Latency.P50, result.Latency.P90, result.Latency.P99, result.Latency.Max)
		if result.Config.Target == "temporal" {
			copy := result
			temporal = &copy
		}
		if result.Config.Target == "zd" && temporal != nil && temporal.ThroughputPerSecond > 0 && temporal.Latency.P50 > 0 {
			fmt.Fprintf(&output, "| %d | zd / temporal | — | — | %.2fx | %.2fx | %.2fx | %.2fx | %.2fx |\n",
				result.Config.Concurrency, result.ThroughputPerSecond/temporal.ThroughputPerSecond,
				result.Latency.P50/temporal.Latency.P50, ratio(result.Latency.P90, temporal.Latency.P90),
				ratio(result.Latency.P99, temporal.Latency.P99), ratio(result.Latency.Max, temporal.Latency.Max))
		}
	}
	return output.String()
}

func ratio(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func RecoveryResult(ctx context.Context, config Config, elapsedSeconds float64) (Result, error) {
	metricsContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	metrics, err := scrapeMetrics(metricsContext, config.MetricsURL)
	if err != nil {
		return Result{}, fmt.Errorf("scrape recovery metrics: %w", err)
	}
	replayedBytes, hasReplayMetric := metrics["dexzd_recovery_last_replayed_bytes"]
	if !hasReplayMetric || replayedBytes <= 0 {
		return Result{}, fmt.Errorf("recovery replay byte metric is missing or zero")
	}
	result := Result{
		SchemaVersion: 1, StartedAt: time.Now().UTC(), Config: config,
		ServerVersion: os.Getenv("DEXBENCH_SERVER_VERSION"), SDKVersion: sdkVersion(),
		Machine: machineInfo(), Attempts: 1, Successes: 1, Errors: map[string]int64{},
		ThroughputPerSecond: 1 / elapsedSeconds, ThroughputUnit: "recoveries",
		Latency:         Percentiles{P50: elapsedSeconds * 1000, P90: elapsedSeconds * 1000, P99: elapsedSeconds * 1000, Max: elapsedSeconds * 1000},
		RecoverySeconds: elapsedSeconds, RecoveryLogBytes: uint64(replayedBytes),
	}
	result.Machine.InstanceType = config.InstanceType
	result.Machine.Region = config.Region
	return result, nil
}

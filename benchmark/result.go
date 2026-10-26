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
	Address string `json:"address"`
	Target string `json:"target"`
	Engine string `json:"engine"`
	Scenario string `json:"scenario"`
	DurationSeconds float64 `json:"duration_seconds"`
	WarmupSeconds float64 `json:"warmup_seconds"`
	Concurrency int `json:"concurrency"`
	WorkerPool int `json:"worker_pool"`
	Steps int `json:"steps"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
	ServerPID int `json:"server_pid,omitempty"`
	ServerProcess string `json:"server_process,omitempty"`
	MetricsURL string `json:"metrics_url,omitempty"`
}

type MachineInfo struct {
	CPUModel string `json:"cpu_model"`
	Cores int `json:"cores"`
	RAMBytes uint64 `json:"ram_bytes"`
	OS string `json:"os"`
	Architecture string `json:"architecture"`
}

type ResourceSample struct {
	At time.Time `json:"at"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes uint64 `json:"rss_bytes"`
}

type ObjectStoreMetrics struct {
	RequestsPerMinute map[string]float64 `json:"requests_per_minute,omitempty"`
	RequestErrors int64 `json:"request_errors"`
	CommitLatencyBuckets map[string]float64 `json:"commit_latency_buckets,omitempty"`
	CommitLatencyCount int64 `json:"commit_latency_count"`
	CommitLatencySumSeconds float64 `json:"commit_latency_sum_seconds"`
}

type Result struct {
	SchemaVersion int `json:"schema_version"`
	StartedAt time.Time `json:"started_at"`
	Config Config `json:"config"`
	ServerVersion string `json:"server_version"`
	SDKVersion string `json:"sdk_version"`
	Machine MachineInfo `json:"machine"`
	Attempts int64 `json:"attempts"`
	Successes int64 `json:"successes"`
	Errors map[string]int64 `json:"errors"`
	ThroughputPerSecond float64 `json:"throughput_per_second"`
	ThroughputUnit string `json:"throughput_unit"`
	Latency Percentiles `json:"latency"`
	StepLatency Percentiles `json:"step_latency,omitempty"`
	StepSamples int `json:"step_samples,omitempty"`
	Resources []ResourceSample `json:"resources,omitempty"`
	ObjectStore *ObjectStoreMetrics `json:"object_store,omitempty"`
}

type latencyCollector struct {
	mu sync.Mutex
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
	if len(values) == 0 { return Percentiles{} }
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	selectValue := func(quantile float64) float64 {
		index := int(math.Ceil(quantile*float64(len(values)))) - 1
		if index < 0 { index = 0 }
		return float64(values[index]) / float64(time.Millisecond)
	}
	return Percentiles{P50: selectValue(0.5), P90: selectValue(0.9), P99: selectValue(0.99), Max: selectValue(1)}
}

func WriteResult(path string, result Result) error {
	file, err := os.Create(path)
	if err != nil { return fmt.Errorf("create result %s: %w", path, err) }
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil { return fmt.Errorf("encode result %s: %w", path, err) }
	return file.Close()
}

func ReadResult(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil { return Result{}, fmt.Errorf("read result %s: %w", path, err) }
	var result Result
	if err := json.Unmarshal(data, &result); err != nil { return Result{}, fmt.Errorf("decode result %s: %w", path, err) }
	if result.SchemaVersion != 1 { return Result{}, fmt.Errorf("unsupported result schema %d in %s", result.SchemaVersion, path) }
	return result, nil
}

func RenderReport(results []Result) string {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Config.Scenario == results[j].Config.Scenario { return results[i].Config.Target < results[j].Config.Target }
		return results[i].Config.Scenario < results[j].Config.Scenario
	})
	var output strings.Builder
	output.WriteString("| Scenario | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |\n")
	output.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, result := range results {
		var errorCount int64
		for _, count := range result.Errors { errorCount += count }
		fmt.Fprintf(&output, "| %s | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f |\n",
			result.Config.Scenario, result.Config.Target, result.Successes, errorCount, result.ThroughputPerSecond,
			result.ThroughputUnit, result.Latency.P50, result.Latency.P90, result.Latency.P99, result.Latency.Max)
	}
	return output.String()
}

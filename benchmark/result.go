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
	Connections     int     `json:"connections,omitempty"`
	Trial           int     `json:"trial,omitempty"`
	// ServerTunables are the server settings this point overrides, as environment names and values.
	ServerTunables map[string]string `json:"server_tunables,omitempty"`
	Steps          int               `json:"steps"`
	TimeoutSeconds float64           `json:"timeout_seconds"`
	ServerPID      int               `json:"server_pid,omitempty"`
	ResourcePIDs   []int             `json:"resource_pids,omitempty"`
	MetricsURL     string            `json:"metrics_url,omitempty"`
	AttributeBytes int               `json:"attribute_bytes"`
	Mode           string            `json:"mode"`
	GitCommit      string            `json:"git_commit,omitempty"`
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
	ServerCores           int                 `json:"server_cores,omitempty"`
	LoadHostResources     []ResourceSample    `json:"load_host_resources,omitempty"`
	MeasurementStartedAt  time.Time           `json:"measurement_started_at"`
	MeasurementEndedAt    time.Time           `json:"measurement_ended_at"`
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

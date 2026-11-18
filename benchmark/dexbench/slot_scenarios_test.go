// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

func slotTestConfig(scenario string, concurrency int) Config {
	return Config{Address: "127.0.0.1:8801", Scenario: scenario, Concurrency: concurrency, DurationSeconds: 1, TimeoutSeconds: 1,
		MetricsURL:  "http://127.0.0.1:9901/metrics",
		SlotStreams: &SlotStreamConfig{StartParallelism: 1, ClientConnections: 1, LoadWave: "square"}}
}

func TestSlotStreamVariantsExpandEveryScenario(t *testing.T) {
	for name, want := range map[string][]string{
		"idle-slots":            {"idle-slots-f1", "idle-slots-f16"},
		"burst":                 {"burst-async", "burst-sync"},
		"fairness":              {"fairness"},
		"periodic":              {"periodic"},
		"backlog-one-partition": {"backlog-one-partition-10ms", "backlog-one-partition-2s"},
	} {
		got := SlotStreamVariants(name)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s expands to %v, want %v", name, got, want)
		}
		for _, variant := range got {
			if !IsSlotStreamScenario(variant) {
				t.Fatalf("%s is not recognized", variant)
			}
		}
	}
	if SlotStreamVariants("sequential-steps") != nil || IsSlotStreamScenario("sequential-steps") {
		t.Fatal("a closed-loop scenario was treated as a slot scenario")
	}
}

func TestResolveSlotStreamSettingsAppliesScenarioDefaults(t *testing.T) {
	for _, test := range []struct {
		scenario    string
		concurrency int
		workerSlots []int
		flowTypes   int
		handler     time.Duration
		durability  dex.StepDurability
	}{
		{"idle-slots-f16", 600, []int{256, 256, 88}, 16, 10 * time.Millisecond, dex.StepDurabilityAsync},
		{"burst-sync", 198, []int{99, 99}, 1, time.Second, dex.StepDurabilitySync},
		{"fairness", 80, []int{10, 10, 10, 10, 10, 10, 10, 10}, 1, 100 * time.Millisecond, dex.StepDurabilityAsync},
		{"periodic", 100, []int{50, 50}, 1, 100 * time.Millisecond, dex.StepDurabilityAsync},
		{"backlog-one-partition-2s", 1000, []int{99}, 1, 2 * time.Second, dex.StepDurabilityAsync},
	} {
		config := slotTestConfig(test.scenario, test.concurrency)
		settings, err := resolveSlotStreamSettings(&config)
		if err != nil {
			t.Fatalf("%s: %v", test.scenario, err)
		}
		if !reflect.DeepEqual(settings.workerSlots, test.workerSlots) || settings.flowTypes != test.flowTypes ||
			settings.handlerDuration != test.handler || settings.durability != test.durability {
			t.Fatalf("%s resolved to %+v", test.scenario, settings)
		}
		if config.SlotStreams.Workers != len(test.workerSlots) || config.SlotStreams.FlowTypes != test.flowTypes {
			t.Fatalf("%s did not record its resolved settings: %+v", test.scenario, config.SlotStreams)
		}
	}
}

func TestResolveSlotStreamSettingsRejectsAdaptiveSlotsUntilTheSDKHasThem(t *testing.T) {
	for _, options := range []SlotStreamConfig{{IsAdaptiveSlots: true}, {MinOpenSlots: 8}} {
		config := slotTestConfig("burst-async", 99)
		options.StartParallelism, options.ClientConnections, options.LoadWave = 1, 1, "square"
		config.SlotStreams = &options
		if _, err := resolveSlotStreamSettings(&config); !errors.Is(err, errAdaptiveSlotsUnsupported) {
			t.Fatalf("adaptive options %+v gave %v", options, err)
		}
	}
}

func TestResolveSlotStreamSettingsRequiresMetricsForIdleSlots(t *testing.T) {
	config := slotTestConfig("idle-slots-f1", 200)
	config.MetricsURL = ""
	if _, err := resolveSlotStreamSettings(&config); err == nil || !strings.Contains(err.Error(), "metrics-url") {
		t.Fatalf("idle-slots without metrics gave %v", err)
	}
}

func TestLoadWaves(t *testing.T) {
	period := 10 * time.Second
	if squareWaveRate(time.Second, period, 1, 9) != 9 || squareWaveRate(6*time.Second, period, 1, 9) != 1 || squareWaveRate(11*time.Second, period, 1, 9) != 9 {
		t.Fatal("square wave is not high then low")
	}
	for elapsed, want := range map[time.Duration]float64{0: 5, 2500 * time.Millisecond: 9, 7500 * time.Millisecond: 1} {
		if got := sineWaveRate(elapsed, period, 1, 9); math.Abs(got-want) > 1e-9 {
			t.Fatalf("sine at %v = %v, want %v", elapsed, got, want)
		}
	}
}

func TestOpenLoopCreditKeepsFractionalStarts(t *testing.T) {
	credit, total := 0.0, 0
	for range 200 {
		var due int
		due, credit = openLoopCredit(credit, 50, 5*time.Millisecond)
		total += due
	}
	// 200 ticks of 5 ms at 50 per second is one second, so 50 starts.
	if total != 50 && total != 49 {
		t.Fatalf("scheduled %d starts in one second at 50 per second", total)
	}
}

func TestJainFairnessIndexAndShares(t *testing.T) {
	if index := jainFairnessIndex([]int64{5, 5, 5, 5}); index != 1 {
		t.Fatalf("equal shares gave %v", index)
	}
	if index := jainFairnessIndex([]int64{8, 0, 0, 0}); index != 0.25 {
		t.Fatalf("one Worker with everything gave %v", index)
	}
	if jainFairnessIndex([]int64{0, 0}) != 0 || workerShares([]int64{0, 0}) != nil {
		t.Fatal("no Steps must give no index and no shares")
	}
	if shares := workerShares([]int64{1, 3}); !reflect.DeepEqual(shares, []float64{0.25, 0.75}) {
		t.Fatalf("shares %v", shares)
	}
}

func TestSlotMetricsFromSeriesSumsFamilies(t *testing.T) {
	text := strings.Join([]string{
		`# TYPE dexzd_matching_worker_streams gauge`,
		`dexzd_matching_worker_streams 200`,
		`dexzd_step_task_queue_entries 7`,
		`dexzd_worker_stream_events_total{event="closed"} 3`,
		`dexzd_worker_stream_events_total{event="delivered"} 90`,
		`dexzd_step_attempts_returned_total{reason="not_delivered"} 2`,
		`dexzd_step_attempts_returned_total{reason="other"} 5`,
		`dexzd_step_schedule_to_start_latency_seconds_bucket{le="0.005"} 10`,
		`dexzd_step_schedule_to_start_latency_seconds_bucket{le="+Inf"} 12`,
		`dexzd_step_schedule_to_start_latency_seconds_count 12`,
		`dexzd_object_store_requests_total{operation="PUT"} 4`,
	}, "\n")
	series, err := parseMetricSeries(strings.NewReader(text), slotMetricPrefixes)
	if err != nil {
		t.Fatal(err)
	}
	metrics := slotMetricsFromSeries(series)
	if metrics.workerStreams != 200 || metrics.taskQueueEntries != 7 || metrics.streamsClosed != 3 || metrics.notDelivered != 2 ||
		metrics.scheduleToStartCount != 12 || metrics.scheduleToStartBuckets["0.005"] != 10 || metrics.scheduleToStartBuckets["+Inf"] != 12 {
		t.Fatalf("metrics %+v", metrics)
	}
	delta := slotMetricsDelta(slotServerMetrics{scheduleToStartBuckets: map[string]float64{"0.005": 4}, scheduleToStartCount: 5}, metrics)
	if delta.ScheduleToStartBuckets["0.005"] != 6 || delta.ScheduleToStartCount != 7 {
		t.Fatalf("delta %+v", delta)
	}
}

func TestSummarizeIdleStreamCostUsesPhaseWindows(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	sample := func(offset int, rssBytes uint64, cpuPercent float64) ResourceSample {
		return ResourceSample{At: start.Add(time.Duration(offset) * time.Second), IntervalSeconds: 1, RSSBytes: rssBytes, CPUPercent: cpuPercent}
	}
	result := Result{
		Resources: []ResourceSample{sample(1, 100, 1), sample(2, 100, 1), sample(6, 300, 3), sample(7, 300, 3)},
		SlotStreams: &SlotStreamResult{
			Idle: &IdleStreamCost{Streams: 100},
			Phases: []SlotStreamPhase{
				{Name: slotPhaseBaseline, StartedAt: start, EndedAt: start.Add(3 * time.Second)},
				{Name: slotPhaseIdle, StartedAt: start.Add(5 * time.Second), EndedAt: start.Add(8 * time.Second)},
			},
		},
	}
	summarizeIdleStreamCost(&result)
	idle := result.SlotStreams.Idle
	if idle.RSSBytesPerStream != 2 || idle.CPUPercentPer1000 != 20 || idle.ResourceSamples != 4 {
		t.Fatalf("idle summary %+v", idle)
	}
}

func TestSplitSlots(t *testing.T) {
	if got := splitSlots(513, 256); !reflect.DeepEqual(got, []int{256, 256, 1}) {
		t.Fatalf("splitSlots = %v", got)
	}
	if got := splitSlotsEvenly(10, 0, 4); !reflect.DeepEqual(got, []int{4, 4, 4}) {
		t.Fatalf("splitSlotsEvenly from slots = %v", got)
	}
	if got := splitSlotsEvenly(10, 4, 0); !reflect.DeepEqual(got, []int{3, 3, 3, 3}) {
		t.Fatalf("splitSlotsEvenly from Workers = %v", got)
	}
}

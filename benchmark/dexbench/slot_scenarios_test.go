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
		SlotStreams: &SlotStreamConfig{StartParallelism: 1, ClientConnections: 1, LoadWave: "square", LoadPeriodSeconds: 10}}
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

func TestOpenLoopScheduleKeepsPlannedTimesAfterALateWakeUp(t *testing.T) {
	constant := func(time.Duration) float64 { return 50 }
	punctual := &openLoopSchedule{}
	var punctualOffsets []time.Duration
	for tick := 5 * time.Millisecond; tick <= time.Second; tick += 5 * time.Millisecond {
		punctualOffsets = append(punctualOffsets, punctual.advanceTo(tick, constant)...)
	}
	late := (&openLoopSchedule{}).advanceTo(time.Second, constant)
	if len(punctualOffsets) != 50 || !reflect.DeepEqual(late, punctualOffsets) {
		t.Fatalf("one late wake-up planned %v, punctual ticks planned %v", late, punctualOffsets)
	}
	if late[0] != 20*time.Millisecond || late[49] != time.Second {
		t.Fatalf("starts are not spread at 20 ms: first %v, last %v", late[0], late[49])
	}
}

func TestOpenLoopScheduleIntegratesTheRateAcrossAWaveChange(t *testing.T) {
	// 100 per second for the first half second, then 0, seen in one late wake-up.
	wave := func(planned time.Duration) float64 { return squareWaveRate(planned, time.Second, 0, 100) }
	due := (&openLoopSchedule{}).advanceTo(time.Second, wave)
	if len(due) != 50 {
		t.Fatalf("planned %d starts, want 50", len(due))
	}
	for _, offset := range due {
		if offset > 500*time.Millisecond {
			t.Fatalf("start planned at %v, inside the zero-rate half", offset)
		}
	}
}

func TestRecorderTagsCountLatencyAndBoundsWithTheSamePhase(t *testing.T) {
	recorder := newSlotProbeRecorder(2)
	first, _ := recorder.beginPhase()
	recorder.recordStart(0, time.Now())
	second, secondStartedAt := recorder.beginPhase()
	recorder.recordStart(1, time.Now())
	recorder.recordStart(1, time.Now())
	firstCounts, firstLatencies, _, _ := recorder.phaseSteps(first)
	secondCounts, secondLatencies, firstStart, lastStart := recorder.phaseSteps(second)
	if !reflect.DeepEqual(firstCounts, []int64{1, 0}) || len(firstLatencies) != 1 ||
		!reflect.DeepEqual(secondCounts, []int64{0, 2}) || len(secondLatencies) != 2 {
		t.Fatalf("phase buckets %v %v, %v %v", firstCounts, firstLatencies, secondCounts, secondLatencies)
	}
	if firstStart.Before(secondStartedAt) || lastStart.Before(firstStart) {
		t.Fatalf("second phase bounds %v..%v, phase start %v", firstStart, lastStart, secondStartedAt)
	}
}

func TestSlotPhaseAtUsesTheEventTimestamp(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	phases := []slotPhaseBucket{{startedAt: start}, {startedAt: start.Add(time.Second)}}
	for at, want := range map[time.Time]int{
		start.Add(-time.Nanosecond):              -1,
		start:                                    0,
		start.Add(time.Second - time.Nanosecond): 0,
		start.Add(time.Second):                   1,
	} {
		if got := slotPhaseAt(phases, at); got != want {
			t.Fatalf("phase at %v = %d, want %d", at, got, want)
		}
	}
}

func TestUnusableReasonsReportEveryLossAndAnyLateSubmission(t *testing.T) {
	if reasons := unusableReasons(SlotRequestLoss{Planned: 10, FlowsStarted: 10, StepsStarted: 10, MaxSchedulerLagMs: 5}); reasons != nil {
		t.Fatalf("a complete point is unusable: %v", reasons)
	}
	if reasons := unusableReasons(SlotRequestLoss{Planned: 10, FlowsStarted: 10, StepsStarted: 10, LateSubmissions: 1, MaxSchedulerLagMs: 25}); len(reasons) != 1 {
		t.Fatalf("one late submission gave %v", reasons)
	}
	reasons := unusableReasons(SlotRequestLoss{Planned: 10, Skipped: 1, StartFailed: 2, StepsNotStarted: 3, LateSubmissions: 4, MaxSchedulerLagMs: 250})
	if len(reasons) != 4 {
		t.Fatalf("reasons %v", reasons)
	}
}

func TestServerIdentityNeedsTwoKnownDifferentProcesses(t *testing.T) {
	old := serverIdentity{processID: 10}
	for _, test := range []struct {
		current     serverIdentity
		isRestarted bool
	}{
		{serverIdentity{processID: 10}, false},
		// An unreadable PID file or a failed scrape gives an unknown identity, never a restart.
		{serverIdentity{}, false},
		{serverIdentity{processID: 11}, true},
	} {
		if got := test.current.isOtherProcessThan(old); got != test.isRestarted {
			t.Fatalf("%+v against %+v = %v", test.current, old, got)
		}
	}
	if (serverIdentity{startTimeSeconds: 2}).isOtherProcessThan(serverIdentity{}) {
		t.Fatal("an unknown old identity counted as a restart")
	}
	if !(serverIdentity{startTimeSeconds: 2}).isOtherProcessThan(serverIdentity{startTimeSeconds: 1}) {
		t.Fatal("a new process start time was not a restart")
	}
}

func TestSineWaveAtTheMinimumPeriodMatchesItsIntegral(t *testing.T) {
	period, low, high := minWavePeriod, 0.0, 1000.0
	wave := func(planned time.Duration) float64 { return sineWaveRate(planned, period, low, high) }
	due := (&openLoopSchedule{}).advanceTo(period/4, wave)
	quarter := period.Seconds() / 4
	// The integral of low + (high-low)(1+sin)/2 over the first quarter period.
	want := low*quarter + (high-low)/2*(quarter+period.Seconds()/(2*math.Pi))
	if math.Abs(float64(len(due))-want) > 1 {
		t.Fatalf("planned %d starts in the first quarter, integral %.2f", len(due), want)
	}
	full := (&openLoopSchedule{}).advanceTo(10*period, wave)
	if wantFull := (low + high) / 2 * 10 * period.Seconds(); math.Abs(float64(len(full))-wantFull) > wantFull*0.01 {
		t.Fatalf("planned %d starts over ten periods, integral %.1f", len(full), wantFull)
	}
}

func TestResolveSlotStreamSettingsRejectsNonFiniteRatesAndShortPeriods(t *testing.T) {
	for name, change := range map[string]func(*SlotStreamConfig){
		"NaN probe rate":         func(options *SlotStreamConfig) { options.ProbeRatePerSecond = math.NaN() },
		"infinite percent":       func(options *SlotStreamConfig) { options.LoadPercents = []float64{math.Inf(1)} },
		"negative low":           func(options *SlotStreamConfig) { options.LoadLowPercent = -1 },
		"two-millisecond period": func(options *SlotStreamConfig) { options.LoadPeriodSeconds = 0.002 },
	} {
		config := slotTestConfig("periodic", 99)
		change(config.SlotStreams)
		if _, err := resolveSlotStreamSettings(&config); err == nil {
			t.Fatalf("%s was accepted", name)
		}
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
	if got := splitSlotsEvenly(10, 0, 4); !reflect.DeepEqual(got, []int{4, 3, 3}) {
		t.Fatalf("splitSlotsEvenly from slots = %v", got)
	}
	if got := splitSlotsEvenly(10, 4, 0); !reflect.DeepEqual(got, []int{3, 3, 2, 2}) {
		t.Fatalf("splitSlotsEvenly from Workers = %v", got)
	}
	if got := splitSlotsEvenly(3, 8, 0); !reflect.DeepEqual(got, []int{1, 1, 1}) {
		t.Fatalf("splitSlotsEvenly with more Workers than slots = %v", got)
	}
}

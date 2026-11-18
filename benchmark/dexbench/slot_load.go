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
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// slotProbeRecorder counts probe Steps per Worker and keeps their schedule-to-start latencies.
type slotProbeRecorder struct {
	workerStarted  []atomic.Int64
	workerFinished []atomic.Int64
	workerBusy     []atomic.Int64
	// firstStartUnixNano and lastStartUnixNano bound the Step starts since the last resetStartBounds.
	firstStartUnixNano atomic.Int64
	lastStartUnixNano  atomic.Int64
	// mutex guards the phase buckets, so a Step's count and latency always land in the same phase.
	mutex             sync.Mutex
	intervalLatencies []time.Duration
	currentPhase      int
	phaseWorkerSteps  [][]int64
	phaseLatencies    [][]time.Duration
}

func newSlotProbeRecorder(workers int) *slotProbeRecorder {
	return &slotProbeRecorder{
		workerStarted:  make([]atomic.Int64, workers),
		workerFinished: make([]atomic.Int64, workers),
		workerBusy:     make([]atomic.Int64, workers),
		currentPhase:   -1,
	}
}

// beginPhase opens a new phase bucket for later Step starts and returns its index.
func (recorder *slotProbeRecorder) beginPhase() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.phaseWorkerSteps = append(recorder.phaseWorkerSteps, make([]int64, len(recorder.workerStarted)))
	recorder.phaseLatencies = append(recorder.phaseLatencies, nil)
	recorder.currentPhase = len(recorder.phaseLatencies) - 1
	return recorder.currentPhase
}

// phaseSteps returns a copy of the per-Worker Step starts and the latencies recorded in phase.
func (recorder *slotProbeRecorder) phaseSteps(phase int) ([]int64, []time.Duration) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]int64(nil), recorder.phaseWorkerSteps[phase]...), append([]time.Duration(nil), recorder.phaseLatencies[phase]...)
}

// recordStart counts a Step start on worker and its delay from the planned start, in one phase bucket.
func (recorder *slotProbeRecorder) recordStart(worker int, plannedAt time.Time) {
	now := time.Now()
	recorder.workerStarted[worker].Add(1)
	recorder.workerBusy[worker].Add(1)
	recorder.firstStartUnixNano.CompareAndSwap(0, now.UnixNano())
	recorder.lastStartUnixNano.Store(now.UnixNano())
	latency := now.Sub(plannedAt)
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.intervalLatencies = append(recorder.intervalLatencies, latency)
	if recorder.currentPhase >= 0 {
		recorder.phaseWorkerSteps[recorder.currentPhase][worker]++
		recorder.phaseLatencies[recorder.currentPhase] = append(recorder.phaseLatencies[recorder.currentPhase], latency)
	}
}

func (recorder *slotProbeRecorder) recordFinish(worker int) {
	recorder.workerBusy[worker].Add(-1)
	recorder.workerFinished[worker].Add(1)
}

func (recorder *slotProbeRecorder) takeIntervalLatencies() []time.Duration {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	latencies := recorder.intervalLatencies
	recorder.intervalLatencies = nil
	return latencies
}

func (recorder *slotProbeRecorder) resetStartBounds() {
	recorder.firstStartUnixNano.Store(0)
	recorder.lastStartUnixNano.Store(0)
}

// totals returns the cumulative started and finished Steps and the handlers running now.
func (recorder *slotProbeRecorder) totals() (int64, int64, int64) {
	var started, finished, busy int64
	for index := range recorder.workerStarted {
		started += recorder.workerStarted[index].Load()
		finished += recorder.workerFinished[index].Load()
		busy += recorder.workerBusy[index].Load()
	}
	return started, finished, busy
}

func (recorder *slotProbeRecorder) busyPerWorker() []int64 {
	busy := make([]int64, len(recorder.workerBusy))
	for index := range recorder.workerBusy {
		busy[index] = recorder.workerBusy[index].Load()
	}
	return busy
}

// slotServerMetrics are the server series the slot stream scenarios follow.
type slotServerMetrics struct {
	workerStreams          float64
	taskQueueEntries       float64
	streamsClosed          float64
	notDelivered           float64
	scheduleToStartBuckets map[string]float64
	scheduleToStartCount   float64
	// processStartTimeSeconds is 0 where the server does not export it (verbose level, Linux only).
	processStartTimeSeconds float64
}

var slotMetricPrefixes = []string{
	"process_start_time_seconds",
	"dexzd_matching_worker_streams",
	"dexzd_step_task_queue_entries",
	"dexzd_worker_stream_events",
	"dexzd_step_attempts_returned",
	"dexzd_step_schedule_to_start_latency_seconds",
}

func scrapeSlotMetrics(ctx context.Context, address string) (slotServerMetrics, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return slotServerMetrics{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return slotServerMetrics{}, err
	}
	series, parseErr := parseMetricSeries(response.Body, slotMetricPrefixes)
	if closeErr := response.Body.Close(); closeErr != nil && parseErr == nil {
		parseErr = closeErr
	}
	if response.StatusCode != http.StatusOK {
		return slotServerMetrics{}, fmt.Errorf("metrics returned %s", response.Status)
	}
	if parseErr != nil {
		return slotServerMetrics{}, parseErr
	}
	return slotMetricsFromSeries(series), nil
}

// slotMetricsFromSeries sums each family over its labels; schedule-to-start buckets are keyed by le.
func slotMetricsFromSeries(series map[string]float64) slotServerMetrics {
	metrics := slotServerMetrics{scheduleToStartBuckets: map[string]float64{}}
	for name, value := range series {
		family := metricFamilyName(name)
		switch {
		case family == "process_start_time_seconds":
			metrics.processStartTimeSeconds = value
		case family == "dexzd_matching_worker_streams":
			metrics.workerStreams += value
		case family == "dexzd_step_task_queue_entries":
			metrics.taskQueueEntries += value
		case family == "dexzd_worker_stream_events_total" && metricLabel(name, "event") == "closed":
			metrics.streamsClosed += value
		case family == "dexzd_step_attempts_returned_total" && metricLabel(name, "reason") == "not_delivered":
			metrics.notDelivered += value
		case family == "dexzd_step_schedule_to_start_latency_seconds_bucket":
			metrics.scheduleToStartBuckets[metricLabel(name, "le")] += value
		case family == "dexzd_step_schedule_to_start_latency_seconds_count":
			metrics.scheduleToStartCount += value
		}
	}
	return metrics
}

// metricFamilyName returns the series name without its label set.
func metricFamilyName(series string) string {
	for index, character := range series {
		if character == '{' {
			return series[:index]
		}
	}
	return series
}

// slotMetricsDelta returns after minus before for the counters of one point.
func slotMetricsDelta(before, after slotServerMetrics) *SlotServerMetricsDelta {
	delta := &SlotServerMetricsDelta{
		ScheduleToStartBuckets: map[string]float64{},
		ScheduleToStartCount:   after.scheduleToStartCount - before.scheduleToStartCount,
		StreamsClosed:          after.streamsClosed - before.streamsClosed,
		NotDelivered:           after.notDelivered - before.notDelivered,
	}
	for bucket, value := range after.scheduleToStartBuckets {
		delta.ScheduleToStartBuckets[bucket] = value - before.scheduleToStartBuckets[bucket]
	}
	return delta
}

// squareWaveRate returns high for the first half of each period and low for the second.
func squareWaveRate(elapsed, period time.Duration, low, high float64) float64 {
	if period <= 0 {
		return high
	}
	if elapsed%period < period/2 {
		return high
	}
	return low
}

// sineWaveRate starts at the midpoint, peaks at a quarter period, and bottoms out at three quarters.
func sineWaveRate(elapsed, period time.Duration, low, high float64) float64 {
	if period <= 0 {
		return high
	}
	phase := 2 * math.Pi * float64(elapsed%period) / float64(period)
	return low + (high-low)*(1+math.Sin(phase))/2
}

// isRestartedServer reports whether after comes from a different server process than before.
// A failed scrape, a new process start time, or a reset counter each show a restart.
func isRestartedServer(before slotServerMetrics, after slotServerMetrics, scrapeErr error) bool {
	if scrapeErr != nil {
		return true
	}
	if before.processStartTimeSeconds != 0 && after.processStartTimeSeconds != before.processStartTimeSeconds {
		return true
	}
	return after.scheduleToStartCount < before.scheduleToStartCount || after.streamsClosed < before.streamsClosed
}

// openLoopPlanStep is the resolution at which the rate is integrated over planned time.
const openLoopPlanStep = time.Millisecond

// openLoopSchedule integrates a rate over planned time, independent of when the scheduler wakes up.
type openLoopSchedule struct {
	planned time.Duration
	credit  float64
}

// advanceTo returns the planned offsets of every start due up to elapsed, using rate at each planned time.
// A late wake-up returns the starts it missed with their original planned offsets.
func (schedule *openLoopSchedule) advanceTo(elapsed time.Duration, rate func(time.Duration) float64) []time.Duration {
	var due []time.Duration
	for schedule.planned < elapsed {
		step := min(openLoopPlanStep, elapsed-schedule.planned)
		schedule.credit += rate(schedule.planned) * step.Seconds()
		schedule.planned += step
		// The epsilon absorbs float drift, so 500 steps of 0.1 make 50 starts, not 49.
		for schedule.credit >= 1-1e-9 {
			schedule.credit--
			due = append(due, schedule.planned)
		}
	}
	return due
}

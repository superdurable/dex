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
	// mutex guards the phase buckets, so a Step's count, latency, and start bounds land in one phase.
	mutex             sync.Mutex
	intervalLatencies []time.Duration
	phases            []slotPhaseBucket
}

// slotPhaseBucket holds the Step starts whose timestamps fall at or after startedAt and before the next bucket.
type slotPhaseBucket struct {
	startedAt   time.Time
	workerSteps []int64
	latencies   []time.Duration
	firstStart  time.Time
	lastStart   time.Time
}

func newSlotProbeRecorder(workers int) *slotProbeRecorder {
	return &slotProbeRecorder{
		workerStarted:  make([]atomic.Int64, workers),
		workerFinished: make([]atomic.Int64, workers),
		workerBusy:     make([]atomic.Int64, workers),
	}
}

// beginPhase opens a phase bucket starting now and returns its index and start time.
// Reading the time under the lock makes the boundary exact: a start stamped later locks after this.
func (recorder *slotProbeRecorder) beginPhase() (int, time.Time) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	startedAt := time.Now()
	recorder.phases = append(recorder.phases, slotPhaseBucket{startedAt: startedAt, workerSteps: make([]int64, len(recorder.workerStarted))})
	return len(recorder.phases) - 1, startedAt
}

// phaseSteps returns a copy of a phase's per-Worker Step starts, its latencies, and its first and last start times.
// The times are zero when the phase saw no Step start.
func (recorder *slotProbeRecorder) phaseSteps(phase int) ([]int64, []time.Duration, time.Time, time.Time) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	bucket := recorder.phases[phase]
	return append([]int64(nil), bucket.workerSteps...), append([]time.Duration(nil), bucket.latencies...), bucket.firstStart, bucket.lastStart
}

// recordStart counts a Step start on worker and its delay from the planned start, in the phase of its timestamp.
func (recorder *slotProbeRecorder) recordStart(worker int, plannedAt time.Time) {
	now := time.Now()
	recorder.workerStarted[worker].Add(1)
	recorder.workerBusy[worker].Add(1)
	latency := now.Sub(plannedAt)
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.intervalLatencies = append(recorder.intervalLatencies, latency)
	phase := slotPhaseAt(recorder.phases, now)
	if phase < 0 {
		return
	}
	bucket := &recorder.phases[phase]
	bucket.workerSteps[worker]++
	bucket.latencies = append(bucket.latencies, latency)
	if bucket.firstStart.IsZero() || now.Before(bucket.firstStart) {
		bucket.firstStart = now
	}
	if now.After(bucket.lastStart) {
		bucket.lastStart = now
	}
}

// slotPhaseAt returns the index of the last bucket that started at or before at, or -1 before the first.
func slotPhaseAt(phases []slotPhaseBucket, at time.Time) int {
	for index := len(phases) - 1; index >= 0; index-- {
		if !at.Before(phases[index].startedAt) {
			return index
		}
	}
	return -1
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

// serverIdentity names one server process: the PID in a supervisor PID file, or its exported start time.
type serverIdentity struct {
	processID        int
	startTimeSeconds float64
}

// isKnown reports whether the identity was actually read; an unknown identity never shows a restart.
func (identity serverIdentity) isKnown() bool {
	return identity.processID != 0 || identity.startTimeSeconds != 0
}

// isOtherProcessThan reports whether both identities were read and name different processes.
func (identity serverIdentity) isOtherProcessThan(before serverIdentity) bool {
	return identity.isKnown() && before.isKnown() && identity != before
}

// openLoopPlanStep is the resolution at which the rate is integrated over planned time.
const openLoopPlanStep = time.Millisecond

// minWavePeriod keeps a wave at 100 or more integration steps per period, so sampling stays within 1% of its integral.
const minWavePeriod = 100 * openLoopPlanStep

// openLoopSchedule integrates a rate over planned time, independent of when the scheduler wakes up.
type openLoopSchedule struct {
	planned time.Duration
	credit  float64
}

// advanceTo returns the planned offsets of every start due up to elapsed, sampling rate at each step's midpoint.
// A late wake-up returns the starts it missed with their original planned offsets.
// rate must return finite, nonnegative values; resolveSlotStreamSettings validates every rate input.
func (schedule *openLoopSchedule) advanceTo(elapsed time.Duration, rate func(time.Duration) float64) []time.Duration {
	var due []time.Duration
	for schedule.planned < elapsed {
		step := min(openLoopPlanStep, elapsed-schedule.planned)
		schedule.credit += rate(schedule.planned+step/2) * step.Seconds()
		schedule.planned += step
		// The epsilon absorbs float drift, so 500 steps of 0.1 make 50 starts, not 49.
		for schedule.credit >= 1-1e-9 {
			schedule.credit--
			due = append(due, schedule.planned)
		}
	}
	return due
}

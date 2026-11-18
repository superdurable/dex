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
	mutex              sync.Mutex
	intervalLatencies  []time.Duration
	phaseLatencies     []time.Duration
}

func newSlotProbeRecorder(workers int) *slotProbeRecorder {
	return &slotProbeRecorder{
		workerStarted:  make([]atomic.Int64, workers),
		workerFinished: make([]atomic.Int64, workers),
		workerBusy:     make([]atomic.Int64, workers),
	}
}

// recordStart counts a Step start on worker and its delay from the scheduled start.
func (recorder *slotProbeRecorder) recordStart(worker int, scheduledAt time.Time) {
	now := time.Now()
	recorder.workerStarted[worker].Add(1)
	recorder.workerBusy[worker].Add(1)
	recorder.firstStartUnixNano.CompareAndSwap(0, now.UnixNano())
	recorder.lastStartUnixNano.Store(now.UnixNano())
	latency := now.Sub(scheduledAt)
	recorder.mutex.Lock()
	recorder.intervalLatencies = append(recorder.intervalLatencies, latency)
	recorder.phaseLatencies = append(recorder.phaseLatencies, latency)
	recorder.mutex.Unlock()
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

func (recorder *slotProbeRecorder) takePhaseLatencies() []time.Duration {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	latencies := recorder.phaseLatencies
	recorder.phaseLatencies = nil
	return latencies
}

func (recorder *slotProbeRecorder) resetStartBounds() {
	recorder.firstStartUnixNano.Store(0)
	recorder.lastStartUnixNano.Store(0)
}

// startedPerWorker returns a copy of the cumulative Step starts of every Worker.
func (recorder *slotProbeRecorder) startedPerWorker() []int64 {
	counts := make([]int64, len(recorder.workerStarted))
	for index := range recorder.workerStarted {
		counts[index] = recorder.workerStarted[index].Load()
	}
	return counts
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
}

var slotMetricPrefixes = []string{
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

// openLoopCredit adds rate*interval to credit and returns the whole starts now due and the remaining credit.
func openLoopCredit(credit, ratePerSecond float64, interval time.Duration) (int, float64) {
	credit += ratePerSecond * interval.Seconds()
	due := math.Floor(credit)
	return int(due), credit - due
}

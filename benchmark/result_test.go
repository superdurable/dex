// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"strings"
	"testing"
	"time"
)

func TestPercentilesUseNearestRank(t *testing.T) {
	values := make([]time.Duration, 100)
	for index := range values {
		values[index] = time.Duration(100-index) * time.Millisecond
	}
	got := calculatePercentiles(values)
	if got.P50 != 50 || got.P90 != 90 || got.P99 != 99 || got.Max != 100 {
		t.Fatalf("unexpected percentiles: %+v", got)
	}
	if empty := calculatePercentiles(nil); empty != (Percentiles{}) {
		t.Fatalf("empty percentiles: %+v", empty)
	}
}

func TestReportSortsAndSumsErrors(t *testing.T) {
	results := []Result{
		{Config: Config{Scenario: "rpc-plain", Target: "zd", Concurrency: 8}, Successes: 8, Errors: map[string]int64{"timeout": 2, "invalid": 1}, ThroughputPerSecond: 4, ThroughputUnit: "rpcs", Latency: Percentiles{P50: 3, P90: 6}},
		{Config: Config{Scenario: "rpc-plain", Target: "temporal", Concurrency: 8}, Successes: 10, ThroughputPerSecond: 5, ThroughputUnit: "rpcs", Latency: Percentiles{P50: 6, P90: 12}},
	}
	got := RenderReport(results)
	if !strings.Contains(got, "| 8 | zd | 8 | 3 | 4.00 rpcs/s | 3.00 |") {
		t.Fatalf("missing zd row: %s", got)
	}
	if strings.Index(got, "| temporal |") > strings.Index(got, "| zd |") {
		t.Fatalf("rows not sorted: %s", got)
	}
	if !strings.Contains(got, "| 8 | zd / temporal | — | — | 0.80x | 0.50x | 0.50x |") {
		t.Fatalf("missing ratios: %s", got)
	}
}

func TestResultJSONRoundTrip(t *testing.T) {
	path := t.TempDir() + "/result.json"
	want := Result{SchemaVersion: 2, Config: Config{Scenario: "attribute-read-direct", Target: "zd", Concurrency: 32}, Attempts: 12, Successes: 11, Errors: map[string]int64{"timeout": 1}}
	if err := WriteResult(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Scenario != want.Config.Scenario || got.Config.Concurrency != want.Config.Concurrency || got.Successes != want.Successes || got.Errors["timeout"] != 1 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestMetricDifferenceReportsRequestsAndCommitHistogram(t *testing.T) {
	before := map[string]float64{"dexzd_object_store_requests_total{operation=\"put\"}": 10, "dexzd_commit_latency_seconds_count": 3}
	after := map[string]float64{"dexzd_object_store_requests_total{operation=\"put\"}": 14, "dexzd_commit_latency_seconds_count": 5}
	got := metricDifference(before, after, 30*time.Second)
	if got.RequestsPerMinute["dexzd_object_store_requests_total{operation=\"put\"}"] != 8 || got.CommitLatencyCount != 2 {
		t.Fatalf("wrong metric difference: %+v", got)
	}
}

func TestReportShowsRecoveryLogSizes(t *testing.T) {
	results := []Result{
		{Config: Config{Scenario: "recovery", Target: "zd", RecoveryTargetMB: 500}, RecoveryLogBytes: 499 << 20, RecoverySeconds: 12.5},
		{Config: Config{Scenario: "recovery", Target: "zd", RecoveryTargetMB: 100}, RecoveryLogBytes: 101 << 20, RecoverySeconds: 3.5},
	}
	got := RenderReport(results)
	if !strings.Contains(got, "| 100 | 101.00 | zd | 3.500 | 0.000 | 0 |") || !strings.Contains(got, "| 500 | 499.00 | zd | 12.500 | 0.000 | 0 |") {
		t.Fatalf("missing recovery sizes: %s", got)
	}
}

func TestChartPlotsBothTargetsAndThroughputRatio(t *testing.T) {
	results := []Result{
		{Config: Config{Target: "temporal", Scenario: "rpc-plain", Concurrency: 8}, Successes: 1, ThroughputPerSecond: 100, ThroughputUnit: "rpcs"},
		{Config: Config{Target: "zd", Scenario: "rpc-plain", Concurrency: 8}, Successes: 1, ThroughputPerSecond: 250, ThroughputUnit: "rpcs"},
		{Config: Config{Target: "zd", Scenario: "recovery", RecoveryTargetMB: 100}, RecoverySeconds: 1.5},
	}
	page := RenderChartHTML(results)
	for _, expected := range []string{"<h2>rpc-plain</h2>", "<b>2.50×</b>", "Throughput (rpcs/s)", `polyline class="line temporal"`, `polyline class="line zd"`, "<h2>recovery</h2>"} {
		if !strings.Contains(page, expected) {
			t.Errorf("chart page is missing %q", expected)
		}
	}
}

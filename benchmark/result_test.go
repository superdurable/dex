// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"os"
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
		{Config: Config{Scenario: "rpc-plain", Target: "zd", Concurrency: 32}, Successes: 9, ThroughputPerSecond: 9, ThroughputUnit: "rpcs"},
		{Config: Config{Scenario: "rpc-plain", Target: "zd", Concurrency: 8}, Successes: 8, Errors: map[string]int64{"timeout": 2, "invalid": 1}, ThroughputPerSecond: 4, ThroughputUnit: "rpcs", Latency: Percentiles{P50: 3, P90: 6}},
	}
	got := RenderReport(results)
	if !strings.Contains(got, "| 8 | zd | 8 | 3 | 4.00 rpcs/s | 3.00 |") {
		t.Fatalf("missing zd row: %s", got)
	}
	if strings.Index(got, "| 32 | zd |") < strings.Index(got, "| 8 | zd |") {
		t.Fatalf("rows not sorted by concurrency: %s", got)
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

// TestReadResultAcceptsRemovedFields keeps result files from earlier benchmark versions readable.
func TestReadResultAcceptsRemovedFields(t *testing.T) {
	path := t.TempDir() + "/old.json"
	old := `{"schema_version": 2, "config": {"target": "temporal", "scenario": "recovery", "concurrency": 1,
		"attribute_store_name": "orders", "resource_cgroups": ["/sys/fs/cgroup/x"], "recovery_target_mb": 100},
		"recovery_log_bytes": 104857600, "recovery_seconds": 3.5, "server_reported_recovery_seconds": 2.5,
		"successes": 1, "errors": {}, "throughput_per_second": 0.28, "throughput_unit": "recoveries"}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Target != "temporal" || got.Config.Scenario != "recovery" || got.Successes != 1 {
		t.Fatalf("old result decoded as %+v", got)
	}
	if report := RenderReport([]Result{got}); !strings.Contains(report, "## recovery") {
		t.Fatalf("old result missing from report: %s", report)
	}
}

func TestChartPlotsServerResults(t *testing.T) {
	results := []Result{
		{Config: Config{Target: "zd", Scenario: "rpc-plain", Concurrency: 1}, Successes: 1, ThroughputPerSecond: 100, ThroughputUnit: "rpcs"},
		{Config: Config{Target: "zd", Scenario: "rpc-plain", Concurrency: 8}, Successes: 1, ThroughputPerSecond: 250, ThroughputUnit: "rpcs"},
	}
	page := RenderChartHTML(results)
	for _, expected := range []string{"<h3>rpc-plain</h3>", "Throughput (rpcs/s)", `polyline class="line zd"`, "<td>rpc-plain</td><td>8</td>"} {
		if !strings.Contains(page, expected) {
			t.Errorf("chart page is missing %q", expected)
		}
	}
	if strings.Contains(strings.ToLower(page), "temporal") {
		t.Errorf("chart page still mentions the removed comparison target")
	}
}

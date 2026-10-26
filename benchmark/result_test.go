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
	for index := range values { values[index] = time.Duration(100-index) * time.Millisecond }
	got := calculatePercentiles(values)
	if got.P50 != 50 || got.P90 != 90 || got.P99 != 99 || got.Max != 100 {
		t.Fatalf("unexpected percentiles: %+v", got)
	}
	if empty := calculatePercentiles(nil); empty != (Percentiles{}) { t.Fatalf("empty percentiles: %+v", empty) }
}

func TestReportSortsAndSumsErrors(t *testing.T) {
	results := []Result{
		{Config: Config{Scenario: "rpc-plain", Target: "zd"}, Successes: 8, Errors: map[string]int64{"timeout": 2, "invalid": 1}, ThroughputPerSecond: 4, ThroughputUnit: "rpcs", Latency: Percentiles{P50: 3}},
		{Config: Config{Scenario: "rpc-plain", Target: "temporal"}, Successes: 10, ThroughputPerSecond: 5, ThroughputUnit: "rpcs"},
	}
	got := RenderReport(results)
	if !strings.Contains(got, "| rpc-plain | zd | 8 | 3 | 4.00 rpcs/s | 3.00 |") { t.Fatalf("missing zd row: %s", got) }
	if strings.Index(got, "| temporal |") > strings.Index(got, "| zd |") { t.Fatalf("rows not sorted: %s", got) }
}

func TestMetricDifferenceReportsRequestsAndCommitHistogram(t *testing.T) {
	before := map[string]float64{"dexzd_object_store_requests_total{operation=\"put\"}": 10, "dexzd_commit_latency_seconds_count": 3}
	after := map[string]float64{"dexzd_object_store_requests_total{operation=\"put\"}": 14, "dexzd_commit_latency_seconds_count": 5}
	got := metricDifference(before, after, 30*time.Second)
	if got.RequestsPerMinute["dexzd_object_store_requests_total{operation=\"put\"}"] != 8 || got.CommitLatencyCount != 2 { t.Fatalf("wrong metric difference: %+v", got) }
}

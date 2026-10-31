// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"slices"
	"testing"
)

func TestParseProcStatHandlesParenthesesInCommand(t *testing.T) {
	stat := []byte("4242 (a b) c)) S 7 4242 4242 0 -1 4194560 100 0 0 0 250 150 0 0 20 0 3 0 9876 123456 300 18446744073709551615")
	process, err := parseProcStat(stat, 100, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if process.parentPID != 7 || process.startTicks != 9876 || process.cpuSeconds != 4 || process.rssBytes != 300*4096 {
		t.Fatalf("unexpected process %+v", process)
	}
}

func TestParsePSTimeFormats(t *testing.T) {
	for input, want := range map[string]float64{"0:01.50": 1.5, "2:03.25": 123.25, "1:00:00.00": 3600, "1-00:00:01": 86401} {
		got, err := parsePSTime(input)
		if err != nil || got != want {
			t.Fatalf("parsePSTime(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
}

func TestProcessTreeFollowsDescendants(t *testing.T) {
	processes := map[int]processTime{1: {}, 10: {parentPID: 1}, 11: {parentPID: 10}, 12: {parentPID: 11}, 20: {parentPID: 1}}
	tree := processTree(processes, []int{10, 99})
	slices.Sort(tree)
	if !slices.Equal(tree, []int{10, 11, 12}) {
		t.Fatalf("tree = %v", tree)
	}
}

func TestProcessTreeUsageReportsIntervalDeltasNotLifetimeAverages(t *testing.T) {
	previous := map[int]processTime{5: {startTicks: 1, cpuSeconds: 100}, 6: {parentPID: 5, startTicks: 1, cpuSeconds: 50}}
	current := map[int]processTime{
		5: {startTicks: 1, cpuSeconds: 100.5, rssBytes: 10},
		6: {parentPID: 5, startTicks: 2, cpuSeconds: 0.25, rssBytes: 20},
		7: {parentPID: 1, cpuSeconds: 9},
	}
	cpuSeconds, rssBytes, count := processTreeUsage(previous, current, []int{5})
	if cpuSeconds != 0.75 || rssBytes != 30 || count != 2 {
		t.Fatalf("usage = %v CPU seconds, %d bytes, %d processes", cpuSeconds, rssBytes, count)
	}
}

func TestMetricLabelExtractsOperation(t *testing.T) {
	if got := metricLabel(`dexzd_object_store_requests_total{operation="get",object_kind="log"}`, "operation"); got != "get" {
		t.Fatalf("operation = %q", got)
	}
}

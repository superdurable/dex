// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"fmt"
	"sort"
	"strings"
)

// loadHostCPULimit marks a point invalid when the load host may have been the bottleneck.
const loadHostCPULimit = 70.0

func RenderReport(results []Result) string {
	sort.SliceStable(results, func(i, j int) bool {
		left, right := results[i].Config, results[j].Config
		if left.Scenario != right.Scenario {
			return left.Scenario < right.Scenario
		}
		if left.Concurrency != right.Concurrency {
			return left.Concurrency < right.Concurrency
		}
		if leftSettings, rightSettings := settingsLabel(results[i]), settingsLabel(results[j]); leftSettings != rightSettings {
			return leftSettings < rightSettings
		}
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.Trial < right.Trial
	})
	var output strings.Builder
	output.WriteString("Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. ")
	output.WriteString("Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. ")
	fmt.Fprintf(&output, "A point whose load host CPU exceeds %.0f%% is marked invalid because the load generator may have been the bottleneck. ", loadHostCPULimit)
	output.WriteString("channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.\n\n")
	for start := 0; start < len(results); {
		end := start
		for end < len(results) && results[end].Config.Scenario == results[start].Config.Scenario {
			end++
		}
		writeScenarioReport(&output, results[start:end])
		start = end
	}
	return output.String()
}

func writeScenarioReport(output *strings.Builder, results []Result) {
	isStepScenario := isSequentialStepsScenario(results[0].Config.Scenario)
	fmt.Fprintf(output, "## %s\n\n", results[0].Config.Scenario)
	if isStepScenario {
		output.WriteString("| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |\n")
		output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	} else {
		output.WriteString("| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |\n")
		output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	}
	for _, result := range results {
		latency := result.Latency
		resourceCells := fmt.Sprintf("%s | %s | %s | %s | %s", serverCPUCell(result), serverHostCell(result),
			loadHostCell(result), putRateCell(result), serverRSSCell(result))
		if isStepScenario {
			fmt.Fprintf(output, "| %d | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %s | %s |\n",
				result.Config.Concurrency, settingsLabel(result), result.Successes, errorCount(result), result.ThroughputPerSecond,
				result.ThroughputUnit, latency.P50, latency.P99, result.StepLatency.P50, result.StepLatency.P90,
				result.StepLatency.P99, result.StepLatency.Max, resourceCells, notes(result))
		} else {
			fmt.Fprintf(output, "| %d | %s | %d | %d | %.2f %s/s | %.2f | %.2f | %.2f | %.2f | %s | %s |\n",
				result.Config.Concurrency, settingsLabel(result), result.Successes, errorCount(result), result.ThroughputPerSecond,
				result.ThroughputUnit, latency.P50, latency.P90, latency.P99, latency.Max, resourceCells, notes(result))
		}
	}
	writeTrialSummary(output, results)
	output.WriteString("\n")
}

// writeTrialSummary adds the median and maximum throughput of each repeated point.
func writeTrialSummary(output *strings.Builder, results []Result) {
	type pointKey struct {
		concurrency int
		settings    string
		connections int
		workerPool  int
		steps       int
	}
	var keys []pointKey
	groups := map[pointKey][]Result{}
	for _, result := range results {
		key := pointKey{concurrency: result.Config.Concurrency, settings: settingsLabel(result), connections: result.Config.Connections,
			workerPool: result.Config.WorkerPool, steps: result.Config.Steps}
		if _, found := groups[key]; !found {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], result)
	}
	hasRepeats := false
	for _, group := range groups {
		hasRepeats = hasRepeats || len(group) > 1
	}
	if !hasRepeats {
		return
	}
	isStepScenario := isSequentialStepsScenario(results[0].Config.Scenario)
	if isStepScenario {
		output.WriteString("\n| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Flow p99 ms | Median Step p99 ms |\n")
		output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: | ---: |\n")
	} else {
		output.WriteString("\n| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |\n")
		output.WriteString("| ---: | --- | ---: | ---: | ---: | ---: |\n")
	}
	for _, key := range keys {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		throughputs := make([]float64, len(group))
		tails := make([]float64, len(group))
		stepTails := make([]float64, len(group))
		for index, result := range group {
			throughputs[index] = result.ThroughputPerSecond
			tails[index] = result.Latency.P99
			stepTails[index] = result.StepLatency.P99
		}
		sort.Float64s(throughputs)
		fmt.Fprintf(output, "| %d | %s | %d | %.2f %s/s | %.2f %s/s | %.2f |", key.concurrency, key.settings, len(group),
			median(throughputs), group[0].ThroughputUnit, throughputs[len(throughputs)-1], group[0].ThroughputUnit, median(tails))
		if isStepScenario {
			fmt.Fprintf(output, " %.2f |", median(stepTails))
		}
		output.WriteString("\n")
	}
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// settingsLabel lists the server overrides of a point in key order, or "default".
func settingsLabel(result Result) string {
	keys := make([]string, 0, len(result.Config.ServerTunables))
	for key := range result.Config.ServerTunables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var pairs []string
	// Result files from earlier versions may name another target.
	if result.Config.Target != "" && result.Config.Target != "zd" {
		pairs = append(pairs, "target="+result.Config.Target)
	}
	for _, key := range keys {
		pairs = append(pairs, strings.TrimPrefix(key, "DEXBENCH_ZD_")+"="+result.Config.ServerTunables[key])
	}
	if len(pairs) == 0 {
		return "default"
	}
	return strings.Join(pairs, " ")
}

func errorCount(result Result) int64 {
	var count int64
	for _, errors := range result.Errors {
		count += errors
	}
	return count
}

// loadHostPercent returns the load generator's mean CPU as a share of all load-host cores.
func loadHostPercent(result Result) (float64, bool) {
	if len(result.LoadHostResources) == 0 || result.Machine.Cores < 1 {
		return 0, false
	}
	cpuMean, _ := summarizeResources(result.LoadHostResources)
	return cpuMean / float64(result.Machine.Cores), true
}

// serverCPUCell shows the mean server CPU, or a dash when no server samples were recorded.
func serverCPUCell(result Result) string {
	if len(result.Resources) == 0 {
		return "—"
	}
	cpuMean, _ := summarizeResources(result.Resources)
	return fmt.Sprintf("%.1f", cpuMean)
}

func serverHostCell(result Result) string {
	if len(result.Resources) == 0 || result.ServerCores < 1 {
		return "—"
	}
	cpuMean, _ := summarizeResources(result.Resources)
	return fmt.Sprintf("%.1f", cpuMean/float64(result.ServerCores))
}

func serverRSSCell(result Result) string {
	if len(result.Resources) == 0 {
		return "—"
	}
	_, rssMax := summarizeResources(result.Resources)
	return fmt.Sprintf("%.1f", rssMax)
}

func loadHostCell(result Result) string {
	percent, ok := loadHostPercent(result)
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%.1f", percent)
}

func putRateCell(result Result) string {
	if result.ObjectStore == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f", result.ObjectStore.RequestsPerMinuteByOperation["put"]/60)
}

func notes(result Result) string {
	var parts []string
	if result.Config.Trial > 0 {
		parts = append(parts, fmt.Sprintf("trial %d", result.Config.Trial))
	}
	if percent, ok := loadHostPercent(result); ok && percent > loadHostCPULimit {
		parts = append(parts, fmt.Sprintf("invalid: load host CPU %.0f%%", percent))
	}
	if result.Config.Connections > 1 {
		parts = append(parts, fmt.Sprintf("%d connections", result.Config.Connections))
	}
	return strings.Join(parts, "; ")
}

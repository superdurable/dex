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
	"html"
	"math"
	"sort"
	"strings"
)

// chartMetric extracts one plotted value from a result; it reports false when the result has no value.
type chartMetric struct {
	title string
	unit  string
	value func(Result) (float64, bool)
}

type chartPoint struct {
	concurrency int
	value       float64
}

// scenarioComparison pairs both targets at the highest concurrency that both of them ran.
type scenarioComparison struct {
	scenario    string
	concurrency int
	temporal    Result
	zd          Result
}

// ratioBar is one row of a speedup chart; ratio above 1 means server-zd is better on that metric.
type ratioBar struct {
	label  string
	ratio  float64
	detail string
}

var chartTargets = []string{"temporal", "zd"}

var chartTargetLabels = map[string]string{"temporal": "Go Server (Temporal)", "zd": "server-zd"}

var chartMetrics = []chartMetric{
	{title: "Throughput", unit: "/s", value: func(result Result) (float64, bool) { return result.ThroughputPerSecond, result.Successes > 0 }},
	{title: "p50 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P50, result.Successes > 0 }},
	{title: "p99 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P99, result.Successes > 0 }},
	{title: "Mean server CPU", unit: "%", value: meanCPUPercent},
	{title: "Peak server RSS", unit: "MiB", value: peakRSSMiB},
	{title: "Throughput per GiB of peak RSS", unit: "/s per GiB", value: operationsPerGiB},
}

// RenderChartHTML returns a self-contained HTML page: headline numbers, per-scenario speedup charts, then details.
func RenderChartHTML(results []Result) string {
	scenarios := map[string]map[string][]Result{}
	var recovery []Result
	for _, result := range results {
		if result.Config.Scenario == "recovery" {
			recovery = append(recovery, result)
			continue
		}
		byTarget := scenarios[result.Config.Scenario]
		if byTarget == nil {
			byTarget = map[string][]Result{}
			scenarios[result.Config.Scenario] = byTarget
		}
		byTarget[result.Config.Target] = append(byTarget[result.Config.Target], result)
	}
	scenarioNames := make([]string, 0, len(scenarios))
	for scenario := range scenarios {
		scenarioNames = append(scenarioNames, scenario)
	}
	sort.Strings(scenarioNames)
	comparisons := make([]scenarioComparison, 0, len(scenarioNames))
	for _, scenario := range scenarioNames {
		if comparison, ok := compareAtSharedConcurrency(scenario, scenarios[scenario]); ok {
			comparisons = append(comparisons, comparison)
		}
	}

	var page strings.Builder
	page.WriteString(chartPageHeader)
	page.WriteString(chartSubtitle(results))
	page.WriteString(headlineTiles(comparisons))
	if len(comparisons) > 0 {
		page.WriteString(`<section class="overview">`)
		page.WriteString(ratioChartSection("Throughput", "How many times more operations per second server-zd completes.",
			comparisonBars(comparisons, throughputRatio)))
		page.WriteString(ratioChartSection("p99 latency", "How many times lower server-zd's 99th-percentile latency is.",
			comparisonBars(comparisons, p99Ratio)))
		page.WriteString(ratioChartSection("Operations per CPU core", "How many times more work server-zd does for each second of server CPU.",
			comparisonBars(comparisons, cpuEfficiencyRatio)))
		page.WriteString(ratioChartSection("Operations per GiB of memory", "How many times more operations per second server-zd sustains for each GiB of peak resident memory.",
			comparisonBars(comparisons, memoryEfficiencyRatio)))
		page.WriteString("</section>\n")
	}
	page.WriteString(`<h2 class="part">Details by scenario</h2><div class="legend">`)
	for _, target := range chartTargets {
		fmt.Fprintf(&page, `<span><i class="swatch %s"></i>%s</span>`, target, chartTargetLabels[target])
	}
	page.WriteString("</div>\n")
	for _, scenario := range scenarioNames {
		byTarget := scenarios[scenario]
		fmt.Fprintf(&page, "<section><h3>%s</h3><div class=\"grid\">\n", html.EscapeString(scenario))
		for _, metric := range chartMetrics {
			series := map[string][]chartPoint{}
			for _, target := range chartTargets {
				series[target] = metricPoints(byTarget[target], metric)
			}
			unit := metric.unit
			if metric.title == "Throughput" {
				unit = chartThroughputUnit(byTarget)
			}
			page.WriteString(lineChartSVG(metric.title, unit, "concurrency", series))
		}
		page.WriteString("</div></section>\n")
	}
	if len(recovery) > 0 {
		series := map[string][]chartPoint{}
		for _, result := range recovery {
			series[result.Config.Target] = append(series[result.Config.Target], chartPoint{concurrency: result.Config.RecoveryTargetMB, value: result.RecoverySeconds})
		}
		for target := range series {
			sort.Slice(series[target], func(i, j int) bool { return series[target][i].concurrency < series[target][j].concurrency })
		}
		page.WriteString("<section><h3>recovery (server-zd only)</h3><div class=\"grid\">\n")
		page.WriteString(lineChartSVG("Time to serve after restart", "s", "log MiB", series))
		page.WriteString("</div></section>\n")
	}
	page.WriteString(resultTable(scenarioNames, scenarios))
	page.WriteString(chartPageFooter)
	return page.String()
}

func compareAtSharedConcurrency(scenario string, byTarget map[string][]Result) (scenarioComparison, bool) {
	temporalByConcurrency := map[int]Result{}
	for _, result := range byTarget["temporal"] {
		if result.Successes > 0 {
			temporalByConcurrency[result.Config.Concurrency] = result
		}
	}
	best := scenarioComparison{scenario: scenario, concurrency: -1}
	for _, result := range byTarget["zd"] {
		temporal, ok := temporalByConcurrency[result.Config.Concurrency]
		if ok && result.Successes > 0 && result.Config.Concurrency > best.concurrency {
			best = scenarioComparison{scenario: scenario, concurrency: result.Config.Concurrency, temporal: temporal, zd: result}
		}
	}
	return best, best.concurrency >= 0
}

func throughputRatio(comparison scenarioComparison) (float64, string, bool) {
	temporal, zd := comparison.temporal.ThroughputPerSecond, comparison.zd.ThroughputPerSecond
	return safeRatio(zd, temporal), fmt.Sprintf("%s vs %s %s/s", formatTick(zd), formatTick(temporal), comparison.zd.ThroughputUnit), temporal > 0 && zd > 0
}

func p99Ratio(comparison scenarioComparison) (float64, string, bool) {
	temporal, zd := comparison.temporal.Latency.P99, comparison.zd.Latency.P99
	return safeRatio(temporal, zd), fmt.Sprintf("%s vs %s ms", formatTick(zd), formatTick(temporal)), temporal > 0 && zd > 0
}

func cpuEfficiencyRatio(comparison scenarioComparison) (float64, string, bool) {
	temporal, temporalOK := operationsPerCPUSecond(comparison.temporal)
	zd, zdOK := operationsPerCPUSecond(comparison.zd)
	return safeRatio(zd, temporal), fmt.Sprintf("%s vs %s per core-second", formatTick(zd), formatTick(temporal)), temporalOK && zdOK
}

// memoryEfficiencyRatio normalizes peak memory by the work done, since a faster target holds more Flows at once.
func memoryEfficiencyRatio(comparison scenarioComparison) (float64, string, bool) {
	temporal, temporalOK := operationsPerGiB(comparison.temporal)
	zd, zdOK := operationsPerGiB(comparison.zd)
	return safeRatio(zd, temporal), fmt.Sprintf("%s vs %s/s per GiB", formatTick(zd), formatTick(temporal)), temporalOK && zdOK
}

func operationsPerGiB(result Result) (float64, bool) {
	rss, ok := peakRSSMiB(result)
	if !ok || rss <= 0 || result.ThroughputPerSecond <= 0 {
		return 0, false
	}
	return result.ThroughputPerSecond / (rss / 1024), true
}

func operationsPerCPUSecond(result Result) (float64, bool) {
	cpu, ok := meanCPUPercent(result)
	if !ok || cpu <= 0 || result.ThroughputPerSecond <= 0 {
		return 0, false
	}
	return result.ThroughputPerSecond / (cpu / 100), true
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func comparisonBars(comparisons []scenarioComparison, ratio func(scenarioComparison) (float64, string, bool)) []ratioBar {
	bars := make([]ratioBar, 0, len(comparisons))
	for _, comparison := range comparisons {
		if value, detail, ok := ratio(comparison); ok {
			bars = append(bars, ratioBar{label: comparison.scenario, ratio: value, detail: fmt.Sprintf("%s at concurrency %d", detail, comparison.concurrency)})
		}
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].ratio > bars[j].ratio })
	return bars
}

// headlineTiles summarizes the overview charts; medians keep one outlier scenario from defining the headline.
func headlineTiles(comparisons []scenarioComparison) string {
	throughput := comparisonBars(comparisons, throughputRatio)
	if len(throughput) == 0 {
		return ""
	}
	wins := 0
	for _, bar := range throughput {
		if bar.ratio > 1 {
			wins++
		}
	}
	tiles := []struct{ value, label, note string }{
		{formatRatio(throughput[0].ratio), "peak throughput advantage", throughput[0].label},
		{formatRatio(medianRatio(throughput)), "median throughput ratio", "across all scenarios"},
		{formatRatio(medianRatio(comparisonBars(comparisons, cpuEfficiencyRatio))), "median work per CPU core", "server-zd vs Go Server"},
		{formatRatio(medianRatio(comparisonBars(comparisons, memoryEfficiencyRatio))), "median work per GiB", "throughput per GiB of peak RSS"},
		{fmt.Sprintf("%d / %d", wins, len(throughput)), "scenarios with higher throughput", "at the highest shared concurrency"},
	}
	var output strings.Builder
	output.WriteString(`<div class="tiles">`)
	for _, tile := range tiles {
		fmt.Fprintf(&output, `<div class="tile"><div class="value">%s</div><div class="label">%s</div><div class="note">%s</div></div>`,
			html.EscapeString(tile.value), html.EscapeString(tile.label), html.EscapeString(tile.note))
	}
	output.WriteString("</div>\n")
	return output.String()
}

func medianRatio(bars []ratioBar) float64 {
	if len(bars) == 0 {
		return 0
	}
	values := make([]float64, len(bars))
	for i, bar := range bars {
		values[i] = bar.ratio
	}
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return math.Sqrt(values[middle-1] * values[middle])
}

// ratioChartSection draws horizontal bars on a log2 axis centered on 1x, so 4x faster and 4x slower look symmetric.
func ratioChartSection(title, description string, bars []ratioBar) string {
	if len(bars) == 0 {
		return ""
	}
	const width, rowHeight, labelWidth, valueWidth, top = 760.0, 30.0, 190.0, 70.0, 44.0
	maxExponent := 1.0
	for _, bar := range bars {
		if bar.ratio > 0 {
			maxExponent = math.Max(maxExponent, math.Ceil(math.Abs(math.Log2(bar.ratio))))
		}
	}
	plotLeft, plotRight := labelWidth, width-valueWidth
	center := (plotLeft + plotRight) / 2
	halfWidth := (plotRight - plotLeft) / 2
	xPosition := func(ratio float64) float64 {
		exponent := math.Max(-maxExponent, math.Min(maxExponent, math.Log2(ratio)))
		return center + halfWidth*exponent/maxExponent
	}
	height := top + rowHeight*float64(len(bars)) + 8

	var svg strings.Builder
	fmt.Fprintf(&svg, `<figure class="ratio-chart"><h3>%s</h3><p class="meta">%s</p>`, html.EscapeString(title), html.EscapeString(description))
	fmt.Fprintf(&svg, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s ratio by scenario">`, width, height, html.EscapeString(title))
	tickStep := math.Max(1, math.Ceil(maxExponent/3))
	for exponent := -maxExponent; exponent <= maxExponent; exponent++ {
		if math.Mod(math.Abs(exponent), tickStep) != 0 {
			continue
		}
		x := xPosition(math.Pow(2, exponent))
		class := "grid"
		if exponent == 0 {
			class = "baseline"
		}
		fmt.Fprintf(&svg, `<line class="%s" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, class, x, x, top-6, height-4)
		fmt.Fprintf(&svg, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x, top-10, formatRatio(math.Pow(2, exponent)))
	}
	fmt.Fprintf(&svg, `<text class="side" x="%.1f" y="13" text-anchor="start">← Go Server better</text>`, plotLeft)
	fmt.Fprintf(&svg, `<text class="side" x="%.1f" y="13" text-anchor="end">server-zd better →</text>`, plotRight)
	for i, bar := range bars {
		y := top + rowHeight*float64(i)
		barY, barHeight := y+6, rowHeight-12
		fmt.Fprintf(&svg, `<text class="row-label" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, labelWidth-10, y+rowHeight/2+4, html.EscapeString(bar.label))
		if bar.ratio <= 0 {
			continue
		}
		end := xPosition(bar.ratio)
		class := "bar zd"
		if bar.ratio < 1 {
			class = "bar temporal"
		}
		left, barWidth := math.Min(center, end), math.Max(2, math.Abs(end-center))
		tip := fmt.Sprintf("%s: %s (%s)", bar.label, formatRatio(bar.ratio), bar.detail)
		fmt.Fprintf(&svg, `<rect class="%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="4" data-tip="%s"/>`,
			class, left, barY, barWidth, barHeight, html.EscapeString(tip))
		labelX, anchor := end+6, "start"
		if bar.ratio < 1 {
			labelX, anchor = end-6, "end"
		}
		fmt.Fprintf(&svg, `<text class="bar-value" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, labelX, y+rowHeight/2+4, anchor, formatRatio(bar.ratio))
	}
	svg.WriteString("</svg></figure>\n")
	return svg.String()
}

func formatRatio(ratio float64) string {
	switch {
	case ratio <= 0:
		return "n/a"
	case ratio >= 10:
		return fmt.Sprintf("%.0f×", ratio)
	case ratio >= 1:
		return fmt.Sprintf("%.1f×", ratio)
	default:
		return fmt.Sprintf("%.2f×", ratio)
	}
}

func chartSubtitle(results []Result) string {
	if len(results) == 0 {
		return "<p class=\"meta\">No results.</p>\n"
	}
	versions := map[string]string{}
	for _, result := range results {
		versions[result.Config.Target] = result.ServerVersion
	}
	machine := results[0].Machine
	host := fmt.Sprintf("%s, %d cores, %.0f GiB RAM", machine.CPUModel, machine.Cores, float64(machine.RAMBytes)/float64(1<<30))
	if machine.InstanceType != "" {
		host = machine.InstanceType + " · " + host
	}
	var parts []string
	for _, target := range chartTargets {
		if version, ok := versions[target]; ok {
			parts = append(parts, fmt.Sprintf("%s: %s", chartTargetLabels[target], version))
		}
	}
	return fmt.Sprintf("<p class=\"meta\">Mode %s · %s<br>%s<br>Overview charts compare both servers at the highest concurrency both ran.</p>\n",
		html.EscapeString(results[0].Config.Mode), html.EscapeString(host), html.EscapeString(strings.Join(parts, " · ")))
}

func metricPoints(results []Result, metric chartMetric) []chartPoint {
	points := make([]chartPoint, 0, len(results))
	for _, result := range results {
		if value, ok := metric.value(result); ok {
			points = append(points, chartPoint{concurrency: result.Config.Concurrency, value: value})
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].concurrency < points[j].concurrency })
	return points
}

func chartThroughputUnit(byTarget map[string][]Result) string {
	for _, target := range chartTargets {
		for _, result := range byTarget[target] {
			if result.ThroughputUnit != "" {
				return result.ThroughputUnit + "/s"
			}
		}
	}
	return "/s"
}

func meanCPUPercent(result Result) (float64, bool) {
	if len(result.Resources) == 0 {
		return 0, false
	}
	var total float64
	for _, sample := range result.Resources {
		total += sample.CPUPercent
	}
	return total / float64(len(result.Resources)), true
}

func peakRSSMiB(result Result) (float64, bool) {
	var peak uint64
	for _, sample := range result.Resources {
		peak = max(peak, sample.RSSBytes)
	}
	return float64(peak) / float64(1<<20), peak > 0
}

// lineChartSVG draws one small chart with evenly spaced x categories, which suits power-of-two concurrency sweeps.
func lineChartSVG(title, unit, xLabel string, series map[string][]chartPoint) string {
	const width, height = 320.0, 200.0
	const left, right, top, bottom = 48.0, 12.0, 28.0, 36.0
	xValues := map[int]bool{}
	maxValue := 0.0
	for _, points := range series {
		for _, point := range points {
			xValues[point.concurrency] = true
			maxValue = math.Max(maxValue, point.value)
		}
	}
	categories := make([]int, 0, len(xValues))
	for value := range xValues {
		categories = append(categories, value)
	}
	sort.Ints(categories)
	if len(categories) == 0 {
		return ""
	}
	yMax := niceCeiling(maxValue)
	plotWidth, plotHeight := width-left-right, height-top-bottom
	xPosition := func(value int) float64 {
		if len(categories) == 1 {
			return left + plotWidth/2
		}
		return left + plotWidth*float64(sort.SearchInts(categories, value))/float64(len(categories)-1)
	}
	yPosition := func(value float64) float64 { return top + plotHeight*(1-value/yMax) }

	var svg strings.Builder
	fmt.Fprintf(&svg, `<figure><svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, width, height, html.EscapeString(title))
	fmt.Fprintf(&svg, `<text class="title" x="%.0f" y="16">%s (%s)</text>`, left, html.EscapeString(title), html.EscapeString(unit))
	for tick := 0; tick <= 4; tick++ {
		value := yMax * float64(tick) / 4
		y := yPosition(value)
		fmt.Fprintf(&svg, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, left, width-right, y, y)
		fmt.Fprintf(&svg, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-6, y+4, formatTick(value))
	}
	for _, category := range categories {
		fmt.Fprintf(&svg, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%d</text>`, xPosition(category), height-bottom+16, category)
	}
	fmt.Fprintf(&svg, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, left+plotWidth/2, height-4, html.EscapeString(xLabel))
	for _, target := range chartTargets {
		points := series[target]
		if len(points) == 0 {
			continue
		}
		coordinates := make([]string, 0, len(points))
		for _, point := range points {
			coordinates = append(coordinates, fmt.Sprintf("%.1f,%.1f", xPosition(point.concurrency), yPosition(point.value)))
		}
		fmt.Fprintf(&svg, `<polyline class="line %s" points="%s"/>`, target, strings.Join(coordinates, " "))
		for _, point := range points {
			tip := fmt.Sprintf("%s, %s %d: %s %s", chartTargetLabels[target], xLabel, point.concurrency, formatTick(point.value), unit)
			fmt.Fprintf(&svg, `<circle class="dot %s" cx="%.1f" cy="%.1f" r="4" data-tip="%s"/>`, target,
				xPosition(point.concurrency), yPosition(point.value), html.EscapeString(tip))
		}
	}
	svg.WriteString("</svg></figure>\n")
	return svg.String()
}

func resultTable(scenarioNames []string, scenarios map[string]map[string][]Result) string {
	var table strings.Builder
	table.WriteString(`<h2 class="part">All results</h2><div class="table-wrap"><table><thead><tr><th>Scenario</th><th>Concurrency</th><th>Server</th><th>Throughput</th><th>p50 ms</th><th>p99 ms</th><th>CPU %</th><th>RSS MiB</th><th>Errors</th></tr></thead><tbody>`)
	for _, scenario := range scenarioNames {
		var rows []Result
		for _, target := range chartTargets {
			rows = append(rows, scenarios[scenario][target]...)
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Config.Concurrency < rows[j].Config.Concurrency })
		for _, result := range rows {
			cpu, _ := meanCPUPercent(result)
			rss, _ := peakRSSMiB(result)
			var errorCount int64
			for _, count := range result.Errors {
				errorCount += count
			}
			fmt.Fprintf(&table, "<tr><td>%s</td><td>%d</td><td><i class=\"swatch %s\"></i>%s</td><td>%s %s/s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td></tr>",
				html.EscapeString(scenario), result.Config.Concurrency, html.EscapeString(result.Config.Target), html.EscapeString(chartTargetLabels[result.Config.Target]),
				formatTick(result.ThroughputPerSecond), html.EscapeString(result.ThroughputUnit), formatTick(result.Latency.P50), formatTick(result.Latency.P99),
				formatTick(cpu), formatTick(rss), errorCount)
		}
	}
	table.WriteString("</tbody></table></div>\n")
	return table.String()
}

func niceCeiling(value float64) float64 {
	if value <= 0 {
		return 1
	}
	magnitude := math.Pow(10, math.Floor(math.Log10(value)))
	for _, step := range []float64{1, 2, 2.5, 5, 10} {
		if value <= step*magnitude {
			return step * magnitude
		}
	}
	return 10 * magnitude
}

func formatTick(value float64) string {
	switch {
	case value >= 100:
		return fmt.Sprintf("%.0f", value)
	case value >= 10:
		return fmt.Sprintf("%.1f", value)
	default:
		return fmt.Sprintf("%.2f", value)
	}
}

const chartPageHeader = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Dex Server Benchmark</title>
<style>
:root { color-scheme: light; --bg: #fcfcfb; --panel: #f3f3f1; --fg: #0b0b0b; --muted: #52514e; --grid: #e2e1dc; --baseline: #8a8984; --temporal: #2a78d6; --zd: #eb6834; }
@media (prefers-color-scheme: dark) { :root:where(:not([data-theme="light"])) { color-scheme: dark; --bg: #1a1a19; --panel: #242423; --fg: #f0efec; --muted: #c3c2b7; --grid: #34342f; --baseline: #8a8984; --temporal: #3987e5; --zd: #d95926; } }
:root[data-theme="dark"] { color-scheme: dark; --bg: #1a1a19; --panel: #242423; --fg: #f0efec; --muted: #c3c2b7; --grid: #34342f; --baseline: #8a8984; --temporal: #3987e5; --zd: #d95926; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.45 system-ui, sans-serif; }
main { max-width: 1200px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 26px; margin: 0 0 4px; } h2.part { font-size: 19px; margin: 40px 0 8px; } h3 { font-size: 16px; margin: 24px 0 4px; }
.meta { color: var(--muted); margin: 4px 0 12px; }
.tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 12px; margin: 20px 0 8px; }
.tile { background: var(--panel); border-radius: 10px; padding: 16px; }
.tile .value { font-size: 38px; font-weight: 700; line-height: 1.1; font-variant-numeric: tabular-nums; }
.tile .label { margin-top: 6px; font-weight: 600; } .tile .note { color: var(--muted); font-size: 12px; }
.overview { display: grid; grid-template-columns: repeat(auto-fit, minmax(520px, 1fr)); gap: 8px 32px; }
@media (max-width: 600px) { .overview { grid-template-columns: 1fr; } }
.legend { display: flex; flex-wrap: wrap; gap: 20px; margin: 8px 0 4px; }
.swatch { display: inline-block; width: 14px; height: 4px; margin-right: 6px; vertical-align: middle; border-radius: 2px; }
.swatch.temporal { background: var(--temporal); } .swatch.zd { background: var(--zd); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 12px; }
figure { margin: 0; } svg { width: 100%; height: auto; }
svg .title { fill: var(--fg); font-size: 12px; font-weight: 600; } svg .tick, svg .side { fill: var(--muted); font-size: 11px; }
svg .row-label { fill: var(--fg); font-size: 13px; } svg .bar-value { fill: var(--fg); font-size: 13px; font-weight: 700; }
svg .grid { stroke: var(--grid); stroke-width: 1; } svg .baseline { stroke: var(--baseline); stroke-width: 1.5; }
svg .bar.zd { fill: var(--zd); } svg .bar.temporal { fill: var(--temporal); }
svg .line { fill: none; stroke-width: 2; } svg .line.temporal { stroke: var(--temporal); } svg .line.zd { stroke: var(--zd); }
svg .dot { stroke: var(--bg); stroke-width: 2; } svg .dot.temporal { fill: var(--temporal); } svg .dot.zd { fill: var(--zd); }
[data-tip] { cursor: default; } [data-tip]:hover { opacity: 0.85; }
#tip { position: fixed; pointer-events: none; background: var(--fg); color: var(--bg); padding: 6px 8px; border-radius: 6px; font-size: 12px; display: none; max-width: 320px; }
.table-wrap { overflow-x: auto; } table { border-collapse: collapse; width: 100%; font-size: 13px; font-variant-numeric: tabular-nums; }
th, td { text-align: left; padding: 5px 10px; border-bottom: 1px solid var(--grid); white-space: nowrap; } th { color: var(--muted); font-weight: 600; }
</style></head><body><main>
<h1>server-zd vs Go Server (Temporal)</h1>
`

const chartPageFooter = `<div id="tip"></div>
<script>
const tip = document.getElementById("tip");
document.addEventListener("mousemove", (event) => {
  const target = event.target.closest("[data-tip]");
  if (!target) { tip.style.display = "none"; return; }
  tip.textContent = target.dataset.tip;
  tip.style.display = "block";
  tip.style.left = Math.min(event.clientX + 12, window.innerWidth - tip.offsetWidth - 8) + "px";
  tip.style.top = (event.clientY + 14) + "px";
});
</script>
</main></body></html>
`

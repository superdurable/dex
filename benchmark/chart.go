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

// targetLabel names a result target; result files from earlier versions may hold other targets.
func targetLabel(target string) string {
	if target == "zd" {
		return "server-zd"
	}
	return target
}

// targetClass picks the series color: server-zd, or one shared color for any other target.
func targetClass(target string) string {
	if target == "zd" {
		return "zd"
	}
	return "other"
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var chartMetrics = []chartMetric{
	{title: "Throughput", unit: "/s", value: func(result Result) (float64, bool) { return result.ThroughputPerSecond, result.Successes > 0 }},
	{title: "p50 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P50, result.Successes > 0 }},
	{title: "p99 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P99, result.Successes > 0 }},
	{title: "Mean server CPU", unit: "%", value: meanCPUPercent},
	{title: "Peak server RSS", unit: "MiB", value: peakRSSMiB},
	{title: "Throughput per GiB of peak RSS", unit: "/s per GiB", value: operationsPerGiB},
}

// RenderChartHTML returns a self-contained HTML page with per-scenario charts and a table of all results.
func RenderChartHTML(results []Result) string {
	scenarios := map[string]map[string][]Result{}
	for _, result := range results {
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

	var page strings.Builder
	page.WriteString(chartPageHeader)
	page.WriteString(chartSubtitle(results))
	page.WriteString(`<h2 class="part">Details by scenario</h2>`)
	for _, scenario := range scenarioNames {
		byTarget := scenarios[scenario]
		fmt.Fprintf(&page, "<section><h3>%s</h3><div class=\"grid\">\n", html.EscapeString(scenario))
		for _, metric := range chartMetrics {
			series := map[string][]chartPoint{}
			for target, targetResults := range byTarget {
				series[target] = metricPoints(targetResults, metric)
			}
			unit := metric.unit
			if metric.title == "Throughput" {
				unit = chartThroughputUnit(byTarget)
			}
			page.WriteString(lineChartSVG(metric.title, unit, "concurrency", series))
		}
		page.WriteString("</div></section>\n")
	}
	page.WriteString(resultTable(scenarioNames, scenarios))
	page.WriteString(chartPageFooter)
	return page.String()
}

func operationsPerGiB(result Result) (float64, bool) {
	rss, ok := peakRSSMiB(result)
	if !ok || rss <= 0 || result.ThroughputPerSecond <= 0 {
		return 0, false
	}
	return result.ThroughputPerSecond / (rss / 1024), true
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
	var parts []string
	for _, target := range sortedKeys(versions) {
		parts = append(parts, fmt.Sprintf("%s: %s", targetLabel(target), versions[target]))
	}
	return fmt.Sprintf("<p class=\"meta\">Mode %s · %s<br>%s</p>\n",
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
	for _, target := range sortedKeys(byTarget) {
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
	for _, target := range sortedKeys(series) {
		points := series[target]
		if len(points) == 0 {
			continue
		}
		coordinates := make([]string, 0, len(points))
		for _, point := range points {
			coordinates = append(coordinates, fmt.Sprintf("%.1f,%.1f", xPosition(point.concurrency), yPosition(point.value)))
		}
		fmt.Fprintf(&svg, `<polyline class="line %s" points="%s"/>`, targetClass(target), strings.Join(coordinates, " "))
		for _, point := range points {
			tip := fmt.Sprintf("%s, %s %d: %s %s", targetLabel(target), xLabel, point.concurrency, formatTick(point.value), unit)
			fmt.Fprintf(&svg, `<circle class="dot %s" cx="%.1f" cy="%.1f" r="4" data-tip="%s"/>`, targetClass(target),
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
		for _, target := range sortedKeys(scenarios[scenario]) {
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
				html.EscapeString(scenario), result.Config.Concurrency, targetClass(result.Config.Target), html.EscapeString(targetLabel(result.Config.Target)),
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
:root { color-scheme: light; --bg: #fcfcfb; --panel: #f3f3f1; --fg: #0b0b0b; --muted: #52514e; --grid: #e2e1dc; --zd: #eb6834; --other: #2a78d6; }
@media (prefers-color-scheme: dark) { :root:where(:not([data-theme="light"])) { color-scheme: dark; --bg: #1a1a19; --panel: #242423; --fg: #f0efec; --muted: #c3c2b7; --grid: #34342f; --zd: #d95926; --other: #3987e5; } }
:root[data-theme="dark"] { color-scheme: dark; --bg: #1a1a19; --panel: #242423; --fg: #f0efec; --muted: #c3c2b7; --grid: #34342f; --zd: #d95926; --other: #3987e5; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.45 system-ui, sans-serif; }
main { max-width: 1200px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 26px; margin: 0 0 4px; } h2.part { font-size: 19px; margin: 40px 0 8px; } h3 { font-size: 16px; margin: 24px 0 4px; }
.meta { color: var(--muted); margin: 4px 0 12px; }
.swatch { display: inline-block; width: 14px; height: 4px; margin-right: 6px; vertical-align: middle; border-radius: 2px; }
.swatch.zd { background: var(--zd); } .swatch.other { background: var(--other); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 12px; }
figure { margin: 0; } svg { width: 100%; height: auto; }
svg .title { fill: var(--fg); font-size: 12px; font-weight: 600; } svg .tick { fill: var(--muted); font-size: 11px; }
svg .grid { stroke: var(--grid); stroke-width: 1; }
svg .line { fill: none; stroke-width: 2; } svg .line.zd { stroke: var(--zd); } svg .line.other { stroke: var(--other); }
svg .dot { stroke: var(--bg); stroke-width: 2; } svg .dot.zd { fill: var(--zd); } svg .dot.other { fill: var(--other); }
[data-tip] { cursor: default; } [data-tip]:hover { opacity: 0.85; }
#tip { position: fixed; pointer-events: none; background: var(--fg); color: var(--bg); padding: 6px 8px; border-radius: 6px; font-size: 12px; display: none; max-width: 320px; }
.table-wrap { overflow-x: auto; } table { border-collapse: collapse; width: 100%; font-size: 13px; font-variant-numeric: tabular-nums; }
th, td { text-align: left; padding: 5px 10px; border-bottom: 1px solid var(--grid); white-space: nowrap; } th { color: var(--muted); font-weight: 600; }
</style></head><body><main>
<h1>server-zd benchmark</h1>
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

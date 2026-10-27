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

var chartTargets = []string{"temporal", "zd"}

var chartTargetLabels = map[string]string{"temporal": "Go Server (Temporal)", "zd": "server-zd"}

var chartMetrics = []chartMetric{
	{title: "Throughput", unit: "/s", value: func(result Result) (float64, bool) { return result.ThroughputPerSecond, true }},
	{title: "p50 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P50, result.Successes > 0 }},
	{title: "p99 latency", unit: "ms", value: func(result Result) (float64, bool) { return result.Latency.P99, result.Successes > 0 }},
	{title: "Mean server CPU", unit: "%", value: meanCPUPercent},
	{title: "Peak server RSS", unit: "MiB", value: peakRSSMiB},
}

// RenderChartHTML returns a self-contained HTML page that plots each scenario's targets against concurrency.
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

	var page strings.Builder
	page.WriteString(chartPageHeader)
	page.WriteString(chartSubtitle(results))
	page.WriteString(`<div class="legend">`)
	for _, target := range chartTargets {
		fmt.Fprintf(&page, `<span><i class="swatch %s"></i>%s</span>`, target, chartTargetLabels[target])
	}
	page.WriteString("</div>\n")
	for _, scenario := range scenarioNames {
		byTarget := scenarios[scenario]
		fmt.Fprintf(&page, "<section><h2>%s</h2>%s<div class=\"grid\">\n", html.EscapeString(scenario), ratioSummary(byTarget))
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
		page.WriteString("<section><h2>recovery</h2><div class=\"grid\">\n")
		page.WriteString(lineChartSVG("Time to serve after restart", "s", "log MiB", series))
		page.WriteString("</div></section>\n")
	}
	page.WriteString("</main></body></html>\n")
	return page.String()
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
	return fmt.Sprintf("<p class=\"meta\">Mode %s · %s<br>%s</p>\n", html.EscapeString(results[0].Config.Mode),
		html.EscapeString(host), html.EscapeString(strings.Join(parts, " · ")))
}

// ratioSummary shows the server-zd to Temporal throughput ratio at the highest shared concurrency.
func ratioSummary(byTarget map[string][]Result) string {
	temporal := metricPoints(byTarget["temporal"], chartMetrics[0])
	zd := metricPoints(byTarget["zd"], chartMetrics[0])
	temporalByConcurrency := map[int]float64{}
	for _, point := range temporal {
		temporalByConcurrency[point.concurrency] = point.value
	}
	for i := len(zd) - 1; i >= 0; i-- {
		if base, ok := temporalByConcurrency[zd[i].concurrency]; ok && base > 0 {
			return fmt.Sprintf("<p class=\"ratio\">server-zd throughput is <b>%.2f×</b> the Go Server at concurrency %d.</p>",
				zd[i].value/base, zd[i].concurrency)
		}
	}
	return ""
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
			fmt.Fprintf(&svg, `<circle class="dot %s" cx="%.1f" cy="%.1f" r="3"><title>%s, %d: %s %s</title></circle>`, target,
				xPosition(point.concurrency), yPosition(point.value), chartTargetLabels[target], point.concurrency, formatTick(point.value), html.EscapeString(unit))
		}
	}
	svg.WriteString("</svg></figure>\n")
	return svg.String()
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
:root { --bg: #ffffff; --fg: #1d1d1f; --muted: #6b6b70; --grid: #e4e4e8; --temporal: #3366cc; --zd: #e8743b; }
@media (prefers-color-scheme: dark) { :root { --bg: #16161a; --fg: #ececf0; --muted: #9a9aa3; --grid: #2c2c33; --temporal: #7aa2f7; --zd: #f5a05c; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 14px/1.45 system-ui, sans-serif; }
main { max-width: 1200px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 22px; margin: 0 0 4px; } h2 { font-size: 17px; margin: 28px 0 4px; }
.meta, .ratio { color: var(--muted); margin: 4px 0 12px; } .ratio b { color: var(--fg); }
.legend { display: flex; gap: 20px; margin: 8px 0 4px; } .swatch { display: inline-block; width: 14px; height: 4px; margin-right: 6px; vertical-align: middle; border-radius: 2px; }
.swatch.temporal { background: var(--temporal); } .swatch.zd { background: var(--zd); }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 12px; }
figure { margin: 0; } svg { width: 100%; height: auto; }
svg .title { fill: var(--fg); font-size: 12px; font-weight: 600; } svg .tick { fill: var(--muted); font-size: 10px; }
svg .grid { stroke: var(--grid); stroke-width: 1; }
svg .line { fill: none; stroke-width: 2; } svg .line.temporal { stroke: var(--temporal); } svg .line.zd { stroke: var(--zd); }
svg .dot.temporal { fill: var(--temporal); } svg .dot.zd { fill: var(--zd); }
</style></head><body><main>
<h1>server-zd vs Go Server (Temporal)</h1>
`

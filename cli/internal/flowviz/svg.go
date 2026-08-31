// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package flowviz

import (
	"bytes"
	"fmt"
	"html"
	"sort"
	"strings"
)

type point struct {
	horizontal float64
	vertical   float64
}

type box struct {
	left   float64
	top    float64
	width  float64
	height float64
}

func RenderSVG(graph *Graph) ([]byte, error) {
	positions, width, height := layoutGraph(graph)
	var output bytes.Buffer
	fmt.Fprintf(&output, `<svg xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="title description" viewBox="0 0 %.0f %.0f" width="100%%">`, width, height)
	output.WriteString(`<title id="title">` + escape(graph.Flow.Name) + ` Flow definition</title>`)
	output.WriteString(`<desc id="description">Static possible-path graph generated from ` + escape(graph.Source.Path) + `</desc>`)
	output.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="context-stroke"/></marker></defs>`)
	output.WriteString(`<style>` + svgStyle + `</style>`)
	fmt.Fprintf(&output, `<rect class="flow-boundary" x="16" y="16" width="%.0f" height="%.0f" rx="18"/>`, width-32, height-32)
	fmt.Fprintf(&output, `<text class="flow-title" x="36" y="48">%s</text>`, escape(graph.Flow.Name))
	for _, edge := range graph.Edges {
		from, fromOK := positions[edge.From]
		to, toOK := positions[edge.To]
		if !fromOK || !toOK {
			continue
		}
		start := point{horizontal: from.left + from.width, vertical: from.top + from.height/2}
		end := point{horizontal: to.left, vertical: to.top + to.height/2}
		if to.left <= from.left {
			start = point{horizontal: from.left + from.width/2, vertical: from.top + from.height}
			end = point{horizontal: to.left + to.width/2, vertical: to.top}
		}
		middleX := (start.horizontal + end.horizontal) / 2
		className := "edge " + edgeClass(edge.Kind)
		fmt.Fprintf(&output, `<path class="%s" data-edge-id="%s" d="M %.1f %.1f L %.1f %.1f L %.1f %.1f L %.1f %.1f" marker-end="url(#arrow)"/>`, className, escape(edge.ID), start.horizontal, start.vertical, middleX, start.vertical, middleX, end.vertical, end.horizontal, end.vertical)
		label := edge.Label
		if edge.Condition != "" {
			label = edge.Condition
		}
		if edge.Multiplicity != "" {
			label = strings.TrimSpace(label + " " + edge.Multiplicity)
		}
		if label != "" {
			fmt.Fprintf(&output, `<text class="edge-label" x="%.1f" y="%.1f">%s</text>`, middleX+4, (start.vertical+end.vertical)/2-5, escape(label))
		}
	}
	for _, node := range graph.Nodes {
		position, ok := positions[node.ID]
		if !ok {
			continue
		}
		renderNode(&output, node, position)
	}
	renderDiagnostics(&output, graph, width, height)
	output.WriteString(`</svg>`)
	return output.Bytes(), nil
}

func layoutGraph(graph *Graph) (map[string]box, float64, float64) {
	const (
		left        = 52.0
		top         = 76.0
		layerGap    = 180.0
		verticalGap = 36.0
	)
	ranks := make(map[string]int, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Kind == "rpc" || node.Kind == "timeout_handler" || node.Kind == "start" {
			ranks[node.ID] = 0
		}
	}
	for iteration := 0; iteration < len(graph.Nodes); iteration++ {
		changed := false
		for _, edge := range graph.Edges {
			if isResourceEdge(edge.Kind) || edge.Kind == "cancel" || edge.From == edge.To {
				continue
			}
			candidate := ranks[edge.From] + 1
			if candidate > ranks[edge.To] && candidate <= len(graph.Nodes) {
				ranks[edge.To] = candidate
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	maxRank := 0
	for _, rank := range ranks {
		if rank > maxRank {
			maxRank = rank
		}
	}
	for _, node := range graph.Nodes {
		if isResourceNode(node.Kind) {
			ranks[node.ID] = maxRank + 1
		}
	}
	layers := make(map[int][]Node)
	for _, node := range graph.Nodes {
		layers[ranks[node.ID]] = append(layers[ranks[node.ID]], node)
	}
	positions := make(map[string]box, len(graph.Nodes))
	maxLayerHeight := 0.0
	for rank, nodes := range layers {
		sort.SliceStable(nodes, func(leftIndex int, rightIndex int) bool {
			return nodes[leftIndex].ID < nodes[rightIndex].ID
		})
		currentY := top
		for _, node := range nodes {
			width := nodeWidth(node)
			height := nodeHeight(node)
			positions[node.ID] = box{left: left + float64(rank)*layerGap, top: currentY, width: width, height: height}
			currentY += height + verticalGap
		}
		if currentY > maxLayerHeight {
			maxLayerHeight = currentY
		}
	}
	diagnosticHeight := 0.0
	if len(graph.Diagnostics) > 0 {
		diagnosticHeight = 52 + float64(len(graph.Diagnostics))*22
	}
	width := left + float64(maxRank+2)*layerGap + 100
	height := maxLayerHeight + diagnosticHeight + 40
	if width < 720 {
		width = 720
	}
	if height < 300 {
		height = 300
	}
	return positions, width, height
}

func renderNode(output *bytes.Buffer, node Node, position box) {
	className := "node " + nodeClass(node.Kind)
	title := node.Name
	if node.Span != nil {
		title = fmt.Sprintf("%s — lines %d:%d–%d:%d", title, node.Span.StartLine, node.Span.StartColumn, node.Span.EndLine, node.Span.EndColumn)
	}
	fmt.Fprintf(output, `<g class="%s" data-node-id="%s"><title>%s</title>`, className, escape(node.ID), escape(title))
	if node.Kind == "decision" {
		centerX := position.left + position.width/2
		centerY := position.top + position.height/2
		fmt.Fprintf(output, `<path d="M %.1f %.1f L %.1f %.1f L %.1f %.1f L %.1f %.1f Z"/>`, centerX, position.top, position.left+position.width, centerY, centerX, position.top+position.height, position.left, centerY)
	} else {
		fmt.Fprintf(output, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="10"/>`, position.left, position.top, position.width, position.height)
	}
	if node.Kind == "step" {
		fmt.Fprintf(output, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, position.left+position.width/2, position.top+24, escape(node.Name))
		renderStepPhases(output, node, position)
	} else {
		fmt.Fprintf(output, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, position.left+position.width/2, position.top+position.height/2+5, escape(node.Name))
	}
	if node.Start {
		fmt.Fprintf(output, `<text class="badge" x="%.1f" y="%.1f">START</text>`, position.left+8, position.top+15)
	}
	output.WriteString(`</g>`)
}

func renderStepPhases(output *bytes.Buffer, node Node, position box) {
	phaseTop := position.top + 38
	phaseHeight := position.height - 40
	if strings.Contains(node.Phase, "wait_for") {
		halfWidth := (position.width - 4) / 2
		fmt.Fprintf(output, `<rect class="phase wait-phase" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/><text class="phase-label" x="%.1f" y="%.1f" text-anchor="middle">WaitFor</text>`, position.left+2, phaseTop, halfWidth, phaseHeight, position.left+2+halfWidth/2, phaseTop+15)
		fmt.Fprintf(output, `<rect class="phase execute-phase" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/><text class="phase-label" x="%.1f" y="%.1f" text-anchor="middle">Execute</text>`, position.left+2+halfWidth, phaseTop, halfWidth, phaseHeight, position.left+2+halfWidth+halfWidth/2, phaseTop+15)
		return
	}
	fmt.Fprintf(output, `<rect class="phase execute-phase" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/><text class="phase-label" x="%.1f" y="%.1f" text-anchor="middle">Execute</text>`, position.left+2, phaseTop, position.width-4, phaseHeight, position.left+position.width/2, phaseTop+15)
}

func renderDiagnostics(output *bytes.Buffer, graph *Graph, width float64, height float64) {
	if len(graph.Diagnostics) == 0 {
		return
	}
	panelHeight := 32 + float64(len(graph.Diagnostics))*22
	panelY := height - panelHeight - 24
	fmt.Fprintf(output, `<g class="diagnostics"><rect x="36" y="%.1f" width="%.1f" height="%.1f" rx="8"/><text class="diagnostic-title" x="52" y="%.1f">Diagnostics</text>`, panelY, width-72, panelHeight, panelY+22)
	for index, diagnostic := range graph.Diagnostics {
		message := fmt.Sprintf("[%s] %s", strings.ToUpper(diagnostic.Severity), diagnostic.Message)
		fmt.Fprintf(output, `<text x="52" y="%.1f">%s</text>`, panelY+48+float64(index)*22, escape(message))
	}
	output.WriteString(`</g>`)
}

func nodeWidth(node Node) float64 {
	width := 110 + float64(len([]rune(node.Name)))*5
	if width < 150 {
		return 150
	}
	if width > 250 {
		return 250
	}
	return width
}

func nodeHeight(node Node) float64 {
	if node.Kind == "decision" {
		return 80
	}
	if node.Kind == "step" {
		return 68
	}
	return 62
}

func nodeClass(kind string) string {
	switch kind {
	case "step":
		return "step-node"
	case "wait", "timer", "subflow":
		return "wait-node"
	case "attribute", "channel", "stream":
		return "resource-node"
	case "terminal":
		return "terminal-node"
	case "rpc", "timeout_handler":
		return "handler-node"
	case "unknown":
		return "unknown-node"
	case "decision":
		return "decision-node"
	default:
		return "default-node"
	}
}

func edgeClass(kind string) string {
	if kind == "failure_transition" {
		return "failure-edge"
	}
	if isResourceEdge(kind) {
		return "resource-edge"
	}
	if kind == "cancel" {
		return "cancel-edge"
	}
	return "transition-edge"
}

func isResourceNode(kind string) bool {
	return kind == "attribute" || kind == "channel" || kind == "stream"
}

func isResourceEdge(kind string) bool {
	return strings.HasPrefix(kind, "resource_") || kind == "wait_condition"
}

func escape(value string) string {
	return html.EscapeString(value)
}

const svgStyle = `
.flow-boundary{fill:#fbf8ff;stroke:#7c3aed;stroke-width:2}.flow-title{font:700 20px system-ui,sans-serif;fill:#4c1d95}
.node rect,.node path{stroke-width:2}.node text{font:600 13px system-ui,sans-serif;fill:#172033}.step-node rect{fill:#e8f1ff;stroke:#2563eb}
.step-node .phase{stroke:none!important}.wait-phase{fill:#fff2dc!important}.execute-phase{fill:#e6f7ed!important}.phase-label{font:600 10px system-ui,sans-serif!important;fill:#334155!important}
.wait-node rect{fill:#fff2dc;stroke:#ea8500}.resource-node rect{fill:#e6f7ed;stroke:#159455}.terminal-node rect{fill:#f0f2f5;stroke:#4b5563}
.handler-node rect{fill:#f2e8ff;stroke:#7c3aed}.unknown-node rect{fill:#fff0f0;stroke:#dc2626;stroke-dasharray:6 4}.decision-node path{fill:#fff7d6;stroke:#b7791f}
.badge{font:700 9px system-ui,sans-serif!important;fill:#1d4ed8!important}.edge{fill:none;stroke-width:2}.transition-edge{stroke:#475569}.failure-edge{stroke:#dc2626;stroke-dasharray:8 5}
.resource-edge{stroke:#159455;stroke-dasharray:5 5}.cancel-edge{stroke:#9333ea;stroke-dasharray:3 5}.edge-label{font:11px system-ui,sans-serif;fill:#334155}
.diagnostics rect{fill:#fff7ed;stroke:#ea580c}.diagnostics text{font:12px system-ui,sans-serif;fill:#7c2d12}.diagnostic-title{font-weight:700!important}`

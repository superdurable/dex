// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package flowviz

import (
	"fmt"
	"sort"
	"strings"
)

const SchemaVersion = "1.0"

type Graph struct {
	SchemaVersion string       `json:"schemaVersion"`
	Valid         bool         `json:"valid"`
	Source        Source       `json:"source"`
	Flow          Flow         `json:"flow"`
	Nodes         []Node       `json:"nodes"`
	Edges         []Edge       `json:"edges"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type Source struct {
	Language string `json:"language"`
	Path     string `json:"path"`
}

type Flow struct {
	Name        string `json:"name"`
	StartStepID string `json:"startStepId,omitempty"`
	Span        *Span  `json:"span,omitempty"`
}

type Node struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	Name      string           `json:"name"`
	ParentID  string           `json:"parentId,omitempty"`
	Condition string           `json:"condition,omitempty"`
	Phase     string           `json:"phase,omitempty"`
	Start     bool             `json:"start,omitempty"`
	External  bool             `json:"external,omitempty"`
	Span      *Span            `json:"span,omitempty"`
	Resource  *ResourceDetails `json:"resource,omitempty"`
	Wait      *WaitDetails     `json:"wait,omitempty"`
	Decision  *DecisionDetails `json:"decision,omitempty"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

type ResourceDetails struct {
	ValueType string `json:"valueType"`
	Map       bool   `json:"map,omitempty"`
}

type WaitDetails struct {
	Type       string          `json:"type"`
	Conditions []WaitCondition `json:"conditions"`
}

type WaitCondition struct {
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	ResourceID string `json:"resourceId,omitempty"`
	SubFlowID  string `json:"subFlowId,omitempty"`
	Expression string `json:"expression,omitempty"`
	Span       *Span  `json:"span,omitempty"`
}

type DecisionDetails struct {
	Type            string         `json:"type"`
	CheckedChannels []string       `json:"checkedChannels,omitempty"`
	Cancellations   []Cancellation `json:"cancellations,omitempty"`
}

type Cancellation struct {
	StepID string `json:"stepId"`
	Scope  string `json:"scope"`
}

type Edge struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	From         string         `json:"from"`
	To           string         `json:"to"`
	Label        string         `json:"label,omitempty"`
	Condition    string         `json:"condition,omitempty"`
	Multiplicity string         `json:"multiplicity,omitempty"`
	Span         *Span          `json:"span,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Span     *Span  `json:"span,omitempty"`
}

type Span struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

func NewGraph(language string, path string) *Graph {
	return &Graph{
		SchemaVersion: SchemaVersion,
		Valid:         true,
		Source:        Source{Language: language, Path: path},
		Nodes:         make([]Node, 0),
		Edges:         make([]Edge, 0),
		Diagnostics:   make([]Diagnostic, 0),
	}
}

func (graph *Graph) AddNode(node Node) {
	for index := range graph.Nodes {
		if graph.Nodes[index].ID == node.ID {
			return
		}
	}
	graph.Nodes = append(graph.Nodes, node)
}

func (graph *Graph) AddEdge(edge Edge) {
	if edge.ID == "" {
		edge.ID = fmt.Sprintf("edge:%04d", len(graph.Edges)+1)
	}
	graph.Edges = append(graph.Edges, edge)
}

func (graph *Graph) SetNodePhase(nodeID string, phase string) {
	for index := range graph.Nodes {
		if graph.Nodes[index].ID == nodeID {
			graph.Nodes[index].Phase = phase
			return
		}
	}
}

func (graph *Graph) AddDiagnostic(severity string, code string, message string, span *Span) {
	graph.Diagnostics = append(graph.Diagnostics, Diagnostic{
		Severity: severity,
		Code:     code,
		Message:  message,
		Span:     span,
	})
	if severity == "error" {
		graph.Valid = false
	}
}

func (graph *Graph) Normalize() {
	graph.addUnknownTargets()
	graph.addUnreachableWarnings()
	graph.addUnusedResourceWarnings()
	sort.SliceStable(graph.Nodes, func(left int, right int) bool {
		return graph.Nodes[left].ID < graph.Nodes[right].ID
	})
	sort.SliceStable(graph.Edges, func(left int, right int) bool {
		first := graph.Edges[left]
		second := graph.Edges[right]
		if first.From != second.From {
			return first.From < second.From
		}
		if first.To != second.To {
			return first.To < second.To
		}
		if first.Kind != second.Kind {
			return first.Kind < second.Kind
		}
		return first.Label < second.Label
	})
	for index := range graph.Edges {
		graph.Edges[index].ID = fmt.Sprintf("edge:%04d", index+1)
	}
	sort.SliceStable(graph.Diagnostics, func(left int, right int) bool {
		first := graph.Diagnostics[left]
		second := graph.Diagnostics[right]
		if first.Severity != second.Severity {
			return first.Severity < second.Severity
		}
		if first.Code != second.Code {
			return first.Code < second.Code
		}
		return first.Message < second.Message
	})
}

func (graph *Graph) addUnknownTargets() {
	known := make(map[string]bool, len(graph.Nodes))
	for _, node := range graph.Nodes {
		known[node.ID] = true
	}
	for _, edge := range graph.Edges {
		if edge.To == "" || known[edge.To] {
			continue
		}
		graph.AddNode(Node{ID: edge.To, Kind: "unknown", Name: "Unknown"})
		known[edge.To] = true
	}
}

func (graph *Graph) addUnreachableWarnings() {
	if graph.Flow.StartStepID == "" {
		return
	}
	reachable := map[string]bool{graph.Flow.StartStepID: true}
	for _, node := range graph.Nodes {
		if node.Kind == "rpc" || node.Kind == "timeout_handler" {
			reachable[node.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, node := range graph.Nodes {
			if node.ParentID == "" || !reachable[node.ParentID] || reachable[node.ID] {
				continue
			}
			reachable[node.ID] = true
			changed = true
		}
		for _, edge := range graph.Edges {
			if !isControlEdge(edge.Kind) {
				continue
			}
			if !reachable[edge.From] || reachable[edge.To] {
				continue
			}
			reachable[edge.To] = true
			changed = true
		}
	}
	for _, node := range graph.Nodes {
		if node.Kind == "step" && !reachable[node.ID] {
			graph.AddDiagnostic("warning", "unreachable_step", fmt.Sprintf("Step %s is not reachable from the start Step or an RPC", node.Name), node.Span)
		}
	}
}

func (graph *Graph) addUnusedResourceWarnings() {
	used := make(map[string]bool)
	for _, edge := range graph.Edges {
		if isResourceEdge(edge.Kind) {
			used[edge.From] = true
			used[edge.To] = true
		}
	}
	for _, node := range graph.Nodes {
		if isResourceNode(node.Kind) && !used[node.ID] {
			graph.AddDiagnostic("warning", "unused_resource", fmt.Sprintf("%s %s has no direct access in the source file", node.Kind, node.Name), node.Span)
		}
	}
}

func isResourceNode(kind string) bool {
	return kind == "attribute" || kind == "channel" || kind == "stream"
}

func isResourceEdge(kind string) bool {
	return strings.HasPrefix(kind, "resource_") || kind == "wait_condition"
}

func isControlEdge(kind string) bool {
	return kind == "transition" || kind == "failure_transition"
}

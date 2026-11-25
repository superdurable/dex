// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

func TestGoAnalyzerLoadsImportsForConnectorModuleVersions(t *testing.T) {
	if goPackagesLoadMode&packages.NeedImports == 0 {
		t.Fatal("Go analyzer must load imports to index Connector module versions")
	}
}

func TestGoAnalyzerSpreadSubFlowWait(t *testing.T) {
	const initialize = `conditions := make([]dex.Condition, 0, len(requests))`
	const appendSubFlow = `for _, request := range requests {
		conditions = append(conditions, dex.SubFlow(step.child, request))
	}`
	const waitAll = `return dex.AllOf(conditions...), nil`
	tests := []struct {
		name     string
		body     string
		waitType string
		accepted bool
	}{
		{"all", initialize + "\n" + appendSubFlow + "\n" + waitAll, "allOf", true},
		{"any", "var conditions []dex.Condition\n" + appendSubFlow + "\nreturn (dex.AnyOf((conditions)...)), nil", "anyOf", true},
		{"empty literal", "conditions := []dex.Condition{}\n" + appendSubFlow + "\n" + waitAll, "allOf", true},
		{"interface child", initialize + "\n" + strings.Replace(appendSubFlow, "step.child", "step.dynamicChild", 1) + "\n" + waitAll, "allOf", false},
		{"dynamic Flow type", initialize + "\n" + strings.Replace(appendSubFlow, "step.child", "step.dynamicTypeChild", 1) + "\n" + waitAll, "allOf", false},
		{"mixed conditions", initialize + "\n" + strings.Replace(appendSubFlow, "request))", "request), dex.Timer(0))", 1) + "\n" + waitAll, "allOf", false},
		{"prepopulated list", "conditions := []dex.Condition{dex.Timer(0)}\n" + appendSubFlow + "\n" + waitAll, "allOf", false},
		{"nonzero make length", strings.Replace(initialize, ", 0,", ", 1,", 1) + "\n" + appendSubFlow + "\n" + waitAll, "allOf", false},
		{"slice alias", initialize + "\n" + appendSubFlow + "\nalias := conditions\nreturn dex.AllOf(alias...), nil", "allOf", false},
		{"slice write after loop", initialize + "\n" + appendSubFlow + "\nconditions = append(conditions, dex.Timer(0))\n" + waitAll, "allOf", false},
		{"spread from another call", "return dex.AllOf(conditionList()...), nil", "allOf", false},
		{"spread in combo", initialize + "\n" + appendSubFlow + "\nreturn dex.AnyComboOf(dex.Combo(conditions...)), nil", "anyComboOf", false},
		{"shadowed append", "append := func(list []dex.Condition, _ dex.Condition) []dex.Condition { return list }\n" + initialize + "\n" + appendSubFlow + "\n" + waitAll, "allOf", false},
		{"slice captured in loop", initialize + "\n" + strings.Replace(appendSubFlow, "request))", "func() string { conditions = nil; return request }()))", 1) + "\n" + waitAll, "allOf", false},
	}

	var waitBranches strings.Builder
	for _, test := range tests {
		fmt.Fprintf(&waitBranches, "case %q:\n%s\n", test.name, test.body)
	}
	graph := analyzeGoSpreadWaitSource(t, fmt.Sprintf(goSpreadWaitSource, waitBranches.String()))
	require.NotContains(t, diagnosticCodes(graph), "go_type_check_failed", "%+v", graph.Diagnostics)
	rejectedCount := 0
	for _, test := range tests {
		if !test.accepted {
			rejectedCount++
		}
		t.Run(test.name, func(t *testing.T) {
			branch := "requests[0] == " + fmt.Sprintf("%q", test.name)
			for _, node := range graph.Nodes {
				if node.Kind != "wait" || node.Condition != branch {
					continue
				}
				require.Equal(t, test.waitType, node.Wait.Type)
				require.Len(t, node.Wait.Conditions, 1)
				if test.accepted {
					requireGoSpreadSubFlowCondition(t, graph, node, "RegisteredChild")
					return
				}
				condition := node.Wait.Conditions[0]
				require.Equal(t, "unknown", condition.Kind)
				require.Empty(t, condition.SubFlowID)
				require.NotNil(t, condition.Span)
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "dynamic_wait_condition" && diagnostic.Span != nil && *diagnostic.Span == *condition.Span {
						require.Equal(t, "error", diagnostic.Severity)
						return
					}
				}
				t.Fatal("missing dynamic_wait_condition diagnostic for spread argument")
			}
			t.Fatalf("missing Wait for %s", branch)
		})
	}

	diagnosticCount := 0
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "dynamic_wait_condition" {
			diagnosticCount++
		}
	}
	require.Equal(t, rejectedCount, diagnosticCount)
}

func TestGoAnalyzerParallelSubFlowsExample(t *testing.T) {
	source := filepath.Join("..", "..", "..", "examples", "go", "patterns", "parallel-subflows", "basic_parent_flow.go")
	graph, err := Analyze(context.Background(), source, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.NotContains(t, diagnosticCodes(graph), "dynamic_wait_condition")
	for _, node := range graph.Nodes {
		if node.Kind == "wait" && node.Wait.Type == "allOf" {
			requireGoSpreadSubFlowCondition(t, graph, node, "ExampleSubFlow")
			return
		}
	}
	t.Fatalf("missing BasicParentFlow AllOf wait: %+v", graph.Diagnostics)
}

func requireGoSpreadSubFlowCondition(t *testing.T, graph *Graph, wait Node, childFlowType string) {
	t.Helper()
	require.Len(t, wait.Wait.Conditions, 1)
	condition := wait.Wait.Conditions[0]
	require.Equal(t, "subflow", condition.Kind)
	require.Equal(t, childFlowType, condition.Label)
	require.NotNil(t, condition.Index)
	require.Zero(t, *condition.Index)
	var childNodes []Node
	for _, node := range graph.Nodes {
		if node.Kind == "subflow" && node.ID == condition.SubFlowID {
			childNodes = append(childNodes, node)
		}
	}
	require.Len(t, childNodes, 1)
	require.Equal(t, childFlowType, childNodes[0].Metadata["flowType"])
	require.NotEmpty(t, childNodes[0].Metadata["sourcePath"])
	var childEdges []Edge
	for _, edge := range graph.Edges {
		if edge.From == wait.ID && edge.Kind == "subflow" {
			childEdges = append(childEdges, edge)
		}
	}
	require.Len(t, childEdges, 1)
	require.Equal(t, condition.SubFlowID, childEdges[0].To)
	require.Equal(t, "start", childEdges[0].Label)
	require.Equal(t, 0, childEdges[0].Metadata["conditionIndex"])
}

func analyzeGoSpreadWaitSource(t *testing.T, source string) *Graph {
	t.Helper()
	directory := t.TempDir()
	sdkDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk-go"))
	require.NoError(t, err)
	module := "module spreadwait\n\ngo 1.26.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.ToSlash(sdkDirectory) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	sourcePath := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, FlowType: "ParentFlow"})
	require.NoError(t, err)
	return graph
}

const goSpreadWaitSource = `package spreadwait

import "github.com/superdurable/dex/sdk-go/dex"

type ParentFlow struct { dex.FlowDefaults }
func (*ParentFlow) GetSteps() []dex.StepDef { return []dex.StepDef{dex.DefineStartStep(LaunchChildren{})} }
func (*ParentFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type ChildFlow struct { dex.FlowDefaults }
func (*ChildFlow) GetFlowType() string { return "RegisteredChild" }
func (*ChildFlow) GetSteps() []dex.StepDef { return nil }
func (*ChildFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type DynamicChildFlow struct { dex.FlowDefaults; name string }
func (flow *DynamicChildFlow) GetFlowType() string { return flow.name }
func (*DynamicChildFlow) GetSteps() []dex.StepDef { return nil }
func (*DynamicChildFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type LaunchChildren struct {
	dex.StepDefaults
	child *ChildFlow
	dynamicChild dex.Flow
	dynamicTypeChild *DynamicChildFlow
}

func (step LaunchChildren) WaitFor(_ dex.Context, requests []string) (*dex.Wait, error) {
	switch requests[0] {
	%s
	default:
		return dex.Until(dex.Timer(0)), nil
	}
}

func (LaunchChildren) Execute(_ dex.Context, _ []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

func conditionList() []dex.Condition { return nil }
`

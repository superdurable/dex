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
		{"spread from field", "return dex.AllOf(step.prebuilt...), nil", "allOf", false},
		{"statement before return", initialize + "\n" + appendSubFlow + "\n_ = len(conditions)\n" + waitAll, "allOf", false},
		{"range over map", initialize + "\n" + strings.Replace(appendSubFlow, "range requests", "range map[string]string{\"request\": requests[0]}", 1) + "\n" + waitAll, "allOf", false},
		{"spread in combo", initialize + "\n" + appendSubFlow + "\nreturn dex.AnyComboOf(dex.Combo(conditions...)), nil", "anyComboOf", false},
		{"top-level combo spread", "combos := []dex.ConditionCombination{dex.Combo(dex.SubFlow(step.child, requests[0]))}\nreturn dex.AnyComboOf(combos...), nil", "anyComboOf", false},
		{"opaque appended element", "conditions := []dex.Condition{dex.Timer(0)}\nconditions = append(conditions, step.prebuilt[0])\n" + waitAll, "allOf", false},
		{"opaque element helper", "return dex.AllOf([]dex.Condition{firstCondition(step.prebuilt)}...), nil", "allOf", false},
		{"slice passed to another call", "conditions := []dex.Condition{dex.Timer(0)}\nreplaceCondition(conditions, dex.SubFlow(step.child, requests[0]))\n" + waitAll, "allOf", false},
		{"alias overwrites element", "conditions := []dex.Condition{dex.Timer(0)}\nalias := conditions\nalias[0] = dex.SubFlow(step.child, requests[0])\n" + waitAll, "allOf", false},
		{"shadowed append", "append := func(list []dex.Condition, _ dex.Condition) []dex.Condition { return list }\n" + initialize + "\n" + appendSubFlow + "\n" + waitAll, "allOf", false},
		{"slice captured in loop", initialize + "\n" + strings.Replace(appendSubFlow, "request))", "func() string { conditions = nil; return request }()))", 1) + "\n" + waitAll, "allOf", false},
	}

	var waitBranches strings.Builder
	for _, test := range tests {
		fmt.Fprintf(&waitBranches, "case %q:\n%s\n", test.name, test.body)
	}
	graph := analyzeGoSpreadWaitSource(t, fmt.Sprintf(goSpreadWaitSource, waitBranches.String()))
	require.NotContains(t, diagnosticCodes(graph), "go_type_check_failed", "%+v", graph.Diagnostics)
	require.False(t, graph.Valid)
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

func TestGoAnalyzerSpreadConditionParameter(t *testing.T) {
	source := fmt.Sprintf(goSpreadWaitSource, "case \"parameter\":\nreturn dex.AllOf(prebuilt...), nil")
	source = strings.NewReplacer("requests []string", "prebuilt []dex.Condition", "switch requests[0]", "switch \"parameter\"", "_ []string", "_ []dex.Condition").Replace(source)
	graph := analyzeGoSpreadWaitSource(t, source)
	require.NotContains(t, diagnosticCodes(graph), "go_type_check_failed", "%+v", graph.Diagnostics)
	require.False(t, graph.Valid)
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "dynamic_wait_condition" {
			require.Equal(t, "error", diagnostic.Severity)
			return
		}
	}
	t.Fatal("missing dynamic_wait_condition diagnostic for parameter")
}

func TestGoAnalyzerSpreadNonSubFlowConditions(t *testing.T) {
	const branches = `
	case "conditional timer":
		conditions := []dex.Condition{approvalChannel.ForOne()}
		if len(requests) > 1 {
			timer, err := approvalTimer(requests[1])
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, timer)
		}
		return dex.AnyOf(conditions...), nil
	case "range timers":
		var conditions []dex.Condition
		for range requests {
			conditions = append(conditions, dex.Timer(0))
		}
		return dex.AllOf(conditions...), nil
	case "combo spread":
		return dex.AnyComboOf([]dex.ConditionCombination{dex.Combo(dex.Timer(0))}...), nil
	case "spread in combo":
		conditions := []dex.Condition{dex.Timer(0)}
		return dex.AnyComboOf(dex.Combo(conditions...)), nil
	case "alias":
		conditions := []dex.Condition{dex.Timer(0)}
		alias := conditions
		return dex.AllOf(alias...), nil
	`
	graph := analyzeGoSpreadWaitSource(t, fmt.Sprintf(goSpreadWaitSource, branches))
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	warningCount := 0
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "dynamic_wait_condition" {
			require.Equal(t, "warning", diagnostic.Severity)
			warningCount++
		}
	}
	require.Equal(t, 5, warningCount)
	unknownCount := 0
	for _, node := range graph.Nodes {
		if node.Wait != nil && len(node.Wait.Conditions) == 1 && node.Wait.Conditions[0].Kind == "unknown" {
			require.Empty(t, node.Wait.Conditions[0].SubFlowID)
			unknownCount++
		}
	}
	require.Equal(t, warningCount, unknownCount)
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

import (
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

type ParentFlow struct { dex.FlowDefaults }
func (*ParentFlow) GetSteps() []dex.StepDef { return []dex.StepDef{dex.DefineStartStep(LaunchChildren{})} }
func (*ParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{approvalChannel}}
}

func (flow *ParentFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetDexSummary, nil), dex.DefineRPC(flow.GetDexDisplay, nil)}
}

func (*ParentFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ParentFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

type ChildFlow struct { dex.FlowDefaults }
func (*ChildFlow) GetFlowType() string { return "RegisteredChild" }
func (*ChildFlow) GetSteps() []dex.StepDef { return nil }
func (*ChildFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type DynamicChildFlow struct { dex.FlowDefaults; name string }
func (flow *DynamicChildFlow) GetFlowType() string { return flow.name }
func (*DynamicChildFlow) GetSteps() []dex.StepDef { return nil }
func (*DynamicChildFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

// dex:group group-id:children group-label:"Children"
// dex:explanation text:"Waits for children."
type LaunchChildren struct {
	dex.StepDefaults
	child *ChildFlow
	dynamicChild dex.Flow
	dynamicTypeChild *DynamicChildFlow
	prebuilt []dex.Condition
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

func firstCondition(conditions []dex.Condition) dex.Condition { return conditions[0] }

func replaceCondition(conditions []dex.Condition, condition dex.Condition) {
	conditions[0] = condition
}

var approvalChannel = dex.DefineChannel[string]("approvals")

func approvalTimer(request string) (dex.Condition, error) {
	if request == "" {
		return nil, errors.New("missing approval request")
	}
	return dex.Timer(0), nil
}
`

// TestGoAnalyzerChannelConstructorsWithOptions keeps Channel nodes and edges for the WithOptions constructors.
func TestGoAnalyzerChannelConstructorsWithOptions(t *testing.T) {
	graph := analyzeGoSpreadWaitSource(t, goChannelWithOptionsSource)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	channelNodes := map[string]Node{}
	for _, node := range graph.Nodes {
		if strings.HasPrefix(node.ID, "resource:channel:") {
			channelNodes[node.Name] = node
		}
	}
	require.Contains(t, channelNodes, "replies")
	require.Contains(t, channelNodes, "events")
	require.NotNil(t, channelNodes["replies"].Resource)
	require.False(t, channelNodes["replies"].Resource.Map)
	require.NotNil(t, channelNodes["events"].Resource)
	require.True(t, channelNodes["events"].Resource.Map, "the ChannelMap variant keeps its map marker")
	edgeKinds := map[string]string{}
	for _, edge := range graph.Edges {
		for name, node := range channelNodes {
			if edge.From == node.ID || edge.To == node.ID {
				edgeKinds[name] = edge.Kind
			}
		}
	}
	require.Equal(t, "wait_condition", edgeKinds["replies"])
	require.Equal(t, "resource_publish", edgeKinds["events"])
}

const goChannelWithOptionsSource = `package spreadwait

import "github.com/superdurable/dex/sdk-go/dex"

var conversationLock = dex.DefineAttribute[bool]("conversation-lock")
var replies = dex.DefineChannelWithOptions[string]("replies", dex.PublishesRequireAttributeLock(conversationLock))
var events = dex.DefineChannelMapWithOptions[string]("events", dex.PublishesRequireAttributeLock(conversationLock))

type ParentFlow struct { dex.FlowDefaults }
func (*ParentFlow) GetSteps() []dex.StepDef { return []dex.StepDef{dex.DefineStartStep(AwaitReply{})} }
func (*ParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{conversationLock},
		Channels:   []dex.ChannelDef{replies, events},
	}
}

func (flow *ParentFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RecordEvent, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(conversationLock)},
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*ParentFlow) RecordEvent(ctx dex.Context, instance string) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, events.Publish(ctx, instance, "event")
}

func (*ParentFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ParentFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:replies group-label:"Replies"
// dex:explanation text:"Waits for one reply."
type AwaitReply struct { dex.StepDefaults }

func (AwaitReply) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return dex.Until(replies.ForOne()), nil
}

func (AwaitReply) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
`

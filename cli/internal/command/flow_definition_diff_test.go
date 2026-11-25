// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowdefinitiondiff"
	"github.com/superdurable/dex/cli/internal/flowviz"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The fixtures are copies of docs/src/data/flow-definitions, generated from runnable examples.
const (
	managerApprovalFixture = "testdata/flow-definition-diff/manager-approval-refund-flow.json"
	subscriptionFixture    = "testdata/flow-definition-diff/subscription-flow.json"
)

func loadFlowDefinitionFixture(t *testing.T, path string) *flowviz.Graph {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	graph, err := flowdefinitiondiff.ParseGraph(content)
	require.NoError(t, err)
	return graph
}

func writeFlowDefinitionFiles(t *testing.T, graphs ...*flowviz.Graph) string {
	t.Helper()
	directory := t.TempDir()
	for _, graph := range graphs {
		content, err := json.Marshal(graph)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(directory, graph.Flow.Name+".json"), content, 0o600))
	}
	return directory
}

func executeFlowDefinitionDiff(args ...string) (string, string, error) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	err := NewApp(bytes.NewReader(nil), stdout, stderr).Execute(context.Background(), args)
	return stdout.String(), stderr.String(), err
}

// runFlowDefinitionDiff runs the command with --json and returns the decoded report and the command error.
func runFlowDefinitionDiff(t *testing.T, args ...string) (flowDefinitionDiffReport, error) {
	t.Helper()
	stdout, stderr, err := executeFlowDefinitionDiff(append([]string{"flow-definition", "diff", "--json"}, args...)...)
	var report flowDefinitionDiffReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), "stdout: %s stderr: %s err: %v", stdout, stderr, err)
	return report, err
}

func diffGraphs(t *testing.T, oldGraph *flowviz.Graph, newGraph *flowviz.Graph, extraArgs ...string) (flowDefinitionDiffReport, error) {
	t.Helper()
	oldDirectory := writeFlowDefinitionFiles(t, oldGraph)
	newDirectory := writeFlowDefinitionFiles(t, newGraph)
	return runFlowDefinitionDiff(t, append([]string{newDirectory, "--old", oldDirectory}, extraArgs...)...)
}

func changeCodes(report flowDefinitionDiffReport) map[string]flowdefinitiondiff.Severity {
	codes := make(map[string]flowdefinitiondiff.Severity, len(report.Changes))
	for _, change := range report.Changes {
		codes[change.Code+" "+change.Name] = change.Severity
	}
	return codes
}

func findNode(t *testing.T, graph *flowviz.Graph, kind string, name string) *flowviz.Node {
	t.Helper()
	for index := range graph.Nodes {
		if graph.Nodes[index].Kind == kind && graph.Nodes[index].Name == name {
			return &graph.Nodes[index]
		}
	}
	require.Failf(t, "node not found", "%s %s", kind, name)
	return nil
}

func removeNode(t *testing.T, graph *flowviz.Graph, kind string, name string) {
	t.Helper()
	id := findNode(t, graph, kind, name).ID
	nodes := graph.Nodes[:0]
	for _, node := range graph.Nodes {
		if node.ID != id && node.ParentID != id {
			nodes = append(nodes, node)
		}
	}
	graph.Nodes = nodes
}

func findAction(t *testing.T, graph *flowviz.Graph, rpcName string) *flowviz.Action {
	t.Helper()
	for index := range graph.V2.Actions {
		if graph.V2.Actions[index].RPCName == rpcName {
			return &graph.V2.Actions[index]
		}
	}
	require.Failf(t, "Action not found", rpcName)
	return nil
}

func removeAction(graph *flowviz.Graph, rpcName string) {
	actions := graph.V2.Actions[:0]
	for _, action := range graph.V2.Actions {
		if action.RPCName != rpcName {
			actions = append(actions, action)
		}
	}
	graph.V2.Actions = actions
}

func findStartField(t *testing.T, graph *flowviz.Graph, name string) *flowviz.StartInputField {
	t.Helper()
	for index := range graph.V2.Start.Input.Fields {
		if graph.V2.Start.Input.Fields[index].Name == name {
			return &graph.V2.Start.Input.Fields[index]
		}
	}
	require.Failf(t, "start input field not found", name)
	return nil
}

func removeStartField(graph *flowviz.Graph, name string) {
	fields := graph.V2.Start.Input.Fields[:0]
	for _, field := range graph.V2.Start.Input.Fields {
		if field.Name != name {
			fields = append(fields, field)
		}
	}
	graph.V2.Start.Input.Fields = fields
}

func findManagerDecisionWait(t *testing.T, graph *flowviz.Graph) *flowviz.WaitDetails {
	t.Helper()
	for index := range graph.Nodes {
		if graph.Nodes[index].Kind == "wait" && graph.Nodes[index].ParentID == "step:RequestManagerApproval" {
			return graph.Nodes[index].Wait
		}
	}
	require.Fail(t, "RequestManagerApproval wait not found")
	return nil
}

func shiftSpan(span *flowviz.Span) {
	if span != nil {
		span.StartLine += 10
		span.EndLine += 10
	}
}

// renameNodeID changes an analyzer node ID and every reference to it, as an unrelated source edit would.
func renameNodeID(graph *flowviz.Graph, oldID string, newID string) {
	for index := range graph.Nodes {
		node := &graph.Nodes[index]
		if node.ID == oldID {
			node.ID = newID
		}
		if node.ParentID == oldID {
			node.ParentID = newID
		}
		if node.Wait != nil {
			for conditionIndex := range node.Wait.Conditions {
				if node.Wait.Conditions[conditionIndex].ResourceID == oldID {
					node.Wait.Conditions[conditionIndex].ResourceID = newID
				}
			}
		}
	}
	for index := range graph.Edges {
		if graph.Edges[index].From == oldID {
			graph.Edges[index].From = newID
		}
		if graph.Edges[index].To == oldID {
			graph.Edges[index].To = newID
		}
	}
	if graph.Flow.StartStepID == oldID {
		graph.Flow.StartStepID = newID
	}
}

// addStartInputShapes gives the start input nested, enum, nullable, and fixed-length fields to mutate.
func addStartInputShapes(graph *flowviz.Graph) {
	length := int64(2)
	graph.V2.Start.Input.Fields = append(graph.V2.Start.Input.Fields,
		flowviz.StartInputField{Name: "items", Schema: flowviz.StartInputSchema{Kind: "array", Items: &flowviz.StartInputSchema{Kind: "string"}}},
		flowviz.StartInputField{Name: "labels", Schema: flowviz.StartInputSchema{Kind: "map", Values: &flowviz.StartInputSchema{Kind: "string"}}},
		flowviz.StartInputField{Name: "pair", Schema: flowviz.StartInputSchema{Kind: "array", FixedLength: &length, Items: &flowviz.StartInputSchema{Kind: "string"}}},
		flowviz.StartInputField{Name: "tier", Schema: flowviz.StartInputSchema{Kind: "string", EnumValues: []flowviz.StartInputEnumValue{
			{Name: "gold", Value: "gold"}, {Name: "silver", Value: "silver"},
		}}},
		flowviz.StartInputField{Name: "note", Schema: flowviz.StartInputSchema{Kind: "string", Nullable: true}},
	)
}

func TestFlowDefinitionDiffIgnoresSourcePositionsAndNodeIDs(t *testing.T) {
	for _, fixture := range []string{managerApprovalFixture, subscriptionFixture} {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			oldGraph := loadFlowDefinitionFixture(t, fixture)
			newGraph := loadFlowDefinitionFixture(t, fixture)
			newGraph.GeneratedAt = "2030-01-01T00:00:00Z"
			newGraph.Source.Path = "moved/elsewhere.go"
			shiftSpan(newGraph.Flow.Span)
			for index := range newGraph.Nodes {
				shiftSpan(newGraph.Nodes[index].Span)
			}
			for index := range newGraph.Edges {
				shiftSpan(newGraph.Edges[index].Span)
			}
			for _, node := range append([]flowviz.Node(nil), newGraph.Nodes...) {
				if node.Kind == "wait" || node.Kind == "channel" || node.Kind == "step" {
					renameNodeID(newGraph, node.ID, node.ID+"-moved")
				}
			}

			report, err := diffGraphs(t, oldGraph, newGraph)

			require.NoError(t, err)
			require.Empty(t, report.Changes)
			require.Equal(t, []string{oldGraph.Flow.Name}, report.ComparedFlowTypes)
		})
	}
}

type flowDefinitionDiffCase struct {
	name string
	// prepare edits both graphs first, so a change can start from a shape the fixture lacks.
	prepare      func(t *testing.T, graph *flowviz.Graph)
	mutate       func(t *testing.T, graph *flowviz.Graph)
	expectedCode string
	expectedName string
	severity     flowdefinitiondiff.Severity
	// alsoExpected lists the other changes the edit must report; the report must hold nothing else.
	alsoExpected map[string]flowdefinitiondiff.Severity
}

func runFlowDefinitionDiffCases(t *testing.T, fixture string, cases []flowDefinitionDiffCase) {
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			oldGraph := loadFlowDefinitionFixture(t, fixture)
			newGraph := loadFlowDefinitionFixture(t, fixture)
			if testCase.prepare != nil {
				testCase.prepare(t, oldGraph)
				testCase.prepare(t, newGraph)
			}
			testCase.mutate(t, newGraph)

			report, err := diffGraphs(t, oldGraph, newGraph)

			expected := map[string]flowdefinitiondiff.Severity{testCase.expectedCode + " " + testCase.expectedName: testCase.severity}
			expectedBreaking := 0
			for key, severity := range testCase.alsoExpected {
				expected[key] = severity
			}
			for _, severity := range expected {
				if severity == flowdefinitiondiff.SeverityBreaking {
					expectedBreaking++
				}
			}
			require.Equal(t, expected, changeCodes(report))
			require.Equal(t, expectedBreaking, report.Summary.Breaking)
			if expectedBreaking > 0 {
				require.Equal(t, 3, ExitCode(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// setManagerDecisionWait replaces the one RequestManagerApproval wait condition with a Channel condition.
func setManagerDecisionWait(label string, expression string) func(*testing.T, *flowviz.Graph) {
	return func(t *testing.T, graph *flowviz.Graph) {
		condition := &findManagerDecisionWait(t, graph).Conditions[0]
		condition.Label, condition.Expression = label, expression
	}
}

func makeManagerDecisionsChannelMap(t *testing.T, graph *flowviz.Graph) {
	findNode(t, graph, "channel", "manager-decisions").Resource.Map = true
}

func TestFlowDefinitionDiffClassifiesBreakingChanges(t *testing.T) {
	addStream := func(_ *testing.T, graph *flowviz.Graph) {
		graph.Nodes = append(graph.Nodes, flowviz.Node{
			ID: "resource:stream:progress", Kind: "stream", Name: "refund-progress", Resource: &flowviz.ResourceDetails{ValueType: "string"},
		})
	}
	runFlowDefinitionDiffCases(t, managerApprovalFixture, []flowDefinitionDiffCase{
		{name: "Step type removed", mutate: func(t *testing.T, graph *flowviz.Graph) { removeNode(t, graph, "step", "IssueRefund") },
			expectedCode: "step_type_removed", expectedName: "IssueRefund", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "RPC removed", mutate: func(t *testing.T, graph *flowviz.Graph) { removeNode(t, graph, "rpc", "RejectRefund") },
			expectedCode: "rpc_removed", expectedName: "RejectRefund", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Attribute removed", mutate: func(t *testing.T, graph *flowviz.Graph) { removeNode(t, graph, "attribute", "customer-email") },
			expectedCode: "resource_removed", expectedName: "customer-email", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Channel removed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			removeNode(t, graph, "channel", "manager-decisions")
			wait := findManagerDecisionWait(t, graph)
			wait.Conditions = []flowviz.WaitCondition{{Kind: "timer", Label: "48h timer", Expression: "48 * time.Hour"}}
		}, expectedCode: "resource_removed", expectedName: "manager-decisions", severity: flowdefinitiondiff.SeverityBreaking,
			alsoExpected: map[string]flowdefinitiondiff.Severity{"step_wait_conditions_changed RequestManagerApproval": flowdefinitiondiff.SeverityRisky}},
		{name: "Stream removed", prepare: addStream, mutate: func(t *testing.T, graph *flowviz.Graph) { removeNode(t, graph, "stream", "refund-progress") },
			expectedCode: "resource_removed", expectedName: "refund-progress", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Attribute becomes AttributeMap", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "attribute", "customer-message").Resource.Map = true
		}, expectedCode: "resource_kind_changed", expectedName: "customer-message", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Channel becomes ChannelMap", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "channel", "manager-decisions").Resource.Map = true
		}, expectedCode: "resource_kind_changed", expectedName: "manager-decisions", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "ChannelMap becomes Channel", prepare: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "channel", "manager-decisions").Resource.Map = true
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "channel", "manager-decisions").Resource.Map = false
		}, expectedCode: "resource_kind_changed", expectedName: "manager-decisions", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "start input kind changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findStartField(t, graph, "orderId").Schema = flowviz.StartInputSchema{Kind: "integer", Minimum: "0", Maximum: "10"}
		}, expectedCode: "start_input_kind_changed", expectedName: "orderId", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "nested array item kind changed", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate: func(t *testing.T, graph *flowviz.Graph) {
				findStartField(t, graph, "items").Schema.Items = &flowviz.StartInputSchema{Kind: "boolean"}
			}, expectedCode: "start_input_kind_changed", expectedName: "items[]", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "nested map value kind changed", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate: func(t *testing.T, graph *flowviz.Graph) {
				findStartField(t, graph, "labels").Schema.Values = &flowviz.StartInputSchema{Kind: "number"}
			}, expectedCode: "start_input_kind_changed", expectedName: "labels{}", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Action input field type changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Input.Fields[0].ValueType = "int64"
		}, expectedCode: "action_input_field_type_changed", expectedName: "ApproveRefund.reason", severity: flowdefinitiondiff.SeverityBreaking},
		{name: "Action input kind changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "RejectRefund").Input = flowviz.ActionInput{Kind: "none"}
		}, expectedCode: "action_input_kind_changed", expectedName: "RejectRefund", severity: flowdefinitiondiff.SeverityBreaking},
	})
}

func TestFlowDefinitionDiffClassifiesRiskyChanges(t *testing.T) {
	withIndexes := func(_ *testing.T, graph *flowviz.Graph) {
		graph.V2.IndexedAttributes = []flowviz.IndexedAttribute{
			{AttributeKey: "case-status", IndexKey: "CaseStatus", IndexType: "keyword", ValueType: "string"},
			{AttributeKey: "refund-id", IndexKey: "RefundID", IndexType: "keyword", ValueType: "string"},
		}
	}
	runFlowDefinitionDiffCases(t, managerApprovalFixture, []flowDefinitionDiffCase{
		{name: "value type import alias changed", prepare: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "channel", "manager-decisions").Resource.ValueType = "pkg.ManagerDecision"
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "channel", "manager-decisions").Resource.ValueType = "alias.ManagerDecision"
		}, expectedCode: "resource_type_changed", expectedName: "manager-decisions", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Stream value type changed", prepare: func(_ *testing.T, graph *flowviz.Graph) {
			graph.Nodes = append(graph.Nodes, flowviz.Node{
				ID: "resource:stream:progress", Kind: "stream", Name: "refund-progress", Resource: &flowviz.ResourceDetails{ValueType: "string"},
			})
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "stream", "refund-progress").Resource.ValueType = "ProgressUpdate"
		}, expectedCode: "resource_type_changed", expectedName: "refund-progress", severity: flowdefinitiondiff.SeverityRisky},
		{name: "value type equivalent spelling", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "attribute", "refund-id").Resource.ValueType = "[]uint8"
		}, prepare: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "attribute", "refund-id").Resource.ValueType = "[]byte"
		}, expectedCode: "resource_type_changed", expectedName: "refund-id", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start Step changed while both Step types stay", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.Start.StepType = "CheckOrder"
		}, expectedCode: "start_step_changed", expectedName: "CheckOrder", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Step gains WaitFor", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findNode(t, graph, "step", "CheckOrder").Phase = "wait_for+execute"
		}, expectedCode: "step_phase_changed", expectedName: "CheckOrder", severity: flowdefinitiondiff.SeverityRisky},
		{name: "wait adds a timer", mutate: func(t *testing.T, graph *flowviz.Graph) {
			wait := findManagerDecisionWait(t, graph)
			wait.Type = "anyOf"
			wait.Conditions = append(wait.Conditions, flowviz.WaitCondition{Kind: "timer", Label: "48h timer", Expression: "48 * time.Hour"})
		}, expectedCode: "step_wait_conditions_changed", expectedName: "RequestManagerApproval", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Channel wait literal count changed", prepare: setManagerDecisionWait("manager-decisions.for 2", "2"),
			mutate:       setManagerDecisionWait("manager-decisions.for 3", "3"),
			expectedCode: "step_wait_conditions_changed", expectedName: "RequestManagerApproval", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Channel wait keyword count changed", prepare: setManagerDecisionWait("manager-decisions.for N", "count=1"),
			mutate:       setManagerDecisionWait("manager-decisions.for N", "count=2"),
			expectedCode: "step_wait_conditions_changed", expectedName: "RequestManagerApproval", severity: flowdefinitiondiff.SeverityRisky},
		{name: "ChannelMap wait instance keyword changed", prepare: func(t *testing.T, graph *flowviz.Graph) {
			makeManagerDecisionsChannelMap(t, graph)
			setManagerDecisionWait("manager-decisions.for 1", "instance='blue'")(t, graph)
		}, mutate: setManagerDecisionWait("manager-decisions.for 1", "instance='green'"),
			expectedCode: "step_wait_conditions_changed", expectedName: "RequestManagerApproval", severity: flowdefinitiondiff.SeverityRisky},
		{name: "ChannelMap wait positional instance changed", prepare: func(t *testing.T, graph *flowviz.Graph) {
			makeManagerDecisionsChannelMap(t, graph)
			setManagerDecisionWait(`manager-decisions["blue"].for 1`, `"blue"`)(t, graph)
		}, mutate: setManagerDecisionWait(`manager-decisions["green"].for 1`, `"green"`),
			expectedCode: "step_wait_conditions_changed", expectedName: "RequestManagerApproval", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action condition value changed to one string with a comma", prepare: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"a", "b"}
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"a, b"}
		}, expectedCode: "action_condition_changed", expectedName: "ApproveRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action condition value changed JSON type", prepare: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{float64(1)}
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"1"}
		}, expectedCode: "action_condition_changed", expectedName: "ApproveRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action condition removed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition = nil
		}, expectedCode: "action_condition_changed", expectedName: "ApproveRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action input field Attribute key changed", prepare: func(t *testing.T, graph *flowviz.Graph) {
			field := &findAction(t, graph, "ApproveRefund").Input.Fields[0]
			field.Source, field.AttributeKey = "attribute", "customer-message"
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Input.Fields[0].AttributeKey = "case-status"
		}, expectedCode: "action_input_field_source_changed", expectedName: "ApproveRefund.reason", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input required field added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.Start.Input.Fields = append(graph.V2.Start.Input.Fields,
				flowviz.StartInputField{Name: "region", Required: true, Schema: flowviz.StartInputSchema{Kind: "string"}})
		}, expectedCode: "start_input_required_field_added", expectedName: "region", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input field now required", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findStartField(t, graph, "paymentTimeoutsToSimulate").Required = true
		}, expectedCode: "start_input_field_now_required", expectedName: "paymentTimeoutsToSimulate", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input field removed", mutate: func(_ *testing.T, graph *flowviz.Graph) { removeStartField(graph, "reason") },
			expectedCode: "start_input_field_removed", expectedName: "reason", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input range narrowed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findStartField(t, graph, "paymentTimeoutsToSimulate").Schema.Minimum = "0"
		}, expectedCode: "start_input_range_narrowed", expectedName: "paymentTimeoutsToSimulate", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input enum value removed", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate: func(t *testing.T, graph *flowviz.Graph) {
				findStartField(t, graph, "tier").Schema.EnumValues = []flowviz.StartInputEnumValue{{Name: "gold", Value: "gold"}}
			}, expectedCode: "start_input_enum_value_removed", expectedName: "tier", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input enum restriction added", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findStartField(t, graph, "customer").Schema.EnumValues = []flowviz.StartInputEnumValue{{Name: "vip", Value: "vip"}}
		}, expectedCode: "start_input_enum_restricted", expectedName: "customer", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input null rejected", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate:       func(t *testing.T, graph *flowviz.Graph) { findStartField(t, graph, "note").Schema.Nullable = false },
			expectedCode: "start_input_null_rejected", expectedName: "note", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input format changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findStartField(t, graph, "customer").Schema.Format = "date-time"
		}, expectedCode: "start_input_format_changed", expectedName: "customer", severity: flowdefinitiondiff.SeverityRisky},
		{name: "start input fixed length changed", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate: func(t *testing.T, graph *flowviz.Graph) {
				length := int64(3)
				findStartField(t, graph, "pair").Schema.FixedLength = &length
			}, expectedCode: "start_input_fixed_length_changed", expectedName: "pair", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action removed", mutate: func(_ *testing.T, graph *flowviz.Graph) { removeAction(graph, "RejectRefund") },
			expectedCode: "action_removed", expectedName: "RejectRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action permission changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").RequiredPermission = "refund.approve"
		}, expectedCode: "action_permission_changed", expectedName: "ApproveRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action condition changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"escalated"}
		}, expectedCode: "action_condition_changed", expectedName: "ApproveRefund", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action input field removed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Input.Fields = []flowviz.ActionInputField{
				{FieldName: "comment", ValueType: "string", Source: "user", Description: "Comment"},
			}
		}, expectedCode: "action_input_field_removed", expectedName: "ApproveRefund.reason", severity: flowdefinitiondiff.SeverityRisky,
			alsoExpected: map[string]flowdefinitiondiff.Severity{"action_input_field_added ApproveRefund.comment": flowdefinitiondiff.SeverityCompatible}},
		{name: "Action input field now required", prepare: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Input.Fields[0].Required = false
		}, mutate: func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Input.Fields[0].Required = true
		}, expectedCode: "action_input_field_now_required", expectedName: "ApproveRefund.reason", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action required input field added", mutate: func(t *testing.T, graph *flowviz.Graph) {
			approve := findAction(t, graph, "ApproveRefund")
			approve.Input.Fields = append(approve.Input.Fields,
				flowviz.ActionInputField{FieldName: "ticket", ValueType: "string", Source: "user", Required: true})
		}, expectedCode: "action_input_required_field_added", expectedName: "ApproveRefund.ticket", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Action input field source changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			field := &findAction(t, graph, "ApproveRefund").Input.Fields[0]
			field.Source, field.AttributeKey = "attribute", "customer-message"
		}, expectedCode: "action_input_field_source_changed", expectedName: "ApproveRefund.reason", severity: flowdefinitiondiff.SeverityRisky},
		{name: "indexed Attribute changed", prepare: withIndexes, mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.IndexedAttributes[0].IndexType = "fulltext"
		}, expectedCode: "indexed_attribute_changed", expectedName: "case-status", severity: flowdefinitiondiff.SeverityRisky},
		{name: "indexed Attribute removed", prepare: withIndexes, mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.IndexedAttributes = graph.V2.IndexedAttributes[:1]
		}, expectedCode: "indexed_attribute_removed", expectedName: "refund-id", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Summary RPC changed", mutate: func(_ *testing.T, graph *flowviz.Graph) { graph.V2.Summary.RPCName = "GetRefundSummary" },
			expectedCode: "summary_rpc_changed", expectedName: "GetRefundSummary", severity: flowdefinitiondiff.SeverityRisky},
		{name: "Display RPC cleared", mutate: func(_ *testing.T, graph *flowviz.Graph) { graph.V2.Display = flowviz.RPCView{} },
			expectedCode: "display_rpc_changed", expectedName: "GetDexDisplay", severity: flowdefinitiondiff.SeverityRisky},
		{name: "source language changed", mutate: func(t *testing.T, graph *flowviz.Graph) {
			graph.Source.Language = "python"
			findNode(t, graph, "attribute", "refund-id").Resource.ValueType = "str"
		}, expectedCode: "source_language_changed", expectedName: "ManagerApprovalRefundFlow", severity: flowdefinitiondiff.SeverityRisky},
	})
}

func TestFlowDefinitionDiffClassifiesCompatibleAdditions(t *testing.T) {
	runFlowDefinitionDiffCases(t, managerApprovalFixture, []flowDefinitionDiffCase{
		{name: "Step type added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.Nodes = append(graph.Nodes, flowviz.Node{ID: "step:Escalate", Kind: "step", Name: "Escalate", Phase: "execute"})
		}, expectedCode: "step_type_added", expectedName: "Escalate", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "RPC added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.Nodes = append(graph.Nodes, flowviz.Node{ID: "rpc:Escalate", Kind: "rpc", Name: "Escalate"})
		}, expectedCode: "rpc_added", expectedName: "Escalate", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "ChannelMap added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.Nodes = append(graph.Nodes, flowviz.Node{ID: "resource:channel:Escalations", Kind: "channel", Name: "escalations",
				Resource: &flowviz.ResourceDetails{ValueType: "string", Map: true}})
		}, expectedCode: "resource_added", expectedName: "escalations", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "optional start input field added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.Start.Input.Fields = append(graph.V2.Start.Input.Fields,
				flowviz.StartInputField{Name: "priority", Schema: flowviz.StartInputSchema{Kind: "string"}})
		}, expectedCode: "start_input_field_added", expectedName: "priority", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "start input enum value added", prepare: func(_ *testing.T, graph *flowviz.Graph) { addStartInputShapes(graph) },
			mutate: func(t *testing.T, graph *flowviz.Graph) {
				schema := &findStartField(t, graph, "tier").Schema
				schema.EnumValues = append(schema.EnumValues, flowviz.StartInputEnumValue{Name: "bronze", Value: "bronze"})
			}, expectedCode: "start_input_enum_value_added", expectedName: "tier", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "Action added", mutate: func(_ *testing.T, graph *flowviz.Graph) {
			graph.V2.Actions = append(graph.V2.Actions, flowviz.Action{
				RPCName: "Escalate", Label: "Escalate", RequiredPermission: "refund.manage", Input: flowviz.ActionInput{Kind: "none"},
			})
		}, expectedCode: "action_added", expectedName: "Escalate", severity: flowdefinitiondiff.SeverityCompatible},
		{name: "optional Action input field added", mutate: func(t *testing.T, graph *flowviz.Graph) {
			approve := findAction(t, graph, "ApproveRefund")
			approve.Input.Fields = append(approve.Input.Fields, flowviz.ActionInputField{FieldName: "note", ValueType: "string", Source: "user"})
		}, expectedCode: "action_input_field_added", expectedName: "ApproveRefund.note", severity: flowdefinitiondiff.SeverityCompatible},
	})
}

func TestFlowDefinitionDiffReportsPythonWaitConditionChange(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, subscriptionFixture)
	newGraph := loadFlowDefinitionFixture(t, subscriptionFixture)
	for index := range newGraph.Nodes {
		node := &newGraph.Nodes[index]
		if node.Kind == "wait" && node.ParentID == "step:Cancel" {
			node.Wait.Conditions[0].ResourceID = "resource:channel:SubscriptionFlow.update_charge_amount"
			node.Wait.Conditions[0].Label = "update-charge-amount.for 1"
		}
	}

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.NoError(t, err)
	require.Len(t, report.Changes, 1)
	change := report.Changes[0]
	require.Equal(t, "step_wait_conditions_changed", change.Code)
	require.Equal(t, "Cancel", change.Name)
	require.Contains(t, change.Message, "channel cancel-subscription for n count=1")
	require.Contains(t, change.Message, "channel update-charge-amount for n count=1")
}

func TestFlowDefinitionDiffIgnoresEquivalentWaitAndActionConditionSpellings(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*testing.T, *flowviz.Graph)
		mutate  func(*testing.T, *flowviz.Graph)
	}{
		{"positional and keyword count", setManagerDecisionWait("manager-decisions.for 2", "2"),
			setManagerDecisionWait("manager-decisions.for N", "count=2")},
		{"ForOne and ForN(1)", setManagerDecisionWait("manager-decisions.for 1", ""),
			setManagerDecisionWait("manager-decisions.for 1", "1")},
		{"dynamic count variable renamed", setManagerDecisionWait("manager-decisions.for requestedCount", "requestedCount"),
			setManagerDecisionWait("manager-decisions.for count", "count")},
		{"ChannelMap positional and keyword instance", func(t *testing.T, graph *flowviz.Graph) {
			makeManagerDecisionsChannelMap(t, graph)
			setManagerDecisionWait(`manager-decisions["blue"].for 1`, `"blue"`)(t, graph)
		}, setManagerDecisionWait("manager-decisions.for 1", "instance='blue'")},
		{"timer expression changed", func(t *testing.T, graph *flowviz.Graph) {
			wait := findManagerDecisionWait(t, graph)
			wait.Conditions = append(wait.Conditions, flowviz.WaitCondition{Kind: "timer", Label: "48h timer", Expression: "48 * time.Hour"})
		}, func(t *testing.T, graph *flowviz.Graph) {
			findManagerDecisionWait(t, graph).Conditions[1] = flowviz.WaitCondition{Kind: "timer", Label: "deadline timer", Expression: "deadline"}
		}},
		{"Action condition values reordered and repeated", func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"a", "b"}
		}, func(t *testing.T, graph *flowviz.Graph) {
			findAction(t, graph, "ApproveRefund").Condition.Values = []any{"b", "a", "b"}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
			newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
			testCase.prepare(t, oldGraph)
			testCase.prepare(t, newGraph)
			testCase.mutate(t, newGraph)

			report, err := diffGraphs(t, oldGraph, newGraph)

			require.NoError(t, err)
			require.Empty(t, report.Changes)
		})
	}
}

func TestFlowDefinitionDiffKeepsTheBreakingReportWithAllowBreaking(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, newGraph, "step", "IssueRefund")

	report, err := diffGraphs(t, oldGraph, newGraph, "--allow-breaking")

	require.NoError(t, err)
	require.Equal(t, 1, report.Summary.Breaking)
	require.Equal(t, "step_type_removed", report.Changes[0].Code)
}

func TestFlowDefinitionDiffRejectsUnusableGraphs(t *testing.T) {
	valid := loadFlowDefinitionFixture(t, managerApprovalFixture)
	partial := loadFlowDefinitionFixture(t, managerApprovalFixture)
	partial.Valid = false
	removeNode(t, partial, "step", "IssueRefund")
	other := loadFlowDefinitionFixture(t, subscriptionFixture)
	malformedDirectory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(malformedDirectory, "broken.json"),
		[]byte(`{"schemaVersion":"2.0","valid":true,"flow":{"name":"BrokenFlow"},"nodes":"invalid"}`), 0o600))

	cases := []struct {
		name          string
		newDirectory  string
		oldDirectory  string
		expectedError string
	}{
		{"malformed new graph without a baseline", malformedDirectory, writeFlowDefinitionFiles(t, other), `new definition file ` + malformedDirectory},
		{"partial new graph", writeFlowDefinitionFiles(t, partial), writeFlowDefinitionFiles(t, valid), `(Flow type "ManagerApprovalRefundFlow")`},
		{"partial old graph", writeFlowDefinitionFiles(t, valid), writeFlowDefinitionFiles(t, partial), `old definition file `},
		{"old graph only on the old side", writeFlowDefinitionFiles(t, other), writeFlowDefinitionFiles(t, partial), `old definition file `},
		{"old paths without definitions", writeFlowDefinitionFiles(t, valid), t.TempDir(), "no old Flow definitions found"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, stderr, err := executeFlowDefinitionDiff("flow-definition", "diff", testCase.newDirectory,
				"--old", testCase.oldDirectory, "--allow-breaking")
			require.Error(t, err, stderr)
			require.Equal(t, 1, ExitCode(err))
			require.Contains(t, err.Error(), testCase.expectedError)
			require.Empty(t, stdout)
		})
	}
}

// TestFlowDefinitionDiffRejectsEveryUnreadableFile covers files that upload would skip in a directory.
func TestFlowDefinitionDiffRejectsEveryUnreadableFile(t *testing.T) {
	defects := map[string]string{
		"invalid JSON":            `{"schemaVersion":"2.0",`,
		"schemaVersion 1.0":       `{"schemaVersion":"1.0","valid":true,"flow":{"name":"LegacyFlow"}}`,
		"missing schemaVersion":   `{"name":"package","version":"1.0.0"}`,
		"empty flow.name":         `{"schemaVersion":"2.0","valid":true,"flow":{"name":""}}`,
		"flow.name of wrong type": `{"schemaVersion":"2.0","valid":true,"flow":{"name":7}}`,
	}
	subscription := loadFlowDefinitionFixture(t, subscriptionFixture)
	for defectName, content := range defects {
		for _, side := range []string{"new", "old"} {
			for _, form := range []string{"directory", "file"} {
				t.Run(fmt.Sprintf("%s %s %s", defectName, side, form), func(t *testing.T) {
					directory := writeFlowDefinitionFiles(t, subscription)
					badPath := filepath.Join(directory, "defect.json")
					require.NoError(t, os.WriteFile(badPath, []byte(content), 0o600))
					badArgument := directory
					if form == "file" {
						badArgument = badPath
					}
					goodDirectory := writeFlowDefinitionFiles(t, subscription)
					args := []string{"flow-definition", "diff", goodDirectory, "--old", badArgument, "--allow-breaking"}
					if side == "new" {
						args = []string{"flow-definition", "diff", badArgument, "--old", goodDirectory, "--allow-breaking"}
					}

					stdout, _, err := executeFlowDefinitionDiff(args...)

					require.Equal(t, 1, ExitCode(err), "error: %v", err)
					require.Contains(t, err.Error(), side+" definition file "+badPath)
					require.Empty(t, stdout)
				})
			}
		}
	}

	t.Run("new side is checked before the server", func(t *testing.T) {
		directory := t.TempDir()
		badPath := filepath.Join(directory, "defect.json")
		require.NoError(t, os.WriteFile(badPath, []byte(defects["invalid JSON"]), 0o600))
		service := &storedFlowDefinitionService{}
		_, _, err := executeFlowDefinitionDiff("flow-definition", "diff", directory, "--server", startStoredFlowDefinitionService(t, service))
		require.Equal(t, 1, ExitCode(err))
		require.Contains(t, err.Error(), "new definition file "+badPath)
		require.Empty(t, service.batches())
	})
}

func TestFlowDefinitionDiffReportsAddedAndRemovedFlowTypesFromFiles(t *testing.T) {
	managerApproval := loadFlowDefinitionFixture(t, managerApprovalFixture)
	subscription := loadFlowDefinitionFixture(t, subscriptionFixture)

	report, err := runFlowDefinitionDiff(t, writeFlowDefinitionFiles(t, managerApproval), "--old", writeFlowDefinitionFiles(t, subscription))

	require.Equal(t, 3, ExitCode(err))
	require.Empty(t, report.ComparedFlowTypes)
	codes := changeCodes(report)
	require.Equal(t, flowdefinitiondiff.SeverityBreaking, codes["flow_type_removed SubscriptionFlow"])
	require.Equal(t, flowdefinitiondiff.SeverityCompatible, codes["flow_type_added ManagerApprovalRefundFlow"])
}

func TestFlowDefinitionDiffReadsRepeatedOldPathsAndAnEmptyNewDirectory(t *testing.T) {
	managerApproval := loadFlowDefinitionFixture(t, managerApprovalFixture)
	subscription := loadFlowDefinitionFixture(t, subscriptionFixture)

	report, err := runFlowDefinitionDiff(t, writeFlowDefinitionFiles(t, managerApproval),
		"--old", writeFlowDefinitionFiles(t, managerApproval), "--old", writeFlowDefinitionFiles(t, subscription))
	require.Equal(t, 3, ExitCode(err))
	require.Equal(t, []string{"ManagerApprovalRefundFlow"}, report.ComparedFlowTypes)
	require.Equal(t, map[string]flowdefinitiondiff.Severity{"flow_type_removed SubscriptionFlow": flowdefinitiondiff.SeverityBreaking}, changeCodes(report))

	report, err = runFlowDefinitionDiff(t, t.TempDir(), "--old", writeFlowDefinitionFiles(t, managerApproval))
	require.Equal(t, 3, ExitCode(err))
	require.Equal(t, map[string]flowdefinitiondiff.Severity{"flow_type_removed ManagerApprovalRefundFlow": flowdefinitiondiff.SeverityBreaking}, changeCodes(report))
}

// storedFlowDefinitionService answers GetFlowDefinitions from a fixed map, leaving out unknown Flow types.
type storedFlowDefinitionService struct {
	dexpb.UnimplementedFlowServiceServer
	contents map[string][]byte
	// isBatchTooLarge answers ResourceExhausted to every request for more than one Flow type.
	isBatchTooLarge  bool
	mutex            sync.Mutex
	requestedBatches [][]string
}

func (service *storedFlowDefinitionService) GetFlowDefinitions(
	_ context.Context,
	request *dexpb.GetFlowDefinitionsRequest,
) (*dexpb.GetFlowDefinitionsResponse, error) {
	service.mutex.Lock()
	service.requestedBatches = append(service.requestedBatches, request.GetFlowTypes())
	service.mutex.Unlock()
	if service.isBatchTooLarge && len(request.GetFlowTypes()) > 1 {
		return nil, status.Error(codes.ResourceExhausted, "FLOW_DEFINITION_RESPONSE_TOO_LARGE")
	}
	response := &dexpb.GetFlowDefinitionsResponse{}
	for _, flowType := range request.GetFlowTypes() {
		if content, exists := service.contents[flowType]; exists {
			response.Definitions = append(response.Definitions, &dexpb.FlowDefinition{
				Info:    &dexpb.FlowDefinitionInfo{FlowType: flowType},
				Content: content,
			})
		}
	}
	return response, nil
}

func (service *storedFlowDefinitionService) batches() [][]string {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return append([][]string(nil), service.requestedBatches...)
}

func startStoredFlowDefinitionService(t *testing.T, service *storedFlowDefinitionService) string {
	t.Helper()
	return startGRPCServer(t, func(server *grpc.Server) { dexpb.RegisterFlowServiceServer(server, service) })
}

func TestFlowDefinitionDiffReadsOldDefinitionsFromServer(t *testing.T) {
	storedContent, err := os.ReadFile(managerApprovalFixture)
	require.NoError(t, err)
	service := &storedFlowDefinitionService{contents: map[string][]byte{
		"ManagerApprovalRefundFlow": storedContent,
		"UnrelatedFlow":             []byte(`{"schemaVersion":"2.0","valid":true,"flow":{"name":"UnrelatedFlow"}}`),
	}}
	address := startStoredFlowDefinitionService(t, service)
	managerApproval := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, managerApproval, "rpc", "ApproveRefund")
	newDirectory := writeFlowDefinitionFiles(t, managerApproval, loadFlowDefinitionFixture(t, subscriptionFixture))

	report, err := runFlowDefinitionDiff(t, newDirectory, "--server", address)

	require.Equal(t, 3, ExitCode(err))
	require.Equal(t, [][]string{{"ManagerApprovalRefundFlow", "SubscriptionFlow"}}, service.batches())
	require.Equal(t, []string{"ManagerApprovalRefundFlow"}, report.ComparedFlowTypes)
	require.Equal(t, map[string]flowdefinitiondiff.Severity{
		"rpc_removed ApproveRefund":         flowdefinitiondiff.SeverityBreaking,
		"baseline_missing SubscriptionFlow": flowdefinitiondiff.SeverityRisky,
	}, changeCodes(report))
}

func TestFlowDefinitionDiffPassesWithAPartialServerBaseline(t *testing.T) {
	storedContent, err := os.ReadFile(managerApprovalFixture)
	require.NoError(t, err)
	service := &storedFlowDefinitionService{contents: map[string][]byte{"ManagerApprovalRefundFlow": storedContent}}
	newDirectory := writeFlowDefinitionFiles(t, loadFlowDefinitionFixture(t, managerApprovalFixture), loadFlowDefinitionFixture(t, subscriptionFixture))

	report, err := runFlowDefinitionDiff(t, newDirectory, "--server", startStoredFlowDefinitionService(t, service))

	require.NoError(t, err)
	require.Equal(t, map[string]flowdefinitiondiff.Severity{"baseline_missing SubscriptionFlow": flowdefinitiondiff.SeverityRisky}, changeCodes(report))
}

func TestFlowDefinitionDiffFailsWhenTheServerHasNoBaseline(t *testing.T) {
	address := startStoredFlowDefinitionService(t, &storedFlowDefinitionService{})
	newDirectory := writeFlowDefinitionFiles(t, loadFlowDefinitionFixture(t, managerApprovalFixture))

	_, _, err := executeFlowDefinitionDiff("flow-definition", "diff", newDirectory, "--server", address)

	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, err.Error(), "no stored definition for any of the 1 Flow types")
}

func TestFlowDefinitionDiffSplitsServerRequests(t *testing.T) {
	minimalGraph := func(flowType string) []byte {
		return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","valid":true,"source":{"language":"go","path":"flow.go"},`+
			`"flow":{"name":%q},"nodes":[],"edges":[],"diagnostics":[]}`, flowType))
	}
	writeMinimalGraphs := func(t *testing.T, count int) (string, map[string][]byte) {
		directory, contents := t.TempDir(), make(map[string][]byte, count)
		for index := 0; index < count; index++ {
			flowType := fmt.Sprintf("Flow%03d", index)
			contents[flowType] = minimalGraph(flowType)
			require.NoError(t, os.WriteFile(filepath.Join(directory, flowType+".json"), contents[flowType], 0o600))
		}
		return directory, contents
	}

	t.Run("257 Flow types", func(t *testing.T) {
		directory, contents := writeMinimalGraphs(t, 257)
		service := &storedFlowDefinitionService{contents: contents}
		report, err := runFlowDefinitionDiff(t, directory, "--server", startStoredFlowDefinitionService(t, service))
		require.NoError(t, err)
		require.Len(t, report.ComparedFlowTypes, 257)
		batches := service.batches()
		require.Len(t, batches, 2)
		require.Len(t, batches[0], 256)
		require.Equal(t, []string{"Flow256"}, batches[1])
	})

	t.Run("response too large", func(t *testing.T) {
		directory, contents := writeMinimalGraphs(t, 2)
		service := &storedFlowDefinitionService{contents: contents, isBatchTooLarge: true}
		report, err := runFlowDefinitionDiff(t, directory, "--server", startStoredFlowDefinitionService(t, service))
		require.NoError(t, err)
		require.Equal(t, []string{"Flow000", "Flow001"}, report.ComparedFlowTypes)
		require.Equal(t, [][]string{{"Flow000", "Flow001"}, {"Flow000"}, {"Flow001"}}, service.batches())
	})
}

func TestFlowDefinitionDiffOutputFormats(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, newGraph, "step", "IssueRefund")
	oldDirectory := writeFlowDefinitionFiles(t, oldGraph)
	newDirectory := writeFlowDefinitionFiles(t, newGraph)

	t.Run("table by default", func(t *testing.T) {
		stdout, _, err := executeFlowDefinitionDiff("flow-definition", "diff", newDirectory, "--old", oldDirectory)
		require.Equal(t, 3, ExitCode(err))
		lines := strings.Split(stdout, "\n")
		require.Regexp(t, `^SEVERITY\s+FLOW TYPE\s+ELEMENT\s+NAME\s+CHANGE$`, lines[0])
		require.Regexp(t, `^breaking\s+ManagerApprovalRefundFlow\s+Step type\s+IssueRefund\s+Step type removed`, lines[1])
		require.Contains(t, stdout, "1 breaking, 0 risky, 0 compatible; compared 1 Flow types.")
	})
	for _, args := range [][]string{
		{"--output", "json", "flow-definition", "diff", newDirectory, "--old", oldDirectory},
		{"flow-definition", "diff", newDirectory, "--old", oldDirectory, "--output", "json"},
		{"flow-definition", "diff", newDirectory, "--old", oldDirectory, "--json", "--output", "json"},
	} {
		t.Run(strings.Join(args[:2], " ")+" json", func(t *testing.T) {
			stdout, _, err := executeFlowDefinitionDiff(args...)
			require.Equal(t, 3, ExitCode(err))
			var report flowDefinitionDiffReport
			require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
			require.Equal(t, 1, report.Summary.Breaking)
		})
	}
	t.Run("explicit table", func(t *testing.T) {
		stdout, _, err := executeFlowDefinitionDiff("flow-definition", "diff", newDirectory, "--old", oldDirectory, "--output", "table")
		require.Equal(t, 3, ExitCode(err))
		require.True(t, strings.HasPrefix(stdout, "SEVERITY"), stdout)
	})
	t.Run("json conflicts with root table", func(t *testing.T) {
		_, _, err := executeFlowDefinitionDiff("--output", "table", "flow-definition", "diff", newDirectory, "--old", oldDirectory, "--json")
		require.Equal(t, 2, ExitCode(err))
	})
	t.Run("json conflicts with command table", func(t *testing.T) {
		_, _, err := executeFlowDefinitionDiff("flow-definition", "diff", newDirectory, "--old", oldDirectory, "--json", "--output", "table")
		require.Equal(t, 2, ExitCode(err))
	})
	t.Run("help shows the table default", func(t *testing.T) {
		stdout, _, err := executeFlowDefinitionDiff("flow-definition", "diff", "--help")
		require.NoError(t, err)
		require.Contains(t, stdout, `json or table (default "table")`)
	})
}

func TestFlowDefinitionDiffRejectsMissingNewPaths(t *testing.T) {
	_, _, err := executeFlowDefinitionDiff("flow-definition", "diff", "--old", t.TempDir())
	require.Equal(t, 2, ExitCode(err))

	_, _, err = executeFlowDefinitionDiff("flow-definition", "diff", filepath.Join(t.TempDir(), "missing.json"), "--old", t.TempDir())
	require.Equal(t, 2, ExitCode(err))
}

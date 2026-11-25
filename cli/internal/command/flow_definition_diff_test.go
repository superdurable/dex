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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowdefinitiondiff"
	"github.com/superdurable/dex/cli/internal/flowviz"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
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

// runFlowDefinitionDiff runs the command with --json and returns the decoded report and the command error.
func runFlowDefinitionDiff(t *testing.T, args ...string) (flowDefinitionDiffReport, error) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	err := app.Execute(context.Background(), append([]string{"flow-definition", "diff", "--json"}, args...))
	var report flowDefinitionDiffReport
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report), "stdout: %s stderr: %s", stdout.String(), stderr.String())
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

func TestFlowDefinitionDiffReportsBreakingChangesAndExitsWithStatus3(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, newGraph, "step", "IssueRefund")
	removeNode(t, newGraph, "rpc", "RejectRefund")
	findNode(t, newGraph, "attribute", "refund-id").Resource.ValueType = "int64"
	findNode(t, newGraph, "attribute", "customer-message").Resource.Map = true
	removeNode(t, newGraph, "attribute", "customer-email")
	newGraph.V2.Start.StepType = "CheckOrder"
	findStartField(t, newGraph, "orderId").Schema = flowviz.StartInputSchema{Kind: "integer", Minimum: "0", Maximum: "10"}
	fields := newGraph.V2.Start.Input.Fields[:0]
	for _, field := range newGraph.V2.Start.Input.Fields {
		if field.Name != "reason" {
			fields = append(fields, field)
		}
	}
	newGraph.V2.Start.Input.Fields = fields
	findAction(t, newGraph, "ApproveRefund").Input.Fields[0].ValueType = "int64"
	findAction(t, newGraph, "RejectRefund").Input = flowviz.ActionInput{Kind: "none"}

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.Error(t, err)
	require.Equal(t, 3, ExitCode(err))
	require.Contains(t, err.Error(), "breaking changes")
	codes := changeCodes(report)
	for _, expected := range []string{
		"step_type_removed IssueRefund",
		"rpc_removed RejectRefund",
		"resource_type_changed refund-id",
		"resource_kind_changed customer-message",
		"resource_removed customer-email",
		"start_step_changed CheckOrder",
		"start_input_kind_changed orderId",
		"start_input_field_removed reason",
		"action_input_field_type_changed ApproveRefund.reason",
		"action_input_kind_changed RejectRefund",
	} {
		require.Equal(t, flowdefinitiondiff.SeverityBreaking, codes[expected], "%s in %v", expected, codes)
	}
	require.Equal(t, report.Summary.Breaking, len(report.Changes))
	require.Equal(t, flowdefinitiondiff.SeverityBreaking, report.Changes[0].Severity)

	_, err = diffGraphs(t, oldGraph, newGraph, "--allow-breaking")
	require.NoError(t, err)
}

func TestFlowDefinitionDiffReportsRiskyChangesWithoutFailing(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	oldGraph.V2.IndexedAttributes = []flowviz.IndexedAttribute{
		{AttributeKey: "case-status", IndexKey: "CaseStatus", IndexType: "keyword", ValueType: "string"},
		{AttributeKey: "refund-id", IndexKey: "RefundID", IndexType: "keyword", ValueType: "string"},
	}
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph.V2.IndexedAttributes = []flowviz.IndexedAttribute{
		{AttributeKey: "case-status", IndexKey: "CaseStatus", IndexType: "fulltext", ValueType: "string"},
	}
	findNode(t, newGraph, "step", "CheckOrder").Phase = "wait_for+execute"
	for index := range newGraph.Nodes {
		if newGraph.Nodes[index].Kind == "wait" {
			wait := newGraph.Nodes[index].Wait
			wait.Type = "anyOf"
			wait.Conditions = append(wait.Conditions, flowviz.WaitCondition{Kind: "timer", Label: "48h timer", Expression: "48 * time.Hour"})
		}
	}
	newGraph.V2.Start.Input.Fields = append(newGraph.V2.Start.Input.Fields,
		flowviz.StartInputField{Name: "region", Required: true, Schema: flowviz.StartInputSchema{Kind: "string"}})
	findStartField(t, newGraph, "paymentTimeoutsToSimulate").Required = true
	findStartField(t, newGraph, "paymentTimeoutsToSimulate").Schema.Minimum = "0"
	approve := findAction(t, newGraph, "ApproveRefund")
	approve.RequiredPermission = "refund.approve"
	approve.Input.Fields = append(approve.Input.Fields,
		flowviz.ActionInputField{FieldName: "ticket", ValueType: "string", Source: "user", Required: true})
	newGraph.V2.Summary.RPCName = "GetRefundSummary"

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.NoError(t, err)
	codes := changeCodes(report)
	for _, expected := range []string{
		"step_phase_changed CheckOrder",
		"step_wait_conditions_changed RequestManagerApproval",
		"start_input_required_field_added region",
		"start_input_field_now_required paymentTimeoutsToSimulate",
		"start_input_range_narrowed paymentTimeoutsToSimulate",
		"action_permission_changed ApproveRefund",
		"action_input_required_field_added ApproveRefund.ticket",
		"indexed_attribute_changed case-status",
		"indexed_attribute_removed refund-id",
		"summary_rpc_changed GetRefundSummary",
	} {
		require.Equal(t, flowdefinitiondiff.SeverityRisky, codes[expected], "%s in %v", expected, codes)
	}
	require.Zero(t, report.Summary.Breaking)
	require.Equal(t, report.Summary.Risky, len(report.Changes))
}

func TestFlowDefinitionDiffReportsPythonWaitConditionChange(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, subscriptionFixture)
	newGraph := loadFlowDefinitionFixture(t, subscriptionFixture)
	for index := range newGraph.Nodes {
		node := &newGraph.Nodes[index]
		if node.Kind == "wait" && node.ParentID == "step:Cancel" {
			node.Wait.Conditions[0].ResourceID = "resource:channel:SubscriptionFlow.update_charge_amount"
		}
	}

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.NoError(t, err)
	require.Len(t, report.Changes, 1)
	change := report.Changes[0]
	require.Equal(t, "step_wait_conditions_changed", change.Code)
	require.Equal(t, "Cancel", change.Name)
	require.Contains(t, change.Message, "channel cancel-subscription")
	require.Contains(t, change.Message, "channel update-charge-amount")
}

func TestFlowDefinitionDiffReportsAdditionsAsCompatible(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph.Nodes = append(newGraph.Nodes,
		flowviz.Node{ID: "step:Escalate", Kind: "step", Name: "Escalate", Phase: "execute"},
		flowviz.Node{ID: "rpc:Escalate", Kind: "rpc", Name: "Escalate"},
		flowviz.Node{ID: "resource:attribute:Escalated", Kind: "attribute", Name: "escalated", Resource: &flowviz.ResourceDetails{ValueType: "bool"}},
		flowviz.Node{ID: "resource:channel:Escalations", Kind: "channel", Name: "escalations", Resource: &flowviz.ResourceDetails{ValueType: "string"}},
	)
	newGraph.V2.Start.Input.Fields = append(newGraph.V2.Start.Input.Fields,
		flowviz.StartInputField{Name: "priority", Schema: flowviz.StartInputSchema{Kind: "string"}})
	newGraph.V2.Actions = append(newGraph.V2.Actions, flowviz.Action{
		RPCName: "Escalate", Label: "Escalate", RequiredPermission: "refund.manage", Input: flowviz.ActionInput{Kind: "none"},
	})
	findAction(t, newGraph, "ApproveRefund").Input.Fields = append(findAction(t, newGraph, "ApproveRefund").Input.Fields,
		flowviz.ActionInputField{FieldName: "note", ValueType: "string", Source: "user"})

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.NoError(t, err)
	codes := changeCodes(report)
	for _, expected := range []string{
		"step_type_added Escalate",
		"rpc_added Escalate",
		"resource_added escalated",
		"resource_added escalations",
		"start_input_field_added priority",
		"action_added Escalate",
		"action_input_field_added ApproveRefund.note",
	} {
		require.Equal(t, flowdefinitiondiff.SeverityCompatible, codes[expected], "%s in %v", expected, codes)
	}
	require.Equal(t, report.Summary.Compatible, len(report.Changes))
}

func TestFlowDefinitionDiffSkipsValueTypesWhenSourceLanguageChanges(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph.Source.Language = "python"
	findNode(t, newGraph, "attribute", "refund-id").Resource.ValueType = "str"

	report, err := diffGraphs(t, oldGraph, newGraph)

	require.NoError(t, err)
	require.Len(t, report.Changes, 1)
	require.Equal(t, "source_language_changed", report.Changes[0].Code)
	require.Equal(t, flowdefinitiondiff.SeverityRisky, report.Changes[0].Severity)
}

func TestFlowDefinitionDiffReportsAddedAndRemovedFlowTypesFromFiles(t *testing.T) {
	managerApproval := loadFlowDefinitionFixture(t, managerApprovalFixture)
	subscription := loadFlowDefinitionFixture(t, subscriptionFixture)
	oldDirectory := writeFlowDefinitionFiles(t, subscription)
	newDirectory := writeFlowDefinitionFiles(t, managerApproval)

	report, err := runFlowDefinitionDiff(t, newDirectory, "--old", oldDirectory)

	require.Equal(t, 3, ExitCode(err))
	require.Empty(t, report.ComparedFlowTypes)
	codes := changeCodes(report)
	require.Equal(t, flowdefinitiondiff.SeverityBreaking, codes["flow_type_removed SubscriptionFlow"])
	require.Equal(t, flowdefinitiondiff.SeverityCompatible, codes["flow_type_added ManagerApprovalRefundFlow"])
}

// storedFlowDefinitionService answers GetFlowDefinitions from a fixed map, leaving out unknown Flow types.
type storedFlowDefinitionService struct {
	dexpb.UnimplementedFlowServiceServer
	contents         map[string][]byte
	requestedBatches [][]string
}

func (service *storedFlowDefinitionService) GetFlowDefinitions(
	_ context.Context,
	request *dexpb.GetFlowDefinitionsRequest,
) (*dexpb.GetFlowDefinitionsResponse, error) {
	service.requestedBatches = append(service.requestedBatches, request.GetFlowTypes())
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

func TestFlowDefinitionDiffReadsOldDefinitionsFromServer(t *testing.T) {
	storedContent, err := os.ReadFile(managerApprovalFixture)
	require.NoError(t, err)
	service := &storedFlowDefinitionService{contents: map[string][]byte{
		"ManagerApprovalRefundFlow": storedContent,
		"UnrelatedFlow":             []byte(`{"schemaVersion":"2.0","flow":{"name":"UnrelatedFlow"}}`),
	}}
	address := startGRPCServer(t, func(server *grpc.Server) { dexpb.RegisterFlowServiceServer(server, service) })
	managerApproval := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, managerApproval, "rpc", "ApproveRefund")
	newDirectory := writeFlowDefinitionFiles(t, managerApproval, loadFlowDefinitionFixture(t, subscriptionFixture))

	report, err := runFlowDefinitionDiff(t, newDirectory, "--server", address)

	require.Equal(t, 3, ExitCode(err))
	require.Equal(t, [][]string{{"ManagerApprovalRefundFlow", "SubscriptionFlow"}}, service.requestedBatches)
	require.Equal(t, []string{"ManagerApprovalRefundFlow"}, report.ComparedFlowTypes)
	codes := changeCodes(report)
	require.Len(t, codes, 2)
	require.Equal(t, flowdefinitiondiff.SeverityBreaking, codes["rpc_removed ApproveRefund"])
	require.Equal(t, flowdefinitiondiff.SeverityCompatible, codes["flow_type_added SubscriptionFlow"])
}

func TestFlowDefinitionDiffWritesTableByDefault(t *testing.T) {
	oldGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	newGraph := loadFlowDefinitionFixture(t, managerApprovalFixture)
	removeNode(t, newGraph, "step", "IssueRefund")
	oldDirectory := writeFlowDefinitionFiles(t, oldGraph)
	newDirectory := writeFlowDefinitionFiles(t, newGraph)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	err := NewApp(bytes.NewReader(nil), stdout, stderr).Execute(context.Background(),
		[]string{"flow-definition", "diff", newDirectory, "--old", oldDirectory})

	require.Equal(t, 3, ExitCode(err))
	lines := strings.Split(stdout.String(), "\n")
	require.Regexp(t, `^SEVERITY\s+FLOW TYPE\s+ELEMENT\s+NAME\s+CHANGE$`, lines[0])
	require.Regexp(t, `^breaking\s+ManagerApprovalRefundFlow\s+Step type\s+IssueRefund\s+Step type removed`, lines[1])
	require.Contains(t, stdout.String(), "1 breaking, 0 risky, 0 compatible; compared 1 Flow types.")
}

func TestFlowDefinitionDiffRejectsMissingNewPaths(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	err := NewApp(bytes.NewReader(nil), stdout, stderr).Execute(context.Background(),
		[]string{"flow-definition", "diff", "--old", t.TempDir()})
	require.Equal(t, 2, ExitCode(err))

	err = NewApp(bytes.NewReader(nil), stdout, stderr).Execute(context.Background(),
		[]string{"flow-definition", "diff", filepath.Join(t.TempDir(), "missing.json"), "--old", t.TempDir()})
	require.Equal(t, 2, ExitCode(err))
}

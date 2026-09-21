// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeV2RefundFlows(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	tests := []struct {
		name               string
		source             string
		wantFlowType       string
		wantGroupIDs       []string
		wantSummaryFields  []string
		wantActionRPCNames []string
		wantIndexTypes     map[string]string
	}{
		{
			name: "deterministic",
			source: filepath.Join(repositoryRoot,
				"examples/go/products/customer-refund/deterministic/workflow.go"),
			wantFlowType:       "CustomerRefundFlow",
			wantGroupIDs:       []string{"intake", "evidence", "control", "resolution", "failure", "close"},
			wantSummaryFields:  []string{"charge-reference", "refund-amount", "recommended-action"},
			wantActionRPCNames: []string{},
			wantIndexTypes:     map[string]string{"case-status": "keyword"},
		},
		{
			name: "agentic",
			source: filepath.Join(repositoryRoot,
				"examples/go/products/customer-refund/agentic/workflow.go"),
			wantFlowType:      "AgenticCustomerRefundFlow",
			wantGroupIDs:      []string{"intake", "reasoning", "evidence", "control", "resolution", "failure", "close"},
			wantSummaryFields: []string{"in-charge-ref", "ev-payment-amount", "recommended-action", "guardrail-rule"},
			wantActionRPCNames: []string{
				"ApproveRefund", "RejectRefund", "ConfirmCustomerMessage", "EditCustomerMessage",
			},
			wantIndexTypes: map[string]string{
				"case-status":    "keyword",
				"customer-email": "fulltext",
				"refund-amount":  "double",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), test.source, flowviz.AnalyzeOptions{
				SchemaVersion: flowviz.SchemaVersionV2,
			})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Equal(t, flowviz.SchemaVersionV2, graph.SchemaVersion)
			require.Equal(t, test.wantFlowType, graph.Flow.Name)
			require.NotNil(t, graph.V2)
			require.Equal(t, test.wantGroupIDs, v2GroupIDs(graph.Groups))
			require.Equal(t, test.wantSummaryFields, v2ViewFieldKeys(graph.V2.Summary.Fields))
			require.Equal(t, test.wantActionRPCNames, v2ActionRPCNames(graph.V2.Actions))
			// Keyed rather than positional: declaring another Indexed Attribute must not move this.
			indexTypes := make(map[string]string, len(graph.V2.IndexedAttributes))
			for _, attribute := range graph.V2.IndexedAttributes {
				require.Equal(t, attribute.AttributeKey, attribute.IndexKey)
				indexTypes[attribute.AttributeKey] = attribute.IndexType
			}
			require.Equal(t, test.wantIndexTypes, indexTypes)
			for _, node := range graph.Nodes {
				if node.Kind != "step" {
					continue
				}
				require.NotNil(t, node.Metadata, node.ID)
				explanation, ok := node.Metadata["explanation"].(string)
				require.True(t, ok, node.ID)
				require.NotEmpty(t, explanation, node.ID)
			}

			firstJSON, err := flowviz.MarshalJSON(graph)
			require.NoError(t, err)
			secondGraph, err := flowviz.Analyze(context.Background(), test.source, flowviz.AnalyzeOptions{
				SchemaVersion: flowviz.SchemaVersionV2,
			})
			require.NoError(t, err)
			secondJSON, err := flowviz.MarshalJSON(secondGraph)
			require.NoError(t, err)
			require.Equal(t, firstJSON, secondJSON)
			require.NotContains(t, string(firstJSON), `"order"`)
		})
	}
}

func TestVisualizeV2PreservesDirectiveLineOrderWithoutParameterOrder(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	source := filepath.Join(repositoryRoot, "examples/go/products/customer-refund/agentic/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{
		SchemaVersion: flowviz.SchemaVersionV2,
	})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)

	require.Equal(t, "intake", graph.Groups[0].ID)
	require.Equal(t, "Intake", graph.Groups[0].Label)
	require.Equal(t, []string{"reason", "gateRequestKey"}, v2ActionInputNames(graph.V2.Actions[1].Input.Fields))
	require.Equal(t, []string{"user", "attribute"}, v2ActionInputSources(graph.V2.Actions[1].Input.Fields))
}

func TestVisualizeV2RejectsPython(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	_, err := flowviz.Analyze(
		context.Background(),
		filepath.Join(repositoryRoot, "examples/python/dex_examples/patterns/recovery/failure_recovery_flow.py"),
		flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2},
	)
	require.EqualError(t, err, "schema version 2.0 supports Go source only")
}

func TestVisualizeV2ReportsMalformedNamedDirectives(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	graph, err := flowviz.Analyze(
		context.Background(),
		filepath.Join(repositoryRoot, "cli/internal/command/testfixtures/visualization-v2-invalid/workflow.go"),
		flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV2},
	)
	require.NoError(t, err)
	require.False(t, graph.Valid)
	messages := make([]string, 0, len(graph.Diagnostics))
	for _, diagnostic := range graph.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	require.Contains(t, messages, "dex:group unknown argument unexpected")
	require.Contains(t, messages, "dex:indexed-attribute repeats argument attribute-key")
	require.Contains(t, messages, "dex:field missing required argument description")
	require.Contains(t, messages, "dex:field description: unterminated quoted string")
	require.Contains(t, messages, "Step missingGroupStep must declare exactly one dex:group directive")
	require.Contains(t, messages, "Step missingGroupStep must declare exactly one dex:explanation directive")
	require.Contains(t, messages, "Step invalidV2Step must declare exactly one dex:explanation directive")
	require.Contains(t, messages, `dex:indexed-attribute attribute-key "declared-indexed" does not match the Go declaration "actual-indexed"`)
	require.Contains(t, messages, `dex:field value-type "string" does not match Attribute "flag" type "bool"`)
	require.Contains(t, messages, "GetDexDisplay must be read-only")
	require.Contains(t, messages, `dex:input input field "missing" is not in the RPC input struct`)
	require.Contains(t, messages, "Action RPC UnregisteredAction must be registered in GetRPCs")
	encoded, err := flowviz.MarshalJSON(graph)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"groups": []`)
	require.Contains(t, string(encoded), `"v2": {`)
}

func v2GroupIDs(groups []flowviz.StepGroup) []string {
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	return ids
}

func v2ViewFieldKeys(fields []flowviz.ViewField) []string {
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.AttributeKey)
	}
	return keys
}

func v2ActionRPCNames(actions []flowviz.Action) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, action.RPCName)
	}
	return names
}

func v2ActionInputNames(fields []flowviz.ActionInputField) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.FieldName)
	}
	return names
}

func v2ActionInputSources(fields []flowviz.ActionInputField) []string {
	sources := make([]string, 0, len(fields))
	for _, field := range fields {
		sources = append(sources, field.Source)
	}
	return sources
}

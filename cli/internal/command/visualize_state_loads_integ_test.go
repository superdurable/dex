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
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeReportsUnloadedStateReads(t *testing.T) {
	source := stateLoadsFixture(t, "unloaded/workflow.go")
	for _, lint := range []string{"", flowviz.LintApplication} {
		t.Run("lint="+lint, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{Lint: lint})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Equal(t, []string{
				"attribute_map_enumeration_not_loaded",
				"attribute_map_instance_not_loaded",
				"attribute_map_read_not_loaded",
				"attribute_map_read_not_loaded",
				"attribute_map_read_not_loaded",
				"channel_messages_not_loaded",
				"channel_messages_not_loaded",
				"invocation_load_directive",
			}, sortedDiagnosticCodes(graph.Diagnostics))

			enumeration := diagnosticWithCode(t, graph, "attribute_map_enumeration_not_loaded", "snapshotRecipients")
			require.Contains(t, enumeration.Message, "Add ExecuteLoadAttributeMaps: []dex.AttributeDef{Subscribers}")
			require.Contains(t, enumeration.Message, "exact-instance loads cannot enumerate")
			require.Equal(t, sourceLine(t, source, "Subscribers.AllInstanceKeys(ctx)"), enumeration.Span.StartLine)

			helperRead := diagnosticWithCode(t, graph, "attribute_map_read_not_loaded", "notifyRecipients")
			require.Contains(t, helperRead.Message, `reads AttributeMap "subscribers" (Get in deliveryAddress at delivery_address.go:`)
			require.Contains(t, helperRead.Message, "Add ExecuteLoadAttributeMapInstances: []dex.AttributeMapLoad{Subscribers.Load(key)}")
			require.Equal(t, sourceLine(t, source, "deliveryAddress(ctx, Subscribers, recipient)"), helperRead.Span.StartLine)

			rpcRead := diagnosticWithCode(t, graph, "attribute_map_read_not_loaded", "RPC GetSubscriber")
			require.Contains(t, rpcRead.Message, "to its dex.RPCOptions")
			require.Contains(t, rpcRead.Message, "// dex:invocation-load attribute-map:subscribers")

			instance := diagnosticWithCode(t, graph, "attribute_map_instance_not_loaded", "RPC GetLocale")
			require.Equal(t, `RPC GetLocale reads instance "locale" of "settings" (Get) but loads only "theme".`, instance.Message)

			channel := diagnosticWithCode(t, graph, "channel_messages_not_loaded", "RPC ListDecisions")
			require.Contains(t, channel.Message, `pending messages of Channel "decisions" (PendingMessages)`)
			require.Contains(t, channel.Message, "Add LoadChannels: []dex.ChannelDef{Decisions}")

			channelMap := diagnosticWithCode(t, graph, "channel_messages_not_loaded", "awaitReply WaitFor")
			require.Contains(t, channelMap.Message, `pending messages of ChannelMap "replies" (FindPendingMessage)`)
			require.Contains(t, channelMap.Message, "Add WaitForLoadChannelMapInstances: []dex.ChannelMapLoad{Replies.LoadMessages(key)}")

			directive := diagnosticWithCode(t, graph, "invocation_load_directive", "GetContact")
			require.Equal(t, sourceLine(t, source, "// dex:invocation-load attribute-map:contacts"), directive.Span.StartLine)
		})
	}
}

func TestVisualizeWritesPartialJSONForUnloadedStateReads(t *testing.T) {
	outputPrefix := filepath.Join(t.TempDir(), "unloaded")
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{
		"visualize", stateLoadsFixture(t, "unloaded/workflow.go"), "--json", "--out", outputPrefix,
	})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	data, readErr := os.ReadFile(outputPrefix + ".json")
	require.NoError(t, readErr)
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(data, &graph))
	require.False(t, graph.Valid)
	require.Contains(t, diagnosticCodes(graph.Diagnostics), "attribute_map_read_not_loaded")
}

func TestVisualizeFollowsHelpersAndLoopBodies(t *testing.T) {
	source := stateLoadsFixture(t, "loaded/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	helperWrite := graphEdge(t, graph, "resource_write", "step:deliverMessages", "resource:attribute:ReleaseIDs", "Set")
	require.Equal(t, "helper", helperWrite.Metadata["via"])
	require.Equal(t, "recordRelease", helperWrite.Metadata["function"])
	require.Equal(t, sourceLine(t, source, "recordRelease(ctx, ReleaseIDs, recipient)"), helperWrite.Span.StartLine)
	require.True(t, hasEdge(graph.Edges, "resource_read", "resource:channel:Replies", "step:deliverMessages"))
	require.True(t, hasEdge(graph.Edges, "resource_read", "resource:channel:Retries", "step:drainRetries"))

	pollDecisions := make([]string, 0)
	for _, node := range nodesOfKind(graph.Nodes, "decision") {
		if node.ParentID == "step:pollRetries" {
			pollDecisions = append(pollDecisions, node.Name)
		}
	}
	sort.Strings(pollDecisions)
	require.Equal(t, []string{"goTo", "gracefulComplete"}, pollDecisions)
}

func TestVisualizeAcceptsDeclaredStateLoads(t *testing.T) {
	source := stateLoadsFixture(t, "loaded/workflow.go")
	for _, lint := range []string{"", flowviz.LintApplication} {
		t.Run("lint="+lint, func(t *testing.T) {
			graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{Lint: lint})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
		})
	}
}

func TestVisualizeReportsUnresolvedStateLoadsOnlyWithApplicationLints(t *testing.T) {
	source := stateLoadsFixture(t, "unresolved/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	graph, err = flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{Lint: flowviz.LintApplication})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Equal(t, []string{"state_load_unresolved", "state_load_unresolved", "state_load_unresolved"}, sortedDiagnosticCodes(graph.Diagnostics))
	step := diagnosticWithCode(t, graph, "state_load_unresolved", "Step listSubscribers Execute")
	require.Contains(t, step.Message, "(sharedoptions.ExecuteWholeMaps(Subscribers) at workflow.go:")
	rpc := diagnosticWithCode(t, graph, "state_load_unresolved", "RPC GetSubscriber")
	require.Contains(t, rpc.Message, "(sharedoptions.RPCWholeMaps(Subscribers) at workflow.go:")
	timeout := diagnosticWithCode(t, graph, "state_load_unresolved", "HandleTimeout")
	require.Contains(t, timeout.Message, "without dex.FlowTimeoutHandlerOptions in this package")
}

func TestVisualizeApplicationLints(t *testing.T) {
	source := stateLoadsFixture(t, "lints/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	graph, err = flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{Lint: flowviz.LintApplication})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Equal(t, []string{
		"action_state_not_rechecked",
		"cross_flow_read_not_found_unhandled",
		"start_flow_request_id_missing",
	}, sortedDiagnosticCodes(graph.Diagnostics))

	action := diagnosticWithCode(t, graph, "action_state_not_rechecked", "Action ApproveDraft")
	require.Contains(t, action.Message, `is enabled when "draft-status" matches, but its handler never reads "draft-status"`)

	startFlow := diagnosticWithCode(t, graph, "start_flow_request_id_missing", "SubscriberRegistryFlow")
	require.Contains(t, startFlow.Message, "(in ensureRegistry at registry.go:")
	require.Equal(t, sourceLine(t, source, "ensureRegistry(context.Background(), step.client, registryID)"), startFlow.Span.StartLine)

	crossFlow := diagnosticWithCode(t, graph, "cross_flow_read_not_found_unhandled", "snapshotRecipients")
	require.Contains(t, crossFlow.Message, "reads SubscriberRegistryFlow.ListSubscribers but does not handle a missing Flow")
}

func TestVisualizeReportsMergedConnectorBranchesOnlyWithApplicationLints(t *testing.T) {
	source := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/connector-factory/mergedbranches/workflow.go")
	graph, err := flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)

	graph, err = flowviz.Analyze(context.Background(), source, flowviz.AnalyzeOptions{Lint: flowviz.LintApplication})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Equal(t, []string{"connector_branches_merged_unread"}, sortedDiagnosticCodes(graph.Diagnostics))
	merged := diagnosticWithCode(t, graph, "connector_branches_merged_unread", "saveSummary")
	require.Equal(
		t,
		"saveSummary receives branches completed, defect of GenerateSummary but never reads result.Branch; failure branches would be handled as success. Switch on result.Branch or route failures to their own Step.",
		merged.Message,
	)
	require.Equal(t, sourceLine(t, source, "func (saveSummary) Execute("), merged.Span.StartLine)
}

func TestVisualizeValidatesLintOption(t *testing.T) {
	app := NewApp(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{"visualize", stateLoadsFixture(t, "loaded/workflow.go"), "--json", "--lint", "strict"})
	require.Error(t, err)
	require.Equal(t, 2, ExitCode(err))

	pythonSource := filepath.Join(t.TempDir(), "flow.py")
	require.NoError(t, os.WriteFile(pythonSource, []byte(minimalPythonFlow), 0o644))
	err = app.Execute(context.Background(), []string{"visualize", pythonSource, "--json", "--lint", flowviz.LintApplication})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, err.Error(), "--lint app supports Go source only")
}

func stateLoadsFixture(t *testing.T, relativePath string) string {
	t.Helper()
	return filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/visualization-state-loads", relativePath)
}

func sortedDiagnosticCodes(diagnostics []flowviz.Diagnostic) []string {
	codes := diagnosticCodes(diagnostics)
	sort.Strings(codes)
	return codes
}

func diagnosticWithCode(t *testing.T, graph *flowviz.Graph, code string, messageFragment string) flowviz.Diagnostic {
	t.Helper()
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, messageFragment) {
			require.NotNil(t, diagnostic.Span, diagnostic.Message)
			return diagnostic
		}
	}
	require.Failf(t, "diagnostic not found", "%s containing %q in %+v", code, messageFragment, graph.Diagnostics)
	return flowviz.Diagnostic{}
}

func sourceLine(t *testing.T, path string, fragment string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for index, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, fragment) {
			return index + 1
		}
	}
	require.Failf(t, "source fragment not found", "%q in %s", fragment, path)
	return 0
}

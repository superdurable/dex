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
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeJavaMetadataParity(t *testing.T) {
	javaPath := javaVisualizationExecutable(t)
	fixture := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/v2-metadata-parity-java")
	goGraph, err := flowviz.Analyze(t.Context(), filepath.Join(fixture, "workflow.go"), flowviz.AnalyzeOptions{FlowType: "OrderFlow"})
	require.NoError(t, err)
	require.True(t, goGraph.Valid, "%+v", goGraph.Diagnostics)
	javaGraph, err := flowviz.Analyze(t.Context(), filepath.Join(fixture, "workflow.java"), flowviz.AnalyzeOptions{JavaPath: javaPath})
	require.NoError(t, err)
	require.True(t, javaGraph.Valid, "%+v", javaGraph.Diagnostics)
	require.NotEmpty(t, goGraph.V2.IndexedAttributes)
	require.NotEmpty(t, goGraph.V2.Summary.Fields)
	require.NotEmpty(t, goGraph.V2.Display.Fields)
	require.NotNil(t, goGraph.V2.Start)
	require.Equal(t, goGraph.V2.IndexedAttributes, javaGraph.V2.IndexedAttributes)
	require.Equal(t, goGraph.V2.Summary, javaGraph.V2.Summary)
	require.Equal(t, goGraph.V2.Display, javaGraph.V2.Display)
	require.Equal(t, goGraph.V2.Start, javaGraph.V2.Start)
	goShape := projectWorkflowPattern(goGraph)
	javaShape := projectWorkflowPattern(javaGraph)
	require.Equal(t, map[string]bool{
		"transition Begin -> Finish when ready": true,
	}, goShape.conditionalEdges)
	require.Equal(t, goShape.conditionalEdges, javaShape.conditionalEdges)
	require.Equal(t, map[string]bool{
		"resource_lock step:Begin -> resource:attribute:state (wait_for)":     true,
		"resource_lock step:Begin -> resource:attribute:statusText (execute)": true,
	}, goShape.lockEdges)
	require.Equal(t, goShape.lockEdges, javaShape.lockEdges)
	require.Contains(t, goShape.accessEdges, "resource_read resource:attribute:state -> step:Begin (execute)")
	require.Contains(t, goShape.accessEdges, "resource_write step:Begin -> resource:attribute:statusText (execute)")
	require.Equal(t, goShape.accessEdges, javaShape.accessEdges)
}

func TestVisualizeJavaExecutableSelection(t *testing.T) {
	source := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/flowviz/testdata/java/workflow.java")
	missing := filepath.Join(t.TempDir(), "no-java")
	t.Setenv("DEX_JAVA", missing)
	for _, arguments := range [][]string{nil, {"--java", missing}} {
		var output bytes.Buffer
		app := NewApp(strings.NewReader(""), &output, &bytes.Buffer{})
		err := app.Execute(t.Context(), append([]string{"visualize", source, "--language", "java", "--json"}, arguments...))
		require.Error(t, err)
		require.Equal(t, 1, ExitCode(err))
		var graph flowviz.Graph
		require.NoError(t, json.Unmarshal(output.Bytes(), &graph))
		require.False(t, graph.Valid)
		require.Len(t, graph.Diagnostics, 1)
		require.Equal(t, "java_analyzer_failed", graph.Diagnostics[0].Code)
	}
	t.Run("flag overrides DEX_JAVA", func(t *testing.T) {
		javaPath, err := exec.LookPath("java")
		if err != nil {
			t.Skip("Java visualization tests require JDK 17+ java")
		}
		var output bytes.Buffer
		app := NewApp(strings.NewReader(""), &output, &bytes.Buffer{})
		require.NoError(t, app.Execute(t.Context(), []string{"visualize", source, "--java", javaPath, "--json"}))
		var graph flowviz.Graph
		require.NoError(t, json.Unmarshal(output.Bytes(), &graph))
		require.True(t, graph.Valid)
	})
}

func TestVisualizeJavaWorkflowPatternParity(t *testing.T) {
	javaPath := javaVisualizationExecutable(t)
	repository := visualizerRepositoryRoot(t)
	examples := filepath.Join(repository, "examples")
	mapping, err := os.ReadFile(filepath.Join(examples, "workflow-patterns-mapping.md"))
	require.NoError(t, err)
	patternRows := regexp.MustCompile("(?m)^\\| WCP-[0-9]+ \\|.*?\\| (\\w+Flow)(?: \\([^|]*\\))? \\|$")
	goSources, err := filepath.Glob(filepath.Join(examples, "go/patterns/workflow-patterns/*.go"))
	require.NoError(t, err)
	goFlowFiles := make(map[string]string)
	flowDeclarations := regexp.MustCompile(`(?m)^type (\w+Flow) struct`)
	for _, source := range goSources {
		data, err := os.ReadFile(source)
		require.NoError(t, err)
		for _, declaration := range flowDeclarations.FindAllSubmatch(data, -1) {
			goFlowFiles[string(declaration[1])] = source
		}
	}
	rows := patternRows.FindAllSubmatch(mapping, -1)
	require.Len(t, rows, 43)
	seen := make(map[string]bool)
	for _, row := range rows {
		flowType := string(row[1])
		if seen[flowType] {
			continue
		}
		seen[flowType] = true
		t.Run(flowType, func(t *testing.T) {
			t.Parallel()
			goSource := goFlowFiles[flowType]
			require.NotEmpty(t, goSource)
			javaSource := filepath.Join(examples, "java/src/main/java/io/superdurable/dex/patterns/workflowpatterns", flowType+".java")
			goGraph, err := analyzeParallelVisualization(t, goSource, flowviz.AnalyzeOptions{FlowType: flowType})
			require.NoError(t, err)
			javaGraph, err := analyzeParallelVisualization(t, javaSource, flowviz.AnalyzeOptions{FlowType: flowType, JavaPath: javaPath})
			require.NoError(t, err)
			require.Equal(t, flowType, goGraph.Flow.Name)
			require.Equal(t, flowType, javaGraph.Flow.Name)
			goShape := projectWorkflowPattern(goGraph)
			javaShape := projectWorkflowPattern(javaGraph)
			require.Equal(t, goShape.steps, javaShape.steps)
			if flowType == "RunTimeMultipleInstancesFlow" {
				// Java shares Steps and the decision helper with DesignTimeMultipleInstancesFlow.java.
				require.Contains(t, diagnosticCodes(javaGraph.Diagnostics), "hidden_dex_decision")
				require.Contains(t, diagnosticCodes(javaGraph.Diagnostics), "step_type_unresolved")
				return
			}
			require.Equal(t, goShape.resources, javaShape.resources, "resources")
			for _, edge := range workflowPatternEdgesMissingFromGo(flowType) {
				require.NotContains(t, goShape.edges, edge)
				require.Contains(t, javaShape.edges, edge)
				delete(javaShape.edges, edge)
			}
			require.Equal(t, goShape.edges, javaShape.edges, "control edges")
			require.Equal(t, goShape.waits, javaShape.waits, "Wait condition kinds")
			require.Equal(t, goShape.lockEdges, javaShape.lockEdges, "lock edges")
			javaConditions, javaAccesses := additionalJavaWorkflowPatternEdges(flowType)
			requireWorkflowPatternEdgeParity(t, goShape.accessEdges, javaShape.accessEdges, javaAccesses, "resource access edges")
			requireWorkflowPatternEdgeParity(t, workflowPatternConditionalTargets(goShape),
				workflowPatternConditionalTargets(javaShape), javaConditions, "conditional control edges")
		})
	}
}

func additionalJavaWorkflowPatternEdges(flowType string) (conditional, access []string) {
	// Some Go join decisions flatten their movements, and some queued-message RPC reads are only visible in Java.
	switch flowType {
	case "BlockingPartialJoinFlow":
		return []string{"transition BlockingJoin -> ProcessRound", "transition BlockingJoin -> StartBlockingRound"}, []string{
			"resource_read resource:channel:branchASignal -> rpc:getqueuedbranchsignalcount (rpc)",
			"resource_read resource:channel:branchBSignal -> rpc:getqueuedbranchsignalcount (rpc)",
			"resource_read resource:channel:branchCSignal -> rpc:getqueuedbranchsignalcount (rpc)",
		}
	case "CancellingPartialJoinFlow":
		return nil, []string{
			"resource_read resource:channel:carrierAReleased -> rpc:getqueuedcarrierreleasecount (rpc)",
			"resource_read resource:channel:carrierBReleased -> rpc:getqueuedcarrierreleasecount (rpc)",
			"resource_read resource:channel:carrierCReleased -> rpc:getqueuedcarrierreleasecount (rpc)",
		}
	case "CancellingPartialJoinForMultipleInstancesFlow":
		return nil, []string{"resource_read resource:channel:quorumInspectionReleased -> rpc:getqueuedquorumreleasecount (rpc)"}
	case "StaticPartialJoinForMultipleInstancesFlow":
		return []string{"transition AcceptInspections -> ApproveBatch", "transition AcceptInspections -> StartInspections"}, nil
	case "StructuredPartialJoinFlow":
		return []string{"transition AcceptQuotes -> PlaceOrder", "transition AcceptQuotes -> RequestQuotes"}, nil
	case "GeneralizedAndJoinFlow":
		return []string{"transition MatchInvoiceAndPayment -> MatchInvoiceAndPayment", "transition MatchInvoiceAndPayment -> RecordMatch"}, nil
	default:
		return nil, nil
	}
}

func requireWorkflowPatternEdgeParity(t *testing.T, goEdges, javaEdges map[string]bool, javaOnly []string, description string) {
	t.Helper()
	for _, edge := range javaOnly {
		require.NotContains(t, goEdges, edge)
		require.Contains(t, javaEdges, edge)
		delete(javaEdges, edge)
	}
	require.Equal(t, goEdges, javaEdges, description)
}

func workflowPatternEdgesMissingFromGo(flowType string) []string {
	// The Go analyzer misses movements returned via a helper or chained to a local RPCResult.
	switch flowType {
	case "DesignTimeMultipleInstancesFlow":
		return []string{"transition StartDesignTimeReviews -> CollectReviews", "transition StartDesignTimeReviews -> ReviewDocument"}
	case "CancelTaskFlow":
		return []string{"transition RPC -> ArrangeSelfDelivery"}
	case "CancelRegionFlow":
		return []string{"transition RPC -> NotifyRejection"}
	case "CancelMultipleInstanceTaskFlow":
		return []string{"transition RPC -> ReportTranslations"}
	case "CompleteMultipleInstanceTaskFlow":
		return []string{"transition RPC -> PublishReviewResults"}
	default:
		return nil
	}
}

type workflowPatternProjection struct {
	steps            map[string]bool
	resources        map[string]bool
	edges            map[string]bool
	conditionalEdges map[string]bool
	lockEdges        map[string]bool
	accessEdges      map[string]bool
	waits            map[string]bool
}

func projectWorkflowPattern(graph *flowviz.Graph) workflowPatternProjection {
	shape := workflowPatternProjection{
		steps: make(map[string]bool), resources: make(map[string]bool),
		edges: make(map[string]bool), waits: make(map[string]bool),
		conditionalEdges: make(map[string]bool), lockEdges: make(map[string]bool), accessEdges: make(map[string]bool),
	}
	nodes := make(map[string]flowviz.Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
		switch node.Kind {
		case "step":
			shape.steps[node.Name] = true
		case "attribute", "channel", "stream":
			shape.resources[node.Kind+" "+node.Name] = true
		case "wait":
			for _, condition := range node.Wait.Conditions {
				shape.waits[node.ParentID+" "+condition.Kind] = true
			}
		}
	}
	for _, edge := range graph.Edges {
		switch edge.Kind {
		case "transition", "cancel", "failure_transition", "subflow":
			from, to := nodes[edge.From], nodes[edge.To]
			condition := edge.Condition
			if condition == "" && (graph.Source.Language == "go" || edge.Kind == "subflow") {
				condition = from.Condition
			}
			if from.ParentID != "" {
				from = nodes[from.ParentID]
			}
			if from.Kind == "rpc" {
				from.Name = "RPC"
			}
			if to.Kind == "step" || to.Kind == "subflow" {
				key := edge.Kind + " " + from.Name + " -> " + to.Name
				shape.edges[key] = true
				if condition != "" {
					shape.conditionalEdges[key+" when "+condition] = true
				}
			}
		case "resource_lock":
			shape.lockEdges[workflowPatternResourceEdge(edge, nodes)] = true
		case "resource_read", "resource_write", "resource_publish":
			shape.accessEdges[workflowPatternResourceEdge(edge, nodes)] = true
		}
	}
	return shape
}

func workflowPatternConditionalTargets(shape workflowPatternProjection) map[string]bool {
	// Example conditions use host-language expressions. The paired fixture checks the exact condition text.
	targets := make(map[string]bool)
	for edge := range shape.conditionalEdges {
		target, _, _ := strings.Cut(edge, " when ")
		targets[target] = true
	}
	return targets
}

func workflowPatternResourceEdge(edge flowviz.Edge, nodes map[string]flowviz.Node) string {
	phase, _ := edge.Metadata["phase"].(string)
	return edge.Kind + " " + workflowPatternEndpoint(nodes[edge.From]) + " -> " + workflowPatternEndpoint(nodes[edge.To]) + " (" + phase + ")"
}

func workflowPatternEndpoint(node flowviz.Node) string {
	switch node.Kind {
	case "attribute", "channel", "stream":
		return "resource:" + node.Kind + ":" + node.Name
	case "rpc":
		return "rpc:" + strings.ToLower(node.Name)
	default:
		return node.ID
	}
}

func javaVisualizationFlowTypes(graph *flowviz.Graph) []string {
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "multiple_flows" {
			if _, names, ok := strings.Cut(diagnostic.Message, "use --flow with one of: "); ok {
				return strings.Split(names, ", ")
			}
		}
	}
	return nil
}

func requireJavaExampleGraph(t *testing.T, source string, graph *flowviz.Graph) {
	t.Helper()
	require.Equal(t, "2.0", graph.SchemaVersion)
	require.NotEmpty(t, graph.Flow.Name)
	for _, diagnostic := range diagnosticsWithoutMissingVisualizationMetadata(t, source, graph.Diagnostics) {
		if graph.Flow.Name == "RunTimeMultipleInstancesFlow" && diagnostic.Code == "hidden_dex_decision" {
			continue
		}
		require.NotEqual(t, "error", diagnostic.Severity, "%s: %s", diagnostic.Code, diagnostic.Message)
	}
	requireExampleGraphLintWarnings(t, graph, source)
}

func javaVisualizationExecutable(t *testing.T) string {
	t.Helper()
	path := os.Getenv("DEX_JAVA")
	if path == "" {
		path = "java"
	}
	executable, err := exec.LookPath(path)
	if err != nil {
		t.Skip("Java visualization tests require JDK 17+ java")
	}
	return executable
}

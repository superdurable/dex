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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeRustCargoSelectionAndMissingToolchain(t *testing.T) {
	source := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/flowviz/testdata/rust/workflow.rs")
	missing := filepath.Join(t.TempDir(), "cargo")
	t.Setenv("DEX_CARGO", missing)
	var stdout bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &bytes.Buffer{})
	args := []string{"visualize", source, "--language", "rust", "--flow", "Delivery", "--json"}
	err := app.Execute(t.Context(), args)
	require.Error(t, err)
	require.NotZero(t, ExitCode(err))
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
	require.False(t, graph.Valid)
	require.Len(t, graph.Diagnostics, 1)
	require.Equal(t, "rust_analyzer_failed", graph.Diagnostics[0].Code)

	cargo := requireVisualizeRustCargo(t)
	stdout.Reset()
	require.NoError(t, app.Execute(t.Context(), append(args, "--cargo", cargo)))
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph))
	require.Equal(t, "Delivery", graph.Flow.Name)
}

func TestVisualizeRustFlowSelectionScopesResources(t *testing.T) {
	requireVisualizeRustCargo(t)
	source := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/flowviz/testdata/rust/workflow.rs")
	all := readVisualizeExampleGraph(t, source, "")
	require.False(t, all.Valid)
	require.Len(t, all.Diagnostics, 1)
	require.Equal(t, "multiple_flows", all.Diagnostics[0].Code)
	require.Contains(t, all.Diagnostics[0].Message, "Delivery, VerifyAddress")

	child := readVisualizeRustParityGraph(t, source, "VerifyAddress")
	require.True(t, child.Valid, "%+v", child.Diagnostics)
	require.Equal(t, "step:CheckAddress", child.Flow.StartStepID)
	require.Empty(t, nodesOfKind(child.Nodes, "attribute"))
	require.Empty(t, nodesOfKind(child.Nodes, "channel"))
	require.Empty(t, nodesOfKind(child.Nodes, "rpc"))

	missing := readVisualizeExampleGraph(t, source, "Missing")
	require.False(t, missing.Valid)
	require.Equal(t, "flow_not_found", missing.Diagnostics[0].Code)
}

func TestVisualizeRustMetadataParity(t *testing.T) {
	requireVisualizeRustCargo(t)
	source := filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/v2-metadata-parity-rust")
	goGraph := readVisualizeRustParityGraph(t, filepath.Join(source, "workflow.go"), "IntakeFlow")
	rustGraph := readVisualizeRustParityGraph(t, filepath.Join(source, "workflow.rs"), "IntakeFlow")
	require.True(t, goGraph.Valid, "%+v", goGraph.Diagnostics)
	require.True(t, rustGraph.Valid, "%+v", rustGraph.Diagnostics)
	require.NotEmpty(t, goGraph.V2.IndexedAttributes)
	require.NotEmpty(t, goGraph.V2.Summary.Fields)
	require.NotEmpty(t, goGraph.V2.Display.Fields)
	require.NotNil(t, goGraph.V2.Start)
	require.Equal(t, goGraph.V2.IndexedAttributes, rustGraph.V2.IndexedAttributes)
	require.Equal(t, goGraph.V2.Summary, rustGraph.V2.Summary)
	require.Equal(t, goGraph.V2.Display, rustGraph.V2.Display)
	require.Equal(t, goGraph.V2.Start, rustGraph.V2.Start)
}

func TestVisualizeRustWorkflowPatternParity(t *testing.T) {
	requireVisualizeRustCargo(t)
	root := visualizerRepositoryRoot(t)
	goDirectory := filepath.Join(root, "examples/go/patterns/workflow-patterns")
	rustDirectory := filepath.Join(root, "examples/rust/src/workflow_patterns")
	patternMapping, err := os.ReadFile(filepath.Join(root, "examples/workflow-patterns-mapping.md"))
	require.NoError(t, err)
	goSources := visualizeWorkflowPatternSources(t, goDirectory, ".go")
	rustSources := visualizeWorkflowPatternSources(t, rustDirectory, ".rs")
	for flowType, goSource := range goSources {
		if !strings.Contains(string(patternMapping), "| "+flowType+" ") && !strings.Contains(string(patternMapping), "| "+flowType+"|") {
			continue
		}
		t.Run(flowType, func(t *testing.T) {
			t.Parallel()
			rustSource := rustSources[flowType]
			require.NotEmpty(t, rustSource)
			hiddenSteps := workflowPatternHiddenDecisions(flowType)
			goOnlyHiddenSteps := goWorkflowPatternHiddenDecisions[flowType]
			goGraph := readVisualizeRustParityGraph(t, goSource, flowType, append(hiddenSteps, goOnlyHiddenSteps...)...)
			rustGraph := readVisualizeRustParityGraph(t, rustSource, flowType, hiddenSteps...)
			expected := visualizePatternStructure(goGraph)
			for _, step := range goOnlyHiddenSteps {
				delete(expected, "transition:"+step+":Dynamic Dex decision")
			}
			if target := goCancellationRPCMovement(flowType); target != "" {
				expected["transition:rpc:"+target] = true
			}
			actual := visualizePatternStructure(rustGraph)
			if branches, ok := rustWorkflowPatternExternalJoinBranches[flowType]; ok {
				for _, target := range branches.targets {
					transition := "transition:" + branches.owner + ":" + target
					require.Contains(t, expected, transition)
					delete(expected, transition)
				}
				dynamicTransition := "transition:" + branches.owner + ":Dynamic Step target"
				require.Contains(t, actual, dynamicTransition)
				delete(actual, dynamicTransition)
			}
			require.Equal(t, expected, actual)
		})
	}
}

// Go misses NextSteps on an RPC result assigned to a local before CancelSteps.
func goCancellationRPCMovement(flowType string) string {
	switch flowType {
	case "CancelTaskFlow":
		return "ArrangeSelfDelivery"
	case "CancelRegionFlow":
		return "NotifyRejection"
	case "CancelMultipleInstanceTaskFlow":
		return "ReportTranslations"
	case "CompleteMultipleInstanceTaskFlow":
		return "PublishReviewResults"
	default:
		return ""
	}
}

func scanVisualizeExample(t *testing.T, source string, graph *flowviz.Graph) {
	t.Helper()
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code != "multiple_flows" {
			continue
		}
		requireVisualizeExampleDiagnostics(t, graph, source, nil, []string{"multiple_flows"})
		var flowNames []string
		for _, node := range graph.Nodes {
			if strings.HasPrefix(node.ID, "unknown:flow:") {
				flowNames = append(flowNames, node.Name)
			}
		}
		require.Greater(t, len(flowNames), 1)
		for _, flowName := range flowNames {
			t.Run(flowName, func(t *testing.T) {
				t.Parallel()
				selected := readVisualizeExampleGraph(t, source, flowName)
				require.Equal(t, flowName, selected.Flow.Name)
				requireScannedExampleDiagnostics(t, source, selected)
			})
		}
		return
	}
	requireScannedExampleDiagnostics(t, source, graph)
}

func requireScannedExampleDiagnostics(t *testing.T, source string, graph *flowviz.Graph) {
	t.Helper()
	var hiddenSteps, additionalErrors []string
	switch filepath.Base(source) {
	case "instance_creation.rs", "multiple_instances.py":
		hiddenSteps = workflowPatternHiddenDecisions(graph.Flow.Name)
	case "multi_choice_and_merges.py":
		// Python cannot resolve the branch target drawn from the selected Step list.
		if graph.Flow.Name == "MultiChoiceSynchronizingMergeFlow" {
			additionalErrors = []string{"unknown_step_target"}
		}
	}
	requireVisualizeExampleDiagnostics(t, graph, source, hiddenSteps, additionalErrors)
}

func visualizeWorkflowPatternSources(t *testing.T, directory string, extension string) map[string]string {
	t.Helper()
	sources := make(map[string]string)
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != extension {
			return err
		}
		graph := readVisualizeExampleGraph(t, path, "")
		if graph.Flow.Name != "" {
			sources[graph.Flow.Name] = path
		}
		for _, node := range graph.Nodes {
			if strings.HasPrefix(node.ID, "unknown:flow:") {
				sources[node.Name] = path
			}
		}
		return nil
	}))
	require.NotEmpty(t, sources)
	return sources
}

func visualizePatternStructure(graph *flowviz.Graph) map[string]bool {
	shapes := make(map[string]bool)
	nodes := make(map[string]flowviz.Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
		switch node.Kind {
		case "step", "attribute", "channel", "stream":
			shapes[node.Kind+":"+node.Name] = true
		}
		if node.Wait != nil {
			for _, condition := range node.Wait.Conditions {
				shapes["wait:"+condition.Kind] = true
			}
		}
	}
	for _, edge := range graph.Edges {
		if edge.Kind != "transition" && edge.Kind != "cancel" && edge.Kind != "failure_transition" && edge.Kind != "subflow" {
			continue
		}
		from := nodes[edge.From]
		if from.ParentID != "" {
			from = nodes[from.ParentID]
		}
		to := nodes[edge.To]
		fromName := from.Name
		if from.Kind == "rpc" {
			fromName = "rpc"
		}
		shapes[edge.Kind+":"+fromName+":"+to.Name] = true
	}
	return shapes
}

func readVisualizeRustParityGraph(t *testing.T, source string, flowType string, hiddenSteps ...string) *flowviz.Graph {
	t.Helper()
	graph := readVisualizeExampleGraph(t, source, flowType)
	require.Equal(t, flowType, graph.Flow.Name)
	requireVisualizeExampleDiagnostics(t, graph, source, hiddenSteps, nil)
	return graph
}

func requireVisualizeExampleDiagnostics(t *testing.T, graph *flowviz.Graph, source string, hiddenSteps, additionalErrors []string) {
	t.Helper()
	expectedErrors := append([]string(nil), additionalErrors...)
	if filepath.Ext(source) == ".rs" && filepath.Base(filepath.Dir(source)) == "workflow_patterns" {
		for range rustWorkflowPatternExternalJoinBranches[graph.Flow.Name].targets {
			expectedErrors = append(expectedErrors, "dynamic_step_target")
		}
	}
	var expectedHiddenOwners []string
	for _, step := range hiddenSteps {
		expectedErrors = append(expectedErrors, "hidden_dex_decision")
		expectedHiddenOwners = append(expectedHiddenOwners, "step:"+step)
	}
	diagnostics := diagnosticsWithoutMissingVisualizationMetadata(t, source, graph.Diagnostics)
	var actualErrors []string
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			actualErrors = append(actualErrors, diagnostic.Code)
		}
	}
	require.ElementsMatch(t, expectedErrors, actualErrors, "%+v", diagnostics)

	var actualHiddenOwners []string
	for _, node := range graph.Nodes {
		if node.Kind == "unknown" && node.Name == "Dynamic Dex decision" {
			for _, edge := range graph.Edges {
				if edge.To == node.ID && edge.Kind == "transition" && edge.Label == "dynamic" {
					actualHiddenOwners = append(actualHiddenOwners, edge.From)
				}
			}
		}
	}
	require.ElementsMatch(t, expectedHiddenOwners, actualHiddenOwners)
}

// Join branch helper defined in another file; the analyzer reads one file.
var rustWorkflowPatternExternalJoinBranches = map[string]struct {
	owner   string
	targets []string
}{
	"ParallelSplitSynchronizationFlow": {
		owner:   "SplitOrder",
		targets: []string{"PackItems", "ChargePayment"},
	},
	"MultiChoiceSynchronizingMergeFlow": {
		owner:   "SelectNotificationChannels",
		targets: []string{"SendEmail", "SendSms", "SendPush"},
	},
	"StructuredPartialJoinFlow": {
		owner:   "RequestQuotes",
		targets: []string{"QuoteFromSupplierA", "QuoteFromSupplierB", "QuoteFromSupplierC"},
	},
	"BlockingPartialJoinFlow": {
		owner:   "StartBlockingRound",
		targets: []string{"SignalFromBranchA", "SignalFromBranchB", "SignalFromBranchC"},
	},
	"CancellingPartialJoinFlow": {
		owner:   "RequestBids",
		targets: []string{"BidFromCarrierA", "BidFromCarrierB", "BidFromCarrierC"},
	},
}

// These Steps return decisions from review starters in all three languages.
func workflowPatternHiddenDecisions(flowType string) []string {
	switch flowType {
	case "DesignTimeMultipleInstancesFlow":
		return []string{"StartDesignTimeReviews"}
	case "RunTimeMultipleInstancesFlow":
		return []string{"StartRunTimeReviews"}
	default:
		return nil
	}
}

// These Go Steps delegate terminal decisions. Their Rust counterparts express those decisions directly.
var goWorkflowPatternHiddenDecisions = map[string][]string{
	"InterleavedRoutingFlow":                     {"AuditExpenses", "AuditPayroll", "AuditTaxes", "CompleteAudits"},
	"InterleavedParallelRoutingFlow":             {"CompleteOnboardingTasks"},
	"CompleteMultipleInstanceTaskFlow":           {"SubmitPeerReview"},
	"NoPriorKnowledgeMultipleInstancesFlow":      {"AnalyzeSample"},
	"DynamicPartialJoinForMultipleInstancesFlow": {"EvaluateBid"},
	"CriticalSectionFlow":                        {"CommitStock", "FinishCriticalSections", "PublishPrice"},
}

func warmVisualizeRustAnalyzer(t *testing.T, repositoryRoot string) {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		return
	}
	source := filepath.Join(repositoryRoot, "cli/internal/flowviz/testdata/rust/workflow.rs")
	graph := readVisualizeExampleGraph(t, source, "Delivery")
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
}

func readVisualizeExampleGraph(t *testing.T, source string, flowType string) *flowviz.Graph {
	t.Helper()
	graph, err := analyzeParallelVisualization(t, source, flowviz.AnalyzeOptions{FlowType: flowType})
	require.NoError(t, err)
	require.Equal(t, "2.0", graph.SchemaVersion)
	valid := true
	for _, diagnostic := range graph.Diagnostics {
		valid = valid && diagnostic.Severity != "error"
	}
	require.Equal(t, valid, graph.Valid, "%+v", graph.Diagnostics)
	return graph
}

func requireVisualizeRustCargo(t *testing.T) string {
	t.Helper()
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("Rust visualization requires Cargo on PATH")
	}
	return cargo
}

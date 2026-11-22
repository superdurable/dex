// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatchtest"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/fakeledger"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// connectorToolsConformance is agents/conformance/connector_tools.json, which every core reads.
type connectorToolsConformance struct {
	InputSchema   json.RawMessage `json:"inputSchema"`
	ArgumentCases []struct {
		Arguments json.RawMessage `json:"arguments"`
		Violation *string         `json:"violation"`
	} `json:"argumentCases"`
	RefusedSchemas []struct {
		Schema json.RawMessage `json:"schema"`
		Error  string          `json:"error"`
	} `json:"refusedSchemas"`
	BranchCases []struct {
		Kind              string            `json:"kind"`
		Branch            string            `json:"branch"`
		RequiredBranches  []string          `json:"requiredBranches"`
		AgentBranches     map[string]string `json:"agentBranches"`
		MayHaveDispatched bool              `json:"mayHaveDispatched"`
		Outcome           string            `json:"outcome"`
	} `json:"branchCases"`
	ToolNameCases []struct {
		OperationID string `json:"operationId"`
		SnakeName   string `json:"snakeName"`
	} `json:"toolNameCases"`
	ResultCases []struct {
		JSON string `json:"json"`
		Text string `json:"text"`
	} `json:"resultCases"`
	TruncationCases []struct {
		Output               string `json:"output"`
		MaxResultBytes       int    `json:"maxResultBytes"`
		ExpectedOutput       string `json:"expectedOutput"`
		TruncatedOutputBytes *int64 `json:"truncatedOutputBytes"`
	} `json:"truncationCases"`
	SendCapCases []struct {
		Name       string                     `json:"name"`
		Kind       string                     `json:"kind"`
		Agent      map[string]json.RawMessage `json:"agent"`
		Definition struct {
			RepeatOnUnknown string `json:"repeatOnUnknown"`
			MaxSends        int32  `json:"maxSends"`
		} `json:"definition"`
		MaxSends *int32 `json:"maxSends"`
	} `json:"sendCapCases"`
	BoundedResendAttemptCases []struct {
		OperationAttempts int32 `json:"operationAttempts"`
		ManifestMaxSends  int32 `json:"manifestMaxSends"`
		CallMaxSends      int32 `json:"callMaxSends"`
		Attempts          int32 `json:"attempts"`
	} `json:"boundedResendAttemptCases"`
	BoundedResendScenarios struct {
		Cases []struct {
			Name            string   `json:"name"`
			Text            string   `json:"text"`
			DeferVariant    string   `json:"deferVariant"`
			PolicyMaxSends  *int32   `json:"policyMaxSends"`
			Reference       string   `json:"reference"`
			AttemptVariants []string `json:"attemptVariants"`
			Outcomes        []string `json:"outcomes"`
			Writes          int      `json:"writes"`
		} `json:"cases"`
	} `json:"boundedResendScenarios"`
	ArgumentRefusalCases struct {
		Cases []struct {
			Name               string  `json:"name"`
			Attempt            int32   `json:"attempt"`
			PreviousCheckpoint *string `json:"previousCheckpoint"`
			Arguments          string  `json:"arguments"`
			Outcome            string  `json:"outcome"`
		} `json:"cases"`
	} `json:"argumentRefusalCases"`
}

func readConnectorToolsConformance(t *testing.T) connectorToolsConformance {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "conformance", "connector_tools.json"))
	require.NoError(t, err)
	var conformance connectorToolsConformance
	require.NoError(t, json.Unmarshal(content, &conformance))
	return conformance
}

func TestArgumentsAreCheckedAsTheConformanceSuiteSays(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	schema, err := parseInputSchema(conformance.InputSchema)
	require.NoError(t, err, "the conformance schema is accepted")
	require.NotEmpty(t, conformance.ArgumentCases)
	for _, testCase := range conformance.ArgumentCases {
		arguments, err := pythonjson.ParseOrdered(testCase.Arguments)
		require.NoError(t, err)
		expected := ""
		if testCase.Violation != nil {
			expected = *testCase.Violation
		}
		require.Equal(t, expected, schema.findInputViolation(arguments), string(testCase.Arguments))
	}
}

func TestASchemaWithAnUncheckedKeywordIsRefused(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.RefusedSchemas)
	for _, testCase := range conformance.RefusedSchemas {
		_, err := parseInputSchema(testCase.Schema)
		require.EqualError(t, err, testCase.Error, string(testCase.Schema))
	}
}

func TestBranchesMapAsTheConformanceSuiteSays(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.BranchCases)
	for _, testCase := range conformance.BranchCases {
		required := make([]sdkgo.BranchID, 0, len(testCase.RequiredBranches))
		for _, branch := range testCase.RequiredBranches {
			required = append(required, sdkgo.BranchID(branch))
		}
		agentBranches := map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{}
		for branch, outcome := range testCase.AgentBranches {
			agentBranches[sdkgo.BranchID(branch)] = sdkgo.AgentBranchOutcome(outcome)
		}
		// mayHaveDispatched is deprecated and ignored: only the branch decides.
		require.Equal(t, ConnectorBranchOutcome(testCase.Outcome),
			ClassifyConnectorBranch(sdkgo.BranchID(testCase.Branch), required, agentBranches),
			"%s %s %v %v", testCase.Kind, testCase.Branch, testCase.RequiredBranches, testCase.AgentBranches)
	}
}

func TestOperationIDsBecomeSnakeCaseAsTheConformanceSuiteSays(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.ToolNameCases)
	for _, testCase := range conformance.ToolNameCases {
		require.Equal(t, testCase.SnakeName, lowerCamelToSnake(testCase.OperationID), testCase.OperationID)
	}
	require.Equal(t, "fake-ledger_get_balance",
		DefaultConnectorToolName(sdkgo.OperationRef{ConnectorID: "fake-ledger", OperationID: "getBalance"}))
}

func TestResultTextIsTheSameInEveryCore(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.ResultCases)
	for _, testCase := range conformance.ResultCases {
		text, err := FormatConnectorResult(testCase.JSON)
		require.NoError(t, err)
		require.Equal(t, testCase.Text, text, testCase.JSON)
	}
}

func TestResultsAreTruncatedAsTheConformanceSuiteSays(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.TruncationCases)
	for _, testCase := range conformance.TruncationCases {
		result := BoundConnectorResult(testCase.Output, testCase.MaxResultBytes)
		require.Equal(t, testCase.ExpectedOutput, result.Output)
		require.Equal(t, testCase.TruncatedOutputBytes, result.TruncatedOutputBytes)
	}
}

func TestSendCapsAreReadAsTheConformanceSuiteSays(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.SendCapCases)
	for _, testCase := range conformance.SendCapCases {
		manifest := sendCapManifest{
			repeatOnUnknown: decodeJSONValue(t, testCase.Agent["repeatOnUnknown"]),
			maxSends:        decodeJSONValue(t, testCase.Agent["maxSends"]),
		}
		expected := int32(0)
		if testCase.MaxSends != nil {
			expected = *testCase.MaxSends
		}
		require.Equal(t, expected, connectorSendCap(sdkgo.AgentOperationKind(testCase.Kind), manifest,
			sdkgo.RepeatOnUnknown(testCase.Definition.RepeatOnUnknown), testCase.Definition.MaxSends), testCase.Name)
	}
}

func TestABoundedResendsAttemptsFollowTheConformanceSuite(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.BoundedResendAttemptCases)
	for _, testCase := range conformance.BoundedResendAttemptCases {
		require.Equal(t, testCase.Attempts,
			boundedResendAttempts(testCase.OperationAttempts, testCase.ManifestMaxSends, testCase.CallMaxSends), "%+v", testCase)
	}
}

func TestBoundedResendsFollowTheConformanceScenariosAcrossDeploys(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.BoundedResendScenarios.Cases)
	for index, scenario := range conformance.BoundedResendScenarios.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			t.Parallel()
			provider := fakeledger.NewProvider(t.TempDir())
			policies := map[string]dexagents.ToolPolicy{}
			if scenario.PolicyMaxSends != nil {
				policies["send"] = dexagents.ToolPolicy{Effect: dexagents.ToolEffectNonIdempotentWrite, MaxSends: scenario.PolicyMaxSends}
			}
			arguments, err := pythonjson.Marshal(pythonjson.Object{
				{Key: "accountId", Value: "a-1"}, {Key: "text", Value: scenario.Text},
			}, pythonjson.Options{ShouldEscapeNonASCII: true})
			require.NoError(t, err)
			deferred, err := boundedResendSource(t, provider, scenario.DeferVariant).DeferredToolCall(
				dexagents.AgentTurnContext{ToolPolicies: policies},
				dexagents.SourceToolCall{ToolCallID: "call-1", ToolName: "send", Arguments: arguments})
			require.NoError(t, err)
			require.NotNil(t, deferred.ToolSourceReference)
			require.Equal(t, scenario.Reference, *deferred.ToolSourceReference)
			attempts := newSendAttempts(fmt.Sprintf("session-%d", index))
			outcomes := []string{}
			for _, variant := range scenario.AttemptVariants {
				outcomes = append(outcomes, attempts.run(t, boundedResendSource(t, provider, variant), deferred))
				if outcomes[len(outcomes)-1] != "retry" {
					break
				}
			}
			require.Equal(t, scenario.Outcomes, outcomes)
			require.Len(t, provider.Writes("sendMessage"), scenario.Writes)
		})
	}
}

func TestRefusedArgumentsFailOnEveryAttempt(t *testing.T) {
	t.Parallel()
	conformance := readConnectorToolsConformance(t)
	require.NotEmpty(t, conformance.ArgumentRefusalCases.Cases)
	for index, testCase := range conformance.ArgumentRefusalCases.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()
			provider := fakeledger.NewProvider(t.TempDir())
			attempts := newSendAttempts(fmt.Sprintf("refusal-%d", index))
			attempts.checkpoint = testCase.PreviousCheckpoint
			for attempts.context.Attempt() < testCase.Attempt {
				attempts.context.AdvanceAttempt()
			}
			reference := `{"tool": "send", "maxSends": 3}`
			deferred := dexagents.DeferredToolCall{ToolName: "send", Arguments: testCase.Arguments, ToolSourceReference: &reference}
			require.Equal(t, testCase.Outcome, attempts.runOnce(t, boundedResendSource(t, provider, "cap3"), deferred))
			require.Zero(t, provider.Sends("sendMessage"))
			require.Zero(t, provider.Calls("sendMessage"), "the operation never ran")
		})
	}
}

// boundedResendSource is a Worker's sendMessage tool: limited with cap 3 or 5, or cap 3 whose
// manifest drops the tier.
func boundedResendSource(t *testing.T, provider *fakeledger.Provider, variant string) *ConnectorToolSource {
	t.Helper()
	definitionCap := int32(3)
	if variant == "cap5" {
		definitionCap = 5
	}
	agentTool, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.SendMessage{Provider: provider, MaxSends: definitionCap}, fakeledger.Connection,
		fakeledger.SendMessageMetadata(definitionCap))
	require.NoError(t, err)
	manifest := sendCapManifest{repeatOnUnknown: string(sdkgo.RepeatOnUnknownLimited), maxSends: definitionCap}
	if variant == "fallback" {
		manifest = sendCapManifest{}
	}
	tool, err := newConnectorTool(agentTool, agentTool.Metadata(), connectorToolOptions{name: "send", manifest: &manifest})
	require.NoError(t, err)
	source, err := NewConnectorToolSource([]*ConnectorTool{tool})
	require.NoError(t, err)
	return source
}

// sendAttempts runs one call's attempts as the tool Step does, with its text checkpoint.
type sendAttempts struct {
	context    *singledispatchtest.Context
	checkpoint *string
	attempt    int32
}

func newSendAttempts(flowID string) *sendAttempts {
	return &sendAttempts{context: singledispatchtest.NewContext(flowID, "CallSingleAttemptTool-5")}
}

func (attempts *sendAttempts) run(t *testing.T, source *ConnectorToolSource, deferred dexagents.DeferredToolCall) string {
	t.Helper()
	attempts.attempt++
	if attempts.attempt > 1 {
		attempts.context.AdvanceAttempt()
	}
	return attempts.runOnce(t, source, deferred)
}

func (attempts *sendAttempts) runOnce(t *testing.T, source *ConnectorToolSource, deferred dexagents.DeferredToolCall) string {
	t.Helper()
	record := dexagents.AgentToolCall{
		ToolCallID: "call-1", TurnID: "turn-1", ToolName: deferred.ToolName, Arguments: deferred.Arguments,
		Effect: dexagents.ToolEffectNonIdempotentWrite, Status: dexagents.ToolCallStatusApproved,
		ToolSourceReference: deferred.ToolSourceReference,
	}
	outcome, err := source.ExecuteDeferredToolCall(attempts.context, dexagents.DeferredToolExecution{
		FlowID:              attempts.context.FlowID(),
		ToolCall:            record,
		IdempotencyKey:      attempts.context.FlowID() + "/turn-1/call-1",
		Attempt:             int64(attempts.context.Attempt()),
		Heartbeat:           func() error { return nil },
		HeartbeatCheckpoint: attempts.checkpoint,
		SaveHeartbeatCheckpoint: func(checkpoint string) error {
			attempts.checkpoint = &checkpoint
			return nil
		},
	})
	return describeOutcome(t, outcome, err)
}

// describeOutcome names how one attempt ended: succeeded, failed, outcome_unknown, or retry.
func describeOutcome(t *testing.T, outcome dexagents.DeferredToolOutcome, err error) string {
	t.Helper()
	var toolError *dexagents.ToolExecutionError
	var unknownError *dexagents.ToolOutcomeUnknownError
	var retryError *sdkgo.RetryError
	switch {
	case errors.As(err, &toolError):
		return "failed"
	case errors.As(err, &unknownError):
		return "outcome_unknown"
	case errors.As(err, &retryError):
		return "retry"
	case err != nil:
		t.Fatalf("the attempt failed: %v", err)
	}
	require.IsType(t, &dexagents.ToolExecutionResult{}, outcome)
	return "succeeded"
}

func decodeJSONValue(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if raw == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	return value
}

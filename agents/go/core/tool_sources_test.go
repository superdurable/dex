// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/sdk-go/dex"
)

// fakeToolSource lists fixed tools and records what reaches it.
type fakeToolSource struct {
	name          string
	tools         []SourceToolDefinition
	listingError  error
	listingDelay  time.Duration
	declaresTools bool
	executed      []DeferredToolExecution
	stepOptions   []AgentToolCall
}

func (source *fakeToolSource) SourceName() string { return source.name }

func (source *fakeToolSource) ListTools(ctx context.Context) ([]SourceToolDefinition, error) {
	if source.listingDelay > 0 {
		select {
		case <-time.After(source.listingDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return source.tools, source.listingError
}

func (source *fakeToolSource) DeferredToolCall(turn AgentTurnContext, call SourceToolCall) (DeferredToolCall, error) {
	reference := `{"tool": "` + call.ListedTool.Name + `"}`
	isApprovalRequired := false
	return DeferredToolCall{
		ToolCallID: call.ToolCallID, ToolName: call.ToolName, Arguments: call.Arguments,
		Policy:              ToolPolicy{Effect: ToolEffectNonIdempotentWrite, RequiresApproval: &isApprovalRequired},
		ToolSourceReference: &reference, Ordered: true,
	}, nil
}

func (source *fakeToolSource) ExecuteDeferredToolCall(_ context.Context, call DeferredToolExecution) (DeferredToolOutcome, error) {
	source.executed = append(source.executed, call)
	return &ToolExecutionResult{Output: "ran " + call.ToolCall.ToolName}, nil
}

func (source *fakeToolSource) CachedTools() []SourceToolDefinition { return source.tools }

func (source *fakeToolSource) ToolCallStepOptions(toolCall AgentToolCall) (*ToolCallStepOptions, error) {
	source.stepOptions = append(source.stepOptions, toolCall)
	return &ToolCallStepOptions{ExecuteMethodTimeout: time.Minute, BoundedResendExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 4}}, nil
}

type declaringToolSource struct {
	*fakeToolSource
}

func (source declaringToolSource) DeclaredToolNames() []string {
	names := []string{}
	for _, tool := range source.tools {
		names = append(names, tool.Name)
	}
	return names
}

func tool(name string) SourceToolDefinition {
	return SourceToolDefinition{Name: name, Description: "The " + name + " tool.", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func TestARouterStoresTheSourceNameInTheEnvelopeTheOtherCoresWrite(t *testing.T) {
	t.Parallel()
	source := &fakeToolSource{name: "connectors", tools: []SourceToolDefinition{tool("send")}}
	for envelopeKey, expected := range map[string]string{
		FrameworkToolSourceEnvelopeKey: `{"dex_tool_source": "connectors", "reference": "{\"tool\": \"send\"}"}`,
		"stepwise_tool_source":         `{"stepwise_tool_source": "connectors", "reference": "{\"tool\": \"send\"}"}`,
	} {
		router, err := NewToolSourceRouter([]ToolSource{source}, WithToolSourceEnvelopeKey(envelopeKey))
		require.NoError(t, err)
		listing, err := router.ListSourceTools(context.Background(), AgentTurnContext{})
		require.NoError(t, err)
		require.Equal(t, []SourceToolDefinition{tool("send")}, listing.Tools())
		require.True(t, listing.HasTool("send"))
		call, err := listing.DeferredToolCall(AgentTurnContext{}, "call-1", "send", `{"text": "hi"}`)
		require.NoError(t, err)
		require.Equal(t, expected, *call.ToolSourceReference)
		require.True(t, call.Ordered, "the source's choices reach the session")
		record := AgentToolCall{ToolCallID: "call-1", ToolName: "send", ToolSourceReference: call.ToolSourceReference}
		require.True(t, router.IsSourceToolCall(record))

		outcome, err := router.ExecuteDeferredToolCall(context.Background(), DeferredToolExecution{ToolCall: record, Attempt: 2})
		require.NoError(t, err)
		require.Equal(t, "ran send", outcome.(*ToolExecutionResult).Output)
		executed := source.executed[len(source.executed)-1]
		require.Equal(t, `{"tool": "send"}`, *executed.ToolCall.ToolSourceReference, "the source reads its own reference")
		require.Equal(t, int64(2), executed.Attempt)

		options, err := router.ToolCallStepOptions(record)
		require.NoError(t, err)
		require.Equal(t, time.Minute, options.ExecuteMethodTimeout)
		require.Equal(t, `{"tool": "send"}`, *source.stepOptions[len(source.stepOptions)-1].ToolSourceReference)
	}
	envelope, err := writeToolSourceEnvelope("dex_tool_source", "café", nil)
	require.NoError(t, err)
	require.Equal(t, "{\"dex_tool_source\": \"caf\\u00e9\", \"reference\": null}", envelope)
	sourceName, reference, isEnveloped := readToolSourceEnvelope(&envelope, "dex_tool_source")
	require.True(t, isEnveloped)
	require.Equal(t, "café", sourceName)
	require.Nil(t, reference)
}

func TestARouterRunsOnlyTheCallsItsSourcesBuilt(t *testing.T) {
	t.Parallel()
	router, err := NewToolSourceRouter([]ToolSource{&fakeToolSource{name: "connectors"}})
	require.NoError(t, err)
	for _, reference := range []*string{nil, ptr(`{"tool": "send"}`), ptr(`not json`), ptr(`{"dex_tool_source": 7}`),
		ptr(`{"dex_tool_source": null}`), ptr(`{"dex_tool_source": "connectors", "reference": 7}`),
		ptr(`{"stepwise_tool_source": "connectors", "reference": null}`)} {
		record := AgentToolCall{ToolName: "send", ToolSourceReference: reference}
		require.False(t, router.IsSourceToolCall(record))
		options, err := router.ToolCallStepOptions(record)
		require.NoError(t, err)
		require.Nil(t, options)
		_, err = router.ExecuteDeferredToolCall(context.Background(), DeferredToolExecution{ToolCall: record})
		require.EqualError(t, err, "no tool source built the call of tool send")
	}
	gone := AgentToolCall{ToolName: "send", ToolSourceReference: ptr(`{"dex_tool_source": "mcp", "reference": null}`)}
	require.True(t, router.IsSourceToolCall(gone))
	_, err = router.ExecuteDeferredToolCall(context.Background(), DeferredToolExecution{ToolCall: gone})
	require.EqualError(t, err, "tool source mcp, which built the call of tool send, is not configured on this Worker")
	_, err = router.ToolCallStepOptions(gone)
	require.Error(t, err)
}

func TestARouterRefusesNameCollisionsAndBadOptions(t *testing.T) {
	t.Parallel()
	first := declaringToolSource{&fakeToolSource{name: "a", tools: []SourceToolDefinition{tool("send")}}}
	second := declaringToolSource{&fakeToolSource{name: "b", tools: []SourceToolDefinition{tool("send")}}}
	_, err := NewToolSourceRouter([]ToolSource{first, second})
	require.EqualError(t, err, "two tool sources list a tool named send")
	_, err = NewToolSourceRouter([]ToolSource{first}, WithReservedToolNames("send"))
	require.EqualError(t, err, "tool send of a tool source uses the name of a framework tool")
	_, err = NewToolSourceRouter([]ToolSource{first, &fakeToolSource{name: "a"}})
	require.EqualError(t, err, "two tool sources are named a")
	_, err = NewToolSourceRouter([]ToolSource{&fakeToolSource{name: " "}})
	require.ErrorContains(t, err, "non-blank source name")
	_, err = NewToolSourceRouter([]ToolSource{nil})
	require.ErrorContains(t, err, "is nil")
	_, err = NewToolSourceRouter(nil, WithToolSourceListingTimeout(0))
	require.ErrorContains(t, err, "must be positive")
	_, err = NewToolSourceRouter(nil, WithToolSourceEnvelopeKey("reference"))
	require.ErrorContains(t, err, "must not be blank")
	router, err := NewToolSourceRouter([]ToolSource{first}, WithReservedToolNames("lookup"))
	require.NoError(t, err)
	require.Equal(t, []string{"send"}, router.DeclaredToolNames())
	require.Len(t, router.Sources(), 1)
	cached, err := router.CachedSourceTools()
	require.NoError(t, err)
	require.Equal(t, []SourceToolDefinition{tool("send")}, cached)
	_, err = router.ListSourceTools(context.Background(), AgentTurnContext{}, "send")
	require.EqualError(t, err, "tool send of a tool source uses the name of a framework tool")
}

func TestASourceThatFailsOrTakesTooLongOffersNothingInThatModelCall(t *testing.T) {
	t.Parallel()
	slow := &fakeToolSource{name: "slow", tools: []SourceToolDefinition{tool("slow_tool")}, listingDelay: time.Minute}
	failing := &fakeToolSource{name: "failing", tools: []SourceToolDefinition{tool("failing_tool")}, listingError: errors.New("down")}
	healthy := &fakeToolSource{name: "healthy", tools: []SourceToolDefinition{tool("b"), tool("a")}}
	router, err := NewToolSourceRouter([]ToolSource{slow, failing, healthy}, WithToolSourceListingTimeout(50*time.Millisecond))
	require.NoError(t, err)
	listing, err := router.ListSourceTools(context.Background(), AgentTurnContext{})
	require.NoError(t, err)
	require.Equal(t, []SourceToolDefinition{tool("b"), tool("a")}, listing.Tools(), "in each source's order")
	call, err := listing.DeferredToolCall(AgentTurnContext{}, "call-1", "slow_tool", "{}")
	require.NoError(t, err)
	require.Nil(t, call, "the runner builds a call that no listing offers")
}

func TestARecordedToolTheListingLacksIsReplayOnly(t *testing.T) {
	t.Parallel()
	source := &fakeToolSource{name: "connectors", tools: []SourceToolDefinition{tool("send")}}
	router, err := NewToolSourceRouter([]ToolSource{source})
	require.NoError(t, err)
	listing, err := router.ListSourceTools(context.Background(), AgentTurnContext{})
	require.NoError(t, err)
	call, err := listing.DeferredToolCall(AgentTurnContext{}, "call-1", "send", "{}")
	require.NoError(t, err)
	turn := AgentTurnContext{ToolCalls: map[string]AgentToolCall{"call-1": {
		ToolCallID: "call-1", ToolName: "send", ToolSourceReference: call.ToolSourceReference, Status: ToolCallStatusSucceeded,
	}}}
	_, err = listing.DeferredToolCall(turn, "call-1", "send", "{}")
	require.EqualError(t, err, "tool call ID call-1 is already recorded in this turn")

	source.listingError = errors.New("down")
	listing, err = router.ListSourceTools(context.Background(), turn)
	require.NoError(t, err)
	require.Empty(t, listing.Tools())
	require.Equal(t, []SourceToolDefinition{tool("send")}, listing.ReplayOnlyTools())
	require.True(t, listing.IsReplayOnly("send"))
	require.False(t, listing.HasTool("send"))
	require.Equal(t, "send did not run: its tool source does not offer this tool now.", listing.NotOfferedResult("send"))
	_, err = listing.DeferredToolCall(AgentTurnContext{}, "call-2", "send", "{}")
	require.EqualError(t, err, "tool send is offered only to replay its recorded calls")
}

func TestAToolPolicyKeepsItsMaxSendsAndOnlyValidCapsAreAccepted(t *testing.T) {
	t.Parallel()
	five := int32(5)
	policy := ResolveToolPolicy("send", map[string]ToolPolicy{"send": {Effect: ToolEffectNonIdempotentWrite, MaxSends: &five}},
		ToolEffectUnclassified, nil)
	require.Equal(t, &five, policy.MaxSends)
	for _, maxSends := range []int32{0, 11, -1} {
		_, err := NewAgentSessionFlow(EchoTestRunner{}, "Agent", WithToolPolicies(map[string]ToolPolicy{
			"send": {Effect: ToolEffectNonIdempotentWrite, MaxSends: &maxSends},
		}))
		require.ErrorContains(t, err, "must be from 1 to 10", "max sends %d", maxSends)
	}
	for _, maxSends := range []int32{1, 10} {
		_, err := NewAgentSessionFlow(EchoTestRunner{}, "Agent", WithToolPolicies(map[string]ToolPolicy{
			"send": {Effect: ToolEffectNonIdempotentWrite, MaxSends: &maxSends},
		}))
		require.NoError(t, err)
	}
}

func TestOrderedWritesAfterOneThatDidNotSucceedAreSkippedAsNotRun(t *testing.T) {
	t.Parallel()
	queued := AgentToolCall{ToolCallID: "call-2", ToolName: "send", Status: ToolCallStatusApproved, WriteQueued: true, OrderedWrite: true}
	skipped := skippedToolCall(queued)
	require.Equal(t, ToolCallStatusFailed, skipped.Status)
	require.True(t, skipped.Skipped)
	require.False(t, skipped.WriteQueued)
	require.Equal(t, "the tool did not run, so it is safe to call again: an earlier write of this turn did not succeed", *skipped.Error)
	outcome, err := DescribeToolCallOutcome(skipped)
	require.NoError(t, err)
	require.Equal(t, "The tool call failed: "+ToolCallNotRunErrorPrefix+": "+ToolCallSkippedAfterWriteDetail, outcome)
}

// EchoTestRunner is a runner without tools, for option checks.
type EchoTestRunner struct{}

func (EchoTestRunner) BuildUserMessage(text string) (FrameworkMessage, error) {
	return FrameworkMessage{Role: MessageRoleUser, Text: &text, FrameworkItem: `{}`}, nil
}

func (EchoTestRunner) RunModelStep(context.Context, AgentTurnContext) (AgentTurnOutcome, error) {
	return &TurnCompleted{}, nil
}

func (EchoTestRunner) ExecuteDeferredToolCall(context.Context, DeferredToolExecution) (DeferredToolOutcome, error) {
	return nil, &ToolExecutionError{Message: "no tools"}
}

// Every core reads every written form of names.json's envelopes, and the Go form canonicalizes to the shared one.
func TestTheGoCoreReadsEveryCoresToolSourceEnvelope(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile(filepath.Join("..", "..", "conformance", "names.json"))
	require.NoError(t, err)
	var names struct {
		StepwiseToolSourceReference struct {
			Keys struct {
				SourceName string `json:"source_name"`
				Reference  string `json:"reference"`
			} `json:"keys"`
			Cases []struct {
				SourceName string   `json:"source_name"`
				Reference  *string  `json:"reference"`
				Written    []string `json:"written"`
				Canonical  string   `json:"canonical"`
			} `json:"cases"`
		} `json:"stepwise_tool_source_reference"`
	}
	require.NoError(t, json.Unmarshal(text, &names))
	envelopes := names.StepwiseToolSourceReference
	require.Equal(t, toolSourceReferenceKey, envelopes.Keys.Reference)
	require.NotEmpty(t, envelopes.Cases)
	for _, envelopeCase := range envelopes.Cases {
		for _, written := range envelopeCase.Written {
			sourceName, reference, isEnveloped := readToolSourceEnvelope(&written, envelopes.Keys.SourceName)
			require.True(t, isEnveloped, written)
			require.Equal(t, envelopeCase.SourceName, sourceName, written)
			require.Equal(t, envelopeCase.Reference, reference, written)
		}
		own, err := writeToolSourceEnvelope(envelopes.Keys.SourceName, envelopeCase.SourceName, envelopeCase.Reference)
		require.NoError(t, err)
		parsed, err := pythonjson.ParseOrdered([]byte(own))
		require.NoError(t, err)
		canonical, err := pythonjson.Marshal(parsed, pythonjson.Options{IsCompact: true, ShouldSortKeys: true, ShouldEscapeNonASCII: true})
		require.NoError(t, err)
		require.Equal(t, envelopeCase.Canonical, canonical, envelopeCase.SourceName)
	}
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/connectormock"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatch"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatchtest"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/fakeledger"
	"github.com/superdurable/dex/sdk-go/dex"
)

type ledgerTools struct {
	provider    *fakeledger.Provider
	getBalance  *ConnectorTool
	payout      *ConnectorTool
	note        *ConnectorTool
	sendMessage *ConnectorTool
}

func newLedgerTools(t *testing.T, options ...ConnectorToolOption) ledgerTools {
	t.Helper()
	provider := fakeledger.NewProvider(t.TempDir())
	query, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		fakeledger.GetBalance{Provider: provider}, fakeledger.Connection, fakeledger.GetBalanceMetadata())
	require.NoError(t, err)
	payout, err := sdkgo.NewMutationAgentTool[fakeledger.CreatePayoutInput, fakeledger.Payout](
		fakeledger.CreatePayout{Provider: provider}, fakeledger.Connection, fakeledger.CreatePayoutMetadata())
	require.NoError(t, err)
	note, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.PostNote{Provider: provider}, fakeledger.Connection, fakeledger.PostNoteMetadata())
	require.NoError(t, err)
	message, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.SendMessage{Provider: provider}, fakeledger.Connection, fakeledger.SendMessageMetadata(0))
	require.NoError(t, err)
	tools := ledgerTools{provider: provider}
	for target, agentTool := range map[**ConnectorTool]sdkgo.AgentTool{
		&tools.getBalance: query, &tools.payout: payout, &tools.note: note, &tools.sendMessage: message,
	} {
		*target, err = NewConnectorTool(agentTool, options...)
		require.NoError(t, err)
	}
	return tools
}

func (tools ledgerTools) source(t *testing.T, options ...ConnectorToolSourceOption) *ConnectorToolSource {
	t.Helper()
	source, err := NewConnectorToolSource([]*ConnectorTool{tools.getBalance, tools.payout, tools.note, tools.sendMessage}, options...)
	require.NoError(t, err)
	return source
}

func defer_(t *testing.T, source *ConnectorToolSource, policies map[string]dexagents.ToolPolicy, toolName string, arguments string) dexagents.DeferredToolCall {
	t.Helper()
	call, err := source.DeferredToolCall(dexagents.AgentTurnContext{ToolPolicies: policies},
		dexagents.SourceToolCall{ToolCallID: "call-1", ToolName: toolName, Arguments: arguments})
	require.NoError(t, err)
	return call
}

// runAttempt runs one tool Step attempt of a deferred call with a fresh Step context.
func runAttempt(
	t *testing.T,
	source *ConnectorToolSource,
	deferred dexagents.DeferredToolCall,
	stepContext *singledispatchtest.Context,
	checkpoint **string,
) (dexagents.DeferredToolOutcome, error) {
	t.Helper()
	record := dexagents.AgentToolCall{
		ToolCallID: deferred.ToolCallID, TurnID: "turn-1", ToolName: deferred.ToolName, Arguments: deferred.Arguments,
		Effect: deferred.Policy.Effect, Status: dexagents.ToolCallStatusApproved, ToolSourceReference: deferred.ToolSourceReference,
	}
	return source.ExecuteDeferredToolCall(stepContext, dexagents.DeferredToolExecution{
		FlowID: stepContext.FlowID(), ToolCall: record, Attempt: int64(stepContext.Attempt()),
		Heartbeat: func() error { return nil }, HeartbeatCheckpoint: *checkpoint,
		SaveHeartbeatCheckpoint: func(saved string) error { *checkpoint = &saved; return nil },
	})
}

func TestEachOperationKindMapsToItsEffectAndStepOptions(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	require.Equal(t, "fake-ledger_get_balance", tools.getBalance.Name())
	require.Equal(t, dexagents.ToolEffectRead, tools.getBalance.Effect())
	require.Equal(t, "fake-ledger_create_payout", tools.payout.Name())
	require.Equal(t, dexagents.ToolEffectIdempotentWrite, tools.payout.Effect())
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, tools.note.Effect())
	require.Equal(t, "Read the balance of one ledger account.", tools.getBalance.Description())
	require.True(t, tools.payout.IsApprovalRecommended())
	require.False(t, tools.note.IsApprovalRecommended())
	for _, tool := range []*ConnectorTool{tools.getBalance, tools.payout, tools.note} {
		require.Equal(t, 30*time.Second, tool.ExecuteMethodTimeout())
		// The connector policy's one-hour window, narrowed to the tool Steps' three attempts.
		require.Equal(t, int32(3), tool.RetryPolicy().MaximumAttempts)
		require.Equal(t, time.Hour, tool.RetryPolicy().TotalDuration)
		require.Zero(t, tool.MaxSends())
	}
	require.Equal(t, fakeledger.Connection, tools.note.Connection())
	require.Equal(t, sdkgo.AgentOperationKindQuery, tools.getBalance.Kind())
}

func TestALimitedMutationKeepsItsEffectAndTakesASendCap(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, tools.sendMessage.Effect())
	require.Equal(t, int32(3), tools.sendMessage.MaxSends())
	// The operation's five attempts already reach the cap of 3.
	require.Equal(t, int32(5), tools.sendMessage.RetryPolicy().MaximumAttempts)
	older, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.SendMessage{Provider: tools.provider, MaxSends: -1}, fakeledger.Connection, fakeledger.SendMessageMetadata(-1))
	require.NoError(t, err)
	olderTool, err := NewConnectorTool(older)
	require.NoError(t, err)
	require.Zero(t, olderTool.MaxSends(), "a definition from before the tiers never resends")
	require.Equal(t, int32(3), olderTool.RetryPolicy().MaximumAttempts)
}

func TestAToolPolicysMaxSendsIsPinnedOnTheCallAndRaisesItsAttempts(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	arguments := `{"accountId": "a-1", "text": "hello"}`
	deferred := defer_(t, source, nil, "fake-ledger_send_message", arguments)
	require.Equal(t, `{"tool": "fake-ledger_send_message", "maxSends": 3}`, *deferred.ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, deferred.Policy.Effect)
	require.True(t, deferred.Ordered)
	five := int32(5)
	raised := defer_(t, source, map[string]dexagents.ToolPolicy{
		"fake-ledger_send_message": {Effect: dexagents.ToolEffectRead, MaxSends: &five},
	}, "fake-ledger_send_message", arguments)
	require.Equal(t, `{"tool": "fake-ledger_send_message", "maxSends": 5}`, *raised.ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, raised.Policy.Effect, "a session policy never changes the effect")
	options, err := source.ToolCallStepOptions(dexagents.AgentToolCall{ToolName: raised.ToolName, ToolSourceReference: raised.ToolSourceReference})
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, options.ExecuteMethodTimeout)
	require.Equal(t, int32(5), options.ExecuteRetry.MaximumAttempts)
	// The operation's 5 attempts plus the 2 sends the policy raised the cap by.
	require.Equal(t, int32(7), options.BoundedResendExecuteRetry.MaximumAttempts)
	one := int32(1)
	off := defer_(t, source, map[string]dexagents.ToolPolicy{
		"fake-ledger_send_message": {Effect: dexagents.ToolEffectNonIdempotentWrite, MaxSends: &one},
	}, "fake-ledger_send_message", arguments)
	options, err = source.ToolCallStepOptions(dexagents.AgentToolCall{ToolName: off.ToolName, ToolSourceReference: off.ToolSourceReference})
	require.NoError(t, err)
	require.Nil(t, options.BoundedResendExecuteRetry, "a cap of 1 keeps one attempt")
	note := defer_(t, source, map[string]dexagents.ToolPolicy{
		"fake-ledger_post_note": {Effect: dexagents.ToolEffectNonIdempotentWrite, MaxSends: &five},
	}, "fake-ledger_post_note", arguments)
	require.Equal(t, `{"tool": "fake-ledger_post_note"}`, *note.ToolSourceReference, "other tools ignore MaxSends")
	read := defer_(t, source, nil, "fake-ledger_get_balance", `{"accountId": "a-1"}`)
	require.False(t, read.Ordered, "reads run in parallel")
	options, err = source.ToolCallStepOptions(dexagents.AgentToolCall{ToolName: "gone", ToolSourceReference: ptr(`{"tool": "gone"}`)})
	require.NoError(t, err)
	require.Nil(t, options, "a tool this source no longer has gets the defaults")
}

func TestApprovalFollowsTheMetadataUnlessTheApplicationOrSessionOverridesIt(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	arguments := `{"accountId": "a-1", "amount": 9000}`
	require.True(t, *defer_(t, source, nil, "fake-ledger_create_payout", arguments).Policy.RequiresApproval)
	require.False(t, *defer_(t, source, nil, "fake-ledger_post_note", `{"accountId": "a-1", "text": "x"}`).Policy.RequiresApproval)
	no := false
	overridden := defer_(t, source, map[string]dexagents.ToolPolicy{
		"fake-ledger_create_payout": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: &no},
	}, "fake-ledger_create_payout", arguments)
	require.False(t, *overridden.Policy.RequiresApproval)
	require.Equal(t, dexagents.ToolEffectIdempotentWrite, overridden.Policy.Effect)

	application := newLedgerTools(t, WithRequiresApproval(false)).source(t)
	require.False(t, *defer_(t, application, nil, "fake-ledger_create_payout", arguments).Policy.RequiresApproval)

	predicate := newLedgerTools(t, WithApprovalPredicate(func(values map[string]any) bool {
		amount, _ := values["amount"].(float64)
		if amount == 4242 {
			panic("an unexpected amount")
		}
		return amount > 5000
	})).source(t)
	require.True(t, *defer_(t, predicate, nil, "fake-ledger_create_payout", arguments).Policy.RequiresApproval)
	require.False(t, *defer_(t, predicate, nil, "fake-ledger_create_payout", `{"accountId": "a-1", "amount": 10}`).Policy.RequiresApproval)
	require.False(t, *defer_(t, predicate, nil, "fake-ledger_create_payout", `{"accountId": "a-1"}`).Policy.RequiresApproval,
		"arguments that break the schema never run, so they never wait")
	require.False(t, *defer_(t, predicate, nil, "fake-ledger_create_payout", `not json`).Policy.RequiresApproval)
	require.True(t, *defer_(t, predicate, nil, "fake-ledger_create_payout", `{"accountId": "a-1", "amount": 4242}`).Policy.RequiresApproval,
		"a predicate that panics requires approval")
}

func TestToolsAndSourcesRefuseWhatTheCoreCannotHonour(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	_, err := NewConnectorToolSource([]*ConnectorTool{tools.getBalance, tools.getBalance})
	require.ErrorContains(t, err, "two connector tools share the name fake-ledger_get_balance")
	_, err = NewConnectorToolSource(nil, WithSourceName(" "))
	require.ErrorContains(t, err, "must not be blank")
	_, err = NewConnectorToolSource(nil, WithSourceMaxResultBytes(0))
	require.ErrorContains(t, err, "must be positive")
	_, err = NewConnectorTool(nil)
	require.Error(t, err)
	query, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		fakeledger.GetBalance{Provider: tools.provider}, fakeledger.Connection, fakeledger.GetBalanceMetadata())
	require.NoError(t, err)
	_, err = NewConnectorTool(query, WithMaxResultBytes(0))
	require.ErrorContains(t, err, "must be positive")
	metadata := fakeledger.GetBalanceMetadata()
	metadata.InputSchema = json.RawMessage(`{"type": "object", "properties": {"accountId": {"type": "string", "pattern": "^a"}}}`)
	patterned, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		fakeledger.GetBalance{Provider: tools.provider}, fakeledger.Connection, metadata)
	require.NoError(t, err)
	_, err = NewConnectorTool(patterned)
	require.EqualError(t, err, "fake-ledger getBalance: inputSchema.properties.accountId uses pattern, which agent tools do not check")
	named, err := NewConnectorTool(query, WithToolName("ledger_balance"), WithToolDescription("Read a balance."))
	require.NoError(t, err)
	require.Equal(t, "ledger_balance", named.Name())
	require.Equal(t, "Read a balance.", named.Description())
	source, err := NewConnectorToolSource([]*ConnectorTool{named}, WithSourceName("ledger"))
	require.NoError(t, err)
	require.Equal(t, "ledger", source.SourceName())
	require.Equal(t, []string{"ledger_balance"}, source.DeclaredToolNames())
	listed, err := source.ListTools(context.Background())
	require.NoError(t, err)
	require.Equal(t, []dexagents.SourceToolDefinition{{
		Name: "ledger_balance", Description: "Read a balance.", InputSchema: fakeledger.GetBalanceMetadata().InputSchema,
	}}, listed)
	require.Equal(t, listed, source.CachedTools())
	_, err = source.DeferredToolCall(dexagents.AgentTurnContext{}, dexagents.SourceToolCall{ToolName: "fake-ledger_get_balance"})
	require.ErrorContains(t, err, "is not configured on this Worker")
}

func TestEachBranchKindMapsToItsOutcomeWithTheSameTextAsTheOtherCores(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	for name, testCase := range map[string]struct {
		toolName  string
		arguments string
		output    string
		failure   string
		isUnknown bool
	}{
		"a found balance": {toolName: "fake-ledger_get_balance", arguments: `{"accountId": "a-1"}`,
			output: `{"accountId":"a-1","amount":1250}`},
		"a rejected read": {toolName: "fake-ledger_get_balance", arguments: `{"accountId": "missing"}`,
			failure: "fake-ledger getBalance returned notFound: NOT_FOUND: no such account"},
		"an unlisted query branch": {toolName: "fake-ledger_get_balance", arguments: `{"accountId": "invalid"}`,
			failure: "fake-ledger getBalance returned invalidResponse: PROTOCOL: the ledger answered nonsense"},
		"a rejected write": {toolName: "fake-ledger_post_note", arguments: `{"accountId": "a-1", "text": "reject"}`,
			failure: "fake-ledger postNote returned providerRejected: PROVIDER_REJECTION: account is closed"},
		"a paid payout": {toolName: "fake-ledger_create_payout", arguments: `{"accountId": "a-1", "amount": 5}`,
			output: `{"amount":5,"payoutId":"createPayout-1"}`},
		"arguments that break the schema": {toolName: "fake-ledger_get_balance", arguments: `{"accountId": 7}`,
			failure: "invalid arguments for fake-ledger_get_balance: arguments.accountId must be string"},
		"arguments that are not JSON": {toolName: "fake-ledger_get_balance", arguments: `{"accountId"`,
			failure: "invalid arguments for fake-ledger_get_balance: arguments are not valid JSON"},
		"arguments that are not an object": {toolName: "fake-ledger_get_balance", arguments: `[1]`,
			failure: "invalid arguments for fake-ledger_get_balance: expected a JSON object"},
		"arguments the input type refuses": {toolName: "fake-ledger_get_balance",
			arguments: `{"accountId": "a-1", "padding": 100000000000000000000000000000}`,
			failure:   "invalid arguments for fake-ledger_get_balance: arguments do not decode"},
		"a message whose read-back cannot confirm it": {toolName: "fake-ledger_send_message",
			arguments: `{"accountId": "a-1", "text": "hello"}`, isUnknown: true},
	} {
		t.Run(name, func(t *testing.T) {
			deferred := defer_(t, source, nil, testCase.toolName, testCase.arguments)
			stepContext := singledispatchtest.NewContext("session-"+strings.ReplaceAll(name, " ", "-"), "CallTool-3")
			var checkpoint *string
			if testCase.isUnknown {
				// A later attempt without a marker reads back, and the cap is spent.
				for range 3 {
					stepContext.AdvanceAttempt()
				}
			}
			outcome, err := runAttempt(t, source, deferred, stepContext, &checkpoint)
			switch {
			case testCase.isUnknown:
				var unknownError *dexagents.ToolOutcomeUnknownError
				require.ErrorAs(t, err, &unknownError)
				require.Equal(t, "fake-ledger sendMessage returned uncertain: TRANSPORT: read-back found no message", unknownError.Message)
			case testCase.failure != "":
				var toolError *dexagents.ToolExecutionError
				require.ErrorAs(t, err, &toolError)
				require.Equal(t, testCase.failure, toolError.Message)
			default:
				require.NoError(t, err)
				require.Equal(t, testCase.output, outcome.(*dexagents.ToolExecutionResult).Output)
			}
		})
	}
	require.Equal(t, 3, tools.provider.Calls("getBalance"), "refused arguments never reach the operation")
}

func TestALongResultReachesTheModelTruncatedAndSaysItsSize(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t, WithMaxResultBytes(40))
	source := tools.source(t)
	deferred := defer_(t, source, nil, "fake-ledger_get_balance", `{"accountId": "a-1", "padding": 100}`)
	var checkpoint *string
	outcome, err := runAttempt(t, source, deferred, singledispatchtest.NewContext("session-long", "CallTool-3"), &checkpoint)
	require.NoError(t, err)
	result := outcome.(*dexagents.ToolExecutionResult)
	full := `{"accountId":"a-1","amount":1250,"filler":"` + strings.Repeat("x", 100) + `"}`
	require.Equal(t, full[:40]+"\n[Dex] truncated, 145 bytes", result.Output)
	require.Equal(t, int64(len(full)), *result.TruncatedOutputBytes)
}

// A connector Retry fails the attempt for the tool Step's retry policy, with the connector's delay,
// and a later attempt maps its branch.
func TestAConnectorRetryFailsTheAttemptWithItsDelay(t *testing.T) {
	t.Parallel()
	definition := fakeledger.GetBalanceDefinition()
	mock := connectormock.NewQuery[fakeledger.GetBalanceInput, fakeledger.Balance](t, definition)
	busy := sdkgo.Failure{Kind: sdkgo.FailureRateLimit, Provider: fakeledger.ConnectorID, Operation: "getBalance", Message: "slow down"}
	mock.Respond(
		connectormock.Retry[fakeledger.Balance](busy, 2500*time.Millisecond),
		connectormock.Retry[fakeledger.Balance](busy, 0),
		connectormock.Branch[fakeledger.Balance]("found", fakeledger.Balance{AccountID: "a-1", Amount: 7}, nil),
	)
	agentTool, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		mock, fakeledger.Connection, fakeledger.GetBalanceMetadata())
	require.NoError(t, err)
	tool, err := NewConnectorTool(agentTool)
	require.NoError(t, err)
	source, err := NewConnectorToolSource([]*ConnectorTool{tool})
	require.NoError(t, err)
	deferred := defer_(t, source, nil, tool.Name(), `{"accountId": "a-1"}`)
	stepContext := singledispatchtest.NewContext("session-retry", "CallTool-3")
	var checkpoint *string

	_, err = runAttempt(t, source, deferred, stepContext, &checkpoint)
	var retryError *sdkgo.RetryError
	require.ErrorAs(t, err, &retryError)
	require.Equal(t, "connector fake-ledger getBalance retry (RATE_LIMIT): slow down", retryError.Error())
	var retryAfter *dex.RetryAfterError
	require.ErrorAs(t, err, &retryAfter)
	require.Equal(t, 2*time.Second, retryAfter.After, "Dex takes whole seconds")

	stepContext.AdvanceAttempt()
	_, err = runAttempt(t, source, deferred, stepContext, &checkpoint)
	require.ErrorAs(t, err, &retryError)
	require.False(t, errors.As(err, &retryAfter), "a retry without a delay uses the Step's backoff")

	stepContext.AdvanceAttempt()
	outcome, err := runAttempt(t, source, deferred, stepContext, &checkpoint)
	require.NoError(t, err)
	require.Equal(t, `{"accountId":"a-1","amount":7}`, outcome.(*dexagents.ToolExecutionResult).Output)
	calls := mock.Calls()
	require.Len(t, calls, 3)
	require.Equal(t, calls[0].CallID, calls[2].CallID, "every attempt of one tool Step keeps the Call ID")
}

// A Mutation's uncertain branch, defect, and unlisted branch map as the other cores map them, not
// as sdkgo's fence-aware outcome does.
func TestAMutationsBranchesMapByTheBranchAlone(t *testing.T) {
	t.Parallel()
	definition := fakeledger.PostNoteDefinition()
	lost := sdkgo.Failure{Kind: sdkgo.FailureTransport, Provider: fakeledger.ConnectorID, Operation: "postNote", Message: "timed out"}
	for name, testCase := range map[string]struct {
		response connectormock.Case[fakeledger.Stored]
		unknown  string
		failed   string
	}{
		"uncertain": {response: connectormock.Uncertain(fakeledger.Stored{}, lost),
			unknown: "fake-ledger postNote returned uncertain: TRANSPORT: timed out"},
		"defect": {response: connectormock.Branch[fakeledger.Stored](sdkgo.DefectBranchID, fakeledger.Stored{},
			&sdkgo.Failure{Kind: sdkgo.FailureLocalDefect, Provider: fakeledger.ConnectorID, Operation: "postNote", Message: "bug"}),
			failed: "fake-ledger postNote returned defect: LOCAL_DEFECT: bug"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := connectormock.NewMutation[fakeledger.TextInput, fakeledger.Stored](t, definition)
			mock.Respond(testCase.response)
			agentTool, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
				mock, fakeledger.Connection, fakeledger.PostNoteMetadata())
			require.NoError(t, err)
			tool, err := NewConnectorTool(agentTool)
			require.NoError(t, err)
			source, err := NewConnectorToolSource([]*ConnectorTool{tool})
			require.NoError(t, err)
			var checkpoint *string
			_, err = runAttempt(t, source, defer_(t, source, nil, tool.Name(), `{"accountId": "a-1", "text": "x"}`),
				singledispatchtest.NewContext("session-"+name, "CallSingleAttemptTool-3"), &checkpoint)
			if testCase.unknown != "" {
				var unknownError *dexagents.ToolOutcomeUnknownError
				require.ErrorAs(t, err, &unknownError)
				require.Equal(t, testCase.unknown, unknownError.Message)
				return
			}
			var toolError *dexagents.ToolExecutionError
			require.ErrorAs(t, err, &toolError)
			require.Equal(t, testCase.failed, toolError.Message)
		})
	}
}

// The fence's markers survive as the tool Step's text checkpoint: a lost response resends once,
// a definite refusal is never resent, and the cap ends unconfirmed sends as outcome_unknown.
func TestALimitedWriteResendsWithinItsCapAndNeverAfterADefiniteRefusal(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	deferred := defer_(t, source, nil, "fake-ledger_send_message", `{"accountId": "a-1", "text": "lose-first"}`)
	stepContext := singledispatchtest.NewContext("session-resend", "CallSingleAttemptTool-4")
	var checkpoint *string
	_, err := runAttempt(t, source, deferred, stepContext, &checkpoint)
	var retryError *sdkgo.RetryError
	require.ErrorAs(t, err, &retryError)
	require.NotNil(t, checkpoint, "the fence's claim is the tool Step's text checkpoint")
	var marker map[string]any
	require.NoError(t, json.Unmarshal([]byte(*checkpoint), &marker))
	require.Contains(t, marker, "singleDispatchMarkers")
	stepContext.AdvanceAttempt()
	outcome, err := runAttempt(t, source, deferred, stepContext, &checkpoint)
	require.NoError(t, err)
	require.Equal(t, `{"id":"sendMessage-2"}`, outcome.(*dexagents.ToolExecutionResult).Output)
	require.Len(t, tools.provider.Writes("sendMessage"), 2, "one lost send and one resend")

	refused := defer_(t, source, nil, "fake-ledger_send_message", `{"accountId": "a-1", "text": "reject-on-send"}`)
	var refusedCheckpoint *string
	_, err = runAttempt(t, source, refused, singledispatchtest.NewContext("session-refused", "CallSingleAttemptTool-4"), &refusedCheckpoint)
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, err, &toolError)
	require.Equal(t, "fake-ledger sendMessage returned providerRejected: PROVIDER_REJECTION: message refused", toolError.Message)

	capped := defer_(t, source, nil, "fake-ledger_send_message", `{"accountId": "a-1", "text": "lose-always"}`)
	cappedContext := singledispatchtest.NewContext("session-capped", "CallSingleAttemptTool-4")
	var cappedCheckpoint *string
	outcomes := []string{}
	for attempt := 1; attempt <= 5; attempt++ {
		if attempt > 1 {
			cappedContext.AdvanceAttempt()
		}
		outcome, err := runAttempt(t, source, capped, cappedContext, &cappedCheckpoint)
		outcomes = append(outcomes, describeOutcome(t, outcome, err))
		if outcomes[len(outcomes)-1] != "retry" {
			break
		}
	}
	require.Equal(t, []string{"retry", "retry", "retry", "outcome_unknown"}, outcomes)
	require.Equal(t, 2+1+3, tools.provider.Sends("sendMessage"), "the resent, refused, and capped calls' sends")
}

func TestTheStepContextKeepsHeartbeatValuesAsTheCheckpointText(t *testing.T) {
	t.Parallel()
	stepContext := singledispatchtest.NewContext("session-1", "CallTool-1")
	heartbeats := 0
	var saved *string
	connectorContext := newConnectorStepContext(stepContext, dexagents.DeferredToolExecution{
		Heartbeat:               func() error { heartbeats++; return nil },
		SaveHeartbeatCheckpoint: func(checkpoint string) error { saved = &checkpoint; return nil },
	})
	isFound, err := connectorContext.GetLastHeartbeatValue(&map[string]any{})
	require.NoError(t, err)
	require.False(t, isFound)
	require.NoError(t, connectorContext.RecordHeartbeat(map[string]any{"b": 1, "a": "é"}))
	require.Equal(t, "{\"a\":\"\\u00e9\",\"b\":1}", *saved)
	require.NoError(t, connectorContext.RecordHeartbeat(nil))
	var typedNil *singledispatch.Marker
	require.NoError(t, connectorContext.RecordHeartbeat(typedNil))
	require.Equal(t, 2, heartbeats, "a nil value repeats the saved checkpoint")

	next := newConnectorStepContext(stepContext, dexagents.DeferredToolExecution{HeartbeatCheckpoint: saved})
	var read map[string]any
	isFound, err = next.GetLastHeartbeatValue(&read)
	require.NoError(t, err)
	require.True(t, isFound)
	require.Equal(t, map[string]any{"a": "é", "b": float64(1)}, read)
	require.Error(t, next.RecordHeartbeat("value"), "a Step that cannot save checkpoints refuses a value")
	opaque := "opaque checkpoint"
	_, err = newConnectorStepContext(stepContext, dexagents.DeferredToolExecution{HeartbeatCheckpoint: &opaque}).
		GetLastHeartbeatValue(&read)
	require.Error(t, err, "a checkpoint that is not JSON does not decode")
	require.Equal(t, "session-1", next.FlowID())
	require.Equal(t, "CallTool-1", next.StepExecutionID())
}

func TestACallOutsideAToolStepOrOfAGoneToolFailsTheAttempt(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	deferred := defer_(t, source, nil, "fake-ledger_get_balance", `{"accountId": "a-1"}`)
	_, err := source.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
		ToolCall: dexagents.AgentToolCall{ToolName: deferred.ToolName, Arguments: deferred.Arguments, ToolSourceReference: deferred.ToolSourceReference},
	})
	require.EqualError(t, err, "a connector tool call needs the tool Step's dex.Context")
	var checkpoint *string
	gone := dexagents.DeferredToolCall{ToolName: "fake-ledger_close_account", ToolSourceReference: ptr(`{"tool": "fake-ledger_close_account"}`)}
	_, err = runAttempt(t, source, gone, singledispatchtest.NewContext("session-gone", "CallTool-1"), &checkpoint)
	require.EqualError(t, err, "connector tool fake-ledger_close_account is not configured on this Worker")
	var toolError *dexagents.ToolExecutionError
	require.False(t, errors.As(err, &toolError), "the effect class decides a removed tool's outcome")
}

type derivedContextKey struct{}

// A source runs a call under the execution's StepContext, even when ctx is a derived context.
func TestACallWithADerivedContextRunsWithTheExecutionsStepContext(t *testing.T) {
	t.Parallel()
	tools := newLedgerTools(t)
	source := tools.source(t)
	deferred := defer_(t, source, nil, "fake-ledger_send_message", `{"accountId": "a-1", "text": "hello"}`)
	record := dexagents.AgentToolCall{
		ToolCallID: deferred.ToolCallID, TurnID: "turn-1", ToolName: deferred.ToolName, Arguments: deferred.Arguments,
		Effect: deferred.Policy.Effect, Status: dexagents.ToolCallStatusApproved, ToolSourceReference: deferred.ToolSourceReference,
	}
	stepContext := singledispatchtest.NewContext("session-derived", "CallSingleAttemptTool-1")
	derived := context.WithValue(stepContext, derivedContextKey{}, "span")
	var checkpoint *string
	execution := dexagents.DeferredToolExecution{
		FlowID: stepContext.FlowID(), ToolCall: record, Attempt: 1,
		Heartbeat: func() error { return nil }, HeartbeatCheckpoint: checkpoint,
		SaveHeartbeatCheckpoint: func(saved string) error { checkpoint = &saved; return nil },
	}

	_, err := source.ExecuteDeferredToolCall(derived, execution)
	require.EqualError(t, err, "a connector tool call needs the tool Step's dex.Context", "a derived ctx alone names no Step")
	require.Zero(t, tools.provider.Sends("sendMessage"))

	execution.StepContext = stepContext
	outcome, err := source.ExecuteDeferredToolCall(derived, execution)
	require.NoError(t, err)
	require.Equal(t, `{"id":"sendMessage-1"}`, outcome.(*dexagents.ToolExecutionResult).Output)
	require.NotNil(t, checkpoint, "the fence kept its marker in the Step's checkpoint")
	require.Equal(t, 1, tools.provider.Sends("sendMessage"))
}

func ptr[Value any](value Value) *Value {
	return &value
}

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
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ConnectorToolSourceOption configures one ConnectorToolSource. Pass options to
// NewConnectorToolSource.
//
// Since 0.6.16.
type ConnectorToolSourceOption interface {
	applyConnectorToolSource(options *connectorToolSourceOptions)
}

type connectorToolSourceOptions struct {
	sourceName     string
	maxResultBytes int
}

type connectorToolSourceOptionFunc func(options *connectorToolSourceOptions)

func (option connectorToolSourceOptionFunc) applyConnectorToolSource(options *connectorToolSourceOptions) {
	option(options)
}

// WithSourceName sets the stable name stored with each call. The default is
// DefaultConnectorSourceName. Keep it across deploys: a stored call whose source name no Worker has
// fails by its effect class.
//
// Since 0.6.16.
func WithSourceName(sourceName string) ConnectorToolSourceOption {
	return connectorToolSourceOptionFunc(func(options *connectorToolSourceOptions) {
		options.sourceName = sourceName
	})
}

// WithSourceMaxResultBytes sets the default bound on each call's result, in bytes of UTF-8 text. The
// default is DefaultConnectorResultMaxBytes. It must be positive.
//
// Since 0.6.16.
func WithSourceMaxResultBytes(maxResultBytes int) ConnectorToolSourceOption {
	return connectorToolSourceOptionFunc(func(options *connectorToolSourceOptions) {
		options.maxResultBytes = maxResultBytes
	})
}

// ConnectorToolSource offers reviewed Connector operations to an agent as tools.
//
// It implements dexagents.ToolSource, dexagents.CachedToolSource,
// dexagents.DeclaredToolNameSource, and dexagents.ToolCallStepOptionsProvider.
// Pass it to builtinloop.WithToolSources. It lists exactly the tools the
// application passes; no Connector is exposed by default. Each call runs one
// operation invocation in its own tool Step through
// sdkgo.AgentTool.RunAttemptWithOptions, so the connector's Call ID,
// idempotency key, credential handling, and single-dispatch fence apply. The
// Call ID derives from the Flow ID and the tool Step's execution ID, so it is
// the same on every attempt of that Step, on a Worker of any core.
//
// Before anything runs, the model's arguments are checked against the
// InputSchema and then decoded into the operation's input type. Either
// failure is a failed call the model can correct; the operation never runs.
//
// Connector writes of one model response run one at a time in the model's
// order, and reads run in parallel. A write whose approval is needed waits for
// it only when its turn comes. Once a write does not succeed, the later
// writes of that response are recorded failed with AgentToolCall.Skipped set.
//
// The branch the operation selects maps as ClassifyConnectorBranch describes:
// a succeeded call returns the result value as JSON text, a rejected or
// failed branch returns a *dexagents.ToolExecutionError with "<connector>
// <operation> returned <branch>: <kind>: <message>", and an unknown branch a
// *dexagents.ToolOutcomeUnknownError with the same text. A connector Retry
// fails the attempt with an *sdkgo.RetryError, wrapped in dex.RetryAfter when
// it asks for a delay of a second or more, so the tool Step's retry policy
// applies. For a bounded resend, the next attempt's fence lets the connector
// read back and resend while the call's sends stay below its cap. A read-back
// that confirms an earlier send succeeds. At the cap, an unconfirmed write
// selects uncertain, so the call records outcome_unknown. When the attempts
// run out, a Query records failed and a Mutation outcome_unknown, so a
// single-attempt write that asks to retry records outcome_unknown and stops
// the later writes. A result longer than the bound reaches the model
// truncated, with ConnectorResultTruncatedNote, and the record's
// TruncatedOutputBytes holds the full size.
//
// A call that needs approval carries no approval display yet, so an approval
// UI shows its arguments.
//
// Since 0.6.16.
type ConnectorToolSource struct {
	sourceName     string
	maxResultBytes int
	tools          []*ConnectorTool
	toolsByName    map[string]*ConnectorTool
}

// NewConnectorToolSource creates the source of tools, in the order the model sees them.
//
// It returns an error when a tool is nil, two tools share a name, the source
// name is blank, or the result bound is not positive.
//
// Since 0.6.16.
func NewConnectorToolSource(tools []*ConnectorTool, options ...ConnectorToolSourceOption) (*ConnectorToolSource, error) {
	configured := connectorToolSourceOptions{sourceName: DefaultConnectorSourceName, maxResultBytes: DefaultConnectorResultMaxBytes}
	for _, option := range options {
		option.applyConnectorToolSource(&configured)
	}
	if strings.TrimSpace(configured.sourceName) == "" {
		return nil, errors.New("connector tool source name must not be blank")
	}
	if configured.maxResultBytes < 1 {
		return nil, errors.New("max result bytes must be positive")
	}
	source := &ConnectorToolSource{
		sourceName:     configured.sourceName,
		maxResultBytes: configured.maxResultBytes,
		toolsByName:    make(map[string]*ConnectorTool, len(tools)),
	}
	for index, tool := range tools {
		if tool == nil {
			return nil, fmt.Errorf("connector tool %d is nil", index)
		}
		if _, isTaken := source.toolsByName[tool.name]; isTaken {
			return nil, fmt.Errorf("two connector tools share the name %s", tool.name)
		}
		source.toolsByName[tool.name] = tool
		source.tools = append(source.tools, tool)
	}
	return source, nil
}

// SourceName returns the source's stable name.
//
// Since 0.6.16.
func (source *ConnectorToolSource) SourceName() string { return source.sourceName }

// ListTools returns the tools; the list is fixed when the source is built, so it never fails.
//
// Since 0.6.16.
func (source *ConnectorToolSource) ListTools(context.Context) ([]dexagents.SourceToolDefinition, error) {
	return source.CachedTools(), nil
}

// CachedTools returns the tools without I/O.
//
// Since 0.6.16.
func (source *ConnectorToolSource) CachedTools() []dexagents.SourceToolDefinition {
	definitions := make([]dexagents.SourceToolDefinition, 0, len(source.tools))
	for _, tool := range source.tools {
		definitions = append(definitions, dexagents.SourceToolDefinition{
			Name: tool.name, Description: tool.description, InputSchema: tool.InputSchema(),
		})
	}
	return definitions
}

// DeclaredToolNames returns every tool name, so a runner refuses a collision when it is built.
//
// Since 0.6.16.
func (source *ConnectorToolSource) DeclaredToolNames() []string {
	names := make([]string, 0, len(source.tools))
	for _, tool := range source.tools {
		names = append(names, tool.name)
	}
	return names
}

// DeferredToolCall builds the call with the metadata's effect and the call's approval.
//
// A session tool policy for the tool changes only whether the call waits for
// approval and, for a tool with MaxSends, the send cap the call pins. The
// reference is {"tool": "<name>"}, or {"tool": "<name>", "maxSends": <n>} for
// a tool with MaxSends, written as the other cores write it. A write is
// ordered. It returns an error when the source has no tool of that name.
//
// Since 0.6.16.
func (source *ConnectorToolSource) DeferredToolCall(
	turn dexagents.AgentTurnContext,
	call dexagents.SourceToolCall,
) (dexagents.DeferredToolCall, error) {
	tool, isKnown := source.toolsByName[call.ToolName]
	if !isKnown {
		return dexagents.DeferredToolCall{}, fmt.Errorf("connector tool %s is not configured on this Worker", call.ToolName)
	}
	sessionPolicy, hasSessionPolicy := turn.ToolPolicies[call.ToolName]
	var requiresApproval bool
	if hasSessionPolicy && sessionPolicy.RequiresApproval != nil {
		requiresApproval = *sessionPolicy.RequiresApproval
	} else {
		requiresApproval = tool.isApprovalRequired(call.Arguments)
	}
	// The reference pins the call's cap, so every attempt, across deploys, passes the same one.
	reference := pythonjson.Object{{Key: "tool", Value: tool.name}}
	if tool.maxSends != 0 {
		callMaxSends := tool.maxSends
		if hasSessionPolicy && sessionPolicy.MaxSends != nil && isConnectorMaxSends(int64(*sessionPolicy.MaxSends)) {
			callMaxSends = *sessionPolicy.MaxSends
		}
		reference = append(reference, pythonjson.Member{Key: "maxSends", Value: callMaxSends})
	}
	referenceText, err := pythonjson.Marshal(reference, pythonjson.Options{ShouldEscapeNonASCII: true})
	if err != nil {
		return dexagents.DeferredToolCall{}, err
	}
	return dexagents.DeferredToolCall{
		ToolCallID:          call.ToolCallID,
		ToolName:            call.ToolName,
		Arguments:           call.Arguments,
		Policy:              dexagents.ToolPolicy{Effect: tool.effect, RequiresApproval: &requiresApproval},
		ToolSourceReference: &referenceText,
		Ordered:             tool.effect != dexagents.ToolEffectRead,
	}, nil
}

// ToolCallStepOptions returns the operation's timeout and retry policy for the call's tool Step.
//
// A call that pinned a send cap of 2 or more also gets
// BoundedResendExecuteRetry, while this Worker's tool still has a send cap:
// the operation's attempts, at least the tool's cap, plus the amount by which
// the pinned cap exceeds it. It returns nil when this source no longer has
// the tool.
//
// Since 0.6.16.
func (source *ConnectorToolSource) ToolCallStepOptions(toolCall dexagents.AgentToolCall) (*dexagents.ToolCallStepOptions, error) {
	tool, isKnown := source.toolsByName[referenceToolName(toolCall)]
	if !isKnown {
		return nil, nil
	}
	options := &dexagents.ToolCallStepOptions{
		ExecuteMethodTimeout: tool.executeMethodTimeout,
		ExecuteRetry:         tool.RetryPolicy(),
	}
	if callMaxSends := referenceMaxSends(toolCall); tool.maxSends != 0 && callMaxSends >= 2 {
		bounded := tool.RetryPolicy()
		bounded.MaximumAttempts = boundedResendAttempts(bounded.MaximumAttempts, tool.maxSends, callMaxSends)
		options.BoundedResendExecuteRetry = bounded
	}
	return options, nil
}

// ExecuteDeferredToolCall checks, decodes, and runs one call, then maps the selected branch.
//
// ctx must be the tool Step's dex.Context, which the session passes to the
// runner. It returns the result value as JSON text, bounded by the result
// bound. It returns a *dexagents.ToolExecutionError when the arguments break
// the schema or do not decode, or the operation selected a rejected or failed
// branch, and a *dexagents.ToolOutcomeUnknownError when it selected an unknown
// branch. Any other error fails the attempt, so the tool Step's retry policy
// and then the call's effect decide: an *sdkgo.RetryError for a connector
// Retry, an error when this source no longer has the tool or ctx is not a
// dex.Context, and the operation's panic or result encoding error.
//
// Since 0.6.16.
func (source *ConnectorToolSource) ExecuteDeferredToolCall(
	ctx context.Context,
	call dexagents.DeferredToolExecution,
) (dexagents.DeferredToolOutcome, error) {
	record := call.ToolCall
	tool, isKnown := source.toolsByName[referenceToolName(record)]
	if !isKnown {
		return nil, fmt.Errorf("connector tool %s is not configured on this Worker", record.ToolName)
	}
	values, err := decodeArgumentsObject(record.Arguments)
	if err != nil {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("invalid arguments for %s: %s", tool.name, err.Error())}
	}
	if problem := tool.inputSchema.findInputViolation(values); problem != "" {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("invalid arguments for %s: %s", tool.name, problem)}
	}
	stepContext, isStepContext := ctx.(dex.Context)
	if !isStepContext || stepContext == nil {
		return nil, errors.New("a connector tool call needs the tool Step's dex.Context")
	}
	var runOptions []sdkgo.RunOption
	if tool.isDefinitionLimited {
		// A pinned cap holds after a deploy that changes the metadata; a call without one never resends.
		maxSends := referenceMaxSends(record)
		if maxSends == 0 {
			maxSends = 1
		}
		runOptions = append(runOptions, sdkgo.WithMaxSends(maxSends))
	}
	attempt, err := tool.agentTool.RunAttemptWithOptions(newConnectorStepContext(stepContext, call),
		json.RawMessage(argumentsOrEmptyObject(record.Arguments)), runOptions...)
	if errors.Is(err, sdkgo.ErrAgentToolArgumentsInvalid) {
		// A fixed text: decoder messages differ between languages and versions.
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("invalid arguments for %s: arguments do not decode", tool.name)}
	}
	if err != nil {
		return nil, err
	}
	if attempt.Outcome == sdkgo.AgentToolAttemptRetry {
		return nil, connectorRetryError(tool.metadata.Operation, attempt)
	}
	switch ClassifyConnectorBranch(attempt.Branch, tool.requiredBranches, tool.metadata.BranchOutcomes) {
	case ConnectorBranchOutcomeSucceeded:
		output, err := FormatConnectorResult(string(attempt.Value))
		if err != nil {
			return nil, fmt.Errorf("%s %s result value: %w", tool.metadata.Operation.ConnectorID, tool.metadata.Operation.OperationID, err)
		}
		maxResultBytes := source.maxResultBytes
		if tool.maxResultBytes != 0 {
			maxResultBytes = tool.maxResultBytes
		}
		return BoundConnectorResult(output, maxResultBytes), nil
	case ConnectorBranchOutcomeUnknown:
		return nil, &dexagents.ToolOutcomeUnknownError{Message: branchDetail(tool.metadata.Operation, attempt.Branch, attempt.Failure)}
	default:
		return nil, &dexagents.ToolExecutionError{Message: branchDetail(tool.metadata.Operation, attempt.Branch, attempt.Failure)}
	}
}

// connectorRetryError asks Dex to retry the tool Step, after the connector's delay in whole seconds.
func connectorRetryError(operation sdkgo.OperationRef, attempt sdkgo.AgentToolAttempt) error {
	failure := sdkgo.Failure{Kind: sdkgo.FailureLocalDefect, Provider: operation.ConnectorID, Operation: operation.OperationID,
		Message: "the connector asked to retry"}
	if attempt.Failure != nil {
		failure = *attempt.Failure
	}
	retryError := &sdkgo.RetryError{Failure: failure}
	// Dex takes whole seconds; a shorter delay uses the retry policy's backoff, as the other cores do.
	if delay := attempt.RetryAfter.Truncate(time.Second); delay >= time.Second {
		return dex.RetryAfter(delay, retryError)
	}
	return retryError
}

// argumentsOrEmptyObject reads empty arguments as {}, as the other cores do.
func argumentsOrEmptyObject(arguments string) string {
	if arguments == "" {
		return "{}"
	}
	return arguments
}

// decodeArgumentsObject parses the arguments; its error is the model-visible reason.
func decodeArgumentsObject(arguments string) (pythonjson.Object, error) {
	parsed, err := pythonjson.ParseOrdered([]byte(argumentsOrEmptyObject(arguments)))
	if err != nil {
		return nil, errors.New("arguments are not valid JSON")
	}
	values, isObject := parsed.(pythonjson.Object)
	if !isObject {
		return nil, errors.New("expected a JSON object")
	}
	return values, nil
}

// referenceToolName returns the tool name the call pinned, or the record's tool name.
func referenceToolName(record dexagents.AgentToolCall) string {
	var reference struct {
		Tool *string `json:"tool"`
	}
	if record.ToolSourceReference == nil || json.Unmarshal([]byte(*record.ToolSourceReference), &reference) != nil || reference.Tool == nil {
		return record.ToolName
	}
	return *reference.Tool
}

// referenceMaxSends returns the send cap the call pinned when it was deferred, or 0.
func referenceMaxSends(record dexagents.AgentToolCall) int32 {
	if record.ToolSourceReference == nil {
		return 0
	}
	parsed, err := pythonjson.ParseOrdered([]byte(*record.ToolSourceReference))
	if err != nil {
		return 0
	}
	reference, isObject := parsed.(pythonjson.Object)
	if !isObject {
		return 0
	}
	value, _ := member(reference, "maxSends")
	number, isNumber := value.(json.Number)
	if !isNumber || !isIntegerLiteral(number) {
		return 0
	}
	maxSends, err := number.Int64()
	if err != nil || !isConnectorMaxSends(maxSends) {
		return 0
	}
	return int32(maxSends)
}

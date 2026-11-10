// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatch"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	metaKeyIdempotencyKey = "io.superdurable.dex/idempotencyKey"
	metaKeyEffect         = "io.superdurable.dex/effect"
	metaKeyApproval       = "io.superdurable.dex/approval"
	metaKeyConnector      = "io.superdurable.dex/connector"
	metaKeyOperation      = "io.superdurable.dex/operation"
	metaKeyConnection     = "io.superdurable.dex/connection"
	metaKeyOutcome        = "io.superdurable.dex/outcome"
	metaKeyBranch         = "io.superdurable.dex/branch"
	metaKeyReplayed       = "io.superdurable.dex/replayed"

	errorDataKeyRetryable    = "io.superdurable.dex/retryable"
	errorDataKeyRetryAfterMs = "io.superdurable.dex/retryAfterMs"
	errorDataKeyNotSent      = "io.superdurable.dex/notSent"

	// localFlowID is the Flow ID of every local Step context; the idempotency key is its Step execution ID.
	localFlowID = "dexcli-connectors-mcp"
	// defaultExecuteTimeout bounds an operation whose definition sets no Execute timeout.
	defaultExecuteTimeout   = 60 * time.Second
	maximumIdempotencyBytes = 1024
)

// toolCallOutcome is the `_meta` outcome of an error result.
type toolCallOutcome string

const (
	toolCallOutcomeSucceeded toolCallOutcome = "succeeded"
	toolCallOutcomeRejected  toolCallOutcome = "rejected"
	toolCallOutcomeUnknown   toolCallOutcome = "unknown"
)

// callStateNamespace derives a call's state file name from its idempotency key.
var callStateNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://superdurable.dev/dexcli/connectors-mcp/call-state/v1"))

// toolCallExecutor runs tool calls. Keyed Mutations keep their fence state in store, so a retry never sends twice.
type toolCallExecutor struct {
	store  *callStateStore
	logger *slog.Logger
}

func newToolCallExecutor(store *callStateStore, logger *slog.Logger) *toolCallExecutor {
	if store == nil || logger == nil {
		panic("tool call executor dependencies must not be nil")
	}
	return &toolCallExecutor{store: store, logger: logger}
}

// storedToolResult is a terminal tool result as the call state keeps it.
type storedToolResult struct {
	IsError    bool            `json:"isError"`
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured,omitempty"`
	Outcome    toolCallOutcome `json:"outcome"`
	Branch     string          `json:"branch,omitempty"`
}

func (executor *toolCallExecutor) handler(binding *toolBinding) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return executor.callTool(ctx, binding, request)
	}
}

// callTool validates the arguments, runs the operation once per idempotency key, and maps its branch.
func (executor *toolCallExecutor) callTool(ctx context.Context, binding *toolBinding, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, problem := checkToolArguments(binding, request.Params.Arguments)
	if problem != "" {
		return newToolResult(rejectedResult(fmt.Sprintf("invalid arguments for %s: %s", binding.name, problem)), false), nil
	}
	idempotencyKey, hasKey, problem := idempotencyKeyOf(request.Params.Meta)
	if problem != "" {
		return newToolResult(rejectedResult(problem), false), nil
	}
	if !hasKey {
		return executor.callWithoutKey(ctx, binding, arguments)
	}
	if binding.runner.kind() == operationKindQuery {
		return executor.callInMemory(ctx, binding, arguments, idempotencyKey, true)
	}
	return executor.callKeyedMutation(ctx, binding, arguments, idempotencyKey)
}

// callWithoutKey runs a call no retry can repeat with the same Call ID, so a connector Retry is final.
func (executor *toolCallExecutor) callWithoutKey(ctx context.Context, binding *toolBinding, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	return executor.callInMemory(ctx, binding, arguments, uuid.NewString(), false)
}

func (executor *toolCallExecutor) callInMemory(
	ctx context.Context,
	binding *toolBinding,
	arguments json.RawMessage,
	stepExecutionID string,
	isRetryable bool,
) (*mcp.CallToolResult, error) {
	stepContext, cancel := newOperationContext(ctx, binding, stepExecutionID, 1, nil, func(json.RawMessage) error { return nil })
	defer cancel()
	result, err := binding.runner.run(stepContext, binding.reference, arguments)
	if err != nil {
		return executor.mapRunError(binding, stepContext, err, isRetryable)
	}
	return newToolResult(mapOperationResult(binding, result), false), nil
}

// callKeyedMutation runs a Mutation under the lock of its key with durable fence state.
func (executor *toolCallExecutor) callKeyedMutation(
	ctx context.Context,
	binding *toolBinding,
	arguments json.RawMessage,
	idempotencyKey string,
) (*mcp.CallToolResult, error) {
	stateKey := uuid.NewSHA1(callStateNamespace, []byte(idempotencyKey)).String()
	unlock, err := executor.store.lock(stateKey)
	if err != nil {
		return nil, retryableError(binding, "call state is unavailable; nothing was sent", false, 0)
	}
	defer func() {
		if err := unlock(); err != nil {
			executor.logger.Warn("connector call state lock was not released cleanly", "tool", binding.name, "error", err)
		}
	}()
	record, found, err := executor.store.read(stateKey)
	if err != nil {
		executor.logger.Error("connector call state cannot be read", "tool", binding.name, "error", err)
		return nil, retryableError(binding, "call state is unreadable; nothing was sent", false, 0)
	}
	digest := requestDigest(binding.name, arguments)
	if found && record.RequestDigest != digest {
		return newToolResult(rejectedResult("the idempotency key was already used for a different tool call; nothing was sent"), false), nil
	}
	if found && len(record.Result) > 0 {
		var stored storedToolResult
		if err := json.Unmarshal(record.Result, &stored); err != nil {
			return nil, fmt.Errorf("decode stored tool result: %w", err)
		}
		return newToolResult(stored, true), nil
	}
	record.ConnectorID = binding.connectorID
	record.OperationID = binding.operation.manifest.OperationID
	record.ConnectionName = binding.connectionName
	record.RequestDigest = digest
	record.Attempt++
	if err := executor.store.write(stateKey, record); err != nil {
		executor.logger.Error("connector call state cannot be written", "tool", binding.name, "error", err)
		return nil, retryableError(binding, "call state is unwritable; nothing was sent", false, 0)
	}
	persistHeartbeat := func(heartbeat json.RawMessage) error {
		record.Heartbeat = heartbeat
		return executor.store.write(stateKey, record)
	}
	// A client timeout or disconnect must not cancel a send in flight.
	stepContext, cancel := newOperationContext(context.WithoutCancel(ctx), binding, idempotencyKey, record.Attempt, record.Heartbeat, persistHeartbeat)
	defer cancel()
	result, runErr := binding.runner.run(stepContext, binding.reference, arguments)
	if runErr != nil && (isConnectorRetry(runErr) || errors.Is(runErr, errArgumentsDoNotDecode)) {
		return executor.mapRunError(binding, stepContext, runErr, true)
	}
	var stored storedToolResult
	if runErr != nil {
		executor.logger.Warn("connector write failed without a branch; its outcome is unknown", "tool", binding.name, "error", runErr)
		stored = storedToolResult{IsError: true, Outcome: toolCallOutcomeUnknown, Text: fmt.Sprintf("%s outcome is unknown: %v", operationLabel(binding), runErr)}
	} else {
		stored = mapOperationResult(binding, result)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	record.Result = encoded
	if err := executor.store.write(stateKey, record); err != nil {
		// A retry then reads the claimed checkpoint and never sends again.
		executor.logger.Warn("connector call result was not stored", "tool", binding.name, "error", err)
	}
	return newToolResult(stored, false), nil
}

func newOperationContext(
	ctx context.Context,
	binding *toolBinding,
	stepExecutionID string,
	attempt int32,
	previousHeartbeat json.RawMessage,
	persistHeartbeat func(json.RawMessage) error,
) (*localStepContext, context.CancelFunc) {
	timeout := binding.runner.stepDefaults().ExecuteMethodTimeout
	if timeout <= 0 {
		timeout = defaultExecuteTimeout
	}
	attemptContext, cancel := context.WithTimeout(ctx, timeout)
	return newLocalStepContext(attemptContext, localFlowID, stepExecutionID, attempt, previousHeartbeat, persistHeartbeat), cancel
}

// mapRunError maps an error without a branch. A connector Retry becomes an error the client may repeat with the key.
func (executor *toolCallExecutor) mapRunError(binding *toolBinding, stepContext *localStepContext, err error, isRetryable bool) (*mcp.CallToolResult, error) {
	if errors.Is(err, errArgumentsDoNotDecode) {
		return newToolResult(rejectedResult(fmt.Sprintf("invalid arguments for %s: %v", binding.name, err)), false), nil
	}
	var retry *sdkgo.RetryError
	if !errors.As(err, &retry) {
		if binding.runner.kind() == operationKindMutation {
			executor.logger.Warn("connector write failed without a branch; its outcome is unknown", "tool", binding.name, "error", err)
			return newToolResult(storedToolResult{IsError: true, Outcome: toolCallOutcomeUnknown, Text: fmt.Sprintf("%s outcome is unknown: %v", operationLabel(binding), err)}, false), nil
		}
		return nil, retryableError(binding, err.Error(), true, 0)
	}
	isNotSent := binding.runner.kind() == operationKindQuery || isProvenNotSent(stepContext)
	detail := fmt.Sprintf("%s asked to retry: %s: %s", operationLabel(binding), retry.Failure.Kind, retry.Failure.Message)
	if isRetryable {
		var retryAfter *dex.RetryAfterError
		delay := time.Duration(0)
		if errors.As(err, &retryAfter) {
			delay = retryAfter.After
		}
		return nil, retryableError(binding, detail, isNotSent, delay)
	}
	if isNotSent {
		return newToolResult(rejectedResult(detail+"; nothing was sent"), false), nil
	}
	return newToolResult(storedToolResult{IsError: true, Outcome: toolCallOutcomeUnknown, Text: detail}, false), nil
}

// mapOperationResult maps a selected branch: success, or an error result whose outcome says if it took effect.
func mapOperationResult(binding *toolBinding, result operationResult) storedToolResult {
	outcome := classifyBranch(binding, result.branch)
	if outcome == toolCallOutcomeSucceeded {
		text, structured := encodeResultValue(result.value)
		return storedToolResult{Text: text, Structured: structured, Outcome: outcome, Branch: string(result.branch)}
	}
	detail := fmt.Sprintf("%s returned %s", operationLabel(binding), result.branch)
	if result.failure != nil {
		detail += fmt.Sprintf(": %s: %s", result.failure.Kind, result.failure.Message)
	}
	return storedToolResult{IsError: true, Text: detail, Outcome: outcome, Branch: string(result.branch)}
}

// classifyBranch applies the manifest's agent.branches first and fails closed for an unlisted Mutation branch.
func classifyBranch(binding *toolBinding, branch sdkgo.BranchID) toolCallOutcome {
	if binding.requiredBranches[branch] {
		return toolCallOutcomeSucceeded
	}
	switch binding.operation.manifest.Agent.Branches[string(branch)] {
	case branchClassificationRejected:
		return toolCallOutcomeRejected
	case branchClassificationUnknown:
		return toolCallOutcomeUnknown
	}
	if branch == sdkgo.UncertainBranchID || binding.runner.kind() == operationKindMutation {
		return toolCallOutcomeUnknown
	}
	return toolCallOutcomeRejected
}

func newToolResult(stored storedToolResult, isReplayed bool) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: stored.Text}},
		IsError: stored.IsError,
	}
	if len(stored.Structured) > 0 {
		result.StructuredContent = stored.Structured
	}
	meta := mcp.Meta{}
	if stored.IsError {
		meta[metaKeyOutcome] = string(stored.Outcome)
	}
	if stored.Branch != "" {
		meta[metaKeyBranch] = stored.Branch
	}
	if isReplayed {
		meta[metaKeyReplayed] = true
	}
	if len(meta) > 0 {
		result.Meta = meta
	}
	return result
}

func rejectedResult(text string) storedToolResult {
	return storedToolResult{IsError: true, Outcome: toolCallOutcomeRejected, Text: text}
}

// retryableError is the JSON-RPC internal error a client may repeat with the same idempotency key.
func retryableError(binding *toolBinding, detail string, isNotSent bool, retryAfter time.Duration) error {
	data := map[string]any{errorDataKeyRetryable: true, errorDataKeyNotSent: isNotSent}
	if retryAfter > 0 {
		data[errorDataKeyRetryAfterMs] = retryAfter.Milliseconds()
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("encode retry error data: %v", err))
	}
	message := detail
	if !strings.HasPrefix(detail, operationLabel(binding)) {
		message = operationLabel(binding) + ": " + detail
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: message, Data: encoded}
}

func isConnectorRetry(err error) bool {
	var retry *sdkgo.RetryError
	return errors.As(err, &retry)
}

// isProvenNotSent reports whether this attempt's last checkpoint proves the write did not reach the provider.
func isProvenNotSent(stepContext *localStepContext) bool {
	heartbeat, hasRecorded := stepContext.recordedHeartbeat()
	if !hasRecorded || len(heartbeat) == 0 {
		return false
	}
	var marker singledispatch.Marker
	if err := json.Unmarshal(heartbeat, &marker); err != nil {
		return false
	}
	return marker.Attempt == stepContext.Attempt() && marker.ProviderID == "" &&
		(marker.State == singledispatch.StateNotSent || marker.State == singledispatch.StateCanResend)
}

// checkToolArguments returns the arguments as JSON, or a problem when they break the input schema.
func checkToolArguments(binding *toolBinding, raw json.RawMessage) (json.RawMessage, string) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage("{}")
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return nil, "arguments are not valid JSON"
	}
	if _, isObject := instance.(map[string]any); !isObject {
		return nil, "expected a JSON object"
	}
	if err := binding.operation.inputSchema.Validate(instance); err != nil {
		return nil, err.Error()
	}
	return raw, ""
}

// idempotencyKeyOf returns the key from `_meta`, whether one was sent, and a problem with a malformed one.
func idempotencyKeyOf(meta mcp.Meta) (string, bool, string) {
	value, found := meta[metaKeyIdempotencyKey]
	if !found || value == nil {
		return "", false, ""
	}
	key, isString := value.(string)
	if !isString || strings.TrimSpace(key) == "" || len(key) > maximumIdempotencyBytes {
		return "", false, fmt.Sprintf("%s must be a non-empty string of at most %d bytes", metaKeyIdempotencyKey, maximumIdempotencyBytes)
	}
	return key, true, ""
}

// requestDigest identifies a tool call by its tool name and canonical arguments.
func requestDigest(toolName string, arguments json.RawMessage) string {
	hash := sha256.New()
	hash.Write([]byte(toolName))
	hash.Write([]byte{0})
	hash.Write(canonicalJSON(arguments))
	return hex.EncodeToString(hash.Sum(nil))
}

// encodeResultValue returns the canonical text of a value and the value itself when it is a JSON object.
func encodeResultValue(value json.RawMessage) (string, json.RawMessage) {
	canonical := canonicalJSON(value)
	if bytes.HasPrefix(canonical, []byte("{")) {
		return string(canonical), canonical
	}
	return string(canonical), nil
}

// canonicalJSON sorts object keys and leaves text unescaped, as the agent cores format connector results.
func canonicalJSON(value json.RawMessage) []byte {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return value
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(decoded); err != nil {
		return value
	}
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

func operationLabel(binding *toolBinding) string {
	return binding.connectorID + " " + binding.operation.manifest.OperationID
}

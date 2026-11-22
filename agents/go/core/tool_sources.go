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
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// DefaultToolSourceListingTimeout is how long one listing waits for each tool source by default: 10
// seconds.
//
// Since 0.6.16.
const DefaultToolSourceListingTimeout = 10 * time.Second

// FrameworkToolSourceEnvelopeKey is the default key of the source name in the envelope that a
// ToolSourceRouter stores as AgentToolCall.ToolSourceReference.
//
// It is the default key of the Python core's FrameworkToolSourceRouter, which
// the Python framework adapters store. The TypeScript framework adapters store
// a key of their own per framework, such as "mastra_tool_source". The built-in
// loop stores builtinloop.ToolSourceEnvelopeKey instead.
//
// Since 0.6.16.
const FrameworkToolSourceEnvelopeKey = "dex_tool_source"

// SourceToolNotOfferedResultFormat is what the model reads for a new call of a tool that its source
// no longer lists; format it with the tool name.
//
// Since 0.6.16.
const SourceToolNotOfferedResultFormat = "%s did not run: its tool source does not offer this tool now."

const toolSourceReferenceKey = "reference"

// SourceToolDefinition describes one tool that a ToolSource lists, as the model sees it.
//
// Since 0.6.16.
type SourceToolDefinition struct {
	// Name is the tool name the model calls. It must not repeat a framework tool, a function
	// tool, or another source's tool.
	Name string
	// Description is the tool description for the model.
	Description string
	// InputSchema is the JSON Schema of the tool's arguments object.
	InputSchema json.RawMessage
}

// SourceToolCall is one model call of a tool that a ToolSource listed.
//
// Since 0.6.16.
type SourceToolCall struct {
	// ToolCallID is the model's tool call ID.
	ToolCallID string
	// ToolName is the listed tool name the model called.
	ToolName string
	// Arguments is the tool arguments as JSON text.
	Arguments string
	// ListedTool is the definition the source listed for this model call. Build the call from it,
	// not from a cache that a refresh may change.
	ListedTool SourceToolDefinition
}

// ToolSource offers tools that an agent lists at run time and runs one tool Step per call.
//
// Pass sources to builtinloop.WithToolSources, or serve them from a framework
// adapter through a ToolSourceRouter. In every RunModel the runner lists each
// source, waiting at most the listing timeout, and offers the listed tools to
// the model. A source whose listing fails or takes longer offers no tools in
// that model call, so a down source never fails a turn; keep the listing
// cheap. When the model calls a listed tool, the source builds the call, so it
// picks the effect, the approval, the ordering, and the
// ToolSourceReference. The runner stores the source's SourceName with that
// reference, so the tool Step reaches the same source on any Worker, without
// a listing, and runs the call through ExecuteDeferredToolCall.
//
// A source may also implement CachedToolSource, DeclaredToolNameSource, and
// ToolCallStepOptionsProvider. connectortools.ConnectorToolSource implements
// all of them.
//
//	source, err := connectortools.NewConnectorToolSource([]*connectortools.ConnectorTool{reply})
//	if err != nil {
//		return err
//	}
//	definition, err := builtinloop.NewAgentDefinition("support", "You answer support threads.", model,
//		builtinloop.WithToolSources(source))
//
// Since 0.6.16.
type ToolSource interface {
	// SourceName returns the source's stable, non-blank name, unique among one runner's sources.
	// Stored calls are routed by it, so keep it across deploys: a stored call whose source no
	// Worker has fails by its effect class.
	SourceName() string
	// ListTools returns the tools to offer now, in the order the model sees them. An error
	// offers no tools of this source in this model call.
	ListTools(ctx context.Context) ([]SourceToolDefinition, error)
	// DeferredToolCall builds the call of one listed tool. Resolve the policy from
	// turn.ToolPolicies, so session policies win. An error fails the RunModel attempt.
	DeferredToolCall(turn AgentTurnContext, call SourceToolCall) (DeferredToolCall, error)
	// ExecuteDeferredToolCall runs one approved call in its tool Step. ctx is the tool Step's
	// context or one derived from it, so read the Step from call.StepContext, never from ctx.
	// call.ToolCall.ToolSourceReference holds the source's own reference. It returns what
	// AgentFrameworkRunner.ExecuteDeferredToolCall returns.
	ExecuteDeferredToolCall(ctx context.Context, call DeferredToolExecution) (DeferredToolOutcome, error)
}

// CachedToolSource is a ToolSource that returns its last listing without I/O.
//
// A framework adapter that fixes its tools before any model call reads it, and
// a ToolSourceRouter reads it for the replay-only tools of a turn.
//
// Since 0.6.16.
type CachedToolSource interface {
	// CachedTools returns the last listing, or none.
	CachedTools() []SourceToolDefinition
}

// DeclaredToolNameSource is a ToolSource that names the tools it always lists.
//
// NewToolSourceRouter refuses a declared name that repeats a reserved name or
// another source's tool, so a collision fails when the agent is built.
//
// Since 0.6.16.
type DeclaredToolNameSource interface {
	// DeclaredToolNames returns the names the source always lists.
	DeclaredToolNames() []string
}

// ToolSourceRouterOption configures one ToolSourceRouter. Pass options to NewToolSourceRouter.
//
// Since 0.6.16.
type ToolSourceRouterOption interface {
	applyToolSourceRouter(options *toolSourceRouterOptions)
}

type toolSourceRouterOptions struct {
	reservedToolNames []string
	listingTimeout    time.Duration
	envelopeKey       string
	logger            *slog.Logger
}

type toolSourceRouterOptionFunc func(options *toolSourceRouterOptions)

func (option toolSourceRouterOptionFunc) applyToolSourceRouter(options *toolSourceRouterOptions) {
	option(options)
}

// WithReservedToolNames names the runner's own tools, which no source tool may use.
//
// Pass every name the runner knows when it builds the router;
// ListSourceTools takes names known later. Calls add to the list.
//
// Since 0.6.16.
func WithReservedToolNames(toolNames ...string) ToolSourceRouterOption {
	copied := slices.Clone(toolNames)
	return toolSourceRouterOptionFunc(func(options *toolSourceRouterOptions) {
		options.reservedToolNames = append(options.reservedToolNames, copied...)
	})
}

// WithToolSourceListingTimeout sets how long one ListSourceTools waits for each source.
//
// The default is DefaultToolSourceListingTimeout. It must be positive.
//
// Since 0.6.16.
func WithToolSourceListingTimeout(timeout time.Duration) ToolSourceRouterOption {
	return toolSourceRouterOptionFunc(func(options *toolSourceRouterOptions) {
		options.listingTimeout = timeout
	})
}

// WithToolSourceEnvelopeKey sets the key of the source name in the stored envelope.
//
// Stored records depend on it, so keep the default,
// FrameworkToolSourceEnvelopeKey, unless the runner already stores another
// key, as the built-in loop does. It must not be blank or "reference".
//
// Since 0.6.16.
func WithToolSourceEnvelopeKey(envelopeKey string) ToolSourceRouterOption {
	return toolSourceRouterOptionFunc(func(options *toolSourceRouterOptions) {
		options.envelopeKey = envelopeKey
	})
}

// WithToolSourceLogger sets the logger for a listing that fails or times out. The default is
// slog.Default().
//
// Since 0.6.16.
func WithToolSourceLogger(logger *slog.Logger) ToolSourceRouterOption {
	return toolSourceRouterOptionFunc(func(options *toolSourceRouterOptions) {
		options.logger = logger
	})
}

// ToolSourceRouter serves ToolSource tools from a runner: listing, building calls, and running them.
//
// A runner owns one router for the sources the application passes. The
// router does the work that is the same for every framework:
//
//   - ListSourceTools lists every source in each RunModel. The runner offers
//     the listed tools to the model; a framework adapter offers them as native
//     tools whose bodies never run.
//   - SourceToolListing.DeferredToolCall builds the call of a listed tool
//     through its source, so the effect, approval, and write ordering come
//     from the source, never from the framework.
//   - ExecuteDeferredToolCall runs a call that a source built, in its own
//     tool Step, through that source, with the whole DeferredToolExecution.
//   - ToolCallStepOptions gives the call's tool Step the source's timeout and
//     retry policy.
//
// The router keeps no session state, so one router serves every session of
// the runner, also concurrently.
//
//	router, err := dexagents.NewToolSourceRouter(sources, dexagents.WithReservedToolNames("lookup_order"))
//	if err != nil {
//		return err
//	}
//	listing, err := router.ListSourceTools(ctx, turn)
//	if err != nil {
//		return nil, err
//	}
//	call, err := listing.DeferredToolCall(turn, toolCallID, toolName, arguments)
//
// Since 0.6.16.
type ToolSourceRouter struct {
	sources           []ToolSource
	sourcesByName     map[string]ToolSource
	reservedToolNames map[string]bool
	declaredToolNames []string
	listingTimeout    time.Duration
	envelopeKey       string
	logger            *slog.Logger
}

// NewToolSourceRouter checks the sources and their declared tool names.
//
// sources are offered in this order. It returns an error when a source is
// nil or has a blank SourceName, two sources share a SourceName, the listing
// timeout is not positive, the envelope key is blank or "reference", or a
// source's DeclaredToolNames repeat a reserved name or another source's tool.
//
// Since 0.6.16.
func NewToolSourceRouter(sources []ToolSource, options ...ToolSourceRouterOption) (*ToolSourceRouter, error) {
	configured := toolSourceRouterOptions{
		listingTimeout: DefaultToolSourceListingTimeout,
		envelopeKey:    FrameworkToolSourceEnvelopeKey,
		logger:         slog.Default(),
	}
	for _, option := range options {
		option.applyToolSourceRouter(&configured)
	}
	if configured.listingTimeout <= 0 {
		return nil, errors.New("tool source listing timeout must be positive")
	}
	if isBlankText(configured.envelopeKey) || configured.envelopeKey == toolSourceReferenceKey {
		return nil, fmt.Errorf("tool source envelope key %q must not be blank or %q", configured.envelopeKey, toolSourceReferenceKey)
	}
	if configured.logger == nil {
		configured.logger = slog.Default()
	}
	router := &ToolSourceRouter{
		sources:           slices.Clone(sources),
		sourcesByName:     make(map[string]ToolSource, len(sources)),
		reservedToolNames: make(map[string]bool, len(configured.reservedToolNames)),
		listingTimeout:    configured.listingTimeout,
		envelopeKey:       configured.envelopeKey,
		logger:            configured.logger,
	}
	for _, toolName := range configured.reservedToolNames {
		router.reservedToolNames[toolName] = true
	}
	for index, source := range router.sources {
		if source == nil {
			return nil, fmt.Errorf("tool source %d is nil", index)
		}
		sourceName := source.SourceName()
		if isBlankText(sourceName) {
			return nil, fmt.Errorf("tool source %d needs a non-blank source name", index)
		}
		if _, isTaken := router.sourcesByName[sourceName]; isTaken {
			return nil, fmt.Errorf("two tool sources are named %s", sourceName)
		}
		router.sourcesByName[sourceName] = source
	}
	declared := map[string]bool{}
	for _, source := range router.sources {
		declaring, isDeclaring := source.(DeclaredToolNameSource)
		if !isDeclaring {
			continue
		}
		names := slices.Sorted(slices.Values(declaring.DeclaredToolNames()))
		for _, toolName := range names {
			if err := refuseSourceToolName(toolName, router.reservedToolNames, declared); err != nil {
				return nil, err
			}
		}
		for _, toolName := range names {
			declared[toolName] = true
		}
		router.declaredToolNames = append(router.declaredToolNames, names...)
	}
	return router, nil
}

// Sources returns the sources, in the order their tools are offered.
//
// Since 0.6.16.
func (router *ToolSourceRouter) Sources() []ToolSource {
	return slices.Clone(router.sources)
}

// DeclaredToolNames returns every tool name the sources declare they always list.
//
// Use it to accept a session tool policy for a source tool, such as one that
// changes a connector tool's approval.
//
// Since 0.6.16.
func (router *ToolSourceRouter) DeclaredToolNames() []string {
	return slices.Clone(router.declaredToolNames)
}

// CachedSourceTools returns each CachedToolSource's CachedTools, without I/O.
//
// Use it where a framework fixes its tools before any model call. A source
// without CachedTools contributes nothing. It returns the tools in source
// order and then in each source's order, or an error when a cached name
// repeats a reserved name or another source's tool.
//
// Since 0.6.16.
func (router *ToolSourceRouter) CachedSourceTools() ([]SourceToolDefinition, error) {
	cached := []SourceToolDefinition{}
	names := map[string]bool{}
	for _, source := range router.sources {
		caching, isCaching := source.(CachedToolSource)
		if !isCaching {
			continue
		}
		for _, definition := range caching.CachedTools() {
			if err := refuseSourceToolName(definition.Name, router.reservedToolNames, names); err != nil {
				return nil, err
			}
			names[definition.Name] = true
			cached = append(cached, definition)
		}
	}
	return cached, nil
}

// ListSourceTools lists every source for one model call.
//
// Each source's ListTools gets at most the listing timeout. A source whose
// listing fails or takes longer offers no tools in this call and is logged, so
// a down source never fails a turn. A tool that this turn already called
// through a source, but that the live listing lacks, goes to
// SourceToolListing.ReplayOnlyTools, from that source's CachedTools, so a
// framework still finds the tool when it resumes with the recorded result.
// reservedToolNames are more runner tool names, known only now, that no
// source tool may use.
//
// It returns an error when a listed name repeats a reserved name or another
// source's tool; no model call should be made then.
//
// Since 0.6.16.
func (router *ToolSourceRouter) ListSourceTools(
	ctx context.Context,
	turn AgentTurnContext,
	reservedToolNames ...string,
) (*SourceToolListing, error) {
	reserved := make(map[string]bool, len(router.reservedToolNames)+len(reservedToolNames))
	for toolName := range router.reservedToolNames {
		reserved[toolName] = true
	}
	for _, toolName := range reservedToolNames {
		reserved[toolName] = true
	}
	listing := &SourceToolListing{
		listedTools:     map[string]listedSourceTool{},
		replayOnlyTools: map[string]listedSourceTool{},
		envelopeKey:     router.envelopeKey,
	}
	listedNames := map[string]bool{}
	for index, source := range router.sources {
		for _, definition := range router.listOneSource(ctx, index, source) {
			if err := refuseSourceToolName(definition.Name, reserved, listedNames); err != nil {
				return nil, err
			}
			listedNames[definition.Name] = true
			listing.listedTools[definition.Name] = listedSourceTool{source: source, definition: definition}
			listing.listedOrder = append(listing.listedOrder, definition.Name)
		}
	}
	records := make([]AgentToolCall, 0, len(turn.ToolCalls))
	for _, record := range turn.ToolCalls {
		records = append(records, record)
	}
	sortByDeferralOrder(records)
	for _, record := range records {
		if listedNames[record.ToolName] {
			continue
		}
		if _, isReplayOnly := listing.replayOnlyTools[record.ToolName]; isReplayOnly {
			continue
		}
		sourceName, _, isEnveloped := readToolSourceEnvelope(record.ToolSourceReference, router.envelopeKey)
		if !isEnveloped {
			continue
		}
		recordedSource, isConfigured := router.sourcesByName[sourceName]
		if !isConfigured {
			continue
		}
		if cached, isCached := cachedSourceTool(recordedSource, record.ToolName); isCached {
			listing.replayOnlyTools[cached.Name] = listedSourceTool{source: recordedSource, definition: cached}
			listing.replayOnlyOrder = append(listing.replayOnlyOrder, cached.Name)
		}
	}
	return listing, nil
}

// IsSourceToolCall reports whether a source built a call, so the router runs it.
//
// It returns true when the record carries this router's envelope.
//
// Since 0.6.16.
func (router *ToolSourceRouter) IsSourceToolCall(toolCall AgentToolCall) bool {
	_, _, isEnveloped := readToolSourceEnvelope(toolCall.ToolSourceReference, router.envelopeKey)
	return isEnveloped
}

// ExecuteDeferredToolCall runs one call that a source built, in its tool Step, through that source.
//
// The source receives the whole DeferredToolExecution, with its own
// reference in place of the envelope, so it keeps the Step context, the
// heartbeat checkpoint, and the attempt. ctx is the context the runner got
// from the session, or one derived from it, such as one that a framework's
// tool node adds values to; the source reads the Step from
// call.StepContext, not from ctx. It returns the source's outcome
// and error, or an error when no source built the call or the source that
// built it is not configured on this Worker; that error fails the attempt, so
// the call's effect class decides its outcome.
//
// Since 0.6.16.
func (router *ToolSourceRouter) ExecuteDeferredToolCall(
	ctx context.Context,
	call DeferredToolExecution,
) (DeferredToolOutcome, error) {
	source, reference, err := router.route(call.ToolCall)
	if err != nil {
		return nil, err
	}
	call.ToolCall.ToolSourceReference = reference
	return source.ExecuteDeferredToolCall(ctx, call)
}

// ToolCallStepOptions returns the source's options for one call's tool Step.
//
// A source that implements ToolCallStepOptionsProvider gives its calls their
// own timeout and retry policy, with its own reference in place of the
// envelope. It returns nil for a call that no source built and for a source
// without options, or an error when a source built the call but is not
// configured on this Worker.
//
// Since 0.6.16.
func (router *ToolSourceRouter) ToolCallStepOptions(toolCall AgentToolCall) (*ToolCallStepOptions, error) {
	if !router.IsSourceToolCall(toolCall) {
		return nil, nil
	}
	source, reference, err := router.route(toolCall)
	if err != nil {
		return nil, err
	}
	provider, isProvider := source.(ToolCallStepOptionsProvider)
	if !isProvider {
		return nil, nil
	}
	toolCall.ToolSourceReference = reference
	return provider.ToolCallStepOptions(toolCall)
}

func (router *ToolSourceRouter) route(toolCall AgentToolCall) (ToolSource, *string, error) {
	sourceName, reference, isEnveloped := readToolSourceEnvelope(toolCall.ToolSourceReference, router.envelopeKey)
	if !isEnveloped {
		return nil, nil, fmt.Errorf("no tool source built the call of tool %s", toolCall.ToolName)
	}
	source, isConfigured := router.sourcesByName[sourceName]
	if !isConfigured {
		return nil, nil, fmt.Errorf("tool source %s, which built the call of tool %s, is not configured on this Worker",
			sourceName, toolCall.ToolName)
	}
	return source, reference, nil
}

func (router *ToolSourceRouter) listOneSource(ctx context.Context, index int, source ToolSource) []SourceToolDefinition {
	type listingResult struct {
		tools []SourceToolDefinition
		err   error
	}
	results := make(chan listingResult, 1)
	// A listing that outlives the wait keeps running until it returns or ctx ends.
	go func() {
		tools, err := source.ListTools(ctx)
		results <- listingResult{tools: tools, err: err}
	}()
	timer := time.NewTimer(router.listingTimeout)
	defer timer.Stop()
	select {
	case result := <-results:
		if result.err != nil {
			router.logger.Warn("dex-agents: a tool source could not list its tools",
				"source_index", index, "source_name", source.SourceName(), "error", result.err)
			return nil
		}
		return result.tools
	case <-timer.C:
		router.logger.Warn("dex-agents: a tool source listed nothing within the listing timeout",
			"source_index", index, "source_name", source.SourceName(), "timeout", router.listingTimeout)
		return nil
	case <-ctx.Done():
		return nil
	}
}

// SourceToolListing holds the source tools that one model call offers, and builds their calls.
//
// ToolSourceRouter.ListSourceTools returns it. Offer Tools to the model and
// build every new call of one of them with DeferredToolCall. A tool that this
// turn already called, but that the live listing lacks, is in
// ReplayOnlyTools; offer it only where the framework needs the tool to resume
// with the recorded result, and answer a new call of it with
// NotOfferedResult inside the framework, so it never reaches a tool Step.
//
// Since 0.6.16.
type SourceToolListing struct {
	listedTools     map[string]listedSourceTool
	listedOrder     []string
	replayOnlyTools map[string]listedSourceTool
	replayOnlyOrder []string
	envelopeKey     string
}

type listedSourceTool struct {
	source     ToolSource
	definition SourceToolDefinition
}

// Tools returns the tools a new call may use, in source order and then in each source's order.
//
// Since 0.6.16.
func (listing *SourceToolListing) Tools() []SourceToolDefinition {
	tools := make([]SourceToolDefinition, 0, len(listing.listedOrder))
	for _, toolName := range listing.listedOrder {
		tools = append(tools, listing.listedTools[toolName].definition)
	}
	return tools
}

// ReplayOnlyTools returns the tools this turn called that the live listing lacks.
//
// Since 0.6.16.
func (listing *SourceToolListing) ReplayOnlyTools() []SourceToolDefinition {
	tools := make([]SourceToolDefinition, 0, len(listing.replayOnlyOrder))
	for _, toolName := range listing.replayOnlyOrder {
		tools = append(tools, listing.replayOnlyTools[toolName].definition)
	}
	return tools
}

// HasTool reports whether a new call of a tool name goes to a source.
//
// Since 0.6.16.
func (listing *SourceToolListing) HasTool(toolName string) bool {
	_, isListed := listing.listedTools[toolName]
	return isListed
}

// IsReplayOnly reports whether a tool name is offered only to replay a recorded call.
//
// Since 0.6.16.
func (listing *SourceToolListing) IsReplayOnly(toolName string) bool {
	_, isReplayOnly := listing.replayOnlyTools[toolName]
	return isReplayOnly
}

// NotOfferedResult returns the text the model reads for a new call of a replay-only tool:
// SourceToolNotOfferedResultFormat with the tool name.
//
// Since 0.6.16.
func (listing *SourceToolListing) NotOfferedResult(toolName string) string {
	return fmt.Sprintf(SourceToolNotOfferedResultFormat, toolName)
}

// DeferredToolCall builds the call of one new model call of a listed tool.
//
// The source builds the call, so its effect, approval, ordering, and
// reference come from the source. A session tool policy reaches the source
// through turn. The listing stores the source's name with the reference, so
// the tool Step finds the same source on any Worker.
//
// It returns nil when no source lists the tool, so the runner builds the
// call itself, and an error when the tool is replay-only, this turn already
// recorded toolCallID, or the source fails to build the call. A new call
// under either of the first two must never reach a tool Step; answer it
// inside the framework instead.
//
// Since 0.6.16.
func (listing *SourceToolListing) DeferredToolCall(
	turn AgentTurnContext,
	toolCallID string,
	toolName string,
	arguments string,
) (*DeferredToolCall, error) {
	if listing.IsReplayOnly(toolName) {
		return nil, fmt.Errorf("tool %s is offered only to replay its recorded calls", toolName)
	}
	listed, isListed := listing.listedTools[toolName]
	if !isListed {
		return nil, nil
	}
	if _, isRecorded := turn.RecordedToolCall(toolCallID); isRecorded {
		return nil, fmt.Errorf("tool call ID %s is already recorded in this turn", toolCallID)
	}
	built, err := listed.source.DeferredToolCall(turn, SourceToolCall{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Arguments:  arguments,
		ListedTool: listed.definition,
	})
	if err != nil {
		return nil, err
	}
	envelope, err := writeToolSourceEnvelope(listing.envelopeKey, listed.source.SourceName(), built.ToolSourceReference)
	if err != nil {
		return nil, err
	}
	built.ToolSourceReference = &envelope
	return &built, nil
}

// writeToolSourceEnvelope writes the envelope as Python's json.dumps does, so every core stores the same text.
func writeToolSourceEnvelope(envelopeKey string, sourceName string, reference *string) (string, error) {
	var referenceValue any
	if reference != nil {
		referenceValue = *reference
	}
	return pythonjson.Marshal(pythonjson.Object{
		{Key: envelopeKey, Value: sourceName},
		{Key: toolSourceReferenceKey, Value: referenceValue},
	}, pythonjson.Options{ShouldEscapeNonASCII: true})
}

// readToolSourceEnvelope returns the source name and reference of an envelope with this key.
func readToolSourceEnvelope(toolSourceReference *string, envelopeKey string) (string, *string, bool) {
	if toolSourceReference == nil {
		return "", nil, false
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*toolSourceReference), &envelope); err != nil || envelope == nil {
		return "", nil, false
	}
	rawSourceName := envelope[envelopeKey]
	var sourceName string
	if !strings.HasPrefix(string(rawSourceName), `"`) || json.Unmarshal(rawSourceName, &sourceName) != nil {
		return "", nil, false
	}
	rawReference, hasReference := envelope[toolSourceReferenceKey]
	if !hasReference || string(rawReference) == "null" {
		return sourceName, nil, true
	}
	var reference string
	if !strings.HasPrefix(string(rawReference), `"`) || json.Unmarshal(rawReference, &reference) != nil {
		return "", nil, false
	}
	return sourceName, &reference, true
}

func cachedSourceTool(source ToolSource, toolName string) (SourceToolDefinition, bool) {
	caching, isCaching := source.(CachedToolSource)
	if !isCaching {
		return SourceToolDefinition{}, false
	}
	for _, definition := range caching.CachedTools() {
		if definition.Name == toolName {
			return definition, true
		}
	}
	return SourceToolDefinition{}, false
}

func refuseSourceToolName(toolName string, reservedToolNames map[string]bool, otherSourceToolNames map[string]bool) error {
	if reservedToolNames[toolName] {
		return fmt.Errorf("tool %s of a tool source uses the name of a framework tool", toolName)
	}
	if otherSourceToolNames[toolName] {
		return fmt.Errorf("two tool sources list a tool named %s", toolName)
	}
	return nil
}

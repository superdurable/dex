// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"log/slog"
	"maps"
	"slices"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// Option configures a Runner and the session Flow built around it.
//
// The options that mirror a dexagents session option, such as
// WithToolPolicies and WithInterruptDeadline, configure the adapter and are
// forwarded to dexagents.NewAgentSessionFlow in the order given. Since 0.6.16.
type Option interface {
	applyOption(options *runnerOptions)
}

type optionFunc func(options *runnerOptions)

func (option optionFunc) applyOption(options *runnerOptions) {
	option(options)
}

type runnerOptions struct {
	toolPolicies             map[string]dexagents.ToolPolicy
	toolEffects              map[string]dexagents.ToolEffect
	defaultToolEffect        dexagents.ToolEffect
	hasDefaultToolEffect     bool
	isStreaming              bool
	toolSources              []dexagents.ToolSource
	toolSourceListingTimeout *time.Duration
	logger                   *slog.Logger
	sessionOptions           []dexagents.AgentSessionOption
}

func newRunnerOptions(options []Option) runnerOptions {
	configured := runnerOptions{
		toolPolicies:      map[string]dexagents.ToolPolicy{},
		toolEffects:       map[string]dexagents.ToolEffect{},
		defaultToolEffect: dexagents.ToolEffectUnclassified,
		logger:            slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option.applyOption(&configured)
		}
	}
	if configured.logger == nil {
		configured.logger = slog.Default()
	}
	return configured
}

// WithToolPolicies sets Dex tool policies by tool name, as dexagents.WithToolPolicies does.
//
// A policy overrides WithToolEffects, a tool's ToolEffectDeclarer, and the
// default effect, and it may set RequiresApproval. The map is copied. A
// policy for a name the agent does not have yet is allowed, for tools that
// Handlers add when the agent runs. Since 0.6.16.
func WithToolPolicies(toolPolicies map[string]dexagents.ToolPolicy) Option {
	copied := maps.Clone(toolPolicies)
	if copied == nil {
		copied = map[string]dexagents.ToolPolicy{}
	}
	return optionFunc(func(options *runnerOptions) {
		options.toolPolicies = copied
		options.sessionOptions = append(options.sessionOptions, dexagents.WithToolPolicies(copied))
	})
}

// WithToolEffects declares the Dex effect of Eino tools by tool name.
//
// Eino tools carry no effect, so every tool needs one from this option,
// WithToolPolicies, a ToolEffectDeclarer, or WithDefaultToolEffect. The
// effect's default approval applies: non_idempotent_write and unclassified
// calls wait for approval. Use WithToolPolicies to change that. Later calls
// add to earlier ones. Since 0.6.16.
func WithToolEffects(toolEffects map[string]dexagents.ToolEffect) Option {
	copied := maps.Clone(toolEffects)
	return optionFunc(func(options *runnerOptions) {
		maps.Copy(options.toolEffects, copied)
	})
}

// WithDefaultToolEffect sets the effect of a tool without a policy or declared effect.
//
// It is forwarded as dexagents.WithDefaultToolEffect. Without it, construction
// fails for a tool that has no effect from another source, and a tool that a
// Handler adds at run time without a ToolEffectDeclarer method gets
// ToolEffectUnclassified, so its calls run once and wait for approval.
// Since 0.6.16.
func WithDefaultToolEffect(effect dexagents.ToolEffect) Option {
	return optionFunc(func(options *runnerOptions) {
		options.defaultToolEffect = effect
		options.hasDefaultToolEffect = true
		options.sessionOptions = append(options.sessionOptions, dexagents.WithDefaultToolEffect(effect))
	})
}

// WithStreaming makes each model call stream, as adk.RunnerConfig.EnableStreaming does.
//
// The model's text and reasoning arrive as AgentEvents deltas while it
// streams. Without it the model call uses Generate, and the whole text is
// written as one delta when the call returns. Since 0.6.16.
func WithStreaming(isStreaming bool) Option {
	return optionFunc(func(options *runnerOptions) {
		options.isStreaming = isStreaming
	})
}

// WithToolSources adds tool sources, such as a connectortools.ConnectorToolSource, whose tools the
// agent lists at run time.
//
// In every RunModel the runner lists each source, waiting at most the listing
// timeout, and offers the listed tools to the model after the agent's own
// tools, as Eino tools whose bodies never run. A source whose listing fails or
// takes longer offers no tools in that model call. A call of a listed tool is
// built by its source, which picks the effect, the approval, and the write
// ordering; a session policy reaches the source through the turn. The call
// runs in its own tool Step through that source, before and outside Eino's
// tool node, so ToolCallMiddlewares and Handler tool wrappers never wrap it,
// on any Worker that has a source of the same SourceName. A name that neither
// the agent nor a listing offered fails as an unknown tool without an
// approval. Construction returns an error when two sources share a SourceName
// or a declared source tool name repeats an agent tool, a tool alias, or
// another source's tool. Later calls add to earlier ones.
//
//	source, err := connectortools.NewConnectorToolSource([]*connectortools.ConnectorTool{reply})
//	if err != nil {
//		return err
//	}
//	flow, err := dexeino.NewAgentSessionFlow(config, "SupportAgent", dexeino.WithToolSources(source))
//
// Since 0.6.16.
func WithToolSources(sources ...dexagents.ToolSource) Option {
	copied := slices.Clone(sources)
	return optionFunc(func(options *runnerOptions) {
		options.toolSources = append(options.toolSources, copied...)
	})
}

// WithToolSourceListingTimeout sets how long one model call waits for each tool source's listing.
//
// The default is dexagents.DefaultToolSourceListingTimeout, 10 seconds. It
// must be positive: construction returns an error for zero or a negative
// timeout. Since 0.6.16.
func WithToolSourceListingTimeout(timeout time.Duration) Option {
	return optionFunc(func(options *runnerOptions) {
		options.toolSourceListingTimeout = &timeout
	})
}

// WithInterruptDeadline sets how long an approval may stay pending; see dexagents.WithInterruptDeadline.
//
// Since 0.6.16.
func WithInterruptDeadline(deadline time.Duration) Option {
	return forwardedOption(dexagents.WithInterruptDeadline(deadline))
}

// WithEventStreamCapacityBytes sets the AgentEvents capacity; see dexagents.WithEventStreamCapacityBytes.
//
// Since 0.6.16.
func WithEventStreamCapacityBytes(capacityBytes int64) Option {
	return forwardedOption(dexagents.WithEventStreamCapacityBytes(capacityBytes))
}

// WithMessageWindowSize sets the AgentMessages window size; see dexagents.WithMessageWindowSize.
//
// Since 0.6.16.
func WithMessageWindowSize(windowSize int64) Option {
	return forwardedOption(dexagents.WithMessageWindowSize(windowSize))
}

// WithMessageChunkSize sets the archived chunk size; see dexagents.WithMessageChunkSize.
//
// Since 0.6.16.
func WithMessageChunkSize(chunkSize int64) Option {
	return forwardedOption(dexagents.WithMessageChunkSize(chunkSize))
}

// WithMessageChunkMaxBytes sets the archived chunk byte cap; see dexagents.WithMessageChunkMaxBytes.
//
// Since 0.6.16.
func WithMessageChunkMaxBytes(chunkMaxBytes int64) Option {
	return forwardedOption(dexagents.WithMessageChunkMaxBytes(chunkMaxBytes))
}

// WithModelRetryPolicy sets the backoff for transient model API errors; see dexagents.WithModelRetryPolicy.
//
// Since 0.6.16.
func WithModelRetryPolicy(policy dexagents.ModelRetryPolicy) Option {
	return forwardedOption(dexagents.WithModelRetryPolicy(policy))
}

// WithLogger sets the logger of the adapter and the session; see dexagents.WithLogger.
//
// Since 0.6.16.
func WithLogger(logger *slog.Logger) Option {
	return optionFunc(func(options *runnerOptions) {
		options.logger = logger
		options.sessionOptions = append(options.sessionOptions, dexagents.WithLogger(logger))
	})
}

// WithSessionOptions forwards dexagents session options that have no option of their own here.
//
// They apply after every other option, so they win. The adapter cannot read
// them, so set tool policies and the default tool effect with
// WithToolPolicies and WithDefaultToolEffect: a policy passed only here does
// not count when construction checks that every tool has an effect, or when a
// tool Step compares a call's record with this deploy's policy for its tool.
// Since 0.6.16.
func WithSessionOptions(sessionOptions ...dexagents.AgentSessionOption) Option {
	copied := slices.Clone(sessionOptions)
	return optionFunc(func(options *runnerOptions) {
		options.sessionOptions = append(options.sessionOptions, lateSessionOptions(copied)...)
	})
}

// lateSessionOptions marks forwarded options so SessionOptions can put them last.
func lateSessionOptions(sessionOptions []dexagents.AgentSessionOption) []dexagents.AgentSessionOption {
	marked := make([]dexagents.AgentSessionOption, 0, len(sessionOptions))
	for _, option := range sessionOptions {
		if option != nil {
			marked = append(marked, lateSessionOption{option})
		}
	}
	return marked
}

// lateSessionOption is a forwarded session option that sorts after the adapter's own.
type lateSessionOption struct {
	dexagents.AgentSessionOption
}

func forwardedOption(sessionOption dexagents.AgentSessionOption) Option {
	return optionFunc(func(options *runnerOptions) {
		options.sessionOptions = append(options.sessionOptions, sessionOption)
	})
}

// orderedSessionOptions returns the adapter's session options, then the ones WithSessionOptions forwarded.
func (options runnerOptions) orderedSessionOptions() []dexagents.AgentSessionOption {
	ordered := make([]dexagents.AgentSessionOption, 0, len(options.sessionOptions))
	var late []dexagents.AgentSessionOption
	for _, option := range options.sessionOptions {
		if marked, isLate := option.(lateSessionOption); isLate {
			late = append(late, marked.AgentSessionOption)
			continue
		}
		ordered = append(ordered, option)
	}
	return append(ordered, late...)
}

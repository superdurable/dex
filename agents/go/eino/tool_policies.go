// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// ToolEffectDeclarer is implemented by an Eino tool that declares its own Dex effect.
//
// Add the method to a tool type you own, so its effect travels with the tool:
//
//	func (refundOrder) DexToolEffect() dexagents.ToolEffect {
//		return dexagents.ToolEffectNonIdempotentWrite
//	}
//
// WithToolPolicies and WithToolEffects override the declared effect. The
// effect's default approval applies. A tool that a Handler adds in BeforeAgent
// may declare its effect the same way. Since 0.6.16.
type ToolEffectDeclarer interface {
	// DexToolEffect returns the tool's effect. It must be a valid dexagents.ToolEffect.
	DexToolEffect() dexagents.ToolEffect
}

// agentTool is one tool the agent can call, with the effect the adapter knows for it.
type agentTool struct {
	name   string
	tool   tool.BaseTool
	effect *dexagents.ToolEffect
}

// agentTools is the agent's tool list, keyed by name, with name aliases resolved.
type agentTools struct {
	ordered       []agentTool
	byName        map[string]agentTool
	canonicalName map[string]string
}

func (tools agentTools) lookup(name string) (agentTool, bool) {
	if canonical, isAlias := tools.canonicalName[name]; isAlias {
		name = canonical
	}
	found, isKnown := tools.byName[name]
	return found, isKnown
}

func (tools agentTools) baseTools() []tool.BaseTool {
	baseTools := make([]tool.BaseTool, 0, len(tools.ordered))
	for _, entry := range tools.ordered {
		baseTools = append(baseTools, entry.tool)
	}
	return baseTools
}

// assembleAgentTools returns every tool the configuration gives the agent.
//
// It is the one place the tool list is built, for model calls, tool Steps, and
// the construction check. It returns an error for a nil tool, a tool whose Info
// fails, two tools of one name, or an alias that names another tool.
func (runner *Runner) assembleAgentTools(ctx context.Context, config *agentConfig) (agentTools, error) {
	assembled := agentTools{byName: map[string]agentTool{}, canonicalName: map[string]string{}}
	candidates := append([]tool.BaseTool{}, config.agent.ToolsConfig.Tools...)
	for _, middleware := range config.agent.Middlewares {
		candidates = append(candidates, middleware.AdditionalTools...)
	}
	for index, candidate := range candidates {
		entry, err := runner.describeAgentTool(ctx, candidate)
		if err != nil {
			return agentTools{}, fmt.Errorf("tool %d: %w", index, err)
		}
		if _, isDuplicate := assembled.byName[entry.name]; isDuplicate {
			return agentTools{}, fmt.Errorf("two tools are named %q", entry.name)
		}
		assembled.byName[entry.name] = entry
		assembled.ordered = append(assembled.ordered, entry)
	}
	for canonical, aliases := range config.agent.ToolsConfig.ToolAliases {
		for _, alias := range aliases.NameAliases {
			if _, isTool := assembled.byName[alias]; isTool && alias != canonical {
				return agentTools{}, fmt.Errorf("tool alias %q of %q is the name of another tool", alias, canonical)
			}
			assembled.canonicalName[alias] = canonical
		}
	}
	return assembled, nil
}

func (runner *Runner) describeAgentTool(ctx context.Context, candidate tool.BaseTool) (agentTool, error) {
	if candidate == nil {
		return agentTool{}, fmt.Errorf("the tool is nil")
	}
	info, err := candidate.Info(ctx)
	if err != nil {
		return agentTool{}, fmt.Errorf("read the tool's Info: %w", err)
	}
	if info == nil || info.Name == "" {
		return agentTool{}, fmt.Errorf("the tool %T has no name", candidate)
	}
	return agentTool{name: info.Name, tool: candidate, effect: runner.frameworkEffect(info.Name, candidate)}, nil
}

// frameworkEffect returns the effect WithToolEffects or the tool itself declares, or nil.
func (runner *Runner) frameworkEffect(name string, candidate tool.BaseTool) *dexagents.ToolEffect {
	if effect, isDeclared := runner.options.toolEffects[name]; isDeclared {
		return &effect
	}
	if declarer, isDeclarer := candidate.(ToolEffectDeclarer); isDeclarer {
		effect := declarer.DexToolEffect()
		return &effect
	}
	return nil
}

// checkToolEffects returns an error naming the first tool that has no valid effect from any source.
func (runner *Runner) checkToolEffects(tools agentTools) error {
	for _, effect := range runner.options.toolEffects {
		if !effect.IsValid() {
			return fmt.Errorf("WithToolEffects has effect %q, which is not a tool effect", effect)
		}
	}
	if runner.options.hasDefaultToolEffect && !runner.options.defaultToolEffect.IsValid() {
		return fmt.Errorf("default tool effect %q is not a tool effect", runner.options.defaultToolEffect)
	}
	for _, entry := range tools.ordered {
		if policy, hasPolicy := runner.options.toolPolicies[entry.name]; hasPolicy {
			if !policy.Effect.IsValid() {
				return fmt.Errorf("tool policy for %q has effect %q, which is not a tool effect", entry.name, policy.Effect)
			}
			continue
		}
		if entry.effect != nil {
			if !entry.effect.IsValid() {
				return fmt.Errorf("tool %q declares effect %q, which is not a tool effect", entry.name, *entry.effect)
			}
			continue
		}
		if !runner.options.hasDefaultToolEffect {
			return fmt.Errorf("tool %q has no Dex effect: declare one with WithToolEffects, WithToolPolicies, "+
				"a ToolEffectDeclarer method, or WithDefaultToolEffect", entry.name)
		}
	}
	return nil
}

// modelCallToolPolicy returns the policy of one requested call of a name no source listing offered.
//
// A session policy wins, then the tool's effect, then the default. A name the
// model was never offered fails fast in CallTool as an unknown tool. The
// effect of a Handler-added tool comes from handlerAdded, as in its tool Step.
func (runner *Runner) modelCallToolPolicy(
	turn dexagents.AgentTurnContext,
	tools agentTools,
	offered map[string]bool,
	handlerAdded handlerAddedToolEffects,
	toolName string,
) dexagents.ToolPolicy {
	_, isAgentTool := tools.lookup(toolName)
	if runner.hasToolSources() && !isAgentTool && !offered[toolName] {
		// As in the built-in loop: an unlisted name never reaches a source, so nobody approves it.
		isApprovalRequired := false
		return dexagents.ToolPolicy{Effect: dexagents.ToolEffectUnclassified, RequiresApproval: &isApprovalRequired}
	}
	if _, hasPolicy := turn.ToolPolicies[toolName]; hasPolicy {
		return turn.ResolveToolPolicy(toolName, nil)
	}
	if entry, isKnown := tools.lookup(toolName); isKnown {
		if _, hasPolicy := turn.ToolPolicies[entry.name]; hasPolicy {
			return turn.ResolveToolPolicy(entry.name, nil)
		}
		return turn.ResolveToolPolicy(entry.name, entry.effect)
	}
	if offered[toolName] {
		// A Handler added this tool at run time, so WithToolEffects or its ToolEffectDeclarer names its effect.
		if handlerAdded.lookupErr != nil {
			// The tool Step may find a stricter effect than any guess, so ask a person.
			unclassified := dexagents.ToolEffectUnclassified
			return turn.ResolveToolPolicy(toolName, &unclassified)
		}
		if effect, isAdded := handlerAdded.effects[toolName]; isAdded {
			return turn.ResolveToolPolicy(toolName, effect)
		}
	}
	isApprovalRequired := false
	return dexagents.ToolPolicy{Effect: dexagents.ToolEffectRead, RequiresApproval: &isApprovalRequired}
}

// handlerAddedToolEffects are the effects of the tools the Handlers' BeforeAgent hooks add, by name.
type handlerAddedToolEffects struct {
	effects   map[string]*dexagents.ToolEffect
	lookupErr error
}

// findHandlerAddedToolEffects returns the effects of the tools that the Handlers' BeforeAgent hooks add.
func (runner *Runner) findHandlerAddedToolEffects(ctx context.Context, config *agentConfig, tools agentTools) handlerAddedToolEffects {
	extended, err := handlerAddedTools(ctx, config, tools.baseTools())
	if err != nil {
		runner.options.logger.Warn("dexeino: a Handler's BeforeAgent failed while reading the effect of a tool it adds",
			slog.Any("error", err))
		return handlerAddedToolEffects{lookupErr: err}
	}
	found := handlerAddedToolEffects{effects: map[string]*dexagents.ToolEffect{}}
	for _, candidate := range extended {
		entry, err := runner.describeAgentTool(ctx, candidate)
		if err != nil {
			continue
		}
		if _, isAgentTool := tools.byName[entry.name]; !isAgentTool {
			found.effects[entry.name] = entry.effect
		}
	}
	return found
}

// toolStepPolicy returns the policy this deploy gives a tool the tool Step found.
func (runner *Runner) toolStepPolicy(requestedName string, entry agentTool) dexagents.ToolPolicy {
	defaultEffect := runner.options.defaultToolEffect
	for _, name := range []string{requestedName, entry.name} {
		if _, hasPolicy := runner.options.toolPolicies[name]; hasPolicy {
			return dexagents.ResolveToolPolicy(name, runner.options.toolPolicies, defaultEffect, nil)
		}
	}
	return dexagents.ResolveToolPolicy(entry.name, runner.options.toolPolicies, defaultEffect, entry.effect)
}

// effectStrictness orders effects by how carefully Dex runs them.
func effectStrictness(effect dexagents.ToolEffect) int {
	switch effect {
	case dexagents.ToolEffectRead:
		return 0
	case dexagents.ToolEffectIdempotentWrite:
		return 1
	default:
		return 2
	}
}

// isStricterThanRecord reports whether this deploy gives the tool a stricter effect than its record has.
func isStricterThanRecord(current dexagents.ToolPolicy, record dexagents.AgentToolCall) bool {
	return effectStrictness(current.Effect) > effectStrictness(record.Effect)
}

// needsApprovalTheRecordLacks reports whether this deploy's policy needs an approval the call was recorded without.
func needsApprovalTheRecordLacks(current dexagents.ToolPolicy, record dexagents.AgentToolCall) bool {
	return current.IsApprovalRequired() && !record.RequiresApproval
}

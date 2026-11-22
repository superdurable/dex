// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// emptyObjectSchema is the input schema of a source tool that declares none.
const emptyObjectSchema = `{"type":"object","properties":{}}`

// newToolSourceRouter builds the router of the WithToolSources sources around the agent's own tool names.
func newToolSourceRouter(options runnerOptions, agentToolNames []string) (*dexagents.ToolSourceRouter, error) {
	routerOptions := []dexagents.ToolSourceRouterOption{
		dexagents.WithReservedToolNames(agentToolNames...),
		dexagents.WithToolSourceLogger(options.logger),
	}
	if options.toolSourceListingTimeout != nil {
		routerOptions = append(routerOptions, dexagents.WithToolSourceListingTimeout(*options.toolSourceListingTimeout))
	}
	return dexagents.NewToolSourceRouter(options.toolSources, routerOptions...)
}

// reservedToolNames returns every name the agent's own tools answer to: tool names and their aliases.
func reservedToolNames(tools agentTools) []string {
	names := make([]string, 0, len(tools.ordered)+len(tools.canonicalName))
	for _, entry := range tools.ordered {
		names = append(names, entry.name)
	}
	for alias := range tools.canonicalName {
		names = append(names, alias)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// hasToolSources reports whether WithToolSources configured any source.
func (runner *Runner) hasToolSources() bool {
	return len(runner.options.toolSources) > 0
}

// listSourceTools returns this model call's listing and its Eino stubs; a tool whose schema fails to parse is skipped.
func (runner *Runner) listSourceTools(
	ctx context.Context,
	turn dexagents.AgentTurnContext,
	tools agentTools,
) (*dexagents.SourceToolListing, []tool.BaseTool, error) {
	if !runner.hasToolSources() {
		return nil, nil, nil
	}
	listing, err := runner.router.ListSourceTools(ctx, turn, reservedToolNames(tools)...)
	if err != nil {
		return nil, nil, err
	}
	stubs := make([]tool.BaseTool, 0, len(listing.Tools()))
	for _, definition := range listing.Tools() {
		stub, err := newSourceToolStub(definition)
		if err != nil {
			runner.options.logger.Warn("dexeino: a tool source listed a tool the model cannot be offered",
				slog.String("tool", definition.Name), slog.Any("error", err))
			continue
		}
		stubs = append(stubs, stub)
	}
	return listing, stubs, nil
}

// cachedSourceToolStubs returns the stubs of the cached source tools, or an error for one the model cannot be offered.
func cachedSourceToolStubs(router *dexagents.ToolSourceRouter) ([]tool.BaseTool, error) {
	cached, err := router.CachedSourceTools()
	if err != nil {
		return nil, err
	}
	stubs := make([]tool.BaseTool, 0, len(cached))
	for _, definition := range cached {
		stub, err := newSourceToolStub(definition)
		if err != nil {
			return nil, err
		}
		stubs = append(stubs, stub)
	}
	return stubs, nil
}

// ToolCallStepOptions returns the options of a tool source's call for its tool Step.
//
// The session reads them before it moves to the call's tool Step: a
// connector call gets its operation's timeout and retry policy. It returns
// nil for a call of the agent's own tools and for a source without options,
// or an error when the source that built the call is not configured on this
// Worker, so the session uses the defaults. It implements
// dexagents.ToolCallStepOptionsProvider. Since 0.6.16.
func (runner *Runner) ToolCallStepOptions(toolCall dexagents.AgentToolCall) (*dexagents.ToolCallStepOptions, error) {
	return runner.router.ToolCallStepOptions(toolCall)
}

// sourceToolStub offers one source tool to the model as an Eino tool; the session runs its calls.
type sourceToolStub struct {
	info *schema.ToolInfo
}

func newSourceToolStub(definition dexagents.SourceToolDefinition) (*sourceToolStub, error) {
	raw := bytes.TrimSpace(definition.InputSchema)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = []byte(emptyObjectSchema)
	}
	parsed := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, parsed); err != nil {
		return nil, fmt.Errorf("the input schema of source tool %s is not a JSON Schema: %w", definition.Name, err)
	}
	return &sourceToolStub{info: &schema.ToolInfo{
		Name: definition.Name, Desc: definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parsed),
	}}, nil
}

func (stub *sourceToolStub) Info(context.Context) (*schema.ToolInfo, error) {
	return stub.info, nil
}

// InvokableRun stops the run like the gate does; the call runs in its tool Step through its source.
func (stub *sourceToolStub) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", tool.Interrupt(ctx, &toolCallGateInfo{ToolCallID: compose.GetToolCallID(ctx), ToolName: stub.info.Name})
}

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
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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

// offeredSourceTools is one model call's source listing and the Eino stubs of the listed tools the model is offered.
type offeredSourceTools struct {
	listing *dexagents.SourceToolListing
	stubs   []tool.BaseTool
	names   map[string]bool
}

// isOffered reports whether the model was offered the source tool of this name, so its source builds the call.
func (offered offeredSourceTools) isOffered(name string) bool {
	return offered.names[name]
}

// listSourceTools returns this model call's listing and its Eino stubs.
//
// A listed tool whose schema the model cannot be offered unchanged is skipped
// with a warning, and a call of its name is not built by its source.
func (runner *Runner) listSourceTools(
	ctx context.Context,
	turn dexagents.AgentTurnContext,
	tools agentTools,
) (offeredSourceTools, error) {
	offered := offeredSourceTools{names: map[string]bool{}}
	if !runner.hasToolSources() {
		return offered, nil
	}
	listing, err := runner.router.ListSourceTools(ctx, turn, reservedToolNames(tools)...)
	if err != nil {
		return offeredSourceTools{}, err
	}
	offered.listing = listing
	for _, definition := range listing.Tools() {
		stub, err := newSourceToolStub(definition)
		if err != nil {
			runner.options.logger.Warn("dexeino: a tool source listed a tool the model cannot be offered",
				slog.String("tool", definition.Name), slog.Any("error", err))
			continue
		}
		offered.stubs = append(offered.stubs, stub)
		offered.names[definition.Name] = true
	}
	return offered, nil
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
	parsed, err := parseSourceToolSchema(definition.Name, raw)
	if err != nil {
		return nil, err
	}
	return &sourceToolStub{info: &schema.ToolInfo{
		Name: definition.Name, Desc: definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parsed),
	}}, nil
}

// parseSourceToolSchema parses a listed input schema so that it encodes to the same JSON again.
//
// Eino's JSON Schema type drops keywords it has no field for, such as
// draft-07 definitions or nullable, so they are kept in its Extras. It
// returns an error when raw is not a JSON Schema, or when the parsed schema
// would still reach the model changed.
func parseSourceToolSchema(toolName string, raw []byte) (*jsonschema.Schema, error) {
	parsed := &jsonschema.Schema{}
	if err := json.Unmarshal(raw, parsed); err != nil {
		return nil, fmt.Errorf("the input schema of source tool %s is not a JSON Schema: %w", toolName, err)
	}
	listed, err := decodeJSONWithNumbers(raw)
	if err != nil {
		return nil, fmt.Errorf("the input schema of source tool %s is not a JSON Schema: %w", toolName, err)
	}
	keepKeywordsWithoutAField(parsed, listed)
	offered, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("the input schema of source tool %s does not encode as JSON: %w", toolName, err)
	}
	if again, err := decodeJSONWithNumbers(offered); err != nil || !isSameJSONValue(listed, again) {
		return nil, fmt.Errorf("the input schema of source tool %s would reach the model changed, as %s", toolName, offered)
	}
	return parsed, nil
}

func decodeJSONWithNumbers(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("more than one JSON value")
	}
	return value, nil
}

// keepKeywordsWithoutAField puts each keyword of listed that node does not encode into node's Extras, in every subschema.
func keepKeywordsWithoutAField(node *jsonschema.Schema, listed any) {
	object, isObject := listed.(map[string]any)
	if node == nil || !isObject {
		return
	}
	// An empty schema encodes as true unless its Extras is set.
	node.Extras = map[string]any{}
	encoded, err := json.Marshal(node)
	encodedKeywords := map[string]json.RawMessage{}
	if err == nil && json.Unmarshal(encoded, &encodedKeywords) != nil {
		encodedKeywords = map[string]json.RawMessage{}
	}
	for keyword, value := range object {
		if _, isEncoded := encodedKeywords[keyword]; !isEncoded {
			node.Extras[keyword] = value
		}
	}
	for keyword, child := range map[string]*jsonschema.Schema{
		"not": node.Not, "if": node.If, "then": node.Then, "else": node.Else, "items": node.Items,
		"contains": node.Contains, "additionalProperties": node.AdditionalProperties,
		"propertyNames": node.PropertyNames, "contentSchema": node.ContentSchema,
	} {
		keepKeywordsWithoutAField(child, object[keyword])
	}
	for keyword, children := range map[string][]*jsonschema.Schema{
		"allOf": node.AllOf, "anyOf": node.AnyOf, "oneOf": node.OneOf, "prefixItems": node.PrefixItems,
	} {
		listedChildren, _ := object[keyword].([]any)
		for index, child := range children {
			if index < len(listedChildren) {
				keepKeywordsWithoutAField(child, listedChildren[index])
			}
		}
	}
	for keyword, children := range map[string]map[string]*jsonschema.Schema{
		"$defs": node.Definitions, "dependentSchemas": node.DependentSchemas, "patternProperties": node.PatternProperties,
	} {
		listedChildren, _ := object[keyword].(map[string]any)
		for name, child := range children {
			keepKeywordsWithoutAField(child, listedChildren[name])
		}
	}
	if node.Properties != nil {
		listedProperties, _ := object["properties"].(map[string]any)
		for property := node.Properties.Oldest(); property != nil; property = property.Next() {
			keepKeywordsWithoutAField(property.Value, listedProperties[property.Key])
		}
	}
}

// isSameJSONValue compares two values decoded with UseNumber, comparing numbers by value.
func isSameJSONValue(left any, right any) bool {
	switch typedLeft := left.(type) {
	case map[string]any:
		typedRight, isMap := right.(map[string]any)
		if !isMap || len(typedLeft) != len(typedRight) {
			return false
		}
		for key, value := range typedLeft {
			other, hasKey := typedRight[key]
			if !hasKey || !isSameJSONValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		typedRight, isList := right.([]any)
		if !isList || len(typedLeft) != len(typedRight) {
			return false
		}
		for index := range typedLeft {
			if !isSameJSONValue(typedLeft[index], typedRight[index]) {
				return false
			}
		}
		return true
	case json.Number:
		typedRight, isNumber := right.(json.Number)
		if !isNumber {
			return false
		}
		leftValue, isLeftValid := new(big.Rat).SetString(typedLeft.String())
		rightValue, isRightValid := new(big.Rat).SetString(typedRight.String())
		return isLeftValid && isRightValid && leftValue.Cmp(rightValue) == 0
	default:
		return left == right
	}
}

func (stub *sourceToolStub) Info(context.Context) (*schema.ToolInfo, error) {
	return stub.info, nil
}

// InvokableRun stops the run like the gate does; the call runs in its tool Step through its source.
func (stub *sourceToolStub) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", tool.Interrupt(ctx, &toolCallGateInfo{ToolCallID: compose.GetToolCallID(ctx), ToolName: stub.info.Name})
}

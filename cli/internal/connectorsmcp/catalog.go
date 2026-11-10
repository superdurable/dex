// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// agentManifestFiles holds one agent manifest per compiled connector, named <connectorId>.json.
//
//go:embed manifests/*.json
var agentManifestFiles embed.FS

// toolEffect is the effect class a tool's `_meta` declares, as the agent cores name it.
type toolEffect string

const (
	toolEffectRead               toolEffect = "read"
	toolEffectIdempotentWrite    toolEffect = "idempotent_write"
	toolEffectNonIdempotentWrite toolEffect = "non_idempotent_write"
)

type operationKind string

const (
	operationKindQuery    operationKind = "query"
	operationKindMutation operationKind = "mutation"
)

// branchClassification is a value of a manifest's agent.branches map.
type branchClassification string

const (
	branchClassificationRejected branchClassification = "rejected"
	branchClassificationUnknown  branchClassification = "unknown"
)

// agentManifest is the agent-facing part of one connector manifest.
type agentManifest struct {
	ConnectorID string `json:"connectorId"`
	// GoModuleRelease names the connector Go module release the operations run with.
	GoModuleRelease string `json:"goModuleRelease"`
	// AgentMetadataSource names the reviewed manifest the agent blocks were taken from.
	AgentMetadataSource string                   `json:"agentMetadataSource"`
	Operations          []agentOperationManifest `json:"operations"`
}

// agentOperationManifest is one operation whose manifest entry says agent.exposed: true.
type agentOperationManifest struct {
	OperationID string          `json:"operationId"`
	Kind        operationKind   `json:"kind"`
	Description string          `json:"description"`
	Agent       agentBlock      `json:"agent"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// agentBlock is a manifest operation's agent block.
type agentBlock struct {
	Exposed  bool                            `json:"exposed"`
	Approval string                          `json:"approval,omitempty"`
	Retry    string                          `json:"retry,omitempty"`
	Branches map[string]branchClassification `json:"branches,omitempty"`
}

// agentOperation is a validated agentOperationManifest with its resolved input schema.
type agentOperation struct {
	manifest    agentOperationManifest
	effect      toolEffect
	inputSchema *jsonschema.Resolved
}

// loadAgentManifest returns the embedded agent manifest of connectorID with every operation validated, or an error
// naming the first invalid entry.
func loadAgentManifest(connectorID string) (agentManifest, map[string]agentOperation, error) {
	contents, err := agentManifestFiles.ReadFile("manifests/" + connectorID + ".json")
	if err != nil {
		return agentManifest{}, nil, fmt.Errorf("read agent manifest of Connector %s: %w", connectorID, err)
	}
	var manifest agentManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return agentManifest{}, nil, fmt.Errorf("decode agent manifest of Connector %s: %w", connectorID, err)
	}
	if manifest.ConnectorID != connectorID {
		return agentManifest{}, nil, fmt.Errorf("agent manifest %s.json describes Connector %q", connectorID, manifest.ConnectorID)
	}
	operations := make(map[string]agentOperation, len(manifest.Operations))
	for _, entry := range manifest.Operations {
		operation, err := validateAgentOperation(entry)
		if err != nil {
			return agentManifest{}, nil, fmt.Errorf("Connector %s operation %s: %w", connectorID, entry.OperationID, err)
		}
		if _, exists := operations[entry.OperationID]; exists {
			return agentManifest{}, nil, fmt.Errorf("Connector %s lists operation %s twice", connectorID, entry.OperationID)
		}
		operations[entry.OperationID] = operation
	}
	return manifest, operations, nil
}

// validateAgentOperation applies the agent cores' manifest rules and returns the operation's effect class.
func validateAgentOperation(entry agentOperationManifest) (agentOperation, error) {
	if !entry.Agent.Exposed {
		return agentOperation{}, fmt.Errorf("agent.exposed is not true")
	}
	if entry.Kind != operationKindQuery && entry.Kind != operationKindMutation {
		return agentOperation{}, fmt.Errorf("unknown kind %q", entry.Kind)
	}
	if entry.Agent.Approval != "" && entry.Agent.Approval != "recommended" {
		return agentOperation{}, fmt.Errorf("unknown agent.approval %q", entry.Agent.Approval)
	}
	if entry.Agent.Retry != "" && (entry.Agent.Retry != "safe" || entry.Kind == operationKindQuery) {
		return agentOperation{}, fmt.Errorf("unknown agent.retry %q", entry.Agent.Retry)
	}
	for branch, classification := range entry.Agent.Branches {
		if classification != branchClassificationRejected && classification != branchClassificationUnknown {
			return agentOperation{}, fmt.Errorf("agent.branches %s must be rejected or unknown", branch)
		}
		if classification == branchClassificationUnknown && entry.Kind == operationKindQuery {
			return agentOperation{}, fmt.Errorf("agent.branches unknown is for Mutations only")
		}
	}
	if strings.TrimSpace(entry.Description) == "" {
		return agentOperation{}, fmt.Errorf("description is empty")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(entry.InputSchema, &schema); err != nil {
		return agentOperation{}, fmt.Errorf("decode inputSchema: %w", err)
	}
	if schema.Type != "object" {
		return agentOperation{}, fmt.Errorf("inputSchema type must be object")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return agentOperation{}, fmt.Errorf("resolve inputSchema: %w", err)
	}
	effect := toolEffectRead
	if entry.Kind == operationKindMutation {
		effect = toolEffectNonIdempotentWrite
		if entry.Agent.Retry == "safe" {
			effect = toolEffectIdempotentWrite
		}
	}
	return agentOperation{manifest: entry, effect: effect, inputSchema: resolved}, nil
}

// toolNameOf returns <connectorId>_<operation in snake case>, the name the agent cores' ConnectorToolSource uses.
func toolNameOf(connectorID string, operationID string) string {
	var name strings.Builder
	name.WriteString(connectorID)
	name.WriteByte('_')
	for index, character := range operationID {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				name.WriteByte('_')
			}
			name.WriteRune(character - 'A' + 'a')
			continue
		}
		name.WriteRune(character)
	}
	return name.String()
}

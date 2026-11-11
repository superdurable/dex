// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Limits on one Flow Definition Graph, checked before Dex Web keeps or draws it.
// The browser checks the same node, edge, and string limits in app/design/definitionLimits.ts.
const (
	// maxFlowDefinitionNodes bounds the Steps, resources, and RPCs the canvas lays out.
	maxFlowDefinitionNodes = 2000
	// maxFlowDefinitionEdges bounds the relations the canvas routes.
	maxFlowDefinitionEdges = 10000
	// maxFlowDefinitionStringCharacters bounds each JSON string, including object keys.
	maxFlowDefinitionStringCharacters = 16 << 10
	// maxFlowDefinitionDepth matches the nesting limit of the Dex Server upload.
	maxFlowDefinitionDepth = 128
	// maxFlowDefinitionConnectorSteps bounds the Connector release lookups one definition causes.
	maxFlowDefinitionConnectorSteps = 200
)

// checkFlowDefinitionDocumentLimits walks the JSON tokens iteratively, so deep nesting cannot exhaust the stack.
func checkFlowDefinitionDocumentLimits(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse JSON: %w", err)
		}
		switch value := token.(type) {
		case json.Delim:
			if value == '{' || value == '[' {
				depth++
				if depth > maxFlowDefinitionDepth {
					return fmt.Errorf("JSON nests deeper than %d levels", maxFlowDefinitionDepth)
				}
			} else {
				depth--
			}
		case string:
			if utf8.RuneCountInString(value) > maxFlowDefinitionStringCharacters {
				return fmt.Errorf("a JSON string is longer than %d characters", maxFlowDefinitionStringCharacters)
			}
		}
	}
}

// checkFlowDefinitionGraphLimits bounds the node, edge, and Connector Step counts of a parsed graph.
func checkFlowDefinitionGraphLimits(nodes []json.RawMessage, edges []json.RawMessage) error {
	if len(nodes) > maxFlowDefinitionNodes {
		return fmt.Errorf("it has %d nodes; the limit is %d", len(nodes), maxFlowDefinitionNodes)
	}
	if len(edges) > maxFlowDefinitionEdges {
		return fmt.Errorf("it has %d edges; the limit is %d", len(edges), maxFlowDefinitionEdges)
	}
	connectorSteps := 0
	for _, raw := range nodes {
		var node struct {
			Kind     string `json:"kind"`
			Metadata struct {
				ConnectorFactory bool `json:"connectorFactory"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			return fmt.Errorf("malformed node: %w", err)
		}
		if node.Kind == "step" && node.Metadata.ConnectorFactory {
			connectorSteps++
		}
	}
	if connectorSteps > maxFlowDefinitionConnectorSteps {
		return fmt.Errorf("it has %d Connector Steps; the limit is %d", connectorSteps, maxFlowDefinitionConnectorSteps)
	}
	return nil
}

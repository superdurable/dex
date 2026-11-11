// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every graph the repository ships must stay within the Dex Web limits.
func TestShippedFlowDefinitionGraphsFitTheLimits(t *testing.T) {
	patterns := []string{
		filepath.Join("..", "docs", "src", "data", "flow-definitions", "*.json"),
		filepath.Join("app", "design", "testdata", "*.json"),
		filepath.Join("app", "rendering", "testdata", "*.json"),
	}
	documents := make([]flowDefinitionDocument, 0)
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			documents = append(documents, flowDefinitionDocument{origin: flowDefinitionOriginDirectory, path: path, data: data})
		}
	}
	sessionGraph, err := os.ReadFile(filepath.Join("..", "agents", "conformance", "session_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := strings.NewReplacer(`"<flow-type>"`, `"SupportAgent"`, `"<action-permission>"`, `"agent.operate"`).Replace(string(sessionGraph))
	documents = append(documents, flowDefinitionDocument{origin: flowDefinitionOriginDirectory, path: "session_graph.json", data: []byte(session)})
	if len(documents) < 10 {
		t.Fatalf("found only %d shipped graphs; the paths moved", len(documents))
	}
	for _, document := range documents {
		if err := checkFlowDefinitionDocumentLimits(document.data); err != nil {
			t.Errorf("%s exceeds a document limit: %v", document.path, err)
		}
		definition := buildFlowDefinition(document)
		if strings.Contains(definition.Error, "too large") || strings.Contains(definition.Error, "exceeds") {
			t.Errorf("%s exceeds a graph limit: %s", document.path, definition.Error)
		}
	}
}

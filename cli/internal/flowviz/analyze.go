// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type AnalyzeOptions struct {
	Language      string
	PythonPath    string
	SchemaVersion string
}

func Analyze(ctx context.Context, sourcePath string, options AnalyzeOptions) (*Graph, error) {
	absolutePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	language, err := resolveLanguage(options.Language, absolutePath)
	if err != nil {
		return nil, err
	}
	schemaVersion, err := resolveSchemaVersion(options.SchemaVersion)
	if err != nil {
		return nil, err
	}
	if schemaVersion == SchemaVersionV2 && language != "go" {
		return nil, fmt.Errorf("schema version 2.0 supports Go source only")
	}
	var graph *Graph
	switch language {
	case "go":
		graph, err = analyzeGo(ctx, absolutePath, data, schemaVersion)
	case "python":
		graph, err = analyzePython(ctx, absolutePath, data, options.PythonPath)
	default:
		panic("validated language was not handled")
	}
	if err != nil {
		return nil, err
	}
	if schemaVersion == SchemaVersionV2 {
		if graph.Groups == nil {
			graph.Groups = make([]StepGroup, 0)
		}
		if graph.V2 == nil {
			graph.V2 = &V2Definition{
				IndexedAttributes: make([]IndexedAttribute, 0),
				Summary:           RPCView{RPCName: "GetDexSummary", Fields: make([]ViewField, 0)},
				Display:           RPCView{RPCName: "GetDexDisplay", Fields: make([]ViewField, 0)},
				Actions:           make([]Action, 0),
			}
		}
	}
	graph.Source.Path = filepath.ToSlash(filepath.Clean(sourcePath))
	graph.Normalize()
	return graph, nil
}

func resolveSchemaVersion(requested string) (string, error) {
	switch strings.TrimSpace(requested) {
	case "", SchemaVersionV1:
		return SchemaVersionV1, nil
	case SchemaVersionV2:
		return SchemaVersionV2, nil
	default:
		return "", fmt.Errorf("schema version must be 1.0 or 2.0")
	}
}

func MarshalJSON(graph *Graph) ([]byte, error) {
	var payload interface{} = graph
	if graph.SchemaVersion == SchemaVersionV2 {
		payload = struct {
			*Graph
			Groups []StepGroup   `json:"groups"`
			V2     *V2Definition `json:"v2"`
		}{Graph: graph, Groups: graph.Groups, V2: graph.V2}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode graph JSON: %w", err)
	}
	return append(data, '\n'), nil
}

func resolveLanguage(requested string, sourcePath string) (string, error) {
	language := strings.ToLower(strings.TrimSpace(requested))
	if language == "" || language == "auto" {
		switch strings.ToLower(filepath.Ext(sourcePath)) {
		case ".go":
			return "go", nil
		case ".py":
			return "python", nil
		default:
			return "", fmt.Errorf("cannot infer language from %s; use --language", filepath.Ext(sourcePath))
		}
	}
	switch language {
	case "go", "python":
		return language, nil
	default:
		return "", fmt.Errorf("language must be auto, go, or python")
	}
}

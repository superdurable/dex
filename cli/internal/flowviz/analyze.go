// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
	Language   string
	PythonPath string
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
	var graph *Graph
	switch language {
	case "go":
		graph, err = analyzeGo(ctx, absolutePath, data)
	case "python":
		graph, err = analyzePython(ctx, absolutePath, data, options.PythonPath)
	default:
		panic("validated language was not handled")
	}
	if err != nil {
		return nil, err
	}
	graph.Source.Path = filepath.ToSlash(filepath.Clean(sourcePath))
	graph.Normalize()
	return graph, nil
}

func MarshalJSON(graph *Graph) ([]byte, error) {
	data, err := json.MarshalIndent(graph, "", "  ")
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

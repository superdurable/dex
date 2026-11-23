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

// LintApplication selects the application lints that applications opt into with --lint app.
const LintApplication = "app"

type AnalyzeOptions struct {
	Language      string
	PythonPath    string
	CargoPath     string
	SchemaVersion string
	FlowType      string
	Lint          string
	// TypeScriptCompilerDirectory is the trusted typescript package directory; empty resolves from the source project.
	TypeScriptCompilerDirectory string
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
	if err := validateSchemaVersion(options.SchemaVersion); err != nil {
		return nil, err
	}
	isApplicationLint, err := resolveLint(options.Lint)
	if err != nil {
		return nil, err
	}
	var graph *Graph
	switch language {
	case "go":
		graph, err = analyzeGo(ctx, absolutePath, data, options.FlowType, isApplicationLint)
	case "python":
		graph, err = analyzePython(ctx, absolutePath, data, options.PythonPath, options.FlowType)
	case "rust":
		graph, err = analyzeRust(ctx, absolutePath, data, options.CargoPath, options.FlowType)
	case "typescript":
		graph, err = analyzeTypeScript(ctx, absolutePath, data, options.FlowType, options.TypeScriptCompilerDirectory)
	default:
		panic("validated language was not handled")
	}
	if err != nil {
		return nil, err
	}
	if graph.Groups == nil {
		graph.Groups = make([]StepGroup, 0)
	}
	if graph.V2 == nil {
		graph.V2 = newEmptyV2Definition()
	}
	graph.Source.Path = filepath.ToSlash(filepath.Clean(sourcePath))
	graph.Normalize()
	return graph, nil
}

func newEmptyV2Definition() *V2Definition {
	return &V2Definition{
		IndexedAttributes: make([]IndexedAttribute, 0),
		Summary:           RPCView{RPCName: "GetDexSummary", Fields: make([]ViewField, 0)},
		Display:           RPCView{RPCName: "GetDexDisplay", Fields: make([]ViewField, 0)},
		Actions:           make([]Action, 0),
	}
}

func validateSchemaVersion(requested string) error {
	switch strings.TrimSpace(requested) {
	case "", SchemaVersionV2:
		return nil
	default:
		return fmt.Errorf("schema version must be 2.0")
	}
}

func flowSelectionDiagnostic(flowType string, candidates []string) (string, string) {
	list := strings.Join(candidates, ", ")
	if flowType == "" {
		return "multiple_flows", "source defines multiple Flows; use --flow with one of: " + list
	}
	if list == "" {
		list = "(none)"
	}
	for _, candidate := range candidates {
		if candidate == flowType {
			return "multiple_flows", fmt.Sprintf("multiple Flows have type %q; available Flow types: %s", flowType, list)
		}
	}
	return "flow_not_found", fmt.Sprintf("Flow type %q was not found; available Flow types: %s", flowType, list)
}

func resolveLint(requested string) (bool, error) {
	switch strings.TrimSpace(requested) {
	case "":
		return false, nil
	case LintApplication:
		return true, nil
	default:
		return false, fmt.Errorf("lint must be %s", LintApplication)
	}
}

func MarshalJSON(graph *Graph) ([]byte, error) {
	payload := struct {
		*Graph
		Groups []StepGroup   `json:"groups"`
		V2     *V2Definition `json:"v2"`
	}{Graph: graph, Groups: graph.Groups, V2: graph.V2}
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
		case ".rs":
			return "rust", nil
		case ".ts", ".tsx", ".mts", ".cts":
			return "typescript", nil
		default:
			return "", fmt.Errorf("cannot infer language from %s; use --language", filepath.Ext(sourcePath))
		}
	}
	switch language {
	case "go", "python", "typescript", "rust":
		return language, nil
	default:
		return "", fmt.Errorf("language must be auto, go, python, typescript, or rust")
	}
}

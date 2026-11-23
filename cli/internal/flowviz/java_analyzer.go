// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed FlowvizJavaAnalyzer.java
var javaAnalyzerSource []byte

type javaAnalyzerRequest struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	FlowType string `json:"flowType"`
}

func analyzeJava(ctx context.Context, sourcePath string, source []byte, javaPath, flowType string) (*Graph, error) {
	request, err := json.Marshal(javaAnalyzerRequest{Path: sourcePath, Source: string(source), FlowType: flowType})
	if err != nil {
		return nil, fmt.Errorf("encode Java analyzer input: %w", err)
	}
	directory, err := os.MkdirTemp("", "dexcli-java-analyzer-")
	if err != nil {
		return nil, fmt.Errorf("create Java analyzer directory: %w", err)
	}
	defer os.RemoveAll(directory)
	scriptPath := filepath.Join(directory, "FlowvizJavaAnalyzer.java")
	if err := os.WriteFile(scriptPath, javaAnalyzerSource, 0o600); err != nil {
		return nil, fmt.Errorf("write Java analyzer source: %w", err)
	}
	if javaPath == "" {
		javaPath = "java"
	}
	command := exec.CommandContext(ctx, javaPath, "--class-path", directory, scriptPath)
	command.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	var result scriptAnalysis
	if err == nil {
		err = json.Unmarshal(stdout.Bytes(), &result)
		if err == nil && (result.Graph.Source.Language != "java" || result.Graph.SchemaVersion != SchemaVersionV2) {
			err = fmt.Errorf("unexpected Java analyzer output")
		}
	}
	if err != nil {
		graph := NewGraph("java", sourcePath)
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		graph.AddDiagnostic("error", "java_analyzer_failed", "Java analysis requires a JDK 17 or later: "+message, nil)
		return graph, nil
	}
	return result.definition(), nil
}

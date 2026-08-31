// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package flowviz

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

//go:embed python_analyzer.py
var pythonAnalyzerSource string

func analyzePython(ctx context.Context, sourcePath string, source []byte, pythonPath string) (*Graph, error) {
	if strings.TrimSpace(pythonPath) == "" {
		pythonPath = "python3"
	}
	request, err := json.Marshal(map[string]string{"path": sourcePath, "source": string(source)})
	if err != nil {
		return nil, fmt.Errorf("encode Python analyzer input: %w", err)
	}
	command := exec.CommandContext(ctx, pythonPath, "-I", "-S", "-c", pythonAnalyzerSource)
	command.Stdin = bytes.NewReader(request)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		graph := NewGraph("python", sourcePath)
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		graph.AddDiagnostic("error", "python_analyzer_failed", message, nil)
		return graph, nil
	}
	var graph Graph
	if err := json.Unmarshal(stdout.Bytes(), &graph); err != nil {
		return nil, fmt.Errorf("decode Python analyzer output: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return &graph, nil
}

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

//go:embed python_analyzer.py
var pythonAnalyzerSource string

//go:embed python_connector_analyzer.py
var pythonConnectorAnalyzerSource string

//go:embed python_binding_analyzer.py
var pythonBindingAnalyzerSource string

//go:embed python_v2_metadata.py
var pythonV2MetadataSource string

func analyzePython(ctx context.Context, sourcePath string, source []byte, pythonPath string, flowType string) (*Graph, error) {
	if strings.TrimSpace(pythonPath) == "" {
		pythonPath = "python3"
	}
	request, err := json.Marshal(map[string]string{"path": sourcePath, "source": string(source), "flowType": flowType})
	if err != nil {
		return nil, fmt.Errorf("encode Python analyzer input: %w", err)
	}
	command, removeScript, err := newPythonAnalyzerCommand(ctx, pythonPath)
	if err != nil {
		return nil, err
	}
	defer removeScript()
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
	var result scriptAnalysis
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("decode Python analyzer output: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return result.definition(), nil
}

func newPythonAnalyzerCommand(ctx context.Context, pythonPath string) (*exec.Cmd, func(), error) {
	// Linux limits each argument to 128 KiB, which the combined script can exceed.
	directory, err := os.MkdirTemp("", "dexcli-python-analyzer-")
	if err != nil {
		return nil, nil, fmt.Errorf("create Python analyzer directory: %w", err)
	}
	removeDirectory := func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: remove Python analyzer directory %s: %v\n", directory, removeErr)
		}
	}
	scriptPath := filepath.Join(directory, "python_analyzer.py")
	script := strings.Join([]string{pythonConnectorAnalyzerSource, pythonBindingAnalyzerSource, pythonV2MetadataSource, pythonAnalyzerSource}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		removeDirectory()
		return nil, nil, fmt.Errorf("write Python analyzer script: %w", err)
	}
	return exec.CommandContext(ctx, pythonPath, "-I", "-S", scriptPath), removeDirectory, nil
}

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
	"os/exec"
	"strings"
)

//go:embed typescript_analyzer.cjs
var typeScriptAnalyzerSource string

func analyzeTypeScript(ctx context.Context, sourcePath string, source []byte, schemaVersion string) (*Graph, error) {
	request, err := json.Marshal(map[string]string{"path": sourcePath, "source": string(source)})
	if err != nil {
		return nil, fmt.Errorf("encode TypeScript analyzer input: %w", err)
	}
	command := exec.CommandContext(ctx, "node", "--input-type=commonjs", "-e", typeScriptAnalyzerSource)
	command.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		graph := NewGraph("typescript", sourcePath)
		graph.SchemaVersion = schemaVersion
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		graph.AddDiagnostic("error", "typescript_analyzer_failed", message, nil)
		return graph, nil
	}
	var result scriptAnalysis
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("decode TypeScript analyzer output: %w", err)
	}
	return result.definition(schemaVersion), nil
}

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
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed FlowvizJavaAnalyzer.java
var javaAnalyzerSource []byte

type javaAnalyzerRequest struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	FlowType string `json:"flowType"`
}

func analyzeJava(ctx context.Context, sourcePath string, source []byte, javaPath, flowType string) (*Graph, error) {
	if javaPath == "" {
		javaPath = "java"
	}
	version := exec.CommandContext(ctx, javaPath, "-version")
	var versionStderr bytes.Buffer
	version.Stderr = &versionStderr
	versionOutput, err := version.Output()
	if err != nil {
		return javaProcessFailure(sourcePath, versionStderr.Bytes(), err, version.ProcessState == nil && ctx.Err() == nil), nil
	}
	major := javaMajorVersion(versionStderr.String())
	if major == 0 {
		major = javaMajorVersion(string(versionOutput))
	}
	if major > 0 && major < 17 {
		return javaProcessFailure(sourcePath, nil, fmt.Errorf("found Java %d", major), true), nil
	}
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
	command := exec.CommandContext(ctx, javaPath, "--class-path", directory, scriptPath)
	command.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if err != nil {
		return javaProcessFailure(sourcePath, stderr.Bytes(), err, command.ProcessState == nil && ctx.Err() == nil), nil
	}
	var result scriptAnalysis
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return javaProcessFailure(sourcePath, stderr.Bytes(), err, false), nil
	}
	if result.Graph.Source.Language != "java" || result.Graph.SchemaVersion != SchemaVersionV2 {
		return javaProcessFailure(sourcePath, stderr.Bytes(), fmt.Errorf("unexpected Java analyzer output"), false), nil
	}
	return result.definition(), nil
}

func javaMajorVersion(output string) int {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		prefix, version, found := strings.Cut(line, ` version "`)
		if !found || (prefix != "java" && prefix != "openjdk") {
			continue
		}
		version = strings.TrimPrefix(version, "1.")
		end := strings.IndexAny(version, `.-+"`)
		if end >= 0 {
			version = version[:end]
		}
		major, _ := strconv.Atoi(version)
		return major
	}
	return 0
}

func javaProcessFailure(sourcePath string, stderr []byte, err error, requiresJDK bool) *Graph {
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		detail = err.Error()
	}
	line, _, _ := strings.Cut(detail, "\n")
	line = strings.TrimSpace(line)
	requiresJDK = requiresJDK || javaLauncherFailed(detail)
	const maxDetailBytes = 240
	if len(line) > maxDetailBytes {
		end := maxDetailBytes
		for !utf8.RuneStart(line[end]) {
			end--
		}
		line = line[:end] + "…"
	}
	message := "Java analysis failed: " + line
	if requiresJDK {
		message = "Java analysis requires a JDK 17 or later: " + line
	}
	graph := NewGraph("java", sourcePath)
	graph.AddDiagnostic("error", "java_analyzer_failed", message, nil)
	return graph
}

func javaLauncherFailed(detail string) bool {
	for line := range strings.SplitSeq(detail, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(line, "unsupportedclassversionerror") ||
			strings.Contains(line, "unsupported class file") ||
			strings.HasPrefix(line, "error: could not find or load main class") ||
			strings.HasPrefix(line, "unrecognized option:") ||
			strings.Contains(line, "unable to locate a java runtime") ||
			strings.HasPrefix(line, "error: could not create the java virtual machine") {
			return true
		}
	}
	return false
}

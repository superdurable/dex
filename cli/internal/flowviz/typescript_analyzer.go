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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed typescript_analyzer.cjs
var typeScriptAnalyzerSource string

type typeScriptAnalyzerRequest struct {
	Path              string `json:"path"`
	Source            string `json:"source"`
	CompilerEntryPath string `json:"compilerEntryPath,omitempty"`
}

// trustedTypeScriptCompiler is a typescript package that the analyzed source cannot redirect.
type trustedTypeScriptCompiler struct {
	packageDirectory string
	entryPath        string
}

func analyzeTypeScript(ctx context.Context, sourcePath string, source []byte, schemaVersion string, compilerDirectory string) (*Graph, error) {
	request := typeScriptAnalyzerRequest{Path: sourcePath, Source: string(source)}
	var compiler *trustedTypeScriptCompiler
	if compilerDirectory != "" {
		resolved, err := resolveTrustedTypeScriptCompiler(compilerDirectory)
		if err != nil {
			return nil, err
		}
		compiler = resolved
		request.CompilerEntryPath = compiler.entryPath
	}
	requestData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode TypeScript analyzer input: %w", err)
	}
	scriptPath, removeScript, err := writeTypeScriptAnalyzerScript()
	if err != nil {
		return nil, err
	}
	defer removeScript()
	command := exec.CommandContext(ctx, "node", typeScriptAnalyzerArguments(scriptPath)...)
	if compiler != nil {
		// NODE_OPTIONS can preload modules and NODE_PATH can redirect resolution.
		command.Dir = compiler.packageDirectory
		command.Env = environmentWithoutNodeVariables(os.Environ())
	}
	command.Stdin = bytes.NewReader(requestData)
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

// writeTypeScriptAnalyzerScript writes the analyzer to a private temporary directory, because Linux caps one argument at 128 KiB.
// It returns the script path and a function that removes the directory, or an error that names the failed step.
func writeTypeScriptAnalyzerScript() (string, func(), error) {
	directory, err := os.MkdirTemp("", "dexcli-typescript-analyzer-")
	if err != nil {
		return "", nil, fmt.Errorf("create TypeScript analyzer directory: %w", err)
	}
	removeDirectory := func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: remove TypeScript analyzer directory %s: %v\n", directory, removeErr)
		}
	}
	scriptPath := filepath.Join(directory, "typescript_analyzer.cjs")
	if err := os.WriteFile(scriptPath, []byte(typeScriptAnalyzerSource), 0o600); err != nil {
		removeDirectory()
		return "", nil, fmt.Errorf("write TypeScript analyzer script: %w", err)
	}
	return scriptPath, removeDirectory, nil
}

// typeScriptAnalyzerArguments returns the node arguments that run the analyzer script file.
func typeScriptAnalyzerArguments(scriptPath string) []string {
	return []string{scriptPath}
}

// resolveTrustedTypeScriptCompiler validates the typescript package directory and resolves its entry file.
// It returns the real package directory and the real entry file inside it, or an error that names the directory.
func resolveTrustedTypeScriptCompiler(directory string) (*trustedTypeScriptCompiler, error) {
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve TypeScript compiler directory %s: %w", directory, err)
	}
	packageDirectory, err := filepath.EvalSymlinks(absoluteDirectory)
	if err != nil {
		return nil, fmt.Errorf("TypeScript compiler directory %s: %w", absoluteDirectory, err)
	}
	info, err := os.Stat(packageDirectory)
	if err != nil {
		return nil, fmt.Errorf("TypeScript compiler directory %s: %w", absoluteDirectory, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("TypeScript compiler directory %s is not a directory", absoluteDirectory)
	}
	manifestData, err := os.ReadFile(filepath.Join(packageDirectory, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		nestedManifest := filepath.Join(absoluteDirectory, "typescript", "package.json")
		if _, nestedErr := os.Stat(nestedManifest); nestedErr == nil {
			return nil, fmt.Errorf("TypeScript compiler directory %s is not the typescript package directory; pass %s", absoluteDirectory, filepath.Join(absoluteDirectory, "typescript"))
		}
		return nil, fmt.Errorf("TypeScript compiler directory %s has no package.json; pass the directory of an installed typescript package, such as PREFIX/node_modules/typescript", absoluteDirectory)
	}
	if err != nil {
		return nil, fmt.Errorf("read TypeScript compiler package.json in %s: %w", absoluteDirectory, err)
	}
	var manifest struct {
		Name string `json:"name"`
		Main string `json:"main"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("decode TypeScript compiler package.json in %s: %w", absoluteDirectory, err)
	}
	if manifest.Name != "typescript" {
		return nil, fmt.Errorf("TypeScript compiler directory %s holds package %q, not typescript", absoluteDirectory, manifest.Name)
	}
	if manifest.Main == "" {
		return nil, fmt.Errorf("TypeScript compiler package.json in %s has no main entry", absoluteDirectory)
	}
	entryPath, err := filepath.EvalSymlinks(filepath.Join(packageDirectory, manifest.Main))
	if err != nil {
		return nil, fmt.Errorf("TypeScript compiler entry %s in %s: %w", manifest.Main, absoluteDirectory, err)
	}
	relativeEntryPath, err := filepath.Rel(packageDirectory, entryPath)
	if err != nil || relativeEntryPath == ".." || strings.HasPrefix(relativeEntryPath, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("TypeScript compiler entry %s is outside %s", manifest.Main, absoluteDirectory)
	}
	entryInfo, err := os.Stat(entryPath)
	if err != nil {
		return nil, fmt.Errorf("TypeScript compiler entry %s in %s: %w", manifest.Main, absoluteDirectory, err)
	}
	if !entryInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("TypeScript compiler entry %s in %s is not a file", manifest.Main, absoluteDirectory)
	}
	return &trustedTypeScriptCompiler{packageDirectory: packageDirectory, entryPath: entryPath}, nil
}

func environmentWithoutNodeVariables(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "NODE_") {
			kept = append(kept, entry)
		}
	}
	return kept
}

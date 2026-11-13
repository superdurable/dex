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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// plantedTypeScriptLayout plants code that writes markerPath when Node loads it, and returns the source to analyze.
type plantedTypeScriptLayout func(t *testing.T, sourcePath string, markerPath string) string

func TestTypeScriptAnalyzerTrustedCompilerNeverLoadsProjectCode(t *testing.T) {
	layouts := map[string]plantedTypeScriptLayout{
		"typescript package in the source directory": func(t *testing.T, sourcePath string, markerPath string) string {
			plantTypeScriptPackage(t, filepath.Join(filepath.Dir(sourcePath), "node_modules", "typescript"), markerPath)
			return sourcePath
		},
		"typescript package in an ancestor directory": func(t *testing.T, sourcePath string, markerPath string) string {
			plantTypeScriptPackage(t, filepath.Join(filepath.Dir(sourcePath), "node_modules", "typescript"), markerPath)
			nestedSourcePath := filepath.Join(filepath.Dir(sourcePath), "src", "flows", filepath.Base(sourcePath))
			require.NoError(t, os.MkdirAll(filepath.Dir(nestedSourcePath), 0o755))
			require.NoError(t, os.Rename(sourcePath, nestedSourcePath))
			return nestedSourcePath
		},
		"package self-reference named typescript": func(t *testing.T, sourcePath string, markerPath string) string {
			directory := filepath.Dir(sourcePath)
			writeTestFile(t, filepath.Join(directory, "package.json"), `{"name":"typescript","exports":"./self-reference.js"}`)
			writeTestFile(t, filepath.Join(directory, "self-reference.js"), plantedTypeScriptModule(t, markerPath))
			return sourcePath
		},
		"typescript package in NODE_PATH": func(t *testing.T, sourcePath string, markerPath string) string {
			nodePath := filepath.Join(t.TempDir(), "node_modules")
			plantTypeScriptPackage(t, filepath.Join(nodePath, "typescript"), markerPath)
			t.Setenv("NODE_PATH", nodePath)
			return sourcePath
		},
		"NODE_OPTIONS preload": func(t *testing.T, sourcePath string, markerPath string) string {
			preloadPath := filepath.Join(filepath.Dir(sourcePath), "preload.js")
			writeTestFile(t, preloadPath, fmt.Sprintf("require(\"node:fs\").writeFileSync(%s, \"loaded\");\n", strconv.Quote(markerPath)))
			t.Setenv("NODE_OPTIONS", "--require="+preloadPath)
			return sourcePath
		},
	}
	for name, plant := range layouts {
		t.Run(name, func(t *testing.T) {
			markerPath := filepath.Join(t.TempDir(), "project-code-ran")
			compilerDirectory := trustedTypeScriptCompilerDirectory(t)
			goldenPath, err := filepath.Abs(filepath.Join("testdata", "typescript", "workflow.golden.json"))
			require.NoError(t, err)
			sourcePath := plant(t, scriptFixture(t, "typescript"), markerPath)
			t.Chdir(filepath.Dir(sourcePath))

			defaultGraph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
			require.NoError(t, err)
			require.FileExists(t, markerPath, "the planted code must run without a trusted compiler: %+v", defaultGraph.Diagnostics)
			require.NoError(t, os.Remove(markerPath))

			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{
				SchemaVersion:               SchemaVersionV2,
				Lint:                        LintApplication,
				TypeScriptCompilerDirectory: compilerDirectory,
			})
			require.NoError(t, err)
			require.NoFileExists(t, markerPath)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
			graph.Source.Path = filepath.Base(sourcePath)
			actual, err := MarshalJSON(graph)
			require.NoError(t, err)
			expected, err := os.ReadFile(goldenPath)
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func TestTypeScriptAnalyzerTrustedCompilerWorksWithoutNodePath(t *testing.T) {
	sourcePath := scriptFixture(t, "typescript")
	compilerDirectory := trustedTypeScriptCompilerDirectory(t)
	t.Setenv("NODE_PATH", "")
	t.Chdir(t.TempDir())
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{
		SchemaVersion:               SchemaVersionV2,
		TypeScriptCompilerDirectory: compilerDirectory,
	})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Equal(t, "CatalogFlow", graph.Flow.Name)
}

func TestTypeScriptAnalyzerRejectsInvalidCompilerDirectory(t *testing.T) {
	cases := map[string]struct {
		directory func(t *testing.T) string
		message   string
	}{
		"missing directory": {
			directory: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") },
			message:   "no such file or directory",
		},
		"file instead of directory": {
			directory: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "typescript.js")
				writeTestFile(t, path, "")
				return path
			},
			message: "is not a directory",
		},
		"directory without package.json": {
			directory: func(t *testing.T) string { return t.TempDir() },
			message:   "has no package.json; pass the directory of an installed typescript package",
		},
		"node_modules directory": {
			directory: func(t *testing.T) string { return filepath.Dir(trustedTypeScriptCompilerDirectory(t)) },
			message:   "is not the typescript package directory; pass " + trustedTypeScriptCompilerDirectory(t),
		},
		"another package": {
			directory: func(t *testing.T) string {
				directory := t.TempDir()
				writeTestFile(t, filepath.Join(directory, "package.json"), `{"name":"not-typescript","main":"index.js"}`)
				writeTestFile(t, filepath.Join(directory, "index.js"), "")
				return directory
			},
			message: `holds package "not-typescript", not typescript`,
		},
		"entry outside the package": {
			directory: func(t *testing.T) string {
				root := t.TempDir()
				directory := filepath.Join(root, "typescript")
				writeTestFile(t, filepath.Join(root, "outside.js"), "")
				writeTestFile(t, filepath.Join(directory, "package.json"), `{"name":"typescript","main":"../outside.js"}`)
				return directory
			},
			message: "TypeScript compiler entry ../outside.js is outside",
		},
		"missing entry": {
			directory: func(t *testing.T) string {
				directory := t.TempDir()
				writeTestFile(t, filepath.Join(directory, "package.json"), `{"name":"typescript","main":"./lib/typescript.js"}`)
				return directory
			},
			message: "TypeScript compiler entry ./lib/typescript.js in",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sourcePath := scriptFixture(t, "typescript")
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{
				SchemaVersion:               SchemaVersionV2,
				TypeScriptCompilerDirectory: testCase.directory(t),
			})
			require.Nil(t, graph)
			require.ErrorContains(t, err, testCase.message)
		})
	}
}

func TestPythonAnalyzerIgnoresTypeScriptCompilerDirectory(t *testing.T) {
	sourcePath := scriptFixture(t, "python")
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{
		SchemaVersion:               SchemaVersionV2,
		TypeScriptCompilerDirectory: filepath.Join(t.TempDir(), "missing"),
	})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
}

func plantTypeScriptPackage(t *testing.T, packageDirectory string, markerPath string) {
	t.Helper()
	writeTestFile(t, filepath.Join(packageDirectory, "package.json"), `{"name":"typescript","main":"index.js"}`)
	writeTestFile(t, filepath.Join(packageDirectory, "index.js"), plantedTypeScriptModule(t, markerPath))
}

// plantedTypeScriptModule records that it ran, then exports the real compiler so default analysis still succeeds.
func plantedTypeScriptModule(t *testing.T, markerPath string) string {
	t.Helper()
	compilerEntryPath := filepath.Join(trustedTypeScriptCompilerDirectory(t), "lib", "typescript.js")
	return fmt.Sprintf("require(\"node:fs\").writeFileSync(%s, \"loaded\");\nmodule.exports = require(%s);\n", strconv.Quote(markerPath), strconv.Quote(compilerEntryPath))
}

// trustedTypeScriptCompilerDirectory resolves from the package directory, so call it before t.Chdir.
func trustedTypeScriptCompilerDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	directory := filepath.Join(root, "web", "node_modules", "typescript")
	require.FileExists(t, filepath.Join(directory, "package.json"))
	return directory
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriptConnectorDefinitionGolden(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
			graph.Source.Path = filepath.Base(sourcePath)
			actual, err := MarshalJSON(graph)
			require.NoError(t, err)
			goldenPath := filepath.Join("testdata", language, "workflow.golden.json")
			if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
				require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
			}
			expected, err := os.ReadFile(goldenPath)
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
		})
	}
}

func TestScriptStepAnnotationDiagnostics(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, replacement := range []string{"", "dex:explanation text:\"\"", "dex:explanation text:\"First.\"\nCOMMENT dex:explanation text:\"Second.\"", "dex:explanation text:\"First.\" extra:true"} {
			t.Run(language+"/"+replacement, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				prefix := "#"
				if language == "typescript" {
					prefix = "//"
				}
				replacement = strings.ReplaceAll(replacement, "COMMENT", prefix)
				modified := strings.Replace(string(source), "dex:explanation text:\"Complete the Flow after the lookup.\"", replacement, 1)
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, Lint: LintApplication})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "v2_step_explanation")
			})
		}
	}
}

func TestScriptDynamicConnectionDisablesConfiguration(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(string(source), "\"primary\"", "read_connection()", 1)), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Contains(t, diagnosticCodes(graph), "connector_connection_name_required")
			for _, node := range graph.Nodes {
				if node.ID == "step:LookupRecord" {
					require.Equal(t, false, node.Metadata["connector"].(map[string]any)["configurationEnabled"])
				}
			}
		})
	}
}

func TestScriptMalformedManifestIsBlocking(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			packagePath := filepath.Join(filepath.Dir(sourcePath), "catalog_connector")
			if language == "typescript" {
				packagePath = filepath.Join(filepath.Dir(sourcePath), "node_modules", "@dex-test", "catalog")
			}
			manifestPath := filepath.Join(packagePath, "dex-connector.json")
			manifest, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, []byte(strings.Replace(string(manifest), "connectors/catalog/v1.2.3", "../catalog/latest", 1)), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph), "connector_manifest")
		})
	}
}

func TestScriptConnectorTargetsMustBeStatic(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			original, replacement := "found=self.finish", "found=choose_step()"
			if language == "typescript" {
				original, replacement = "found: this.finish", "found: chooseStep()"
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Replace(string(source), original, replacement, 1)), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Contains(t, diagnosticCodes(graph), "connector_factory_target")
		})
	}
}

func TestPythonConnectorResolutionUsesSelectedVirtualEnvironment(t *testing.T) {
	sourcePath := scriptFixture(t, "python")
	virtualEnvironment := filepath.Join(t.TempDir(), "venv")
	output, err := exec.Command("python3", "-m", "venv", "--without-pip", virtualEnvironment).CombinedOutput()
	require.NoError(t, err, "%s", output)
	interpreter := filepath.Join(virtualEnvironment, "bin", "python")
	output, err = exec.Command(interpreter, "-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").CombinedOutput()
	require.NoError(t, err, "%s", output)
	packageDirectory := filepath.Join(strings.TrimSpace(string(output)), "catalog_connector")
	require.NoError(t, os.Rename(filepath.Join(filepath.Dir(sourcePath), "catalog_connector"), packageDirectory))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, PythonPath: interpreter})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
}

func TestScriptConnectorNamespaceImports(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			modified := string(source)
			if language == "python" {
				modified = strings.ReplaceAll(modified, "from catalog_connector import create_lookup as lookup_record, create_trigger", "import catalog_connector as catalog")
				modified = strings.ReplaceAll(modified, "lookup_record(", "catalog.create_lookup(")
				modified = strings.ReplaceAll(modified, "create_trigger(", "catalog.create_trigger(")
			} else {
				modified = strings.ReplaceAll(modified, `import { createLookup as lookupRecord, createTrigger } from "@dex-test/catalog";`, `import * as catalog from "@dex-test/catalog";`)
				modified = strings.ReplaceAll(modified, "lookupRecord(", "catalog.createLookup(")
				modified = strings.ReplaceAll(modified, "createTrigger(", "catalog.createTrigger(")
			}
			require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			require.Empty(t, graph.Diagnostics)
		})
	}
}

func scriptFixture(t *testing.T, language string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Setenv("NODE_PATH", filepath.Join(root, "web", "node_modules"))
	directory := t.TempDir()
	require.NoError(t, os.CopyFS(directory, os.DirFS(filepath.Join("testdata", language))))
	if language == "typescript" {
		require.NoError(t, os.Rename(filepath.Join(directory, "node-packages"), filepath.Join(directory, "node_modules")))
		return filepath.Join(directory, "workflow.ts")
	}
	return filepath.Join(directory, "workflow.py")
}

func diagnosticCodes(graph *Graph) []string {
	codes := make([]string, 0, len(graph.Diagnostics))
	for _, diagnostic := range graph.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

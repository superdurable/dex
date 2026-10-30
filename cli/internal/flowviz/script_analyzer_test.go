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
	replacements := map[string][2]string{
		"releaseTag":         {`"connectors/catalog/v1.2.3"`, `"../catalog/latest"`},
		"unit required flag": {`"required": true,`, ""},
		"JSON pointer":       {`"/query"`, `"/query~2"`},
	}
	messages := map[string]string{
		"releaseTag":         `releaseTag "../catalog/latest"`,
		"unit required flag": "configurationUI units require required and bindings",
		"JSON pointer":       `invalid JSON pointer "/query~2"`,
	}
	for _, language := range []string{"python", "typescript"} {
		for name, replacement := range replacements {
			t.Run(language+"/"+name, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				manifestPath := filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json")
				manifest, err := os.ReadFile(manifestPath)
				require.NoError(t, err)
				modified := strings.Replace(string(manifest), replacement[0], replacement[1], 1)
				require.NotEqual(t, string(manifest), modified)
				require.NoError(t, os.WriteFile(manifestPath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "connector_manifest")
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest" {
						require.Contains(t, diagnostic.Message, messages[name])
					}
				}
			})
		}
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

func TestPythonAnalyzerDoesNotRunSitePackagesStartupCode(t *testing.T) {
	sourcePath := scriptFixture(t, "python")
	virtualEnvironment := filepath.Join(t.TempDir(), "venv")
	output, err := exec.Command("python3", "-m", "venv", "--without-pip", virtualEnvironment).CombinedOutput()
	require.NoError(t, err, "%s", output)
	interpreter := filepath.Join(virtualEnvironment, "bin", "python")
	output, err = exec.Command(interpreter, "-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").CombinedOutput()
	require.NoError(t, err, "%s", output)
	purelib := strings.TrimSpace(string(output))
	editableDirectory := t.TempDir()
	require.NoError(t, os.Rename(filepath.Join(filepath.Dir(sourcePath), "catalog_connector"), filepath.Join(editableDirectory, "catalog_connector")))
	probeDirectory := t.TempDir()
	pathProbe := filepath.Join(probeDirectory, "pth-executed")
	customizeProbe := filepath.Join(probeDirectory, "sitecustomize-executed")
	pathConfiguration := fmt.Sprintf("import os; open(%q, 'w').close()\n%s\n", pathProbe, editableDirectory)
	require.NoError(t, os.WriteFile(filepath.Join(purelib, "zz_probe.pth"), []byte(pathConfiguration), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(purelib, "sitecustomize.py"), []byte(fmt.Sprintf("open(%q, 'w').close()\n", customizeProbe)), 0o644))

	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2, PythonPath: interpreter})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
	require.NoFileExists(t, pathProbe)
	require.NoFileExists(t, customizeProbe)

	output, err = exec.Command(interpreter, "-I", "-c", "pass").CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.FileExists(t, pathProbe, "the .pth probe must run under a normal interpreter start")
}

func TestScriptMissingConnectorManifestNamesThePackage(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		for _, removal := range []string{"manifest", "package"} {
			t.Run(language+"/"+removal, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				packagePath := scriptFixturePackage(sourcePath, language)
				expected := filepath.Join(packagePath, "dex-connector.json")
				if removal == "package" {
					require.NoError(t, os.RemoveAll(packagePath))
					expected = "@dex-test/catalog is not installed"
					if language == "python" {
						expected = "catalog_connector cannot be found"
					}
				} else {
					require.NoError(t, os.Remove(filepath.Join(packagePath, "dex-connector.json")))
				}
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				codes := diagnosticCodes(graph)
				require.NotContains(t, codes, language+"_analyzer_failed")
				require.Contains(t, codes, "dynamic_step_target")
				missing := 0
				for _, diagnostic := range graph.Diagnostics {
					if diagnostic.Code == "connector_manifest_missing" {
						missing++
						require.Contains(t, diagnostic.Message, expected)
					}
				}
				require.Equal(t, 2, missing, "%+v", graph.Diagnostics)
			})
		}
	}
}

func TestScriptWrongTypedManifestIsBlocking(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			sourcePath := scriptFixture(t, language)
			manifestPath := filepath.Join(scriptFixturePackage(sourcePath, language), "dex-connector.json")
			var manifest map[string]any
			contents, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(contents, &manifest))
			manifest["factories"].([]any)[0].(map[string]any)["fields"] = []any{"stepType"}
			contents, err = json.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, contents, 0o644))
			graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			codes := diagnosticCodes(graph)
			require.NotContains(t, codes, language+"_analyzer_failed")
			require.Contains(t, codes, "connector_manifest")
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "connector_manifest" {
					require.Contains(t, diagnostic.Message, "factories[0].fields must be an object of strings")
				}
			}
		})
	}
}

func TestScriptDuplicateStepTypesAreBlocking(t *testing.T) {
	type duplicateCase struct {
		name         string
		replacements map[string][2]string
	}
	cases := []duplicateCase{
		{name: "connector factories", replacements: map[string][2]string{
			"python":     {"        self.lookup = lookup_record(", "        self.second_lookup = lookup_record(step_type=\"LookupRecord\", connection_name=CONNECTION_NAME, annotations={\"groupId\": \"lookup\", \"groupLabel\": \"Lookup\", \"explanation\": \"Look up again.\"}, found=self.finish, unavailable=self.finish)\n        self.lookup = lookup_record("},
			"typescript": {"  private readonly lookup = lookupRecord({", "  private readonly secondLookup = lookupRecord({stepType: \"LookupRecord\", connectionName, annotations: {groupId: \"lookup\", groupLabel: \"Lookup\", explanation: \"Look up again.\"}, found: this.finish, unavailable: this.finish});\n  private readonly lookup = lookupRecord({"},
		}},
		{name: "connector and local Step", replacements: map[string][2]string{
			"python":     {`step_type="LookupRecord"`, `step_type="FinishStep"`},
			"typescript": {`stepType: "LookupRecord"`, `stepType: "FinishStep"`},
		}},
	}
	for _, testCase := range cases {
		for _, language := range []string{"python", "typescript"} {
			t.Run(testCase.name+"/"+language, func(t *testing.T) {
				sourcePath := scriptFixture(t, language)
				source, err := os.ReadFile(sourcePath)
				require.NoError(t, err)
				modified := strings.Replace(string(source), testCase.replacements[language][0], testCase.replacements[language][1], 1)
				if testCase.name == "connector factories" {
					registration := map[string][2]string{
						"python":     {"other_steps(self.finish)", "other_steps(self.finish, self.second_lookup)"},
						"typescript": {"otherSteps(this.finish)", "otherSteps(this.finish, this.secondLookup)"},
					}[language]
					modified = strings.Replace(modified, registration[0], registration[1], 1)
				}
				require.NotEqual(t, string(source), modified)
				require.NoError(t, os.WriteFile(sourcePath, []byte(modified), 0o644))
				graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
				require.NoError(t, err)
				require.False(t, graph.Valid)
				require.Contains(t, diagnosticCodes(graph), "duplicate_step_type")
			})
		}
	}
}

func TestTypeScriptAnalyzerResolvesCompilerFromSourceProject(t *testing.T) {
	sourcePath := scriptFixture(t, "typescript")
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	t.Setenv("NODE_PATH", "")
	t.Chdir(filepath.Join(root, "web"))
	graph, err := Analyze(context.Background(), sourcePath, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Len(t, graph.Diagnostics, 1)
	require.Equal(t, "typescript_analyzer_failed", graph.Diagnostics[0].Code)
	require.Contains(t, graph.Diagnostics[0].Message, "typescript package installed in the source project")
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

func scriptFixturePackage(sourcePath string, language string) string {
	if language == "typescript" {
		return filepath.Join(filepath.Dir(sourcePath), "node_modules", "@dex-test", "catalog")
	}
	return filepath.Join(filepath.Dir(sourcePath), "catalog_connector")
}

func diagnosticCodes(graph *Graph) []string {
	codes := make([]string, 0, len(graph.Diagnostics))
	for _, diagnostic := range graph.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

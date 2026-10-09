// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeHandlesRelatedOptionDependencies(t *testing.T) {
	for _, scenario := range []string{"repeated-append", "mutual-slices", "bound-parameter", "field-append", "pointer-copy", "deep-aliases", "recursive-helpers", "expansion-limit"} {
		t.Run(scenario, func(t *testing.T) {
			if os.Getenv("DEXCLI_TEST_OPTION_DEPENDENCY") != scenario {
				executable, err := os.Executable()
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				command := exec.CommandContext(ctx, executable, "-test.run=^TestVisualizeHandlesRelatedOptionDependencies$/^"+scenario+"$", "-test.v")
				command.Env = append(os.Environ(), "DEXCLI_TEST_OPTION_DEPENDENCY="+scenario)
				output, err := command.CombinedOutput()
				require.NoError(t, err, "%s", output)
				return
			}
			debug.SetMaxStack(64 << 20)
			source := optionDependencyFixture(t, scenario)
			for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
				for _, lint := range []string{"", flowviz.LintApplication} {
					output := &bytes.Buffer{}
					app := NewApp(strings.NewReader(""), output, &bytes.Buffer{})
					arguments := []string{"visualize", source, "--json", "--schema-version", schemaVersion}
					if lint != "" {
						arguments = append(arguments, "--lint", lint)
					}
					err := app.Execute(context.Background(), arguments)
					var graph flowviz.Graph
					require.NoError(t, json.Unmarshal(output.Bytes(), &graph), "%v", err)
					if scenario == "recursive-helpers" || scenario == "expansion-limit" {
						if lint == "" {
							require.NoError(t, err)
							require.True(t, graph.Valid, "%+v", graph.Diagnostics)
							require.Empty(t, graph.Diagnostics)
						} else {
							require.Equal(t, 1, ExitCode(err))
							require.NotEmpty(t, graph.Diagnostics)
							for _, diagnostic := range graph.Diagnostics {
								require.Equal(t, "state_load_unresolved", diagnostic.Code, "%+v", graph.Diagnostics)
								if scenario == "expansion-limit" {
									require.Contains(t, diagnostic.Message, "10000 expressions")
								}
							}
						}
						continue
					}
					require.Equal(t, 1, ExitCode(err))
					require.False(t, graph.Valid)
					require.Equal(t, []string{"attribute_map_enumeration_not_loaded"}, sortedDiagnosticCodes(graph.Diagnostics), "%+v", graph.Diagnostics)
					require.Contains(t, graph.Diagnostics[0].Message, `"missing"`)
				}
			}
		})
	}
}

func TestVisualizeDistinguishesSDKNamesFromApplicationHelpers(t *testing.T) {
	for _, scenario := range []string{"decision", "resource"} {
		t.Run(scenario, func(t *testing.T) {
			path := optionDependencyFixture(t, "bound-parameter")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			source := string(data)
			expectedCodes := []string{"attribute_map_enumeration_not_loaded"}
			if scenario == "decision" {
				source = strings.Replace(source, "count := First.MapSize(ctx) + Second.MapSize(ctx) + Third.MapSize(ctx) + Missing.MapSize(ctx)\n\treturn dex.GracefulComplete(count), nil", "First.MapSize(ctx); Second.MapSize(ctx); Third.MapSize(ctx); Missing.MapSize(ctx)\n\treturn ForceCompleteIfChannelsEmpty(), nil", 1)
				source += "\nfunc ForceCompleteIfChannelsEmpty() *dex.StepDecision { return dex.GracefulComplete(0) }\n"
				expectedCodes = append(expectedCodes, "hidden_dex_decision")
			} else {
				source += "\nvar ApplicationAttribute = DefineAttribute()\nfunc DefineAttribute() dex.Attribute[string] { return dex.DefineAttribute[string](\"application\") }\n"
			}
			require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
			for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
				for _, lint := range []string{"", flowviz.LintApplication} {
					output := &bytes.Buffer{}
					app := NewApp(strings.NewReader(""), output, &bytes.Buffer{})
					arguments := []string{"visualize", path, "--json", "--schema-version", schemaVersion}
					if lint != "" {
						arguments = append(arguments, "--lint", lint)
					}
					err := app.Execute(context.Background(), arguments)
					require.Equal(t, 1, ExitCode(err))
					var graph flowviz.Graph
					require.NoError(t, json.Unmarshal(output.Bytes(), &graph))
					require.False(t, graph.Valid)
					require.Equal(t, expectedCodes, sortedDiagnosticCodes(graph.Diagnostics), "%+v", graph.Diagnostics)
				}
			}
		})
	}
}

func optionDependencyFixture(t *testing.T, scenario string) string {
	t.Helper()
	data, err := os.ReadFile(stateLoadsFixture(t, "append-options/workflow.go"))
	require.NoError(t, err)
	source := string(data)
	start := strings.Index(source, "func (ReadMapsStep) GetStepOptions()")
	end := strings.Index(source[start:], "\nfunc (ReadMapsStep) Execute") + start
	require.Greater(t, start, 0)
	require.Greater(t, end, start)
	body, helpers := optionDependencySource(scenario)
	source = source[:start] + "func (ReadMapsStep) GetStepOptions() *dex.StepOptions {\n" + body + "\n}\n" + source[end:] + helpers
	directory := t.TempDir()
	path := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	module := "module optiondependencies\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(visualizerRepositoryRoot(t), "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	return path
}

func optionDependencySource(scenario string) (string, string) {
	switch scenario {
	case "repeated-append":
		body := "maps := []dex.AttributeDef{First}\n" + strings.Repeat("maps = append(maps, Second)\n", 30)
		return body + "maps = append(maps, Third)\nreturn &dex.StepOptions{ExecuteLoadAttributeMaps: maps}", ""
	case "mutual-slices":
		return "maps := []dex.AttributeDef{First}\nalias := maps\nmaps = append(alias, Second)\nalias = append(maps, Third)\nreturn &dex.StepOptions{ExecuteLoadAttributeMaps: alias}", ""
	case "bound-parameter":
		return "return &dex.StepOptions{ExecuteLoadAttributeMaps: appendBoundLoads(forwardMapLoads(forwardMapLoads([]dex.AttributeDef{First, Second})))}", "\nfunc appendBoundLoads(loads []dex.AttributeDef) []dex.AttributeDef { loads = append(loads, Third); return loads }\n"
	case "field-append":
		return "options := new(dex.StepOptions)\noptions.ExecuteLoadAttributeMaps = make([]dex.AttributeDef, 0, 8)\noptions.ExecuteLoadAttributeMaps = append(options.ExecuteLoadAttributeMaps, First)\noptions.ExecuteLoadAttributeMaps = append(options.ExecuteLoadAttributeMaps, Second, Third)\nreturn options", ""
	case "pointer-copy":
		return "base := &dex.StepOptions{ExecuteLoadAttributeMaps: []dex.AttributeDef{First}}\noptions := *base\noptions.ExecuteLoadAttributeMaps = append(options.ExecuteLoadAttributeMaps, Second, Third)\nreturn &options", ""
	case "deep-aliases":
		body := "maps0 := []dex.AttributeDef{First, Second, Third}\n"
		for index := 1; index <= 2000; index++ {
			body += fmt.Sprintf("maps%d := maps%d\n", index, index-1)
		}
		return body + "return &dex.StepOptions{ExecuteLoadAttributeMaps: maps2000}", ""
	case "recursive-helpers":
		return "return &dex.StepOptions{ExecuteLoadAttributeMaps: recurseMapLoads([]dex.AttributeDef{First, Second, Third})}", "\nfunc recurseMapLoads(loads []dex.AttributeDef) []dex.AttributeDef { return append(loads, recurseOtherLoads(loads)...) }\nfunc recurseOtherLoads(loads []dex.AttributeDef) []dex.AttributeDef { return recurseMapLoads(loads) }\n"
	case "expansion-limit":
		helpers := "\nfunc expandLoads0(loads []dex.AttributeDef) []dex.AttributeDef { return loads }\n"
		for depth := 1; depth <= 4; depth++ {
			helpers += fmt.Sprintf("func expandLoads%d(loads []dex.AttributeDef) []dex.AttributeDef {\n", depth)
			for index := 0; index < 11; index++ {
				helpers += fmt.Sprintf("if len(loads) == %d { return expandLoads%d(loads) }\n", index, depth-1)
			}
			helpers += fmt.Sprintf("return expandLoads%d(loads)\n}\n", depth-1)
		}
		return "return &dex.StepOptions{ExecuteLoadAttributeMaps: expandLoads4([]dex.AttributeDef{First, Second, Third})}", helpers
	default:
		panic("unknown option dependency scenario: " + scenario)
	}
}

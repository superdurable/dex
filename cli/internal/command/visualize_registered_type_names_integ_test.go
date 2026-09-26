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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

const registeredTypeNameLinePrefix = "DEX_REGISTERED_TYPE_NAME "

func TestVisualizeGoNamesMatchSDKRegistration(t *testing.T) {
	moduleDirectory := registeredTypeNamesFixtureDirectory(t)
	for _, fixture := range []struct {
		name         string
		source       string
		packageName  string
		printCommand []string
	}{
		{
			name:         "defaults",
			source:       "defaults/workflow.go",
			packageName:  "defaults",
			printCommand: []string{"test", "-run", "^TestPrintRegisteredTypeNames$", "-count=1", "-v", "./defaults"},
		},
		{
			// A go test build compiles package main under its import path, unlike a Worker binary.
			name:         "package main",
			source:       "mainpackage/workflow.go",
			packageName:  "main",
			printCommand: []string{"run", "./mainpackage"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			registered := sdkRegisteredTypeNames(t, moduleDirectory, fixture.printCommand)
			for _, schemaVersion := range []string{flowviz.SchemaVersionV1, flowviz.SchemaVersionV2} {
				graph, err := flowviz.Analyze(
					context.Background(),
					filepath.Join(moduleDirectory, fixture.source),
					flowviz.AnalyzeOptions{SchemaVersion: schemaVersion},
				)
				require.NoError(t, err)
				require.True(t, graph.Valid, "%s: %+v", schemaVersion, graph.Diagnostics)
				require.Equal(t, registered["flow"], graph.Flow.Name, schemaVersion)

				analyzedNames := map[string]string{"flow": graph.Flow.Name}
				defaultNamedSteps := 0
				for _, node := range graph.Nodes {
					if node.Kind != "step" {
						continue
					}
					require.True(t, strings.HasPrefix(node.ID, "step:"), node.ID)
					goType := strings.TrimPrefix(node.ID, "step:")
					analyzedNames[node.ID] = node.Name
					defaultName := fixture.packageName + "." + goType
					if node.Name == defaultName || strings.HasPrefix(node.Name, defaultName+"[") {
						defaultNamedSteps++
						require.Equal(t, goType, node.Metadata["displayName"], node.ID)
						continue
					}
					require.NotContains(t, node.Metadata, "displayName", node.ID)
				}
				require.Equal(t, registered, analyzedNames, schemaVersion)
				require.Positive(t, defaultNamedSteps)
				if schemaVersion == flowviz.SchemaVersionV2 {
					require.NotNil(t, graph.V2.Start)
					require.Equal(t, registered[graph.Flow.StartStepID], graph.V2.Start.StepType)
				}
			}
		})
	}
}

func TestVisualizeGoRejectsUnknowableTypeNames(t *testing.T) {
	moduleDirectory := registeredTypeNamesFixtureDirectory(t)
	for _, test := range []struct {
		name            string
		code            string
		messageContains []string
	}{
		{name: "differentreturns", code: "dynamic_type_name", messageContains: []string{"differentreturns.Flow", "GetFlowType"}},
		{name: "nestedonly", code: "dynamic_type_name", messageContains: []string{"nestedonly.Flow", "GetFlowType"}},
		{name: "variablereturn", code: "dynamic_type_name", messageContains: []string{"variablereturn.finish", "GetStepType"}},
		{name: "dynamicpromoted", code: "dynamic_type_name", messageContains: []string{"dynamicpromoted.finish", "GetStepType"}},
		{name: "interfacepromoted", code: "dynamic_type_name", messageContains: []string{"interfacepromoted.finish", "GetStepType"}},
		{name: "genericflow", code: "generic_flow_type_name", messageContains: []string{"genericflow.Flow", "GetFlowType"}},
		{name: "structtypeargument", code: "unsupported_generic_type_name", messageContains: []string{"structtypeargument.box", "GetStepType"}},
		{name: "twoinstantiations", code: "duplicate_step_type", messageContains: []string{"generic Step box"}},
		{name: "duplicatesteptype", code: "duplicate_step_type", messageContains: []string{`"SharedStepType"`, "first", "second"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph, err := flowviz.Analyze(
				context.Background(),
				filepath.Join(moduleDirectory, "unknowable", test.name, "workflow.go"),
				flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV1},
			)
			require.NoError(t, err)
			require.False(t, graph.Valid)
			messages := make([]string, 0)
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == test.code {
					messages = append(messages, diagnostic.Message)
				}
			}
			require.Len(t, messages, 1, "%+v", graph.Diagnostics)
			for _, fragment := range test.messageContains {
				require.Contains(t, messages[0], fragment)
			}
		})
	}
}

func TestVisualizeGoStepRefTargetsRegisteredStepTypes(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	fixtureDirectory := filepath.Join(repositoryRoot, "cli/internal/command/testfixtures/connector-factory")

	registeredTarget, err := flowviz.Analyze(
		context.Background(),
		filepath.Join(fixtureDirectory, "stepref/workflow.go"),
		flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV1},
	)
	require.NoError(t, err)
	require.True(t, registeredTarget.Valid, "%+v", registeredTarget.Diagnostics)
	require.True(t, hasTransitionFromStepToTarget(registeredTarget, "step:begin", "step:done"))

	goTypeTarget, err := flowviz.Analyze(
		context.Background(),
		filepath.Join(fixtureDirectory, "steprefbare/workflow.go"),
		flowviz.AnalyzeOptions{SchemaVersion: flowviz.SchemaVersionV1},
	)
	require.NoError(t, err)
	require.False(t, goTypeTarget.Valid)
	require.Contains(t, diagnosticCodes(goTypeTarget.Diagnostics), "unknown_step_target")
	require.True(t, hasTransitionFromStepToTarget(goTypeTarget, "step:begin", "unknown:step:done"))
}

func registeredTypeNamesFixtureDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(visualizerRepositoryRoot(t), "cli/internal/command/testfixtures/registered-type-names")
}

func sdkRegisteredTypeNames(t *testing.T, moduleDirectory string, arguments []string) map[string]string {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = moduleDirectory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	registered := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, registeredTypeNameLinePrefix) {
			continue
		}
		nodeID, name, found := strings.Cut(strings.TrimPrefix(line, registeredTypeNameLinePrefix), " ")
		require.True(t, found, line)
		registered[nodeID] = name
	}
	require.Contains(t, registered, "flow", string(output))
	return registered
}

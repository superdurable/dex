// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package dex

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/urfave/cli"
)

func TestEmbeddedWebDirectsToStandaloneCommand(t *testing.T) {
	_, err := newApplication(&config.Config{}, serviceSelection{isWebEnabled: true})
	require.EqualError(t, err, "embedded Dex Web was removed from dex-server; run dex-web separately")
}

func TestWebConfigDefaultsAndOverrides(t *testing.T) {
	defaultConfig := config.Config{}
	require.Equal(t, config.DefaultWebBindAddress, defaultConfig.Web.EffectiveBindAddress())
	require.Equal(t, config.DefaultWebPort, defaultConfig.Web.EffectivePort())
	require.Equal(t, "localhost:8801", defaultConfig.GetWebFlowServiceTargetWithDefault())

	configPath := filepath.Join(t.TempDir(), "web.yaml")
	configBody := `
api:
  port: 9801
web:
  bindAddress: 127.0.0.1
  port: 9802
  flowServiceTarget: dex-api.example:8801
  flowRenderingDirectory: /var/lib/dex/flow-definitions
`
	require.NoError(t, os.WriteFile(configPath, []byte(configBody), 0o600))
	cfg, err := config.NewConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.Web.EffectiveBindAddress())
	require.Equal(t, 9802, cfg.Web.EffectivePort())
	require.Equal(t, "dex-api.example:8801", cfg.GetWebFlowServiceTargetWithDefault())
	require.Equal(t, "/var/lib/dex/flow-definitions", cfg.Web.FlowRenderingDirectory)

	cfg.Web.FlowServiceTarget = ""
	require.Equal(t, "localhost:9801", cfg.GetWebFlowServiceTargetWithDefault())
}

func TestServiceSelection(t *testing.T) {
	testCases := []struct {
		name        string
		arguments   []string
		expected    serviceSelection
		errorString string
	}{
		{
			name: "default",
			expected: serviceSelection{
				isAPIEnabled:         true,
				isInterpreterEnabled: true,
				isMatchingEnabled:    true,
			},
		},
		{name: "web", arguments: []string{"--services", "web"}, expected: serviceSelection{isWebEnabled: true}},
		{name: "api", arguments: []string{"--services", "api"}, expected: serviceSelection{isAPIEnabled: true}},
		{name: "interpreter", arguments: []string{"--services", "interpreter"}, expected: serviceSelection{isInterpreterEnabled: true}},
		{name: "matching", arguments: []string{"--services", "matching"}, expected: serviceSelection{isMatchingEnabled: true}},
		{
			name:      "web and api",
			arguments: []string{"--services", "web, api"},
			expected:  serviceSelection{isWebEnabled: true, isAPIEnabled: true},
		},
		{
			name:      "web and interpreter",
			arguments: []string{"--services", "web, interpreter"},
			expected:  serviceSelection{isWebEnabled: true, isInterpreterEnabled: true},
		},
		{
			name:      "api and interpreter",
			arguments: []string{"--services", "api, interpreter"},
			expected:  serviceSelection{isAPIEnabled: true, isInterpreterEnabled: true},
		},
		{
			name:      "all explicit",
			arguments: []string{"--services", "web,api,interpreter"},
			expected: serviceSelection{
				isWebEnabled:         true,
				isAPIEnabled:         true,
				isInterpreterEnabled: true,
			},
		},
		{name: "empty", arguments: []string{"--services", ""}, errorString: "no services specified for starting"},
		{name: "unknown", arguments: []string{"--services", "scheduler"}, errorString: `invalid service "scheduler"`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cliContext := serviceCLIContext(t, testCase.arguments)
			selection, err := getServices(cliContext)
			if testCase.errorString != "" {
				require.EqualError(t, err, testCase.errorString)
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.expected, selection)
		})
	}
}

func serviceCLIContext(t *testing.T, arguments []string) *cli.Context {
	t.Helper()
	app := BuildCLI(context.Background())
	flagSet := flag.NewFlagSet("start", flag.ContinueOnError)
	for _, commandFlag := range app.Commands[0].Flags {
		commandFlag.Apply(flagSet)
	}
	require.NoError(t, flagSet.Parse(arguments))
	return cli.NewContext(app, flagSet, nil)
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/command"
)

type flowDefinitionUploadOutput struct {
	Definitions []struct {
		FlowType          string `json:"flowType"`
		Outcome           string `json:"outcome"`
		StoredDigest      string `json:"storedDigest"`
		StoredGeneratedAt string `json:"storedGeneratedAt"`
		UploadedDigest    string `json:"uploadedDigest"`
		// UploadedGeneratedAt appears only for KEPT_EXISTING.
		UploadedGeneratedAt string `json:"uploadedGeneratedAt"`
	} `json:"definitions"`
}

type flowDefinitionListOutput struct {
	FlowTypes []string `json:"flowTypes"`
}

// The local stack runs without --flow-rendering-dir; uploaded definitions replace that directory.
func TestFlowDefinitionUploadAndListAgainstLocalStack(t *testing.T) {
	ports := freePorts(t, 2)
	cfg := testConfig(t)
	cfg.DexPort = ports[0]
	cfg.WebPort = ports[1]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	require.Empty(t, cfg.FlowRenderingDirectory)
	output := &synchronizedBuffer{}
	runCtx, cancel := context.WithCancel(context.Background())
	runFinished := make(chan error, 1)
	go func() { runFinished <- newSupervisor(cfg, output, output).Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runFinished:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Error("local stack did not stop")
		}
	})
	waitForHealthyWeb(t, "http://127.0.0.1:"+strconv.Itoa(cfg.WebPort)+"/healthz", runFinished)
	dexAddress := net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.DexPort))

	// More Flow types than one ListFlowDefinitions page (1000) and one upload request (256).
	const flowTypeCount = 1001
	definitionDirectory := t.TempDir()
	nestedDirectory := filepath.Join(definitionDirectory, "nested")
	require.NoError(t, os.Mkdir(nestedDirectory, 0o755))
	expectedFlowTypes := make([]string, 0, flowTypeCount)
	for index := range flowTypeCount {
		flowType := fmt.Sprintf("CliUploadFlow%04d", index)
		expectedFlowTypes = append(expectedFlowTypes, flowType)
		directory := definitionDirectory
		if index%2 == 1 {
			directory = nestedDirectory
		}
		content := fmt.Sprintf(`{"schemaVersion":"2.0","generatedAt":"2026-01-02T03:04:05Z","flow":{"name":%q}}`, flowType)
		require.NoError(t, os.WriteFile(filepath.Join(directory, flowType+".json"), []byte(content), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(definitionDirectory, "README.md"), []byte("ignored"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(nestedDirectory, "package.json"), []byte(`{"name":"skipped"}`), 0o600))

	upload := runFlowDefinitionCommand[flowDefinitionUploadOutput](t, "flow-definition", "upload", definitionDirectory, "--server", dexAddress)
	require.Len(t, upload.Definitions, flowTypeCount)
	for index, definition := range upload.Definitions {
		require.Equal(t, expectedFlowTypes[index], definition.FlowType)
		require.Equal(t, "CREATED", definition.Outcome)
		require.Len(t, definition.StoredDigest, 64)
		require.Equal(t, "2026-01-02T03:04:05Z", definition.StoredGeneratedAt)
	}

	olderPath := filepath.Join(t.TempDir(), "older.json")
	olderContent := `{"schemaVersion":"2.0","generatedAt":"2025-01-01T00:00:00Z","flow":{"name":"CliUploadFlow0000","changed":true}}`
	require.NoError(t, os.WriteFile(olderPath, []byte(olderContent), 0o600))
	kept := runFlowDefinitionCommand[flowDefinitionUploadOutput](t, "flow-definition", "upload", olderPath, "--server", dexAddress)
	require.Len(t, kept.Definitions, 1)
	require.Equal(t, "KEPT_EXISTING", kept.Definitions[0].Outcome)
	require.Len(t, kept.Definitions[0].UploadedDigest, 64)
	require.NotEqual(t, kept.Definitions[0].StoredDigest, kept.Definitions[0].UploadedDigest)
	require.Equal(t, "2025-01-01T00:00:00Z", kept.Definitions[0].UploadedGeneratedAt)
	require.Equal(t, "2026-01-02T03:04:05Z", kept.Definitions[0].StoredGeneratedAt)
	forced := runFlowDefinitionCommand[flowDefinitionUploadOutput](t, "flow-definition", "upload", olderPath, "--force", "--server", dexAddress)
	require.Equal(t, "REPLACED", forced.Definitions[0].Outcome)
	require.Equal(t, "2025-01-01T00:00:00Z", forced.Definitions[0].StoredGeneratedAt)

	list := runFlowDefinitionCommand[flowDefinitionListOutput](t, "flow-definition", "list", "--server", dexAddress)
	require.ElementsMatch(t, expectedFlowTypes, list.FlowTypes)

	invalidPath := filepath.Join(t.TempDir(), "old-schema.json")
	require.NoError(t, os.WriteFile(invalidPath, []byte(`{"schemaVersion":"1.0","flow":{"name":"OldFlow"}}`), 0o600))
	cliApp := command.NewApp(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	err := cliApp.Execute(context.Background(), []string{"flow-definition", "upload", invalidPath, "--server", dexAddress})
	require.ErrorContains(t, err, `schemaVersion must start with "2."`)
}

func runFlowDefinitionCommand[T any](t *testing.T, arguments ...string) T {
	t.Helper()
	cliOutput := &bytes.Buffer{}
	cliErrors := &bytes.Buffer{}
	cliApp := command.NewApp(bytes.NewReader(nil), cliOutput, cliErrors)
	require.NoError(t, cliApp.Execute(context.Background(), arguments), "stderr=%s", cliErrors.String())
	var decoded T
	require.NoError(t, json.Unmarshal(cliOutput.Bytes(), &decoded), "output=%s", cliOutput.String())
	return decoded
}

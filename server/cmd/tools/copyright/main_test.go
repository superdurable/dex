// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeaderTaskUpgradesLegacyAndCreatesNewHeader(t *testing.T) {
	repositoryRoot := t.TempDir()
	runTestGit(t, repositoryRoot, "init", "-q")
	runTestGit(t, repositoryRoot, "config", "user.name", "Copyright Test")
	runTestGit(t, repositoryRoot, "config", "user.email", "copyright@example.com")

	legacyContent := "// Copyright (c) 2020 Legacy Holder\n" +
		"//\n" +
		"// Permission is hereby granted under the legacy MIT terms.\n\n" +
		"package server\n\nfunc Legacy() int { return 1 }\n"
	writeTestFile(t, repositoryRoot, "server/legacy.go", legacyContent)
	runTestGit(t, repositoryRoot, "add", ".")
	runTestGit(t, repositoryRoot, "commit", "-q", "-m", "cutoff")
	cutoff := strings.TrimSpace(runTestGit(t, repositoryRoot, "rev-parse", "HEAD"))

	copyTestTemplates(t, repositoryRoot)
	writeTestJSON(t, repositoryRoot, "script/licenseheaders/policy.json", policy{
		Cutoff:                  cutoff,
		IncludedPrefixes:        []string{"server", "sdk-rust"},
		ForcedNewPrefixes:       []string{"sdk-rust"},
		ExcludedPrefixes:        []string{"docs", "examples"},
		RetainedLicensePrefixes: map[string]string{"examples/go": "mit"},
	})
	writeTestJSON(t, repositoryRoot, "script/licenseheaders/legacy-manifest.json", manifest{
		Cutoff: cutoff,
		Entries: map[string]manifestEntry{
			"server/legacy.go": {
				Classification: "legacy-only",
				BaselineHash:   hashBytes([]byte("package server\n\nfunc Legacy() int { return 1 }\n")),
			},
		},
	})
	writeTestFile(t, repositoryRoot, "server/legacy.go", strings.Replace(legacyContent, "return 1", "return 2", 1))
	writeTestFile(t, repositoryRoot, "sdk-rust/src/lib.rs", "pub fn sdk() {}\n")

	task, err := newHeaderTask(&config{rootDir: repositoryRoot})
	require.NoError(t, err)
	require.NoError(t, task.run())

	mixedContent, err := os.ReadFile(filepath.Join(repositoryRoot, "server/legacy.go"))
	require.NoError(t, err)
	require.Contains(t, string(mixedContent), "Copyright (c) 2020 Legacy Holder")
	require.Contains(t, string(mixedContent), "Modifications Copyright (c) 2026 Super Durable, Inc.")
	newContent, err := os.ReadFile(filepath.Join(repositoryRoot, "sdk-rust/src/lib.rs"))
	require.NoError(t, err)
	require.Contains(t, string(newContent), "SPDX-License-Identifier: LicenseRef-Super-Durable-1.0")

	verifyTask, err := newHeaderTask(&config{rootDir: repositoryRoot, verifyOnly: true})
	require.NoError(t, err)
	require.NoError(t, verifyTask.run())
}

func copyTestTemplates(t *testing.T, repositoryRoot string) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sourceDir := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "script", "licenseheaders")
	destinationDir := filepath.Join(repositoryRoot, "script", "licenseheaders")
	require.NoError(t, os.MkdirAll(destinationDir, 0o755))
	for _, fileName := range []string{
		"apache-2.0.txt",
		"legacy-reference.txt",
		"mit.txt",
		"mixed.txt",
		"super-durable-1.0.txt",
	} {
		content, err := os.ReadFile(filepath.Join(sourceDir, fileName))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(destinationDir, fileName), content, 0o644))
	}
}

func writeTestJSON(t *testing.T, repositoryRoot string, relativePath string, value any) {
	content, err := json.Marshal(value)
	require.NoError(t, err)
	writeTestFile(t, repositoryRoot, relativePath, string(content))
}

func writeTestFile(t *testing.T, repositoryRoot string, relativePath string, content string) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func runTestGit(t *testing.T, repositoryRoot string, arguments ...string) string {
	command := exec.Command("git", arguments...)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return string(output)
}

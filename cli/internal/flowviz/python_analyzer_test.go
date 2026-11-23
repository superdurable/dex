// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonAnalyzerScriptIsPassedAsPrivateFile(t *testing.T) {
	command, removeScript, err := newPythonAnalyzerCommand(t.Context(), "python3")
	require.NoError(t, err)
	t.Cleanup(removeScript)
	for _, argument := range command.Args {
		require.Less(t, len(argument), 100*1024)
	}
	require.Len(t, command.Args, 4)
	require.Equal(t, []string{"python3", "-I", "-S"}, command.Args[:3])
	scriptPath := command.Args[3]
	directory := filepath.Dir(scriptPath)
	directoryInfo, err := os.Stat(directory)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), directoryInfo.Mode().Perm())
	scriptInfo, err := os.Stat(scriptPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), scriptInfo.Mode().Perm())
	removeScript()
	require.NoDirExists(t, directory)
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecuteRequiresAMode(t *testing.T) {
	var stderr bytes.Buffer
	err := Execute(context.Background(), nil, os.Stdin, os.Stdout, &stderr, "test")
	require.EqualError(t, err, "agents-tui: pass --channel-fd, --describe, or --demo")
}

func TestExecuteRejectsArguments(t *testing.T) {
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--demo", "extra"}, os.Stdin, os.Stdout, &stderr, "test")
	var exitError *ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, 2, exitError.Code)
	require.Empty(t, stderr.String())
}

// A bad flag exits 2 and writes nothing, because a launcher's stderr may be
// the terminal it hands over.
func TestExecuteDropsFlagErrors(t *testing.T) {
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--channel-fd=bad"}, os.Stdin, os.Stdout, &stderr, "test")
	var exitError *ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, 2, exitError.Code)
	require.Empty(t, stderr.String())
}

func TestExecuteDemoNeedsATerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		require.NoError(t, writer.Close())
	})
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--demo"}, reader, writer, &stderr, "test")
	require.EqualError(t, err, "agents-tui: --demo needs a terminal on stdin and stdout")
}

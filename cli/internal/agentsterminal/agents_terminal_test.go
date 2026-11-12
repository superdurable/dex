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

func TestExecuteRequiresDemo(t *testing.T) {
	var stderr bytes.Buffer
	err := Execute(context.Background(), nil, os.Stdin, os.Stdout, &stderr)
	require.EqualError(t, err, "agents-tui: only --demo is available in this version")
}

func TestExecuteRejectsArguments(t *testing.T) {
	var stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--demo", "extra"}, os.Stdin, os.Stdout, &stderr)
	require.EqualError(t, err, "agents-tui: unexpected arguments: [extra]")
}

func TestExecuteDemoNeedsATerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reader.Close())
		require.NoError(t, writer.Close())
	})
	var stderr bytes.Buffer
	err = Execute(context.Background(), []string{"--demo"}, reader, writer, &stderr)
	require.EqualError(t, err, "agents-tui: --demo needs a terminal on stdin and stdout")
}

func TestIsTerminalReflowing(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		}
	}
	require.True(t, isTerminalReflowing(lookup(map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"})))
	require.False(t, isTerminalReflowing(lookup(map[string]string{"TERM": "xterm", "XTERM_VERSION": "XTerm(390)"})))
	require.False(t, isTerminalReflowing(lookup(map[string]string{"TERM": "linux"})))
}

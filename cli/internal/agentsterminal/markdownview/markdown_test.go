// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package markdownview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

func TestRenderMarkdownBlocks(t *testing.T) {
	source := strings.Join([]string{
		"# Flaky tests",
		"",
		"Found **3** open issues with `go test` and *retries*. See [the tracker](https://example.com/issues).",
		"",
		"1. First",
		"2. Second item that is long enough to wrap onto another line",
		"",
		"> Quoted note",
		"",
		"```go",
		"backoff := 2 * time.Second // a very long comment that does not fit on one line",
		"```",
		"",
		"---",
	}, "\n")
	theme := terminaltheme.NewTheme(colorprofile.ANSI, true)
	lines := RenderMarkdown(source, 40, theme)
	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		require.LessOrEqual(t, terminaltheme.DisplayWidth(line), 40, "line %q is too wide", line)
		plain = append(plain, strings.TrimRight(terminaltheme.StripStyles(line), " "))
	}
	require.Equal(t, []string{
		"Flaky tests",
		"",
		"Found 3 open issues with go test and",
		"retries. See the tracker",
		"(https://example.com/issues).",
		"",
		"1. First",
		"2. Second item that is long enough to",
		"   wrap onto another line",
		"",
		"│ Quoted note",
		"",
		"                                      go",
		"│ backoff := 2 * time.Second // a very …",
		"",
		"────────────────────────",
	}, plain)
}

func TestRenderMarkdownStylesOnlyWhatItMarks(t *testing.T) {
	theme := terminaltheme.NewTheme(colorprofile.ANSI, true)
	lines := RenderMarkdown("plain `code`", 40, theme)
	require.Len(t, lines, 1)
	require.True(t, strings.HasPrefix(lines[0], "plain "), "body text keeps the default color: %q", lines[0])
	require.Contains(t, lines[0], "\x1b[2m", "inline code is dim")
}

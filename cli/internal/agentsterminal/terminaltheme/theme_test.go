// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package terminaltheme

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func lookupFrom(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestTerminalColorProfileHonorsAnyNoColorValue(t *testing.T) {
	for _, value := range []string{"1", "yes", "anything"} {
		profile := TerminalColorProfile(colorprofile.TrueColor, lookupFrom(map[string]string{"NO_COLOR": value}))
		require.Equal(t, colorprofile.ASCII, profile, "NO_COLOR=%s", value)
	}
	require.Equal(t, colorprofile.ANSI256, TerminalColorProfile(colorprofile.ANSI256, lookupFrom(map[string]string{"NO_COLOR": ""})))
	require.Equal(t, colorprofile.NoTTY, TerminalColorProfile(colorprofile.NoTTY, lookupFrom(map[string]string{"NO_COLOR": "1"})))
}

func TestDownsamplingKeepsStylesWithoutColors(t *testing.T) {
	theme := NewTheme(colorprofile.TrueColor, true)
	styled := theme.Focused.Render("Approve") + theme.Ok.Render("✓") + theme.Secondary.Render("dim")
	require.Equal(t, "\x1b[1;34mApprove\x1b[m\x1b[32m✓\x1b[m\x1b[2mdim\x1b[m", DownsampleStyledText(colorprofile.TrueColor, styled), "truecolor keeps the basic ANSI colors")
	require.Equal(t, "\x1b[1mApprove\x1b[m\x1b[m✓\x1b[m\x1b[2mdim\x1b[m", DownsampleStyledText(colorprofile.ASCII, styled), "NO_COLOR keeps bold and dim")
}

func TestNoColorThemeFocusesWithReverseVideo(t *testing.T) {
	theme := NewTheme(colorprofile.ASCII, true)
	require.False(t, theme.HasColor)
	require.Equal(t, "\x1b[1;7mApprove\x1b[m", theme.Focused.Render("Approve"))
}

func TestIsUnicodeLocale(t *testing.T) {
	require.True(t, IsUnicodeLocale(lookupFrom(map[string]string{"LANG": "en_US.UTF-8"})))
	require.True(t, IsUnicodeLocale(lookupFrom(map[string]string{"LC_ALL": "C.utf8", "LANG": "C"})))
	require.False(t, IsUnicodeLocale(lookupFrom(map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"})), "LC_ALL wins")
	require.False(t, IsUnicodeLocale(lookupFrom(map[string]string{})))
	require.Equal(t, "+", NewTheme(colorprofile.ANSI, false).Symbols.Succeeded)
}

func TestWidthHelpers(t *testing.T) {
	require.Equal(t, WidthTooNarrow, ClassifyWidth(39))
	require.Equal(t, WidthCompact, ClassifyWidth(40))
	require.Equal(t, WidthNarrow, ClassifyWidth(60))
	require.Equal(t, WidthMedium, ClassifyWidth(80))
	require.Equal(t, WidthFull, ClassifyWidth(100))
	require.Equal(t, "…gpt-5.2-codex", TruncateKeepingTail("openai/gpt-5.2-codex", 14, "…"))
	require.Equal(t, "日本…", TruncateToWidth("日本語のテキスト", 5, "…"), "wide characters count two cells")
}

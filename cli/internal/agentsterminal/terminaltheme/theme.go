// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package terminaltheme holds the agents terminal palette, status symbols,
// and the width rules that every agents terminal screen shares.
package terminaltheme

import (
	"bytes"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// The palette uses only the 16 basic ANSI colors, so it follows the terminal
// theme.
const (
	accentColor       = lipgloss.Blue
	accentStrongColor = lipgloss.BrightBlue
	okColor           = lipgloss.Green
	errorColor        = lipgloss.Red
	warningColor      = lipgloss.Yellow
	infoColor         = lipgloss.Cyan
)

// Theme is the set of styles and symbols one agents terminal screen draws
// with. Build it once with NewTheme.
type Theme struct {
	// Symbols holds the status symbols: Unicode by default, ASCII when the
	// locale is not UTF-8.
	Symbols SymbolSet
	// IsUnicode reports Unicode symbols; false means the ASCII fallback.
	IsUnicode bool
	// HasColor reports kept colors. Without them, focus uses reverse video.
	HasColor bool

	// Title styles headings: bold and the strong accent.
	Title lipgloss.Style
	// Accent styles the focus marker, the popup border, and key numbers.
	Accent lipgloss.Style
	// Secondary styles arguments, durations, hints, and previews (dim).
	Secondary lipgloss.Style
	// Strong styles bold body text.
	Strong lipgloss.Style
	// Emphasis styles italic body text.
	Emphasis lipgloss.Style
	// Focused styles the focused option text.
	Focused lipgloss.Style
	// Ok styles only the success symbol.
	Ok lipgloss.Style
	// Error styles only the failure symbol and removed diff lines' sign.
	Error lipgloss.Style
	// Warning styles the attention and warning symbols.
	Warning lipgloss.Style
	// Info styles the spinner and the running symbol.
	Info lipgloss.Style
	// BrandMark styles the startup logo's chevron: the brand green, the one
	// color that is not a status.
	BrandMark lipgloss.Style
}

// NewTheme returns the theme for profile. isUnicode selects Unicode symbols.
func NewTheme(profile colorprofile.Profile, isUnicode bool) Theme {
	hasColor := profile >= colorprofile.ANSI
	base := lipgloss.NewStyle()
	theme := Theme{
		Symbols:   asciiSymbols,
		HasColor:  hasColor,
		Title:     base.Bold(true).Foreground(accentStrongColor),
		Accent:    base.Foreground(accentColor),
		Secondary: base.Faint(true),
		Strong:    base.Bold(true),
		Emphasis:  base.Italic(true),
		Focused:   base.Bold(true).Foreground(accentColor),
		Ok:        base.Foreground(okColor),
		Error:     base.Foreground(errorColor),
		Warning:   base.Foreground(warningColor),
		Info:      base.Foreground(infoColor),
		BrandMark: base.Foreground(okColor),
		IsUnicode: isUnicode,
	}
	if isUnicode {
		theme.Symbols = unicodeSymbols
	}
	if !hasColor {
		theme.Focused = base.Bold(true).Reverse(true)
	}
	return theme
}

// IsUnicodeLocale reports a UTF-8 locale, reading LC_ALL, LC_CTYPE, then
// LANG.
func IsUnicodeLocale(lookupEnv func(string) (string, bool)) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value, ok := lookupEnv(name)
		if !ok || value == "" {
			continue
		}
		upper := strings.ToUpper(value)
		return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
	}
	return false
}

// TerminalColorProfile returns detected, or ASCII when NO_COLOR has any
// value.
func TerminalColorProfile(detected colorprofile.Profile, lookupEnv func(string) (string, bool)) colorprofile.Profile {
	if value, ok := lookupEnv("NO_COLOR"); ok && value != "" && detected > colorprofile.ASCII {
		return colorprofile.ASCII
	}
	return detected
}

// DownsampleStyledText rewrites the SGR sequences in text for profile.
func DownsampleStyledText(profile colorprofile.Profile, text string) string {
	var buffer bytes.Buffer
	writer := colorprofile.Writer{Forward: &buffer, Profile: profile}
	if _, err := writer.WriteString(text); err != nil {
		// bytes.Buffer never fails; keep the original text if it ever does.
		return text
	}
	return buffer.String()
}

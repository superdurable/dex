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
	"github.com/charmbracelet/x/ansi"
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

// The logo is the exception: it uses the Dex brand colors of Dex Web and the
// docs site, one pair per background.
const (
	brandTrackOnLightColor = "#00321b"
	brandHeadOnLightColor  = "#008650"
	brandTrackOnDarkColor  = "#f6f8f4"
	brandHeadOnDarkColor   = "#70eea9"
)

// BrandColors styles the two parts of the Dex logo.
type BrandColors struct {
	// Track styles the logo's bar.
	Track lipgloss.Style
	// Head styles the logo's chevron.
	Head lipgloss.Style
}

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
	// Link styles a URL: the accent color, underlined. It is an ansi.Style
	// because lipgloss underlines each character separately.
	Link ansi.Style
	// BrandOnDark and BrandOnLight style the startup logo in the brand
	// colors, the only colors that are not statuses, for a dark or a light
	// terminal background.
	BrandOnDark  BrandColors
	BrandOnLight BrandColors
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
		Link:      ansi.NewStyle().Underline(true).ForegroundColor(accentColor),
		BrandOnDark: BrandColors{
			Track: base.Foreground(lipgloss.Color(brandTrackOnDarkColor)),
			Head:  base.Foreground(lipgloss.Color(brandHeadOnDarkColor)),
		},
		BrandOnLight: BrandColors{
			Track: base.Foreground(lipgloss.Color(brandTrackOnLightColor)),
			Head:  base.Foreground(lipgloss.Color(brandHeadOnLightColor)),
		},
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

// RenderLink draws text as an underlined OSC 8 hyperlink to url, which
// terminals open on a click or a Cmd/Ctrl-click. A url with spaces or control
// characters is styled but not linked.
func (theme Theme) RenderLink(url string, text string) string {
	if url == "" || strings.ContainsFunc(url, isOutsideLinkCharacters) {
		return theme.Link.Styled(text)
	}
	// ST, not BEL, ends the sequences: BEL is a control character the
	// screen otherwise never draws.
	return "\x1b]8;;" + url + "\x1b\\" + theme.Link.Styled(text) + "\x1b]8;;\x1b\\"
}

func isOutsideLinkCharacters(character rune) bool {
	return character <= ' ' || character > '~'
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

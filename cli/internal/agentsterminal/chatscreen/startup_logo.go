// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// The welcome logo needs this much room; smaller terminals skip it.
const (
	minimumLogoWidth  = 40
	minimumLogoHeight = 15
)

// logoRow is one row of the brand mark: the bar, the chevron, and the text.
type logoRow struct {
	bar     string
	chevron string
	text    string
	isTitle bool
}

var unicodeLogoRows = []logoRow{
	{bar: "▐█▌", chevron: " ▜▙"},
	{bar: "▐█▌", chevron: "  ▜█▙", text: "Dex", isTitle: true},
	{bar: "▐█▌", chevron: "  ▟█▛", text: "Super Durable"},
	{bar: "▐█▌", chevron: " ▟▛"},
}

var asciiLogoRows = []logoRow{
	{bar: "|#|", chevron: " \\\\"},
	{bar: "|#|", chevron: "  \\\\", text: "Dex", isTitle: true},
	{bar: "|#|", chevron: "  //", text: "Super Durable"},
	{bar: "|#|", chevron: " //"},
}

// welcomeBlock opens the message view: the logo, where the session runs,
// and how to start.
type welcomeBlock struct {
	session            sessionfeed.SessionOpened
	isLogoShown        bool
	hasLightBackground bool
}

func (block welcomeBlock) Render(width int, theme terminaltheme.Theme) []string {
	var lines []string
	if block.isLogoShown {
		lines = append(lines, renderStartupLogo(block.brandColors(theme), theme)...)
		lines = append(lines, "")
	}
	session := block.session
	sessionText := session.SessionID
	if sessionText != "" && session.IsNewSession {
		sessionText += theme.Secondary.Render(" (new)")
	} else if sessionText != "" {
		sessionText += theme.Secondary.Render(" (resumed)")
	}
	webLink := ""
	if session.WebURL != "" {
		webLink = theme.RenderLink(session.WebURL, session.WebURL)
	}
	rows := [][2]string{
		{"command", session.Command},
		{"model", session.Model},
		{"directory", session.Workspace},
		{"session", sessionText},
		{"Dex Web", webLink},
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		line := contentIndent + theme.Secondary.Render(terminaltheme.PadRight(row[0]+":", 11)) + row[1]
		lines = append(lines, terminaltheme.TruncateToWidth(line, width, theme.Symbols.Ellipsis))
	}
	tip := "Type a message to start. /help lists commands. Drag selects text; /copy copies the last reply."
	lines = append(lines, "")
	for _, line := range terminaltheme.WrapToWidth(tip, max(width-len(contentIndent), 1)) {
		lines = append(lines, contentIndent+theme.Secondary.Render(strings.TrimRight(line, " ")))
	}
	return lines
}

func (block welcomeBlock) brandColors(theme terminaltheme.Theme) terminaltheme.BrandColors {
	if block.hasLightBackground {
		return theme.BrandOnLight
	}
	return theme.BrandOnDark
}

// renderStartupLogo draws the brand mark. Its bar and green chevron use the
// only non-status colors.
func renderStartupLogo(brand terminaltheme.BrandColors, theme terminaltheme.Theme) []string {
	rows := asciiLogoRows
	if theme.IsUnicode {
		rows = unicodeLogoRows
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		line := contentIndent + brand.Track.Render(row.bar) + brand.Head.Render(row.chevron)
		if row.text != "" {
			gap := strings.Repeat(" ", 7-terminaltheme.DisplayWidth(row.chevron))
			text := theme.Secondary.Render(row.text)
			if row.isTitle {
				text = theme.Strong.Render(row.text)
			}
			line += gap + text
		}
		lines = append(lines, line)
	}
	return lines
}

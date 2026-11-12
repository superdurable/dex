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

	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// The startup logo needs this much room; smaller terminals skip it.
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

// renderStartupLogo draws the brand mark. Its green chevron is the one
// non-status color.
func renderStartupLogo(theme terminaltheme.Theme) []string {
	rows := asciiLogoRows
	if theme.IsUnicode {
		rows = unicodeLogoRows
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		line := contentIndent + theme.Secondary.Render(row.bar) + theme.BrandMark.Render(row.chevron)
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

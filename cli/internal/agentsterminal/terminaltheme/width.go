// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package terminaltheme

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// WidthClass is a terminal width breakpoint. Narrower classes first drop
// secondary columns, then truncate with an ellipsis.
type WidthClass int

const (
	// WidthTooNarrow is below 40 columns: only the input and a resize hint.
	WidthTooNarrow WidthClass = iota
	// WidthCompact is 40-59 columns: short tool rows and a status-only context
	// line.
	WidthCompact
	// WidthNarrow is 60-79 columns: tool rows drop durations; model names
	// are truncated.
	WidthNarrow
	// WidthMedium is 80-99 columns: tool rows drop result summaries.
	WidthMedium
	// WidthFull is 100 columns or more: everything is shown.
	WidthFull
)

// MinimumUsableWidth is the narrowest terminal that gets the full layout.
const MinimumUsableWidth = 40

// ClassifyWidth returns the breakpoint class of a terminal width.
func ClassifyWidth(width int) WidthClass {
	switch {
	case width >= 100:
		return WidthFull
	case width >= 80:
		return WidthMedium
	case width >= 60:
		return WidthNarrow
	case width >= MinimumUsableWidth:
		return WidthCompact
	default:
		return WidthTooNarrow
	}
}

// DisplayWidth returns the number of cells text occupies, ignoring escape
// sequences and counting wide characters as two cells.
func DisplayWidth(text string) int {
	return ansi.StringWidth(text)
}

// ExpandTabs replaces tabs with four spaces, because tab stops are unknown.
func ExpandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "    ")
}

// StripStyles removes escape sequences from text.
func StripStyles(text string) string {
	return ansi.Strip(text)
}

// TruncateToWidth cuts text to width cells, ending it with ellipsis.
func TruncateToWidth(text string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, ellipsis)
}

// TruncateKeepingTail cuts text to width cells from its start.
func TruncateKeepingTail(text string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	textWidth := ansi.StringWidth(text)
	if textWidth <= width {
		return text
	}
	return ansi.TruncateLeft(text, textWidth-width+ansi.StringWidth(ellipsis), ellipsis)
}

// PadRight pads text with spaces to width cells. Wider text is unchanged.
func PadRight(text string, width int) string {
	gap := width - ansi.StringWidth(text)
	if gap <= 0 {
		return text
	}
	return text + strings.Repeat(" ", gap)
}

// PadLeft right-aligns text in width cells. Wider text is unchanged.
func PadLeft(text string, width int) string {
	gap := width - ansi.StringWidth(text)
	if gap <= 0 {
		return text
	}
	return strings.Repeat(" ", gap) + text
}

// WrapToWidth word-wraps text to width cells, breaking overlong words.
func WrapToWidth(text string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}

// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package terminaltheme

// SymbolSet holds one single-cell symbol per status class and the structural
// glyphs.
type SymbolSet struct {
	// SpinnerFrames animates running work.
	SpinnerFrames []string
	// Running is the still frame of running work.
	Running string
	// Succeeded marks succeeded tools and completed sessions.
	Succeeded string
	// Failed marks failed tools and turns.
	Failed string
	// AwaitingUser marks work that waits for an approval or an answer.
	AwaitingUser string
	// Warning marks an unknown outcome or a warning notice.
	Warning string
	// NotRun marks rejected, cancelled, or skipped work.
	NotRun string
	// Queued marks queued or waiting work and neutral notes.
	Queued string
	// Focus marks the focused list item or option.
	Focus string
	// UserPrompt prefixes user messages and the input box.
	UserPrompt string
	// Gutter prefixes tool output, code blocks, and quotes.
	Gutter string
	// Ellipsis ends truncated text.
	Ellipsis string
	// Separator joins the parts of the context line and the key hints.
	Separator string
	// Rule draws horizontal lines.
	Rule string
	// Box corners and sides draw the popup border.
	BoxTopLeft     string
	BoxTopRight    string
	BoxBottomLeft  string
	BoxBottomRight string
	BoxVertical    string
	// DiffAdded and DiffRemoved prefix diff preview lines.
	DiffAdded   string
	DiffRemoved string
}

var unicodeSymbols = SymbolSet{
	SpinnerFrames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Running:        "●",
	Succeeded:      "✓",
	Failed:         "✗",
	AwaitingUser:   "?",
	Warning:        "!",
	NotRun:         "○",
	Queued:         "·",
	Focus:          "▸",
	UserPrompt:     "›",
	Gutter:         "│",
	Ellipsis:       "…",
	Separator:      " · ",
	Rule:           "─",
	BoxTopLeft:     "╭",
	BoxTopRight:    "╮",
	BoxBottomLeft:  "╰",
	BoxBottomRight: "╯",
	BoxVertical:    "│",
	DiffAdded:      "+",
	DiffRemoved:    "-",
}

var asciiSymbols = SymbolSet{
	SpinnerFrames:  []string{"|", "/", "-", "\\"},
	Running:        "*",
	Succeeded:      "+",
	Failed:         "x",
	AwaitingUser:   "?",
	Warning:        "!",
	NotRun:         "o",
	Queued:         ".",
	Focus:          ">",
	UserPrompt:     ">",
	Gutter:         "|",
	Ellipsis:       "~",
	Separator:      " - ",
	Rule:           "-",
	BoxTopLeft:     "+",
	BoxTopRight:    "+",
	BoxBottomLeft:  "+",
	BoxBottomRight: "+",
	BoxVertical:    "|",
	DiffAdded:      "+",
	DiffRemoved:    "-",
}

// SpinnerFrame returns the spinner frame for a frame counter.
func (symbols SymbolSet) SpinnerFrame(frame int) string {
	if frame < 0 {
		frame = -frame
	}
	return symbols.SpinnerFrames[frame%len(symbols.SpinnerFrames)]
}

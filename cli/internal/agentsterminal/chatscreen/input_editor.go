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
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// inputEditor is a multi-line text field. The cursor is a rune index into
// the text.
type inputEditor struct {
	text   []rune
	cursor int
}

// renderedInput is an editor drawn at one width. cursorRow and cursorColumn
// locate the cursor within lines.
type renderedInput struct {
	lines        []string
	cursorRow    int
	cursorColumn int
}

func (editor *inputEditor) Value() string {
	return string(editor.text)
}

func (editor *inputEditor) IsEmpty() bool {
	return len(editor.text) == 0
}

func (editor *inputEditor) SetValue(value string) {
	editor.text = []rune(value)
	editor.cursor = len(editor.text)
}

func (editor *inputEditor) Clear() {
	editor.text = nil
	editor.cursor = 0
}

// HandleEditingKey applies a cursor or editing key and reports whether the
// key was one.
func (editor *inputEditor) HandleEditingKey(key tea.KeyPressMsg) bool {
	switch key.String() {
	case "left", "ctrl+b":
		editor.cursor = max(editor.cursor-1, 0)
	case "right", "ctrl+f":
		editor.cursor = min(editor.cursor+1, len(editor.text))
	case "home", "ctrl+a":
		editor.cursor = editor.lineStart(editor.cursor)
	case "end", "ctrl+e":
		editor.cursor = editor.lineEnd(editor.cursor)
	case "backspace", "ctrl+h":
		editor.deleteRange(editor.cursor-1, editor.cursor)
	case "delete":
		editor.deleteRange(editor.cursor, editor.cursor+1)
	case "ctrl+w", "alt+backspace":
		editor.deleteRange(editor.wordStart(editor.cursor), editor.cursor)
	case "ctrl+u":
		editor.deleteRange(editor.lineStart(editor.cursor), editor.cursor)
	case "ctrl+k":
		editor.deleteRange(editor.cursor, editor.lineEnd(editor.cursor))
	case "alt+left", "ctrl+left":
		editor.cursor = editor.wordStart(editor.cursor)
	case "alt+right", "ctrl+right":
		editor.cursor = editor.wordEnd(editor.cursor)
	default:
		if key.Text == "" || key.Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
			return false
		}
		editor.InsertText(key.Text)
	}
	return true
}

// InsertText inserts text at the cursor. Carriage returns become newlines
// and tabs become spaces.
func (editor *inputEditor) InsertText(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	inserted := []rune(text)
	updated := make([]rune, 0, len(editor.text)+len(inserted))
	updated = append(updated, editor.text[:editor.cursor]...)
	updated = append(updated, inserted...)
	updated = append(updated, editor.text[editor.cursor:]...)
	editor.text = updated
	editor.cursor += len(inserted)
}

// IsCursorOnFirstLine reports whether the cursor is on the first logical line.
func (editor *inputEditor) IsCursorOnFirstLine() bool {
	return !strings.ContainsRune(string(editor.text[:editor.cursor]), '\n')
}

// IsCursorOnLastLine reports whether the cursor is on the last logical line.
func (editor *inputEditor) IsCursorOnLastLine() bool {
	return !strings.ContainsRune(string(editor.text[editor.cursor:]), '\n')
}

// MoveCursorVertically moves the cursor one logical line up or down,
// keeping its column where the line is long enough.
func (editor *inputEditor) MoveCursorVertically(isUp bool) {
	column := editor.cursor - editor.lineStart(editor.cursor)
	if isUp {
		start := editor.lineStart(editor.cursor)
		if start == 0 {
			return
		}
		previousStart := editor.lineStart(start - 1)
		editor.cursor = min(previousStart+column, start-1)
		return
	}
	end := editor.lineEnd(editor.cursor)
	if end == len(editor.text) {
		return
	}
	nextStart := end + 1
	editor.cursor = min(nextStart+column, editor.lineEnd(nextStart))
}

// Render draws the editor with prompt and placeholder. maxRows limits the
// rows, keeping the cursor visible.
func (editor *inputEditor) Render(prompt string, placeholder string, width int, maxRows int) renderedInput {
	promptWidth := ansi.StringWidth(prompt)
	contentWidth := max(width-promptWidth, 1)
	if len(editor.text) == 0 {
		line := prompt + ansi.Truncate(placeholder, contentWidth, "…")
		return renderedInput{lines: []string{line}, cursorRow: 0, cursorColumn: promptWidth}
	}
	builder := inputRowBuilder{}
	cursorRow, cursorColumn := 0, promptWidth
	for index, character := range editor.text {
		if index == editor.cursor {
			cursorRow, cursorColumn = len(builder.rows), promptWidth+builder.currentWidth
		}
		if character == '\n' {
			builder.finishRow()
			continue
		}
		characterWidth := ansi.StringWidth(string(character))
		if builder.currentWidth+characterWidth > contentWidth {
			builder.finishRow()
			if index == editor.cursor {
				cursorRow, cursorColumn = len(builder.rows), promptWidth
			}
		}
		builder.current = append(builder.current, character)
		builder.currentWidth += characterWidth
	}
	if editor.cursor == len(editor.text) {
		if builder.currentWidth >= contentWidth {
			builder.finishRow()
		}
		cursorRow, cursorColumn = len(builder.rows), promptWidth+builder.currentWidth
	}
	builder.finishRow()
	rows := builder.rows
	firstVisibleRow := 0
	if maxRows > 0 && len(rows) > maxRows {
		firstVisibleRow = min(max(cursorRow-maxRows+1, 0), len(rows)-maxRows)
		rows = rows[firstVisibleRow : firstVisibleRow+maxRows]
	}
	indent := strings.Repeat(" ", promptWidth)
	lines := make([]string, 0, len(rows))
	for rowIndex, row := range rows {
		if rowIndex == 0 && firstVisibleRow == 0 {
			lines = append(lines, prompt+row)
			continue
		}
		lines = append(lines, indent+row)
	}
	return renderedInput{lines: lines, cursorRow: cursorRow - firstVisibleRow, cursorColumn: cursorColumn}
}

// inputRowBuilder collects the visual rows of a wrapped input.
type inputRowBuilder struct {
	rows         []string
	current      []rune
	currentWidth int
}

func (builder *inputRowBuilder) finishRow() {
	builder.rows = append(builder.rows, string(builder.current))
	builder.current = builder.current[:0]
	builder.currentWidth = 0
}

func (editor *inputEditor) deleteRange(start int, end int) {
	start = max(start, 0)
	end = min(end, len(editor.text))
	if start >= end {
		return
	}
	editor.text = append(editor.text[:start], editor.text[end:]...)
	editor.cursor = start
}

func (editor *inputEditor) lineStart(position int) int {
	for position > 0 && editor.text[position-1] != '\n' {
		position--
	}
	return position
}

func (editor *inputEditor) lineEnd(position int) int {
	for position < len(editor.text) && editor.text[position] != '\n' {
		position++
	}
	return position
}

func (editor *inputEditor) wordStart(position int) int {
	for position > 0 && unicode.IsSpace(editor.text[position-1]) {
		position--
	}
	for position > 0 && !unicode.IsSpace(editor.text[position-1]) {
		position--
	}
	return position
}

func (editor *inputEditor) wordEnd(position int) int {
	for position < len(editor.text) && unicode.IsSpace(editor.text[position]) {
		position++
	}
	for position < len(editor.text) && !unicode.IsSpace(editor.text[position]) {
		position++
	}
	return position
}

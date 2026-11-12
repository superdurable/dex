// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// reflowTerminal is a test terminal that rewraps lines when it narrows, like
// macOS Terminal.
type reflowTerminal struct {
	mutex      sync.Mutex
	width      int
	height     int
	rows       []terminalRow
	history    []terminalRow
	cursorX    int
	cursorY    int
	lastRune   string
	pending    []byte
	unknownSeq []string
}

// terminalRow is one screen row. isWrapped means the row continues on the
// next one.
type terminalRow struct {
	cells     []string
	isWrapped bool
}

func newReflowTerminal(width int, height int) *reflowTerminal {
	terminal := &reflowTerminal{width: width, height: height}
	for range height {
		terminal.rows = append(terminal.rows, terminal.blankRow())
	}
	return terminal
}

func (terminal *reflowTerminal) blankRow() terminalRow {
	cells := make([]string, terminal.width)
	for index := range cells {
		cells[index] = " "
	}
	return terminalRow{cells: cells}
}

func (terminal *reflowTerminal) Write(data []byte) (int, error) {
	terminal.mutex.Lock()
	defer terminal.mutex.Unlock()
	terminal.pending = append(terminal.pending, data...)
	for len(terminal.pending) > 0 {
		consumed := terminal.consume(terminal.pending)
		if consumed == 0 {
			break
		}
		terminal.pending = terminal.pending[consumed:]
	}
	return len(data), nil
}

// consume handles one character or sequence and returns its byte length, or
// 0 when the sequence is still incomplete.
func (terminal *reflowTerminal) consume(data []byte) int {
	switch data[0] {
	case 0x1b:
		return terminal.consumeEscape(data)
	case '\r':
		terminal.cursorX = 0
		return 1
	case '\n':
		// The test output is no TTY, so the renderer expects ONLCR: a
		// newline also returns the carriage.
		terminal.cursorX = 0
		terminal.lineFeed()
		return 1
	case '\b':
		terminal.cursorX = max(terminal.cursorX-1, 0)
		return 1
	case '\t':
		terminal.cursorX = min((terminal.cursorX/8+1)*8, terminal.width-1)
		return 1
	case 0x07:
		return 1
	}
	if !utf8.FullRune(data) {
		return 0
	}
	character, size := utf8.DecodeRune(data)
	terminal.print(string(character))
	return size
}

func (terminal *reflowTerminal) consumeEscape(data []byte) int {
	if len(data) < 2 {
		return 0
	}
	switch data[1] {
	case '[':
		for index := 2; index < len(data); index++ {
			if data[index] >= 0x40 && data[index] <= 0x7e {
				terminal.applyControlSequence(string(data[2:index]), data[index])
				return index + 1
			}
		}
		return 0
	case ']', 'P', '_', '^':
		for index := 2; index < len(data); index++ {
			if data[index] == 0x07 {
				return index + 1
			}
			if data[index] == 0x1b && index+1 < len(data) && data[index+1] == '\\' {
				return index + 2
			}
		}
		return 0
	case 'M':
		terminal.cursorY = max(terminal.cursorY-1, 0)
		return 2
	case 'D':
		terminal.lineFeed()
		return 2
	case 'E':
		terminal.cursorX = 0
		terminal.lineFeed()
		return 2
	default:
		return 2
	}
}

func (terminal *reflowTerminal) applyControlSequence(parameters string, final byte) {
	isPrivate := strings.ContainsAny(parameters, "?<>=")
	values := parseParameters(strings.TrimLeft(parameters, "?<>="))
	count := max(firstValue(values, 1), 1)
	switch {
	case isPrivate || final == 'm' || final == 'h' || final == 'l' || final == 'u' || final == 'n' || final == 'q' || final == 't' || final == 'c':
	case final == 'A':
		terminal.cursorY = max(terminal.cursorY-count, 0)
	case final == 'B':
		terminal.cursorY = min(terminal.cursorY+count, terminal.height-1)
	case final == 'C':
		terminal.cursorX = min(terminal.cursorX+count, terminal.width-1)
	case final == 'D':
		terminal.cursorX = max(min(terminal.cursorX, terminal.width-1)-count, 0)
	case final == 'E':
		terminal.cursorX, terminal.cursorY = 0, min(terminal.cursorY+count, terminal.height-1)
	case final == 'F':
		terminal.cursorX, terminal.cursorY = 0, max(terminal.cursorY-count, 0)
	case final == 'G':
		terminal.cursorX = min(count-1, terminal.width-1)
	case final == 'H' || final == 'f':
		terminal.cursorY = min(max(firstValue(values, 1), 1)-1, terminal.height-1)
		column := 1
		if len(values) > 1 && values[1] > 0 {
			column = values[1]
		}
		terminal.cursorX = min(column-1, terminal.width-1)
	case final == 'J':
		terminal.eraseDisplay(firstValue(values, 0))
	case final == 'K':
		terminal.eraseLine(firstValue(values, 0))
	case final == 'X':
		terminal.fill(terminal.cursorY, terminal.cursorX, terminal.cursorX+count)
	case final == 'b':
		for range count {
			terminal.print(terminal.lastRune)
		}
	case final == '@':
		row := &terminal.rows[terminal.cursorY]
		x := min(terminal.cursorX, terminal.width-1)
		inserted := append(make([]string, 0, terminal.width), row.cells[:x]...)
		for range count {
			inserted = append(inserted, " ")
		}
		row.cells = append(inserted, row.cells[x:]...)[:terminal.width]
	case final == 'P':
		row := &terminal.rows[terminal.cursorY]
		x := min(terminal.cursorX, terminal.width-1)
		end := min(x+count, terminal.width)
		row.cells = append(append([]string{}, row.cells[:x]...), row.cells[end:]...)
		for len(row.cells) < terminal.width {
			row.cells = append(row.cells, " ")
		}
	case final == 'L':
		for range count {
			terminal.rows = append(terminal.rows[:terminal.cursorY], append([]terminalRow{terminal.blankRow()}, terminal.rows[terminal.cursorY:]...)...)
			terminal.rows = terminal.rows[:terminal.height]
		}
	case final == 'M':
		for range count {
			terminal.rows = append(terminal.rows[:terminal.cursorY], terminal.rows[terminal.cursorY+1:]...)
			terminal.rows = append(terminal.rows, terminal.blankRow())
		}
	case final == 'S':
		for range count {
			terminal.scrollUp()
		}
	default:
		terminal.unknownSeq = append(terminal.unknownSeq, fmt.Sprintf("CSI %s %c", parameters, final))
	}
}

func (terminal *reflowTerminal) print(character string) {
	if character == "" {
		return
	}
	terminal.lastRune = character
	characterWidth := max(ansi.StringWidth(character), 1)
	if terminal.cursorX+characterWidth > terminal.width {
		terminal.rows[terminal.cursorY].isWrapped = true
		terminal.cursorX = 0
		terminal.lineFeed()
	}
	row := &terminal.rows[terminal.cursorY]
	row.cells[terminal.cursorX] = character
	for offset := 1; offset < characterWidth; offset++ {
		row.cells[terminal.cursorX+offset] = ""
	}
	terminal.cursorX += characterWidth
}

func (terminal *reflowTerminal) lineFeed() {
	if terminal.cursorY == terminal.height-1 {
		terminal.scrollUp()
		return
	}
	terminal.cursorY++
}

func (terminal *reflowTerminal) scrollUp() {
	terminal.history = append(terminal.history, terminal.rows[0])
	terminal.rows = append(terminal.rows[1:], terminal.blankRow())
}

func (terminal *reflowTerminal) eraseDisplay(mode int) {
	switch mode {
	case 0:
		terminal.eraseLine(0)
		for row := terminal.cursorY + 1; row < terminal.height; row++ {
			terminal.rows[row] = terminal.blankRow()
		}
	case 1:
		terminal.eraseLine(1)
		for row := 0; row < terminal.cursorY; row++ {
			terminal.rows[row] = terminal.blankRow()
		}
	default:
		for row := range terminal.rows {
			terminal.rows[row] = terminal.blankRow()
		}
	}
}

func (terminal *reflowTerminal) eraseLine(mode int) {
	x := min(terminal.cursorX, terminal.width)
	switch mode {
	case 0:
		terminal.fill(terminal.cursorY, x, terminal.width)
		terminal.rows[terminal.cursorY].isWrapped = false
	case 1:
		terminal.fill(terminal.cursorY, 0, x+1)
	default:
		terminal.rows[terminal.cursorY] = terminal.blankRow()
	}
}

func (terminal *reflowTerminal) fill(row int, from int, to int) {
	for column := max(from, 0); column < min(to, terminal.width); column++ {
		terminal.rows[row].cells[column] = " "
	}
}

// Resize rewraps the lines to width, keeping the cursor in its line.
func (terminal *reflowTerminal) Resize(width int) {
	terminal.mutex.Lock()
	defer terminal.mutex.Unlock()
	type logicalLine struct {
		cells    []string
		isCursor bool
		cursorAt int
	}
	var lines []logicalLine
	current := logicalLine{}
	for rowIndex, row := range terminal.rows {
		if rowIndex == terminal.cursorY {
			current.isCursor, current.cursorAt = true, len(current.cells)+terminal.cursorX
		}
		current.cells = append(current.cells, row.cells...)
		if !row.isWrapped {
			lines = append(lines, current)
			current = logicalLine{}
		}
	}
	terminal.width = width
	var rows []terminalRow
	cursorRow, cursorColumn := 0, 0
	for _, line := range lines {
		cells := line.cells
		for len(cells) > 0 && cells[len(cells)-1] == " " && (!line.isCursor || len(cells) > line.cursorAt) {
			cells = cells[:len(cells)-1]
		}
		start := len(rows)
		for offset := 0; offset == 0 || offset < len(cells); offset += width {
			row := terminal.blankRow()
			copy(row.cells, cells[offset:min(offset+width, len(cells))])
			row.isWrapped = offset+width < len(cells)
			rows = append(rows, row)
		}
		if line.isCursor {
			cursorRow, cursorColumn = start+line.cursorAt/width, line.cursorAt%width
		}
	}
	for len(rows) > terminal.height && len(rows)-1 > cursorRow && isBlankRow(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	for len(rows) > terminal.height && cursorRow > 0 {
		terminal.history = append(terminal.history, rows[0])
		rows = rows[1:]
		cursorRow--
	}
	for len(rows) < terminal.height {
		rows = append(rows, terminal.blankRow())
	}
	terminal.rows = rows[:terminal.height]
	terminal.cursorX, terminal.cursorY = cursorColumn, min(cursorRow, terminal.height-1)
}

// ScreenLines returns the history and then the screen as text, one row per
// line, without trailing spaces.
func (terminal *reflowTerminal) ScreenLines() []string {
	terminal.mutex.Lock()
	defer terminal.mutex.Unlock()
	var lines []string
	for _, row := range append(append([]terminalRow{}, terminal.history...), terminal.rows...) {
		lines = append(lines, strings.TrimRight(strings.Join(row.cells, ""), " "))
	}
	return lines
}

func (terminal *reflowTerminal) UnknownSequences() []string {
	terminal.mutex.Lock()
	defer terminal.mutex.Unlock()
	return append([]string(nil), terminal.unknownSeq...)
}

func parseParameters(text string) []int {
	if text == "" {
		return nil
	}
	var values []int
	for _, part := range strings.Split(text, ";") {
		value, err := strconv.Atoi(strings.SplitN(part, ":", 2)[0])
		if err != nil {
			value = 0
		}
		values = append(values, value)
	}
	return values
}

func firstValue(values []int, fallback int) int {
	if len(values) == 0 || values[0] == 0 {
		return fallback
	}
	return values[0]
}

func isBlankRow(row terminalRow) bool {
	return strings.TrimSpace(strings.Join(row.cells, "")) == ""
}

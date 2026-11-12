// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// terminalFrame replays a PTY log on a width x height screen and returns
// its lines. It knows the sequences the Bubble Tea renderer writes; others
// are skipped.
func terminalFrame(log string, width int, height int) []string {
	screen := &frameScreen{width: width, height: height, bottom: height - 1}
	screen.clear()
	for index := 0; index < len(log); {
		character, size := utf8.DecodeRuneInString(log[index:])
		switch {
		case character == '\x1b' && index+1 < len(log) && log[index+1] == '[':
			index = screen.controlSequence(log, index+2)
			continue
		case character == '\x1b' && index+1 < len(log) && log[index+1] == ']':
			index = skipOperatingSystemCommand(log, index+2)
			continue
		case character == '\x1b' && index+1 < len(log):
			if log[index+1] == 'M' {
				screen.reverseIndex()
			}
			index += 2
			continue
		case character == '\r':
			screen.column = 0
		case character == '\n':
			screen.lineFeed()
		case character == '\b':
			screen.column = max(screen.column-1, 0)
		case character >= ' ':
			screen.print(character)
		}
		index += size
	}
	lines := make([]string, height)
	for row := range screen.cells {
		lines[row] = strings.TrimRight(string(screen.cells[row]), " ")
	}
	return lines
}

type frameScreen struct {
	width, height  int
	cells          [][]rune
	row, column    int
	top, bottom    int
	savedMainCells [][]rune
}

func (screen *frameScreen) clear() {
	screen.cells = make([][]rune, screen.height)
	for row := range screen.cells {
		screen.cells[row] = []rune(strings.Repeat(" ", screen.width))
	}
}

func (screen *frameScreen) print(character rune) {
	if screen.column >= screen.width {
		screen.column = 0
		screen.lineFeed()
	}
	screen.cells[screen.row][screen.column] = character
	screen.column++
}

func (screen *frameScreen) lineFeed() {
	if screen.row == screen.bottom {
		screen.scrollUp(1)
		return
	}
	screen.row = min(screen.row+1, screen.height-1)
}

func (screen *frameScreen) reverseIndex() {
	if screen.row == screen.top {
		screen.scrollDown(1)
		return
	}
	screen.row = max(screen.row-1, 0)
}

func (screen *frameScreen) scrollUp(count int) {
	for range count {
		copy(screen.cells[screen.top:screen.bottom], screen.cells[screen.top+1:screen.bottom+1])
		screen.cells[screen.bottom] = []rune(strings.Repeat(" ", screen.width))
	}
}

func (screen *frameScreen) scrollDown(count int) {
	for range count {
		copy(screen.cells[screen.top+1:screen.bottom+1], screen.cells[screen.top:screen.bottom])
		screen.cells[screen.top] = []rune(strings.Repeat(" ", screen.width))
	}
}

// controlSequence applies one CSI sequence and returns the index after it.
func (screen *frameScreen) controlSequence(log string, start int) int {
	end := start
	for end < len(log) && (log[end] < '@' || log[end] > '~') {
		end++
	}
	if end >= len(log) {
		return len(log)
	}
	parameters, final := log[start:end], log[end]
	if strings.HasPrefix(parameters, "?") {
		if parameters == "?1049" && final == 'h' {
			screen.savedMainCells = screen.cells
			screen.clear()
		}
		if parameters == "?1049" && final == 'l' && screen.savedMainCells != nil {
			screen.cells, screen.savedMainCells = screen.savedMainCells, nil
		}
		return end + 1
	}
	if strings.ContainsAny(parameters, "<>=$ ") {
		return end + 1
	}
	numbers := strings.Split(parameters, ";")
	number := func(position int, fallback int) int {
		if position >= len(numbers) || numbers[position] == "" {
			return fallback
		}
		value, err := strconv.Atoi(numbers[position])
		if err != nil {
			return fallback
		}
		return value
	}
	switch final {
	case 'A':
		screen.row = max(screen.row-number(0, 1), 0)
	case 'B':
		screen.row = min(screen.row+number(0, 1), screen.height-1)
	case 'C':
		screen.column = min(screen.column+number(0, 1), screen.width-1)
	case 'D':
		screen.column = max(screen.column-number(0, 1), 0)
	case 'G':
		screen.column = min(max(number(0, 1)-1, 0), screen.width-1)
	case 'd':
		screen.row = min(max(number(0, 1)-1, 0), screen.height-1)
	case 'H':
		screen.row = min(max(number(0, 1)-1, 0), screen.height-1)
		screen.column = min(max(number(1, 1)-1, 0), screen.width-1)
	case 'J':
		screen.eraseDisplay(number(0, 0))
	case 'K':
		screen.eraseLine(number(0, 0))
	case 'X':
		for column := screen.column; column < min(screen.column+number(0, 1), screen.width); column++ {
			screen.cells[screen.row][column] = ' '
		}
	case 'r':
		screen.top = min(max(number(0, 1)-1, 0), screen.height-1)
		screen.bottom = min(max(number(1, screen.height)-1, screen.top), screen.height-1)
		screen.row, screen.column = 0, 0
	case 'S':
		screen.scrollUp(number(0, 1))
	case 'T':
		screen.scrollDown(number(0, 1))
	}
	return end + 1
}

func (screen *frameScreen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		screen.eraseLine(0)
		for row := screen.row + 1; row < screen.height; row++ {
			screen.cells[row] = []rune(strings.Repeat(" ", screen.width))
		}
	case 1:
		screen.eraseLine(1)
		for row := 0; row < screen.row; row++ {
			screen.cells[row] = []rune(strings.Repeat(" ", screen.width))
		}
	default:
		screen.clear()
	}
}

func (screen *frameScreen) eraseLine(mode int) {
	from, to := screen.column, screen.width
	switch mode {
	case 1:
		from, to = 0, min(screen.column+1, screen.width)
	case 2:
		from = 0
	}
	for column := from; column < to; column++ {
		screen.cells[screen.row][column] = ' '
	}
}

func skipOperatingSystemCommand(log string, start int) int {
	for index := start; index < len(log); index++ {
		if log[index] == '\a' {
			return index + 1
		}
		if log[index] == '\x1b' && index+1 < len(log) && log[index+1] == '\\' {
			return index + 2
		}
	}
	return len(log)
}

// The frame replays partial rewrites: a line redrawn from its middle shows
// its new text, not the bytes written last.
func TestTerminalFrameReplaysPartialRewrites(t *testing.T) {
	log := "\x1b[?1049h\x1b[H\x1b[2J 0 sessions match\r\n hints\x1b[1;2H1\x1b[2;2H\x1b[Kkeys\x1b[3;4Habc\x1b[3;5H\x1b[1XZ"
	require.Equal(t, []string{" 1 sessions match", " keys", "   aZc", ""}, terminalFrame(log, 20, 4))
}

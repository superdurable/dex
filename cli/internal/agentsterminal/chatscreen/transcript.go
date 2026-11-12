// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"sort"
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// transcriptBlock is one finished block. It keeps its data, so a resize lays
// it out again.
type transcriptBlock interface {
	Render(width int, theme terminaltheme.Theme) []string
}

// transcriptBlockKind decides where blank lines separate blocks.
type transcriptBlockKind int

const (
	transcriptBlockNone transcriptBlockKind = iota
	transcriptBlockWelcome
	transcriptBlockUserMessage
	transcriptBlockReply
	transcriptBlockToolRow
	transcriptBlockNotice
)

type transcriptEntry struct {
	kind        transcriptBlockKind
	block       transcriptBlock
	isSeparated bool
}

// transcript holds the finished blocks and their lines at one width; appends
// lay out only the new block.
type transcript struct {
	entries []transcriptEntry
	// layoutWidth is the width of lines and entryStarts; 0 means stale.
	layoutWidth int
	lines       []string
	// entryStarts holds each entry's first line, separator included.
	entryStarts []int
	theme       terminaltheme.Theme
}

func newTranscript(theme terminaltheme.Theme) transcript {
	return transcript{theme: theme}
}

// transcriptAnchor is an entry and a line within it; len(entries) means the
// lines after the transcript.
type transcriptAnchor struct {
	entryIndex int
	lineOffset int
}

// Append adds a block. A blank line separates blocks, except consecutive
// tool rows or notices.
func (history *transcript) Append(kind transcriptBlockKind, block transcriptBlock) {
	isSeparated := false
	if count := len(history.entries); count > 0 {
		previous := history.entries[count-1].kind
		isSeparated = previous != kind || (kind != transcriptBlockToolRow && kind != transcriptBlockNotice)
	}
	history.entries = append(history.entries, transcriptEntry{kind: kind, block: block, isSeparated: isSeparated})
	if history.layoutWidth > 0 {
		history.layOutEntry(len(history.entries) - 1)
	}
}

// Replace swaps the block at index; the next Lines call lays everything out
// again.
func (history *transcript) Replace(index int, block transcriptBlock) {
	history.entries[index].block = block
	history.layoutWidth = 0
}

// Lines returns every line at width. The slice is shared: callers must not
// change it.
func (history *transcript) Lines(width int) []string {
	history.ensureLayout(width)
	return history.lines
}

// Anchor returns the entry and offset of line.
func (history *transcript) Anchor(line int, width int) transcriptAnchor {
	history.ensureLayout(width)
	if line >= len(history.lines) {
		return transcriptAnchor{entryIndex: len(history.entries), lineOffset: line - len(history.lines)}
	}
	entryIndex := sort.Search(len(history.entryStarts), func(index int) bool { return history.entryStarts[index] > line }) - 1
	entryIndex = max(entryIndex, 0)
	return transcriptAnchor{entryIndex: entryIndex, lineOffset: line - history.entryStarts[entryIndex]}
}

// LineOf maps an anchor back to a line at width, clamping the offset to the
// entry's new length.
func (history *transcript) LineOf(anchor transcriptAnchor, width int) int {
	history.ensureLayout(width)
	if anchor.entryIndex >= len(history.entries) {
		return len(history.lines) + anchor.lineOffset
	}
	start := history.entryStarts[anchor.entryIndex]
	end := len(history.lines)
	if anchor.entryIndex+1 < len(history.entryStarts) {
		end = history.entryStarts[anchor.entryIndex+1]
	}
	return start + min(anchor.lineOffset, max(end-start-1, 0))
}

func (history *transcript) ensureLayout(width int) {
	if history.layoutWidth == width {
		return
	}
	history.layoutWidth = width
	history.lines, history.entryStarts = nil, nil
	for index := range history.entries {
		history.layOutEntry(index)
	}
}

func (history *transcript) layOutEntry(index int) {
	entry := history.entries[index]
	history.entryStarts = append(history.entryStarts, len(history.lines))
	if entry.isSeparated {
		history.lines = append(history.lines, "")
	}
	history.lines = append(history.lines, entry.block.Render(history.layoutWidth, history.theme)...)
}

type userMessageBlock struct {
	text   string
	origin string
	images []sessionfeed.ImageAttachment
}

func (block userMessageBlock) Render(width int, theme terminaltheme.Theme) []string {
	return renderUserMessage(block.text, block.origin, block.images, width, theme)
}

type replyBlock struct {
	markdown string
}

func (block replyBlock) Render(width int, theme terminaltheme.Theme) []string {
	return renderAssistantMarkdown(block.markdown, width, theme)
}

type toolRowBlock struct {
	call      sessionfeed.ToolCallChanged
	nameWidth int
	printedAt time.Time
}

func (block toolRowBlock) Render(width int, theme terminaltheme.Theme) []string {
	return renderToolRow(block.call, block.nameWidth, width, toolRowClock{now: block.printedAt}, theme)
}

type noticeBlock struct {
	severity sessionfeed.NoticeSeverity
	text     string
}

func (block noticeBlock) Render(width int, theme terminaltheme.Theme) []string {
	return renderNoticeLine(block.severity, block.text, width, theme)
}

type turnCancelledBlock struct{}

func (turnCancelledBlock) Render(_ int, theme terminaltheme.Theme) []string {
	return []string{contentIndent + theme.Secondary.Render(theme.Symbols.NotRun+" turn cancelled; the reply above is incomplete")}
}

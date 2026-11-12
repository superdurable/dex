// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
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
	kind          transcriptBlockKind
	block         transcriptBlock
	isSeparated   bool
	renderedWidth int
	renderedLines []string
}

// transcript is the ordered list of finished blocks.
type transcript struct {
	entries []transcriptEntry
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
}

// Replace swaps the block at index, for example the welcome block after the
// session changes.
func (history *transcript) Replace(index int, block transcriptBlock) {
	entry := &history.entries[index]
	entry.block, entry.renderedWidth, entry.renderedLines = block, 0, nil
}

// Lines lays out every block at width. Each block's lines are kept until
// the width changes.
func (history *transcript) Lines(width int, theme terminaltheme.Theme) []string {
	var lines []string
	for index := range history.entries {
		entry := &history.entries[index]
		if entry.renderedWidth != width || entry.renderedLines == nil {
			entry.renderedLines, entry.renderedWidth = entry.block.Render(width, theme), width
		}
		if entry.isSeparated {
			lines = append(lines, "")
		}
		lines = append(lines, entry.renderedLines...)
	}
	return lines
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

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
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// pagerChromeHeight is the title row and the status row around the body.
const pagerChromeHeight = 2

// pagerView reads one tool's full output or the earlier history, over the
// whole screen.
type pagerView struct {
	title string
	// tool is the call a tool pager shows; history is nil then.
	tool    sessionfeed.ToolCallChanged
	history *historyPages
	top     int
	// matchRow is the body row of the current search match, or -1.
	matchRow       int
	isTypingSearch bool
	searchInput    inputEditor
	searchQuery    string
	statusText     string
}

// historyPages are the earlier pages /history read, oldest page first.
type historyPages struct {
	sessionID             string
	pages                 [][]sessionfeed.SessionUpdate
	requestedBefore       int64
	earlierBeforeSequence int64
	deletedBeforeSequence int64
	isLoading             bool
	failureText           string
}

func newToolPager(call sessionfeed.ToolCallChanged) *pagerView {
	return &pagerView{title: "Tool output", tool: call, matchRow: -1}
}

func newHistoryPager(sessionID string, earlierBeforeSequence int64, deletedBeforeSequence int64) *pagerView {
	return &pagerView{
		title:    "Earlier messages",
		matchRow: -1,
		history: &historyPages{
			sessionID:             sessionID,
			earlierBeforeSequence: earlierBeforeSequence,
			deletedBeforeSequence: deletedBeforeSequence,
		},
	}
}

// canReadEarlier reports whether a history pager may request the next page.
func (pager *pagerView) canReadEarlier() bool {
	return pager.history != nil && !pager.history.isLoading && pager.history.earlierBeforeSequence > 0
}

// startReadingEarlier marks the request in flight and returns it.
func (pager *pagerView) startReadingEarlier() sessionfeed.ReadEarlierMessages {
	history := pager.history
	history.isLoading = true
	history.failureText = ""
	history.requestedBefore = history.earlierBeforeSequence
	return sessionfeed.ReadEarlierMessages{SessionID: history.sessionID, BeforeSequence: history.requestedBefore}
}

// applyEarlierPage adds an answered page above the rows on screen and keeps
// the rows the user reads in place. It reports false for a stale answer.
func (pager *pagerView) applyEarlierPage(answer sessionfeed.EarlierMessagesRead, size pagerSize, theme terminaltheme.Theme) bool {
	history := pager.history
	if history == nil || !history.isLoading || answer.SessionID != history.sessionID || answer.BeforeSequence != history.requestedBefore {
		return false
	}
	rowsBefore := len(pager.bodyRows(size.width, theme))
	history.isLoading = false
	if answer.FailureText != "" {
		history.failureText = answer.FailureText
		return true
	}
	history.pages = append([][]sessionfeed.SessionUpdate{answer.Messages}, history.pages...)
	history.earlierBeforeSequence = answer.EarlierBeforeSequence
	if answer.DeletedBeforeSequence > 0 {
		history.deletedBeforeSequence = answer.DeletedBeforeSequence
	}
	rowCount := len(pager.bodyRows(size.width, theme))
	pager.top = min(pager.top+max(rowCount-rowsBefore, 0), max(rowCount-size.bodyHeight, 0))
	pager.matchRow = -1
	return true
}

// bodyRows renders the pager body at width.
func (pager *pagerView) bodyRows(width int, theme terminaltheme.Theme) []string {
	if pager.history != nil {
		return renderHistoryPages(*pager.history, width, theme)
	}
	return renderToolOutputPage(pager.tool, width, theme)
}

// pagerKeyResult is what a key did in the pager.
type pagerKeyResult struct {
	isClosed        bool
	isEarlierWanted bool
}

// pagerSize is the pager's width and body height at the current screen size.
type pagerSize struct {
	width      int
	bodyHeight int
}

// HandleKey applies one key at size.
func (pager *pagerView) HandleKey(key tea.KeyPressMsg, size pagerSize, theme terminaltheme.Theme) pagerKeyResult {
	if pager.isTypingSearch {
		pager.handleSearchKey(key, size, theme)
		return pagerKeyResult{}
	}
	rowCount := len(pager.bodyRows(size.width, theme))
	page := max(size.bodyHeight-1, 1)
	isAtTop := pager.top == 0
	isScrollingUp := false
	pager.statusText = ""
	switch key.String() {
	case "q", "esc":
		return pagerKeyResult{isClosed: true}
	case "down", "j", "enter":
		pager.top++
	case "up", "k":
		pager.top--
		isScrollingUp = true
	case "pgdown", "space", "f":
		pager.top += page
	case "pgup", "b":
		pager.top -= page
		isScrollingUp = true
	case "home", "g":
		pager.top = 0
		isScrollingUp = true
	case "end", "G":
		pager.top = rowCount
	case "/":
		pager.isTypingSearch = true
		pager.searchInput.Clear()
		return pagerKeyResult{}
	case "n":
		pager.findMatch(pager.matchRow+1, true, size, theme)
	case "N":
		pager.findMatch(pager.matchRow-1, false, size, theme)
	}
	pager.top = min(max(pager.top, 0), max(rowCount-size.bodyHeight, 0))
	return pagerKeyResult{isEarlierWanted: isAtTop && isScrollingUp && pager.canReadEarlier()}
}

// Scroll moves the body by delta rows, as the mouse wheel does.
func (pager *pagerView) Scroll(delta int, size pagerSize, theme terminaltheme.Theme) {
	rowCount := len(pager.bodyRows(size.width, theme))
	pager.top = min(max(pager.top+delta, 0), max(rowCount-size.bodyHeight, 0))
}

func (pager *pagerView) handleSearchKey(key tea.KeyPressMsg, size pagerSize, theme terminaltheme.Theme) {
	switch key.String() {
	case "esc":
		pager.isTypingSearch = false
	case "enter":
		pager.isTypingSearch = false
		pager.searchQuery = strings.TrimSpace(pager.searchInput.Value())
		pager.findMatch(pager.top, true, size, theme)
	default:
		pager.searchInput.HandleEditingKey(key)
	}
}

// findMatch selects the first row at or after from that holds the query,
// or at or before from when isForward is false, and scrolls to it.
func (pager *pagerView) findMatch(from int, isForward bool, size pagerSize, theme terminaltheme.Theme) {
	pager.matchRow = -1
	if pager.searchQuery == "" {
		return
	}
	query := strings.ToLower(pager.searchQuery)
	step := 1
	if !isForward {
		step = -1
	}
	rows := pager.bodyRows(size.width, theme)
	for row := from; row >= 0 && row < len(rows); row += step {
		if strings.Contains(strings.ToLower(terminaltheme.StripStyles(rows[row])), query) {
			pager.matchRow = row
			if row < pager.top || row >= pager.top+size.bodyHeight {
				pager.top = max(row-size.bodyHeight/3, 0)
			}
			return
		}
	}
	pager.statusText = fmt.Sprintf("no more matches for %q", pager.searchQuery)
}

// Render draws the title row, the body at size, and the status row.
func (pager *pagerView) Render(size pagerSize, waitingText string, theme terminaltheme.Theme) []string {
	symbols := theme.Symbols
	rows := pager.bodyRows(size.width, theme)
	top := min(max(pager.top, 0), max(len(rows)-size.bodyHeight, 0))
	end := min(top+size.bodyHeight, len(rows))
	position := theme.Secondary.Render(fmt.Sprintf("%d-%d of %d", min(top+1, end), end, len(rows)))
	lines := []string{joinLeftAndRight(" "+theme.Title.Render(pager.title), position, size.width, symbols.Ellipsis)}
	for row := top; row < top+size.bodyHeight; row++ {
		switch {
		case row >= len(rows):
			lines = append(lines, "")
		case row == pager.matchRow:
			lines = append(lines, highlightMatch(terminaltheme.StripStyles(rows[row]), pager.searchQuery))
		default:
			lines = append(lines, rows[row])
		}
	}
	return append(lines, pager.renderStatusRow(size.width, waitingText, theme))
}

func (pager *pagerView) renderStatusRow(width int, waitingText string, theme terminaltheme.Theme) string {
	symbols := theme.Symbols
	if pager.isTypingSearch {
		prompt := " " + theme.Accent.Render("/") + " "
		rendered := pager.searchInput.Render(prompt, "", width, 1)
		return insertVirtualCursor(rendered.lines[0], rendered.cursorColumn)
	}
	hints := []string{"j/k scroll", "/ search", "q back"}
	if pager.searchQuery != "" {
		hints = []string{"n next", "N previous", "/ search", "q back"}
	}
	if pager.canReadEarlier() {
		hints = append([]string{"k at the top reads earlier"}, hints...)
	}
	left := ""
	switch {
	case pager.statusText != "":
		left = " " + theme.Secondary.Render(pager.statusText)
	case waitingText != "":
		left = " " + theme.Warning.Render(symbols.AwaitingUser) + " " + waitingText
	}
	room := width - terminaltheme.DisplayWidth(left) - 2
	var fitting string
	for count := len(hints); count > 0; count-- {
		text := strings.Join(hints[:count], symbols.Separator)
		if terminaltheme.DisplayWidth(text) <= room {
			fitting = theme.Secondary.Render(text)
			break
		}
	}
	if left == "" {
		return terminaltheme.TruncateToWidth(" "+fitting, width, symbols.Ellipsis)
	}
	return joinLeftAndRight(left, fitting, width, symbols.Ellipsis)
}

// highlightMatch reverses the first case-insensitive match of query in a
// plain row.
func highlightMatch(plainRow string, query string) string {
	index := strings.Index(strings.ToLower(plainRow), strings.ToLower(query))
	if query == "" || index < 0 {
		return plainRow
	}
	startColumn := terminaltheme.DisplayWidth(plainRow[:index])
	endColumn := startColumn + terminaltheme.DisplayWidth(plainRow[index:index+len(query)])
	rowWidth := terminaltheme.DisplayWidth(plainRow)
	matchStyle := lipgloss.NewStyle().Reverse(true)
	return ansi.Cut(plainRow, 0, startColumn) + matchStyle.Render(ansi.Cut(plainRow, startColumn, endColumn)) + ansi.Cut(plainRow, endColumn, rowWidth)
}

// renderToolOutputPage draws one call: its row, its arguments, and its
// whole output.
func renderToolOutputPage(call sessionfeed.ToolCallChanged, width int, theme terminaltheme.Theme) []string {
	header := call
	header.OutputTail, header.HiddenOutputLineCount = nil, 0
	rows := renderToolRow(header, 0, width, toolRowClock{}, theme)
	if len(call.Arguments) > 0 {
		rows = append(rows, "", contentIndent+theme.Title.Render("Arguments"))
		for _, line := range renderApprovalArguments(call.Arguments, true, max(width-len(contentIndent), 10), theme) {
			rows = append(rows, contentIndent+line)
		}
	}
	heading := "Output"
	if call.Status == sessionfeed.ToolCallFailed || call.Status == sessionfeed.ToolCallOutcomeUnknown {
		heading = "Error"
	}
	rows = append(rows, "", contentIndent+theme.Title.Render(heading))
	output := strings.TrimRight(terminaltheme.ExpandTabs(call.OutputText), "\n")
	if output == "" {
		rows = append(rows, contentIndent+theme.Secondary.Render("The session stored no output for this call."))
	} else {
		for _, line := range strings.Split(output, "\n") {
			for _, wrapped := range terminaltheme.WrapToWidth(line, max(width-len(contentIndent), 1)) {
				rows = append(rows, strings.TrimRight(contentIndent+wrapped, " "))
			}
		}
	}
	if call.IsOutputTruncated {
		rows = append(rows, "", contentIndent+theme.Secondary.Render("The session stored only the start of this output."))
	}
	if call.ImageCount > 0 {
		rows = append(rows, contentIndent+theme.Accent.Render(theme.Symbols.Image)+" "+theme.Secondary.Render(countedNoun(call.ImageCount, "image")+" returned; images are not shown"))
	}
	return rows
}

// renderHistoryPages draws the earlier pages under a line that says what
// lies above them.
func renderHistoryPages(history historyPages, width int, theme terminaltheme.Theme) []string {
	symbols := theme.Symbols
	var top string
	switch {
	case history.failureText != "":
		top = contentIndent + theme.Error.Render(symbols.Failed) + " " + history.failureText
	case history.isLoading:
		top = contentIndent + theme.Info.Render(symbols.Running) + theme.Secondary.Render(" reading earlier messages…")
	case history.earlierBeforeSequence > 0:
		top = contentIndent + theme.Secondary.Render("↑ earlier messages; k at the top reads them")
	case history.deletedBeforeSequence > 0:
		top = contentIndent + theme.Secondary.Render(fmt.Sprintf("%s messages before #%d were removed by the retention window", symbols.Queued, history.deletedBeforeSequence))
	default:
		top = contentIndent + theme.Secondary.Render(symbols.Queued+" start of the session")
	}
	rows := terminaltheme.WrapToWidth(top, width)
	for _, page := range history.pages {
		for _, message := range page {
			rows = append(rows, "")
			switch typed := message.(type) {
			case sessionfeed.UserMessageRecorded:
				rows = append(rows, renderUserMessage(typed.Text, "", typed.Images, width, theme)...)
			case sessionfeed.AssistantMessageRecorded:
				rows = append(rows, renderAssistantMarkdown(typed.Text, width, theme)...)
			case sessionfeed.ToolCallChanged:
				typed.OutputTail = nil
				rows = append(rows, renderToolRow(typed, 0, width, toolRowClock{}, theme)...)
			}
		}
	}
	if len(history.pages) > 0 {
		rows = append(rows, "", contentIndent+theme.Secondary.Render(symbols.Queued+" the chat continues below; q goes back"))
	}
	return rows
}

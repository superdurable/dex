// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionpicker

import (
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The fixed column widths; ID takes the rest.
const (
	statusColumnWidth  = 12
	startedColumnWidth = 11
	updatedColumnWidth = 8
	turnsColumnWidth   = 5
	columnGap          = 2
	markerWidth        = 3
	maxIDColumnWidth   = 40
	minIDColumnWidth   = 12
)

// column is one optional column of the session rows.
type column int

const (
	columnStarted column = iota
	columnUpdated
	columnTurns
)

// rowLayout is the set of columns that fit, and the ID width.
type rowLayout struct {
	idWidth      int
	columns      []column
	showsPreview bool
}

// View draws the picker in its size: header, rows, status, and key hints.
func (picker *SessionPicker) View() string {
	theme := picker.config.Theme
	shown := picker.shownSessions()
	layout := picker.rowLayout(shown)
	var header []string
	header = append(header, picker.titleLine())
	if picker.isTyping || picker.committedQuery != "" {
		header = append(header, picker.searchLine())
	}
	footer := []string{picker.statusLine(len(shown)), " " + theme.Secondary.Render(picker.keyHints())}
	bodyHeight := max(picker.height-len(header)-len(footer), 1)
	var body []string
	switch {
	case !picker.hasLoaded && picker.loadFailure != "":
		body = append(body, "   "+theme.Error.Render(theme.Symbols.Failed)+" "+
			terminaltheme.TruncateToWidth("Could not list sessions: "+picker.loadFailure+" Press r to retry.", picker.width-5, theme.Symbols.Ellipsis))
	case !picker.hasLoaded:
		body = append(body, "   "+theme.Secondary.Render("loading sessions…"))
	case len(shown) == 0 && picker.query == "" && picker.committedQuery == "":
		body = append(body, "   "+picker.config.EmptyHint)
	default:
		body = picker.rowLines(shown, layout, bodyHeight)
	}
	lines := append(header, body...)
	for len(lines) < picker.height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines[:min(len(lines), max(picker.height-len(footer), 0))], footer...)
	for index, line := range lines {
		lines[index] = terminaltheme.TruncateToWidth(line, picker.width, "")
	}
	return strings.Join(lines, "\n")
}

func (picker *SessionPicker) titleLine() string {
	theme := picker.config.Theme
	title := " " + theme.Title.Render("Sessions")
	if picker.config.Heading != "" {
		title += theme.Secondary.Render(theme.Symbols.Separator) + picker.config.Heading
	}
	count := fmt.Sprint(len(picker.sessions))
	if picker.nextPageToken != "" {
		count += "+"
	}
	room := picker.width - terminaltheme.DisplayWidth(count) - 2
	title = terminaltheme.TruncateToWidth(title, room, theme.Symbols.Ellipsis)
	return terminaltheme.PadRight(title, room+1) + theme.Accent.Render(count)
}

func (picker *SessionPicker) searchLine() string {
	theme := picker.config.Theme
	query := picker.committedQuery
	if picker.isTyping {
		query = picker.query + "_"
	}
	return " " + theme.Accent.Render("/") + " " + terminaltheme.TruncateToWidth(query, picker.width-4, theme.Symbols.Ellipsis)
}

func (picker *SessionPicker) statusLine(shownCount int) string {
	theme := picker.config.Theme
	switch {
	case picker.statusText != "":
		symbol := theme.Secondary.Render(theme.Symbols.Queued)
		switch picker.statusSeverity {
		case statusWarning:
			symbol = theme.Warning.Render(theme.Symbols.Warning)
		case statusError:
			symbol = theme.Error.Render(theme.Symbols.Failed)
		}
		return " " + symbol + " " + terminaltheme.TruncateToWidth(picker.statusText, picker.width-4, theme.Symbols.Ellipsis)
	case picker.hasLoaded && picker.loadFailure != "":
		return " " + theme.Error.Render(theme.Symbols.Failed) + " " +
			terminaltheme.TruncateToWidth("Could not load more sessions: "+picker.loadFailure, picker.width-4, theme.Symbols.Ellipsis)
	case picker.isTitleSearch && !picker.isTyping && !picker.hasLoaded && picker.loadFailure == "":
		return " " + theme.Secondary.Render("searching titles in Dex…")
	case picker.isTitleSearch && !picker.isTyping:
		return " " + theme.Secondary.Render(fmt.Sprintf("%d sessions whose titles match", shownCount))
	case picker.isTyping || picker.committedQuery != "":
		text := fmt.Sprintf("searching %d loaded sessions", len(picker.allSessions()))
		if picker.isTyping && !picker.isTitleSearchOff {
			text += "; Enter searches titles in Dex"
		}
		return " " + theme.Secondary.Render(text)
	}
	return ""
}

func (picker *SessionPicker) keyHints() string {
	separator := picker.config.Theme.Symbols.Separator
	if picker.isTyping {
		return strings.Join([]string{"Enter search", "Esc clear"}, separator)
	}
	hints := []string{"Enter resume", "/ search", "j/k move", "r reload"}
	if picker.committedQuery != "" {
		hints = append(hints, "Esc clear search")
	} else {
		hints = append(hints, "Esc "+picker.config.CloseKeyName)
	}
	return terminaltheme.TruncateToWidth(strings.Join(hints, separator), picker.width-2, picker.config.Theme.Symbols.Ellipsis)
}

// rowLayout drops TURNS, UPDATED, then STARTED until a row fits.
// Without summaries, UPDATED and TURNS hide.
func (picker *SessionPicker) rowLayout(shown []sessionlisting.SessionSummary) rowLayout {
	hasSummary := false
	idWidth := minIDColumnWidth
	for _, session := range picker.allSessions() {
		hasSummary = hasSummary || session.HasSummary()
	}
	for _, session := range shown {
		idWidth = max(idWidth, terminaltheme.DisplayWidth(session.SessionID))
	}
	idWidth = min(idWidth, maxIDColumnWidth)
	columns := []column{columnStarted}
	if hasSummary {
		columns = append(columns, columnUpdated, columnTurns)
	}
	fixedWidth := func() int {
		width := markerWidth + statusColumnWidth + columnGap
		for _, current := range columns {
			width += columnGap + columnWidth(current)
		}
		return width
	}
	for len(columns) > 0 && fixedWidth()+idWidth > picker.width {
		columns = columns[:len(columns)-1]
	}
	idWidth = max(min(idWidth, picker.width-fixedWidth()), minIDColumnWidth)
	return rowLayout{idWidth: idWidth, columns: columns, showsPreview: hasSummary}
}

func columnWidth(current column) int {
	switch current {
	case columnStarted:
		return startedColumnWidth
	case columnUpdated:
		return updatedColumnWidth
	}
	return turnsColumnWidth
}

func columnTitle(current column) string {
	switch current {
	case columnStarted:
		return "STARTED"
	case columnUpdated:
		return "UPDATED"
	}
	return "TURNS"
}

// rowLines draws the column titles and the rows that fit around the cursor.
func (picker *SessionPicker) rowLines(shown []sessionlisting.SessionSummary, layout rowLayout, height int) []string {
	theme := picker.config.Theme
	titles := strings.Repeat(" ", markerWidth) + terminaltheme.PadRight("ID", layout.idWidth) +
		strings.Repeat(" ", columnGap) + terminaltheme.PadRight("STATUS", statusColumnWidth)
	for _, current := range layout.columns {
		titles += strings.Repeat(" ", columnGap) + padColumn(current, columnTitle(current))
	}
	lines := []string{theme.Secondary.Render(titles)}
	if len(shown) == 0 {
		return append(lines, "   "+theme.Secondary.Render("No loaded session matches."))
	}
	items := make([][]string, 0, len(shown)+1)
	for index, session := range shown {
		items = append(items, picker.sessionItem(session, layout, index == picker.cursor))
	}
	if picker.hasMoreRow() {
		items = append(items, picker.moreItem(len(shown) == picker.cursor))
	}
	room := max(height-1, 1)
	picker.scrollToCursor(items, room)
	used := 0
	for index := picker.topItem; index < len(items) && used+len(items[index]) <= room; index++ {
		lines = append(lines, items[index]...)
		used += len(items[index])
	}
	return lines
}

// scrollToCursor moves topItem so the cursor's item is in the room.
func (picker *SessionPicker) scrollToCursor(items [][]string, room int) {
	picker.topItem = min(picker.topItem, picker.cursor)
	for picker.topItem < picker.cursor {
		used := 0
		for index := picker.topItem; index <= picker.cursor; index++ {
			used += len(items[index])
		}
		if used <= room {
			break
		}
		picker.topItem++
	}
}

func (picker *SessionPicker) sessionItem(session sessionlisting.SessionSummary, layout rowLayout, isFocused bool) []string {
	theme := picker.config.Theme
	marker := "   "
	if isFocused {
		marker = " " + theme.Accent.Render(theme.Symbols.Focus) + " "
	}
	identifier := terminaltheme.PadRight(shortenSessionID(session.SessionID, layout.idWidth, theme.Symbols.Ellipsis), layout.idWidth)
	if isFocused {
		identifier = theme.Focused.Render(identifier)
	}
	row := marker + identifier + strings.Repeat(" ", columnGap) + picker.statusCell(session.Status)
	for _, current := range layout.columns {
		row += strings.Repeat(" ", columnGap) + padColumn(current, picker.cell(current, session))
	}
	lines := []string{row}
	if session.SessionID == picker.config.CurrentSessionID {
		lines[0] += "  " + theme.Secondary.Render("(this chat)")
	}
	if layout.showsPreview && session.Title != nil {
		preview := terminaltheme.TruncateToWidth(`"`+*session.Title+`"`, picker.width-markerWidth-1, theme.Symbols.Ellipsis)
		lines = append(lines, strings.Repeat(" ", markerWidth)+theme.Secondary.Render(preview))
	}
	return lines
}

func (picker *SessionPicker) moreItem(isFocused bool) []string {
	theme := picker.config.Theme
	text := "Load more"
	if picker.isLoading {
		text = "loading more…"
	}
	if isFocused {
		return []string{" " + theme.Accent.Render(theme.Symbols.Focus) + " " + theme.Focused.Render(text)}
	}
	return []string{"   " + theme.Secondary.Render(text)}
}

func (picker *SessionPicker) statusCell(status dex.FlowStatus) string {
	theme := picker.config.Theme
	name := statusName(status)
	text := terminaltheme.PadRight(name, statusColumnWidth-2)
	switch status {
	case dex.FlowRunning:
		return theme.Info.Render(theme.Symbols.Running) + " " + text
	case dex.FlowCompleted:
		return theme.Ok.Render(theme.Symbols.Succeeded) + " " + text
	case dex.FlowFailed:
		return theme.Error.Render(theme.Symbols.Failed) + " " + text
	case dex.FlowCanceled, dex.FlowTerminated:
		return theme.Secondary.Render(theme.Symbols.NotRun) + " " + text
	}
	return theme.Warning.Render(theme.Symbols.Warning) + " " + text
}

func (picker *SessionPicker) cell(current column, session sessionlisting.SessionSummary) string {
	const missing = "—"
	switch current {
	case columnStarted:
		return session.StartedAt.Local().Format("01-02 15:04")
	case columnUpdated:
		if session.LastActivityAt == nil {
			return missing
		}
		return relativeTime(picker.config.Now().Sub(*session.LastActivityAt))
	}
	if session.TurnCount == nil {
		return missing
	}
	return fmt.Sprint(*session.TurnCount)
}

// padColumn aligns numbers and relative times right, other cells left.
func padColumn(current column, text string) string {
	if current == columnStarted {
		return terminaltheme.PadRight(text, columnWidth(current))
	}
	return terminaltheme.PadLeft(text, columnWidth(current))
}

// shortenSessionID keeps the harness prefix and the random suffix of a long
// ID, such as "openai-sdk-…-a1b2c3".
func shortenSessionID(sessionID string, width int, ellipsis string) string {
	if terminaltheme.DisplayWidth(sessionID) <= width {
		return sessionID
	}
	runes := []rune(sessionID)
	suffixLength := min((width-1)/2, len(runes))
	if index := strings.LastIndex(sessionID, "-"); index >= 0 && len(runes)-len([]rune(sessionID[:index])) <= suffixLength {
		suffixLength = len(runes) - len([]rune(sessionID[:index]))
	}
	prefixLength := max(width-1-suffixLength, 0)
	return string(runes[:prefixLength]) + ellipsis + string(runes[len(runes)-suffixLength:])
}

func relativeTime(elapsed time.Duration) string {
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed/time.Minute))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(elapsed/(24*time.Hour)))
}

func statusName(status dex.FlowStatus) string {
	switch status {
	case dex.FlowRunning, dex.FlowCompleted, dex.FlowFailed, dex.FlowTerminated, dex.FlowCanceled:
		return status.String()
	}
	return "unknown"
}

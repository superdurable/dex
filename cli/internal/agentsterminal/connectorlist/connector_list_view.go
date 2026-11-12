// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorlist

import (
	"fmt"
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// The column widths; CONNECTION takes the rest.
const (
	statusColumnWidth     = 16
	enabledColumnWidth    = 7
	toolsColumnWidth      = 5
	columnGap             = 2
	markerWidth           = 3
	minConnectionWidth    = 12
	maxConnectionWidth    = 40
	otherToolsDisplayName = "(no connector)"
)

// View draws the list: header, rows, status, and key hints.
func (list *ConnectorList) View() string {
	theme := list.config.Theme
	header := []string{list.titleLine()}
	footer := []string{list.statusLine(), " " + theme.Secondary.Render(list.keyHints())}
	bodyHeight := max(list.height-len(header)-len(footer), 1)
	var body []string
	if len(list.rows) == 0 {
		hint := "No connector tools."
		if list.canOpenWeb() {
			hint += " Press a to add a connection in Dex Web."
		}
		body = []string{"   " + hint}
	} else {
		body = list.rowLines(bodyHeight)
	}
	lines := append(header, body...)
	for len(lines) < list.height-len(footer) {
		lines = append(lines, "")
	}
	lines = append(lines[:min(len(lines), max(list.height-len(footer), 0))], footer...)
	for index, line := range lines {
		lines[index] = terminaltheme.TruncateToWidth(line, list.width, "")
	}
	return strings.Join(lines, "\n")
}

func (list *ConnectorList) titleLine() string {
	theme := list.config.Theme
	title := " " + theme.Title.Render("Connectors")
	if list.catalog.MCPURL != "" {
		title += theme.Secondary.Render(theme.Symbols.Separator) + "MCP " + list.catalog.MCPURL
	}
	connectionCount := 0
	for _, row := range list.rows {
		if row.hasIdentity() {
			connectionCount++
		}
	}
	count := fmt.Sprintf("%d connections", connectionCount)
	room := list.width - terminaltheme.DisplayWidth(count) - 2
	title = terminaltheme.TruncateToWidth(title, room, theme.Symbols.Ellipsis)
	return terminaltheme.PadRight(title, room+1) + theme.Accent.Render(count)
}

func (list *ConnectorList) statusLine() string {
	theme := list.config.Theme
	if list.statusText == "" {
		return ""
	}
	symbol := theme.Secondary.Render(theme.Symbols.Queued)
	switch list.statusSeverity {
	case statusOk:
		symbol = theme.Ok.Render(theme.Symbols.Succeeded)
	case statusWarning:
		symbol = theme.Warning.Render(theme.Symbols.Warning)
	case statusError:
		symbol = theme.Error.Render(theme.Symbols.Failed)
	}
	return " " + symbol + " " + terminaltheme.TruncateToWidth(list.statusText, list.width-4, theme.Symbols.Ellipsis)
}

func (list *ConnectorList) keyHints() string {
	hints := []string{"Enter show tools"}
	if list.canOpenWeb() {
		hints = append(hints, "a add in Dex Web")
	}
	if list.canRefresh() {
		hints = append(hints, "r refresh")
	}
	hints = append(hints, "j/k move", "Esc exit")
	separator := list.config.Theme.Symbols.Separator
	return terminaltheme.TruncateToWidth(strings.Join(hints, separator), list.width-2, list.config.Theme.Symbols.Ellipsis)
}

func (list *ConnectorList) connectionWidth() int {
	width := minConnectionWidth
	for _, row := range list.rows {
		width = max(width, terminaltheme.DisplayWidth(connectionName(row)))
	}
	room := list.width - markerWidth - 3*columnGap - statusColumnWidth - enabledColumnWidth - toolsColumnWidth
	return max(min(width, maxConnectionWidth, room), minConnectionWidth)
}

// rowLines draws the column titles and the rows that fit around the cursor.
func (list *ConnectorList) rowLines(height int) []string {
	theme := list.config.Theme
	nameWidth := list.connectionWidth()
	gap := strings.Repeat(" ", columnGap)
	titles := strings.Repeat(" ", markerWidth) + terminaltheme.PadRight("CONNECTION", nameWidth) + gap +
		terminaltheme.PadRight("STATUS", statusColumnWidth) + gap + terminaltheme.PadRight("ENABLED", enabledColumnWidth) +
		gap + terminaltheme.PadLeft("TOOLS", toolsColumnWidth)
	lines := []string{theme.Secondary.Render(titles)}
	items := make([][]string, 0, len(list.rows))
	for index, row := range list.rows {
		items = append(items, list.rowItem(row, index == list.cursor, list.expanded[index], nameWidth))
	}
	room := max(height-1, 1)
	list.topLine = min(list.topLine, list.cursor)
	for list.topLine < list.cursor {
		used := 0
		for index := list.topLine; index <= list.cursor; index++ {
			used += len(items[index])
		}
		if used <= room {
			break
		}
		list.topLine++
	}
	used := 0
	for index := list.topLine; index < len(items); index++ {
		for _, line := range items[index] {
			if used >= room {
				return lines
			}
			lines = append(lines, line)
			used++
		}
	}
	return lines
}

func (list *ConnectorList) rowItem(row connectionRow, isFocused bool, isExpanded bool, nameWidth int) []string {
	theme := list.config.Theme
	marker := "   "
	name := terminaltheme.PadRight(terminaltheme.TruncateToWidth(connectionName(row), nameWidth, theme.Symbols.Ellipsis), nameWidth)
	if isFocused {
		marker = " " + theme.Accent.Render(theme.Symbols.Focus) + " "
		name = theme.Focused.Render(name)
	}
	tools := "—"
	if len(row.tools) > 0 {
		tools = fmt.Sprint(len(row.tools))
	}
	gap := strings.Repeat(" ", columnGap)
	lines := []string{marker + name + gap + list.statusCell(row) + gap +
		terminaltheme.PadRight(row.enabledState(), enabledColumnWidth) + gap + terminaltheme.PadLeft(tools, toolsColumnWidth)}
	if !isExpanded {
		return lines
	}
	if len(row.tools) == 0 {
		return append(lines, "     "+theme.Secondary.Render("no tools"))
	}
	for _, tool := range row.tools {
		detail := ""
		if tool.Operation != nil {
			detail = *tool.Operation
		}
		state := "enabled"
		if !tool.IsEnabled {
			state = "not enabled"
		}
		lines = append(lines, "     "+tool.Name+"  "+theme.Secondary.Render(strings.TrimSpace(detail+"  "+state)))
	}
	return lines
}

func (list *ConnectorList) statusCell(row connectionRow) string {
	theme := list.config.Theme
	width := statusColumnWidth - 2
	switch {
	case !row.hasIdentity() || row.status == "":
		return terminaltheme.PadRight("—", statusColumnWidth)
	case row.status == "authorized":
		return theme.Ok.Render(theme.Symbols.Succeeded) + " " + terminaltheme.PadRight("authorized", width)
	}
	return theme.Warning.Render(theme.Symbols.Warning) + " " +
		terminaltheme.PadRight(terminaltheme.TruncateToWidth(row.status, width, theme.Symbols.Ellipsis), width)
}

func connectionName(row connectionRow) string {
	if !row.hasIdentity() {
		return otherToolsDisplayName
	}
	if row.connection == "" || row.connection == row.connector {
		return row.connector
	}
	return row.connector + "/" + row.connection
}

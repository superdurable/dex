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
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/superdurable/dex/cli/internal/agentsterminal/markdownview"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

const (
	contentIndent          = "  "
	maximumToolNameWidth   = 24
	minimumToolNameWidth   = 16
	toolSummaryColumnWidth = 14
	// maximumToolSummaryWidth keeps a long result from pushing out the
	// argument and dropping the summary column.
	maximumToolSummaryWidth  = 28
	toolDurationColumnWidth  = 6
	maximumToolOutputLines   = 3
	spinnerDelay             = 200 * time.Millisecond
	elapsedTimeDelay         = time.Second
	spinnerFrameInterval     = 80 * time.Millisecond
	maximumLiveReplyLines    = 6
	reasoningTailLength      = 60
	minimumToolArgumentWidth = 12
	maximumToolArgumentWidth = 40
	// maximumContentWidth keeps columns near the text on wide terminals.
	maximumContentWidth = 100
)

// renderUserMessage draws a user message and its image chips for the
// scrollback. A declared sender name replaces the origin label.
func renderUserMessage(text string, origin string, senderName string, images []sessionfeed.ImageAttachment, width int, theme terminaltheme.Theme) []string {
	prefix := theme.Secondary.Render(theme.Symbols.UserPrompt) + " "
	if senderName != "" {
		// The name joins the first line, so it wraps with the text.
		text = senderName + ": " + text
	} else if origin != "" {
		prefix += theme.Secondary.Render("("+origin+")") + " "
	}
	var lines []string
	for index, paragraph := range strings.Split(strings.TrimRight(terminaltheme.ExpandTabs(text), "\n"), "\n") {
		for wrappedIndex, line := range terminaltheme.WrapToWidth(paragraph, max(width-2, 1)) {
			if index == 0 && wrappedIndex == 0 && senderName != "" {
				line = styleSenderName(line, senderName, theme)
			}
			if index == 0 && wrappedIndex == 0 {
				lines = append(lines, strings.TrimRight(prefix+line, " "))
				continue
			}
			lines = append(lines, strings.TrimRight(contentIndent+line, " "))
		}
	}
	if len(images) > 0 {
		lines = append(lines, contentIndent+renderImageChips(images, width-len(contentIndent), theme))
	}
	return lines
}

// styleSenderName highlights the "<name>:" label at the start of a user
// message's first line; a name the wrap split stays plain.
func styleSenderName(line string, senderName string, theme terminaltheme.Theme) string {
	label := senderName + ":"
	if !strings.HasPrefix(line, label) {
		return line
	}
	return theme.Accent.Render(label) + strings.TrimPrefix(line, label)
}

// renderImageChips draws attachments as "▣ name · type · size" chips.
func renderImageChips(images []sessionfeed.ImageAttachment, width int, theme terminaltheme.Theme) string {
	chips := make([]string, 0, len(images))
	for _, image := range images {
		parts := nonEmptyStrings(image.Name, image.MimeType, formatByteCount(image.SizeBytes))
		chips = append(chips, theme.Accent.Render(theme.Symbols.Image)+" "+theme.Secondary.Render(strings.Join(parts, theme.Symbols.Separator)))
	}
	return terminaltheme.TruncateToWidth(strings.Join(chips, "  "), width, theme.Symbols.Ellipsis)
}

// formatByteCount formats a size as "812 B", "240 KB", or "3.4 MB".
func formatByteCount(size int64) string {
	switch {
	case size <= 0:
		return ""
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%d KB", (size+512)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

// renderAssistantMarkdown draws a finished reply block, indented two cells.
func renderAssistantMarkdown(markdown string, width int, theme terminaltheme.Theme) []string {
	lines := markdownview.RenderMarkdown(markdown, max(width-len(contentIndent), 1), theme)
	for index, line := range lines {
		if line != "" {
			lines[index] = contentIndent + line
		}
	}
	return lines
}

// renderPlainReply draws the unfinished reply as wrapped plain text, so
// half-parsed markdown never jumps.
func renderPlainReply(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(terminaltheme.ExpandTabs(text), "\n") {
		for _, line := range terminaltheme.WrapToWidth(paragraph, max(width-len(contentIndent), 1)) {
			lines = append(lines, strings.TrimRight(contentIndent+line, " "))
		}
	}
	if len(lines) > maximumLiveReplyLines {
		lines = lines[len(lines)-maximumLiveReplyLines:]
	}
	return lines
}

// noticeLine is one notice or correction line for the scrollback.
func renderNoticeLine(severity sessionfeed.NoticeSeverity, text string, width int, theme terminaltheme.Theme) []string {
	symbol := theme.Secondary.Render(theme.Symbols.Queued)
	body := text
	bodyStyle := lipgloss.NewStyle()
	switch severity {
	case sessionfeed.NoticeSeverityWarning:
		symbol = theme.Warning.Render(theme.Symbols.Warning)
	case sessionfeed.NoticeSeverityError:
		symbol = theme.Error.Render(theme.Symbols.Failed)
	default:
		body = theme.Secondary.Render(text)
		bodyStyle = theme.Secondary
	}
	wrapped := terminaltheme.WrapToWidth(terminaltheme.ExpandTabs(body), max(width-4, 1))
	if plainText := terminaltheme.ExpandTabs(text); hasLinkableURL(plainText) {
		wrapped = renderLinkedLines(plainText, terminaltheme.WrapToWidth(plainText, max(width-4, 1)), bodyStyle, theme)
	}
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, contentIndent+symbol+" "+line)
			continue
		}
		lines = append(lines, contentIndent+"  "+strings.TrimLeft(line, " "))
	}
	return lines
}

// noticeURLPattern finds the web addresses in a notice, such as Dex Web
// links, without trailing punctuation.
var noticeURLPattern = regexp.MustCompile(`https?://[!-~]*[A-Za-z0-9/#=&%_~+-]`)

func hasLinkableURL(text string) bool {
	for _, candidate := range noticeURLPattern.FindAllString(text, -1) {
		if terminaltheme.IsLinkableURL(candidate) {
			return true
		}
	}
	return false
}

// renderLinkedLines returns the wrapped lines of text in style, with each URL
// piece linked to its whole URL. Each line closes its own link.
func renderLinkedLines(text string, wrapped []string, style lipgloss.Style, theme terminaltheme.Theme) []string {
	urlSpans := noticeURLPattern.FindAllStringIndex(text, -1)
	lines := make([]string, 0, len(wrapped))
	cursor := 0
	for _, line := range wrapped {
		trimmed := strings.TrimSpace(line)
		offset := strings.Index(text[cursor:], trimmed)
		if trimmed == "" || offset < 0 {
			lines = append(lines, style.Render(line))
			continue
		}
		start := cursor + offset
		cursor = start + len(trimmed)
		var rendered strings.Builder
		rendered.WriteString(line[:strings.Index(line, trimmed)])
		position := start
		for _, span := range urlSpans {
			if !terminaltheme.IsLinkableURL(text[span[0]:span[1]]) {
				continue
			}
			pieceStart, pieceEnd := max(span[0], position), min(span[1], cursor)
			if pieceStart >= pieceEnd {
				continue
			}
			rendered.WriteString(renderUnlessEmpty(style, text[position:pieceStart]))
			rendered.WriteString(theme.RenderLink(text[span[0]:span[1]], text[pieceStart:pieceEnd]))
			position = pieceEnd
		}
		rendered.WriteString(renderUnlessEmpty(style, text[position:cursor]))
		lines = append(lines, rendered.String())
	}
	return lines
}

func renderUnlessEmpty(style lipgloss.Style, text string) string {
	if text == "" {
		return ""
	}
	return style.Render(text)
}

// toolRowClock selects the spinner frame and elapsed time of running rows.
type toolRowClock struct {
	now          time.Time
	spinnerFrame int
}

// renderToolRow draws symbol, name, argument, summary, and duration columns.
// A narrow argument drops the summary, then the duration.
func renderToolRow(call sessionfeed.ToolCallChanged, nameWidth int, width int, clock toolRowClock, theme terminaltheme.Theme) []string {
	symbol := toolStatusSymbol(call, clock, theme)
	if terminaltheme.ClassifyWidth(width) <= terminaltheme.WidthCompact {
		nameWidth = min(max(nameWidth, terminaltheme.DisplayWidth(call.ToolName)), max(width/3, 8))
	} else {
		nameWidth = min(max(nameWidth, terminaltheme.DisplayWidth(call.ToolName), minimumToolNameWidth), maximumToolNameWidth)
	}
	name := terminaltheme.PadRight(terminaltheme.TruncateToWidth(call.ToolName, nameWidth, theme.Symbols.Ellipsis), nameWidth)
	columns := newToolRowColumns(call, clock.now, theme.Symbols)
	room := width - len(contentIndent) - 2 - nameWidth - 2
	for room-columns.Width() < minimumToolArgumentWidth {
		if !columns.DropOne() {
			break
		}
	}
	argumentWidth := min(maximumToolArgumentWidth, room-columns.Width())
	if columns.isNote && argumentWidth < minimumToolArgumentWidth {
		columns.summary = terminaltheme.TruncateToWidth(columns.summary, max(room-minimumToolArgumentWidth-2, 4), theme.Symbols.Ellipsis)
		argumentWidth = room - columns.Width()
	}
	argument := terminaltheme.TruncateToWidth(call.KeyArgument, max(argumentWidth, 0), theme.Symbols.Ellipsis)
	trailing := columns.Render()
	if trailing != "" {
		argument = terminaltheme.PadRight(argument, max(argumentWidth, 0))
	}
	rest := strings.TrimRight(argument+trailing, " ")
	line := contentIndent + symbol + " " + strings.TrimRight(name, " ")
	if rest != "" {
		line = contentIndent + symbol + " " + name + "  " + theme.Secondary.Render(rest)
	}
	lines := []string{line}
	lines = append(lines, renderToolOutput(call, width, theme)...)
	return append(lines, renderPlanChecklist(call, width, theme)...)
}

// renderPlanChecklist draws the steps a finished write_todos call wrote.
func renderPlanChecklist(call sessionfeed.ToolCallChanged, width int, theme terminaltheme.Theme) []string {
	if len(call.PlanItems) == 0 || !call.Status.IsFinished() || call.Status == sessionfeed.ToolCallFailed {
		return nil
	}
	return renderPlanItems(call.PlanItems, contentIndent+"  ", width, theme)
}

// renderPlanItems draws plan steps as ☐/☑ checklist lines after indent.
func renderPlanItems(items []sessionfeed.PlanItem, indent string, width int, theme terminaltheme.Theme) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		var line string
		switch item.Status {
		case sessionfeed.PlanItemDone:
			line = theme.Ok.Render(theme.Symbols.TaskDone) + " " + theme.Secondary.Render(item.Title)
		case sessionfeed.PlanItemInProgress:
			line = theme.Accent.Render(theme.Symbols.TaskOpen) + " " + theme.Strong.Render(item.Title)
		default:
			line = theme.Secondary.Render(theme.Symbols.TaskOpen) + " " + item.Title
		}
		lines = append(lines, terminaltheme.TruncateToWidth(indent+line, width, theme.Symbols.Ellipsis))
	}
	return lines
}

// toolRowColumns follow a tool row's argument. The summary column holds the
// result or the status note.
type toolRowColumns struct {
	summary           string
	hasSummaryColumn  bool
	isNote            bool
	duration          string
	hasDurationColumn bool
}

func newToolRowColumns(call sessionfeed.ToolCallChanged, now time.Time, symbols terminaltheme.SymbolSet) toolRowColumns {
	columns := toolRowColumns{hasSummaryColumn: true, duration: toolDurationText(call, now), hasDurationColumn: true}
	if note := toolStatusNote(call); note != "" {
		columns.summary, columns.isNote = note, true
		return columns
	}
	if call.Status.IsFinished() {
		columns.summary = terminaltheme.TruncateToWidth(call.Summary, maximumToolSummaryWidth, symbols.Ellipsis)
		if call.IsOutputTruncated {
			columns.summary += " (truncated)"
		}
		if call.ImageCount > 0 {
			columns.summary = strings.TrimLeft(fmt.Sprintf("%s %s %d", columns.summary, symbols.Image, call.ImageCount), " ")
		}
	}
	return columns
}

// DropOne drops the least important column and reports whether one was left.
// Notes stay.
func (columns *toolRowColumns) DropOne() bool {
	switch {
	case columns.hasSummaryColumn && !columns.isNote:
		columns.hasSummaryColumn, columns.summary = false, ""
	case columns.hasDurationColumn:
		columns.hasDurationColumn, columns.duration = false, ""
	default:
		return false
	}
	return true
}

func (columns toolRowColumns) Width() int {
	return terminaltheme.DisplayWidth(columns.Render())
}

// Render right-aligns each column in a fixed width, so a turn's rows line up.
func (columns toolRowColumns) Render() string {
	var builder strings.Builder
	if columns.hasSummaryColumn {
		builder.WriteString("  " + terminaltheme.PadLeft(columns.summary, toolSummaryColumnWidth))
	}
	if columns.hasDurationColumn {
		builder.WriteString("  " + terminaltheme.PadLeft(columns.duration, toolDurationColumnWidth))
	}
	return builder.String()
}

func renderToolOutput(call sessionfeed.ToolCallChanged, width int, theme terminaltheme.Theme) []string {
	if call.Status != sessionfeed.ToolCallRunning && call.Status != sessionfeed.ToolCallFailed {
		return nil
	}
	outputLines := call.OutputTail
	hiddenCount := call.HiddenOutputLineCount
	if len(outputLines) > maximumToolOutputLines {
		hiddenCount += len(outputLines) - maximumToolOutputLines
		if call.Status == sessionfeed.ToolCallRunning {
			outputLines = outputLines[len(outputLines)-maximumToolOutputLines:]
		} else {
			outputLines = outputLines[:maximumToolOutputLines]
		}
	}
	gutter := contentIndent + "  " + theme.Secondary.Render(theme.Symbols.Gutter) + " "
	lines := make([]string, 0, len(outputLines)+1)
	for _, outputLine := range outputLines {
		shown := strings.TrimRight(terminaltheme.TruncateToWidth(outputLine, max(width-6, 1), theme.Symbols.Ellipsis), " ")
		lines = append(lines, strings.TrimRight(gutter+theme.Secondary.Render(shown), " "))
	}
	if hiddenCount > 0 {
		lines = append(lines, gutter+theme.Secondary.Render(moreLinesText(hiddenCount, theme.Symbols.Ellipsis)))
	}
	return lines
}

func toolStatusSymbol(call sessionfeed.ToolCallChanged, clock toolRowClock, theme terminaltheme.Theme) string {
	switch call.Status {
	case sessionfeed.ToolCallRunning:
		if clock.now.Sub(call.StartedAt) > spinnerDelay {
			return theme.Info.Render(theme.Symbols.SpinnerFrame(clock.spinnerFrame))
		}
		return theme.Info.Render(theme.Symbols.Running)
	case sessionfeed.ToolCallAwaitingApproval, sessionfeed.ToolCallAwaitingAnswer:
		return theme.Warning.Render(theme.Symbols.AwaitingUser)
	case sessionfeed.ToolCallSucceeded:
		return theme.Ok.Render(theme.Symbols.Succeeded)
	case sessionfeed.ToolCallFailed:
		return theme.Error.Render(theme.Symbols.Failed)
	case sessionfeed.ToolCallOutcomeUnknown:
		return theme.Warning.Render(theme.Symbols.Warning)
	case sessionfeed.ToolCallRejected, sessionfeed.ToolCallCancelled, sessionfeed.ToolCallSkipped:
		return theme.Secondary.Render(theme.Symbols.NotRun)
	case sessionfeed.ToolCallFromHistory:
		return theme.Secondary.Render(theme.Symbols.Queued)
	default:
		return theme.Secondary.Render(theme.Symbols.Queued)
	}
}

// toolStatusNote says why a call did not run. Every width keeps it.
func toolStatusNote(call sessionfeed.ToolCallChanged) string {
	withReason := func(status string) string {
		if call.Reason == "" {
			return status
		}
		return status + ": " + call.Reason
	}
	switch call.Status {
	case sessionfeed.ToolCallAwaitingApproval:
		return "awaiting approval"
	case sessionfeed.ToolCallAwaitingAnswer:
		return "awaiting your answer"
	case sessionfeed.ToolCallQueued:
		return "queued"
	case sessionfeed.ToolCallOutcomeUnknown:
		return "outcome unknown"
	case sessionfeed.ToolCallRejected:
		return withReason("rejected")
	case sessionfeed.ToolCallCancelled:
		return withReason("cancelled")
	case sessionfeed.ToolCallSkipped:
		return withReason("skipped")
	default:
		return ""
	}
}

func toolDurationText(call sessionfeed.ToolCallChanged, now time.Time) string {
	if call.Status.IsFinished() {
		if call.Duration <= 0 {
			return ""
		}
		return formatDuration(call.Duration)
	}
	if call.Status != sessionfeed.ToolCallRunning || call.StartedAt.IsZero() {
		return ""
	}
	elapsed := now.Sub(call.StartedAt)
	if elapsed < elapsedTimeDelay {
		return ""
	}
	return formatDuration(elapsed)
}

// moreLinesText says how many lines are hidden, such as "… 18 more lines".
func moreLinesText(count int, ellipsis string) string {
	if count == 1 {
		return ellipsis + " 1 more line"
	}
	return fmt.Sprintf("%s %d more lines", ellipsis, count)
}

// formatDuration formats a duration as "0.4s", "14.8s", or "2m05s".
func formatDuration(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// formatElapsedSeconds formats a turn's elapsed time as "9s" or "2m05s".
func formatElapsedSeconds(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(duration/time.Minute), int((duration%time.Minute)/time.Second))
}

// formatTokenCount formats a token count as "812 tok", "1.2k tok", or
// "3.4M tok".
func formatTokenCount(tokens int64) string {
	switch {
	case tokens < 1000:
		return fmt.Sprintf("%d tok", tokens)
	case tokens < 1_000_000:
		return fmt.Sprintf("%.1fk tok", float64(tokens)/1000)
	default:
		return fmt.Sprintf("%.1fM tok", float64(tokens)/1_000_000)
	}
}

// joinFittingParts joins styled parts with separator into a line of at most
// width cells, truncating the end.
func joinFittingParts(parts []string, separator string, width int, ellipsis string) string {
	return terminaltheme.TruncateToWidth(strings.Join(nonEmptyStrings(parts...), separator), width, ellipsis)
}

// renderHintLine draws the key hints. Compact widths keep only the first two.
func renderHintLine(hints []string, width int, theme terminaltheme.Theme) string {
	if terminaltheme.ClassifyWidth(width) <= terminaltheme.WidthCompact && len(hints) > 2 {
		hints = hints[:2]
	}
	text := " " + strings.Join(hints, theme.Symbols.Separator)
	return theme.Secondary.Render(terminaltheme.TruncateToWidth(text, width, theme.Symbols.Ellipsis))
}

// renderPlanLine draws the one-line plan summary.
func renderPlanLine(items []sessionfeed.PlanItem, width int, theme terminaltheme.Theme) string {
	doneCount := 0
	current := ""
	for _, item := range items {
		switch item.Status {
		case sessionfeed.PlanItemDone:
			doneCount++
		case sessionfeed.PlanItemInProgress:
			if current == "" {
				current = item.Title
			}
		}
	}
	text := theme.Secondary.Render(fmt.Sprintf(" plan%s%d of %d done", theme.Symbols.Separator, doneCount, len(items)))
	if current != "" {
		text += theme.Secondary.Render(theme.Symbols.Separator) + theme.Accent.Render(theme.Symbols.Focus) + " " + current
	}
	return terminaltheme.TruncateToWidth(text, width, theme.Symbols.Ellipsis)
}

// helpKeyRows are the key rows of the help overlay.
var helpKeyRows = [][2]string{
	{"Enter", "send, or queue while a turn runs"},
	{"Shift-Enter", "new line (also Alt-Enter, Ctrl-J)"},
	{"Ctrl-C", "cancel the turn or clear the input; press again to exit"},
	{"Esc", "cancel the running turn (empty input)"},
	{"Ctrl-D", "exit (empty input)"},
	{"Tab", "complete a /command or an /image path; on empty input, focus waiting approvals and queued messages, then Up/Down picks one"},
	{"Up/Down", "scroll the messages (empty input); move in a draft; keep recalling after Ctrl-P"},
	{"Ctrl-P/N", "recall earlier and later messages"},
	{"Ctrl-O", "open the last tool's full output"},
	{"Ctrl-T", "show the plan's steps"},
	{"PgUp/PgDn", "scroll the messages by a page; End follows new ones"},
	{"Drag", "select text to copy with the terminal's copy key"},
	{"F1", "show this help"},
}

// renderHelpOverlay draws the help overlay from the key rows and the
// command registry.
func renderHelpOverlay(registry slashCommandRegistry, width int, theme terminaltheme.Theme) []string {
	keyWidth := 13
	lines := []string{contentIndent + theme.Title.Render("Keys")}
	descriptionWidth := max(width-len(contentIndent)-keyWidth, 10)
	for _, row := range helpKeyRows {
		lines = append(lines, renderHelpRow(row[0], row[1], keyWidth, descriptionWidth)...)
	}
	lines = append(lines, "", contentIndent+theme.Title.Render("Commands"))
	for _, command := range registry.Commands() {
		lines = append(lines, renderHelpRow(completionName(command), command.Description, keyWidth, descriptionWidth)...)
	}
	return lines
}

func renderHelpRow(key string, description string, keyWidth int, descriptionWidth int) []string {
	wrapped := terminaltheme.WrapToWidth(description, descriptionWidth)
	keyText := terminaltheme.PadRight(key, keyWidth)
	if terminaltheme.DisplayWidth(key) >= keyWidth {
		keyText = key + " "
	}
	lines := []string{contentIndent + keyText + wrapped[0]}
	for _, line := range wrapped[1:] {
		lines = append(lines, contentIndent+strings.Repeat(" ", keyWidth)+line)
	}
	return lines
}

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
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/markdownview"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

const (
	contentIndent            = "  "
	maximumToolNameWidth     = 24
	minimumToolNameWidth     = 16
	toolSummaryColumnWidth   = 14
	toolDurationColumnWidth  = 6
	maximumToolOutputLines   = 3
	spinnerDelay             = 200 * time.Millisecond
	elapsedTimeDelay         = time.Second
	spinnerFrameInterval     = 80 * time.Millisecond
	maximumLiveReplyLines    = 6
	reasoningTailLength      = 60
	minimumToolArgumentWidth = 8
)

// renderUserMessage draws a user message for the scrollback.
func renderUserMessage(text string, origin string, width int, theme terminaltheme.Theme) []string {
	prefix := theme.Secondary.Render(theme.Symbols.UserPrompt) + " "
	if origin != "" {
		prefix += theme.Secondary.Render("("+origin+")") + " "
	}
	var lines []string
	for index, paragraph := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		for wrappedIndex, line := range terminaltheme.WrapToWidth(paragraph, max(width-2, 1)) {
			if index == 0 && wrappedIndex == 0 {
				lines = append(lines, prefix+line)
				continue
			}
			lines = append(lines, contentIndent+line)
		}
	}
	return lines
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
			lines = append(lines, contentIndent+line)
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
	switch severity {
	case sessionfeed.NoticeSeverityWarning:
		symbol = theme.Warning.Render(theme.Symbols.Warning)
	case sessionfeed.NoticeSeverityError:
		symbol = theme.Error.Render(theme.Symbols.Failed)
	default:
		body = theme.Secondary.Render(text)
	}
	wrapped := terminaltheme.WrapToWidth(terminaltheme.ExpandTabs(body), max(width-4, 1))
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines = append(lines, contentIndent+symbol+" "+line)
			continue
		}
		lines = append(lines, contentIndent+"  "+line)
	}
	return lines
}

// toolRowClock selects the spinner frame and elapsed time of running rows.
type toolRowClock struct {
	now          time.Time
	spinnerFrame int
}

// renderToolRow draws one tool call. Narrower widths drop the summary, then
// the duration, then truncate.
func renderToolRow(call sessionfeed.ToolCallChanged, nameWidth int, width int, clock toolRowClock, theme terminaltheme.Theme) []string {
	widthClass := terminaltheme.ClassifyWidth(width)
	symbol := toolStatusSymbol(call, clock, theme)
	nameWidth = min(max(nameWidth, terminaltheme.DisplayWidth(call.ToolName), minimumToolNameWidth), maximumToolNameWidth)
	if widthClass <= terminaltheme.WidthCompact {
		nameWidth = min(terminaltheme.DisplayWidth(call.ToolName), max(width/3, 8))
	}
	name := terminaltheme.PadRight(terminaltheme.TruncateToWidth(call.ToolName, nameWidth, theme.Symbols.Ellipsis), nameWidth)

	note := toolStatusNote(call)
	summary := ""
	if widthClass >= terminaltheme.WidthFull && call.Status.IsFinished() && note == "" {
		summary = call.Summary
		if call.IsOutputTruncated {
			summary += " (truncated)"
		}
	}
	duration := ""
	if widthClass >= terminaltheme.WidthMedium {
		duration = toolDurationText(call, clock.now)
	}

	var trailing strings.Builder
	if note != "" {
		trailing.WriteString("  " + note)
	}
	if summary != "" {
		trailing.WriteString("  " + terminaltheme.PadLeft(summary, toolSummaryColumnWidth))
	}
	if widthClass >= terminaltheme.WidthMedium {
		trailing.WriteString("  " + terminaltheme.PadLeft(duration, toolDurationColumnWidth))
	}
	trailingText := strings.TrimRight(trailing.String(), " ")
	fixedWidth := len(contentIndent) + 2 + nameWidth + 2
	argumentWidth := width - fixedWidth - terminaltheme.DisplayWidth(trailingText)
	if argumentWidth < minimumToolArgumentWidth && note != "" {
		trailingText = "  " + terminaltheme.TruncateToWidth(note, max(width-fixedWidth-minimumToolArgumentWidth-2, 4), theme.Symbols.Ellipsis)
		argumentWidth = width - fixedWidth - terminaltheme.DisplayWidth(trailingText)
	}
	argument := terminaltheme.TruncateToWidth(terminaltheme.ExpandTabs(call.KeyArgument), max(argumentWidth, 0), theme.Symbols.Ellipsis)
	if trailingText != "" {
		argument = terminaltheme.PadRight(argument, max(argumentWidth, 0))
	}
	line := contentIndent + symbol + " " + name + "  " + theme.Secondary.Render(argument) + theme.Secondary.Render(trailingText)
	lines := []string{strings.TrimRight(line, " ")}
	return append(lines, renderToolOutput(call, width, theme)...)
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
		lines = append(lines, gutter+theme.Secondary.Render(terminaltheme.TruncateToWidth(terminaltheme.ExpandTabs(outputLine), max(width-6, 1), theme.Symbols.Ellipsis)))
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
	case sessionfeed.ToolCallAwaitingApproval:
		return theme.Warning.Render(theme.Symbols.AwaitingUser)
	case sessionfeed.ToolCallSucceeded:
		return theme.Ok.Render(theme.Symbols.Succeeded)
	case sessionfeed.ToolCallFailed:
		return theme.Error.Render(theme.Symbols.Failed)
	case sessionfeed.ToolCallOutcomeUnknown:
		return theme.Warning.Render(theme.Symbols.Warning)
	case sessionfeed.ToolCallRejected, sessionfeed.ToolCallCancelled, sessionfeed.ToolCallSkipped:
		return theme.Secondary.Render(theme.Symbols.NotRun)
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

// renderLeftRightLine draws left and right parts on one line of width,
// truncating left first. Both parts are already styled.
func renderLeftRightLine(left string, right string, width int, ellipsis string) string {
	rightWidth := terminaltheme.DisplayWidth(right)
	if right == "" {
		return terminaltheme.TruncateToWidth(left, width, ellipsis)
	}
	leftRoom := width - rightWidth - 2
	if leftRoom < 4 {
		return terminaltheme.TruncateToWidth(left, width, ellipsis)
	}
	left = terminaltheme.TruncateToWidth(left, leftRoom, ellipsis)
	return terminaltheme.PadRight(left, width-rightWidth) + right
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
	{"Ctrl-C", "cancel the turn; again to exit"},
	{"Ctrl-D", "exit (empty input)"},
	{"Tab", "complete a /command; on empty input, focus waiting approvals and queued messages"},
	{"Up/Down", "recall earlier messages"},
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
		name := "/" + command.Name
		if command.ArgumentHint != "" {
			name += " " + command.ArgumentHint
		}
		lines = append(lines, renderHelpRow(name, command.Description, keyWidth, descriptionWidth)...)
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

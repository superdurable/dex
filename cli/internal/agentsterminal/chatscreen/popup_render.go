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
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

const (
	maximumPopupWidth          = 100
	collapsedArgumentLineCount = 2
	maximumDiffPreviewLines    = 12
	maximumArgumentNameWidth   = 14
	// minimumPopupHeight fits the borders, one consequence row, and options.
	minimumPopupHeight = 8
)

// popupPosition is the place of the open interrupt among the pending ones.
type popupPosition struct {
	index int
	count int
}

// renderApprovalPopup fits maximumHeight rows when positive. Details collapse
// first; title, consequence, and options stay.
func renderApprovalPopup(
	approval sessionfeed.ApprovalRequested,
	popup interruptPopup,
	position popupPosition,
	width int,
	maximumHeight int,
	now time.Time,
	theme terminaltheme.Theme,
) []string {
	box := newPopupBox(width, theme)
	var header []string
	if text := strings.Join(nonEmptyStrings(approval.SourceLabel, approval.ScopeLabel, approval.EffectLabel), theme.Symbols.Separator); text != "" {
		header = append(header, theme.Secondary.Render(terminaltheme.TruncateToWidth(text, box.contentWidth, theme.Symbols.Ellipsis)))
	}
	if !approval.Deadline.IsZero() {
		header = append(header, theme.Secondary.Render("expires in "+formatRemainingTime(approval.Deadline.Sub(now))))
	}
	details := append([]string{""}, renderApprovalArguments(approval.Arguments, popup.isShowingAllArguments, box.contentWidth, theme)...)
	if approval.Preview != nil {
		details = append(details, "")
		details = append(details, renderFileChangePreview(*approval.Preview, box.contentWidth, theme)...)
	}
	consequence := []string{""}
	if approval.EffectSummary != "" {
		consequence = append(consequence, terminaltheme.WrapToWidth(approval.EffectSummary, box.contentWidth)...)
	} else {
		consequence = append(consequence, theme.Secondary.Render("The tool does not describe its effect."))
	}
	options := []string{""}
	if popup.isEnteringText {
		options = append(options, renderPopupTextInput("Reason", &popup.textInput, box.contentWidth, theme)...)
	} else {
		labels := []string{"Approve", "Reject", "Reject with a reason"}
		keys := []string{"y", "n", "r"}
		for index, label := range labels {
			options = append(options, renderPopupOption(label, keys[index], index == popup.selectedOption, box.contentWidth, theme))
		}
	}
	if maximumHeight > 0 {
		room := maximumHeight - 2 - len(consequence) - len(options)
		if len(header)+len(details) > room {
			header, details = fitPopupDetails(header, details, room, popup.isShowingAllArguments, theme)
		}
	}
	body := append(append(append(header, details...), consequence...), options...)
	title := "Approve " + approval.ToolName + "?"
	return box.Render(title, positionLabel(position), body)
}

// fitPopupDetails cuts the details, then the header, to room rows, ending
// with a hidden-row count.
func fitPopupDetails(header []string, details []string, room int, isShowingAll bool, theme terminaltheme.Theme) ([]string, []string) {
	detailsRoom := room - len(header)
	if detailsRoom < 2 {
		header = header[:min(len(header), max(room-1, 0))]
		detailsRoom = room - len(header)
	}
	if detailsRoom <= 0 {
		return header, nil
	}
	hiddenCount := len(details) - detailsRoom + 1
	hint := moreLinesText(hiddenCount, theme.Symbols.Ellipsis)
	if !isShowingAll {
		hint += theme.Symbols.Separator + "e shows all"
	}
	return header, append(details[:detailsRoom-1], theme.Secondary.Render(hint))
}

// renderQuestionPopup fits maximumHeight rows when positive. A long question
// collapses; options stay.
func renderQuestionPopup(
	question sessionfeed.QuestionAsked,
	popup interruptPopup,
	position popupPosition,
	width int,
	maximumHeight int,
	theme terminaltheme.Theme,
) []string {
	box := newPopupBox(width, theme)
	questionLines := terminaltheme.WrapToWidth(question.Question, box.contentWidth)
	options := []string{""}
	for index, option := range questionOptions(question) {
		if option.isFreeText && popup.isEnteringText {
			options = append(options, renderPopupTextInput("Answer", &popup.textInput, box.contentWidth, theme)...)
			continue
		}
		isFocused := index == popup.selectedOption && !popup.isEnteringText
		options = append(options, renderPopupOption(option.label, "", isFocused, box.contentWidth, theme))
	}
	if room := maximumHeight - 2 - len(options); maximumHeight > 0 && len(questionLines) > max(room, 1) {
		kept := max(room-1, 1)
		hiddenCount := len(questionLines) - kept
		questionLines = append(questionLines[:kept], theme.Secondary.Render(moreLinesText(hiddenCount, theme.Symbols.Ellipsis)))
	}
	return box.Render("The agent asks", positionLabel(position), append(questionLines, options...))
}

func renderApprovalArguments(arguments []sessionfeed.ApprovalArgument, isShowingAll bool, contentWidth int, theme terminaltheme.Theme) []string {
	nameWidth := 0
	for _, argument := range arguments {
		nameWidth = max(nameWidth, terminaltheme.DisplayWidth(argument.Name))
	}
	nameWidth = min(nameWidth, maximumArgumentNameWidth)
	valueWidth := max(contentWidth-nameWidth-2, 8)
	indent := strings.Repeat(" ", nameWidth+2)
	var lines []string
	for _, argument := range arguments {
		name := terminaltheme.PadRight(terminaltheme.TruncateToWidth(argument.Name, nameWidth, theme.Symbols.Ellipsis), nameWidth)
		var valueLines []string
		for _, paragraph := range strings.Split(terminaltheme.ExpandTabs(argument.Value), "\n") {
			valueLines = append(valueLines, terminaltheme.WrapToWidth(paragraph, valueWidth)...)
		}
		isCollapsed := !isShowingAll && len(valueLines) > collapsedArgumentLineCount
		if isCollapsed {
			valueLines = valueLines[:collapsedArgumentLineCount]
			last := valueLines[collapsedArgumentLineCount-1]
			valueLines[collapsedArgumentLineCount-1] = terminaltheme.TruncateToWidth(last+theme.Symbols.Ellipsis, valueWidth, theme.Symbols.Ellipsis)
		}
		for index, valueLine := range valueLines {
			prefix := indent
			if index == 0 {
				prefix = name + "  "
			}
			lines = append(lines, prefix+valueLine)
		}
		if isCollapsed {
			characterCount := utf8.RuneCountInString(argument.Value)
			lines = append(lines, indent+theme.Secondary.Render(fmt.Sprintf("(%d chars%se shows all)", characterCount, theme.Symbols.Separator)))
		}
	}
	return lines
}

func renderFileChangePreview(preview sessionfeed.FileChangePreview, contentWidth int, theme terminaltheme.Theme) []string {
	counts := fmt.Sprintf("+%d -%d", preview.AddedLineCount, preview.RemovedLineCount)
	if preview.IsCredentialFile {
		summary := preview.Path + "  " + counts + theme.Symbols.Separator + "content hidden"
		return []string{terminaltheme.TruncateToWidth(summary, contentWidth, theme.Symbols.Ellipsis)}
	}
	lines := []string{
		terminaltheme.TruncateToWidth(preview.Path, contentWidth-len(counts)-2, theme.Symbols.Ellipsis) + "  " + theme.Secondary.Render(counts),
		theme.Secondary.Render("diff against the file now"),
	}
	shown := preview.Lines
	if len(shown) > maximumDiffPreviewLines {
		shown = shown[:maximumDiffPreviewLines]
	}
	for _, diffLine := range shown {
		number := ""
		if diffLine.LineNumber > 0 {
			number = strconv.Itoa(diffLine.LineNumber)
		}
		sign := " "
		switch diffLine.Kind {
		case sessionfeed.DiffLineAdded:
			sign = theme.Ok.Render(theme.Symbols.DiffAdded)
		case sessionfeed.DiffLineRemoved:
			sign = theme.Error.Render(theme.Symbols.DiffRemoved)
		}
		prefix := theme.Secondary.Render(terminaltheme.PadLeft(number, 5)) + "  " + sign + " "
		lines = append(lines, prefix+terminaltheme.TruncateToWidth(terminaltheme.ExpandTabs(diffLine.Text), contentWidth-9, theme.Symbols.Ellipsis))
	}
	if hidden := len(preview.Lines) - len(shown); hidden > 0 {
		lines = append(lines, theme.Secondary.Render(moreLinesText(hidden, theme.Symbols.Ellipsis)))
	}
	return lines
}

func renderPopupOption(label string, key string, isFocused bool, contentWidth int, theme terminaltheme.Theme) string {
	marker := "  "
	text := label
	if isFocused {
		marker = theme.Accent.Render(theme.Symbols.Focus) + " "
		text = theme.Focused.Render(label)
	}
	line := marker + text
	if key == "" {
		return line
	}
	return terminaltheme.PadRight(line, contentWidth-1) + theme.Secondary.Render(key)
}

func renderPopupTextInput(label string, editor *inputEditor, contentWidth int, theme terminaltheme.Theme) []string {
	prompt := theme.Accent.Render(theme.Symbols.Focus) + " " + label + " " + theme.Accent.Render(theme.Symbols.UserPrompt) + " "
	rendered := editor.Render(prompt, "", contentWidth, 3)
	lines := rendered.lines
	cursorLine := lines[rendered.cursorRow]
	lines[rendered.cursorRow] = insertVirtualCursor(cursorLine, rendered.cursorColumn)
	return lines
}

// insertVirtualCursor draws a reverse-video cell, because the terminal cursor
// stays with the main input.
func insertVirtualCursor(line string, column int) string {
	lineWidth := terminaltheme.DisplayWidth(line)
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	if column >= lineWidth {
		return line + cursorStyle.Render(" ")
	}
	before := ansi.Cut(line, 0, column)
	under := ansi.Cut(line, column, column+1)
	after := ansi.Cut(line, column+1, lineWidth)
	return before + cursorStyle.Render(terminaltheme.StripStyles(under)) + after
}

// popupBox draws the thin rounded border around popup content.
type popupBox struct {
	width        int
	contentWidth int
	theme        terminaltheme.Theme
}

func newPopupBox(width int, theme terminaltheme.Theme) popupBox {
	boxWidth := min(width-2, maximumPopupWidth)
	return popupBox{width: boxWidth, contentWidth: max(boxWidth-4, 10), theme: theme}
}

// Render draws the bordered box, with title and rightLabel in its top border.
func (box popupBox) Render(title string, rightLabel string, body []string) []string {
	symbols := box.theme.Symbols
	border := box.theme.Accent
	rightPart := ""
	if rightLabel != "" {
		rightPart = " " + rightLabel + " " + symbols.Rule
	}
	titleRoom := box.width - 5 - terminaltheme.DisplayWidth(rightPart)
	titleText := terminaltheme.TruncateToWidth(title, titleRoom, symbols.Ellipsis)
	fillWidth := box.width - 5 - terminaltheme.DisplayWidth(titleText) - terminaltheme.DisplayWidth(rightPart)
	top := " " + border.Render(symbols.BoxTopLeft+symbols.Rule+" ") + box.theme.Title.Render(titleText) + " " +
		border.Render(strings.Repeat(symbols.Rule, max(fillWidth, 0))+rightPart+symbols.BoxTopRight)
	lines := []string{top}
	for _, line := range body {
		content := terminaltheme.PadRight(terminaltheme.TruncateToWidth(line, box.contentWidth, symbols.Ellipsis), box.contentWidth)
		lines = append(lines, " "+border.Render(symbols.BoxVertical)+" "+content+" "+border.Render(symbols.BoxVertical))
	}
	bottom := " " + border.Render(symbols.BoxBottomLeft+strings.Repeat(symbols.Rule, box.width-2)+symbols.BoxBottomRight)
	return append(lines, bottom)
}

func positionLabel(position popupPosition) string {
	if position.count <= 1 {
		return ""
	}
	return fmt.Sprintf("%d of %d", position.index+1, position.count)
}

func formatRemainingTime(remaining time.Duration) string {
	switch {
	case remaining <= 0:
		return "0s"
	case remaining < time.Minute:
		return fmt.Sprintf("%ds", int(remaining.Seconds()))
	case remaining < time.Hour:
		return fmt.Sprintf("%dm", int(remaining.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(remaining.Hours()), int(remaining.Minutes())%60)
	}
}

func nonEmptyStrings(values ...string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

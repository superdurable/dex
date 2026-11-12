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

// approvalPopupLayout is the approval popup at one size.
type approvalPopupLayout struct {
	lines []string
	// isConsequenceVisible reports that the whole consequence shows next to
	// the options. Approving needs it, or a finished read.
	isConsequenceVisible bool
	// consequenceLineCount and readingRoom size the reading view, where the
	// consequence scrolls alone.
	consequenceLineCount int
	readingRoom          int
}

// consequenceWarning replaces Approve when the consequence does not fit.
const consequenceWarning = "Enlarge the window or press e to read the consequence."

// layoutApprovalPopup reserves rows for the consequence and options; the
// arguments scroll in the rest.
func layoutApprovalPopup(
	approval sessionfeed.ApprovalRequested,
	popup interruptPopup,
	position popupPosition,
	width int,
	maximumHeight int,
	now time.Time,
	theme terminaltheme.Theme,
) approvalPopupLayout {
	box := newPopupBox(width, theme)
	var details []string
	if text := strings.Join(nonEmptyStrings(approval.SourceLabel, approval.ScopeLabel, approval.EffectLabel), theme.Symbols.Separator); text != "" {
		details = append(details, theme.Secondary.Render(terminaltheme.TruncateToWidth(text, box.contentWidth, theme.Symbols.Ellipsis)))
	}
	if !approval.Deadline.IsZero() {
		details = append(details, theme.Secondary.Render("expires in "+formatRemainingTime(approval.Deadline.Sub(now))))
	}
	arguments := renderApprovalArguments(approval.Arguments, popup.isShowingAllArguments, box.contentWidth, theme)
	var consequence []string
	if approval.EffectSummary != "" {
		details = append(append(details, ""), arguments...)
		consequence = terminaltheme.WrapToWidth(approval.EffectSummary, box.contentWidth)
	} else {
		// Without a described effect, the concrete arguments are the consequence.
		consequence = append(arguments, theme.Secondary.Render("The tool does not describe its effect."))
	}
	if approval.Preview != nil {
		details = append(append(details, ""), renderFileChangePreview(*approval.Preview, box.contentWidth, theme)...)
	}
	isTight := maximumHeight > 0 && !popup.isReadingConsequence && 1+len(consequence)+1+len(approvalOptionLabels) > maximumHeight-2
	options := renderApprovalOptions(popup, isTight && !popup.hasReadConsequence, box.contentWidth, theme)
	title := "Approve " + approval.ToolName + "?"
	layout := approvalPopupLayout{consequenceLineCount: len(consequence), readingRoom: max(maximumHeight-3, 1)}
	room := maximumHeight - 2
	switch {
	case maximumHeight <= 0:
		layout.isConsequenceVisible = true
		layout.lines = box.Render(title, positionLabel(position), joinPopupSections(details, consequence, options))
	case popup.isReadingConsequence:
		start := min(popup.consequenceScroll, max(len(consequence)-layout.readingRoom, 0))
		window := consequence[start:min(start+layout.readingRoom, len(consequence))]
		footer := theme.Secondary.Render(fmt.Sprintf("lines %d-%d of %d · PgUp/PgDn read · e back", start+1, start+len(window), len(consequence)))
		layout.lines = box.Render(title, positionLabel(position), append(append([]string{}, window...), footer))
	case 1+len(consequence)+1+len(options) <= room:
		layout.isConsequenceVisible = true
		detailsRoom := room - (1 + len(consequence) + 1 + len(options))
		shownDetails := scrollPopupBody(details, detailsRoom, popup.scrollFromBottom, -1, true, theme)
		layout.lines = box.Render(title, positionLabel(position), joinPopupSections(shownDetails, consequence, options))
	default:
		warning := theme.Warning.Render(terminaltheme.TruncateToWidth(consequenceWarning, box.contentWidth, theme.Symbols.Ellipsis))
		body := append([]string{warning}, options...)
		if room > len(body) {
			body = append([]string{warning}, append(consequence[:min(room-len(body), len(consequence))], options...)...)
		}
		layout.lines = box.Render(title, positionLabel(position), body[:min(len(body), max(room, 0))])
	}
	return layout
}

// joinPopupSections joins non-empty sections with one empty line between.
func joinPopupSections(sections ...[]string) []string {
	var body []string
	for _, section := range sections {
		if len(section) == 0 {
			continue
		}
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, section...)
	}
	return body
}

// approvalOptionLabels are the approval choices in order.
var approvalOptionLabels = []string{"Approve", "Reject", "Reject with a reason"}

func renderApprovalOptions(popup interruptPopup, isApproveBlocked bool, contentWidth int, theme terminaltheme.Theme) []string {
	if popup.isEnteringText {
		return renderPopupTextInput("Reason", &popup.textInput, contentWidth, theme)
	}
	labels := append([]string{}, approvalOptionLabels...)
	keys := []string{"y", "n", "r"}
	if isApproveBlocked {
		labels[0], keys[0] = "Approve after reading the consequence", "e"
	}
	options := make([]string, 0, len(labels))
	for index, label := range labels {
		options = append(options, renderPopupOption(label, keys[index], index == popup.selectedOption, contentWidth, theme))
	}
	return options
}

// renderQuestionPopup fits maximumHeight rows when positive; a long body
// scrolls.
func renderQuestionPopup(
	question sessionfeed.QuestionAsked,
	popup interruptPopup,
	position popupPosition,
	width int,
	maximumHeight int,
	theme terminaltheme.Theme,
) []string {
	box := newPopupBox(width, theme)
	body := terminaltheme.WrapToWidth(question.Question, box.contentWidth)
	body = append(body, "")
	focusLine := -1
	for index, option := range questionOptions(question) {
		if option.isFreeText && popup.isEnteringText {
			focusLine = len(body)
			body = append(body, renderPopupTextInput("Answer", &popup.textInput, box.contentWidth, theme)...)
			continue
		}
		isFocused := index == popup.selectedOption && !popup.isEnteringText
		if isFocused {
			focusLine = len(body)
		}
		body = append(body, renderPopupOption(option.label, "", isFocused, box.contentWidth, theme))
	}
	body = scrollPopupBody(body, maximumHeight-2, popup.scrollFromBottom, focusLine, maximumHeight > 0, theme)
	return box.Render("The agent asks", positionLabel(position), body)
}

// scrollPopupBody shows room rows of body, scrolled up from the end, with
// arrows for hidden rows.
func scrollPopupBody(body []string, room int, scrollFromBottom int, focusLine int, isBounded bool, theme terminaltheme.Theme) []string {
	if !isBounded || len(body) <= room {
		return body
	}
	if room <= 0 {
		return nil
	}
	visible := room
	if room >= 4 {
		visible = room - 2
	}
	end := len(body) - min(max(scrollFromBottom, 0), len(body)-visible)
	start := end - visible
	// Until the user scrolls, the focused line pulls the window to it.
	if scrollFromBottom == 0 && focusLine >= 0 && focusLine < start {
		start, end = focusLine, focusLine+visible
	}
	if scrollFromBottom == 0 && focusLine >= end {
		start, end = focusLine-visible+1, focusLine+1
	}
	window := body[start:end]
	if visible == room {
		return window
	}
	above, below := "", ""
	if start > 0 {
		above = theme.Secondary.Render(fmt.Sprintf("↑ %s · PgUp", strings.TrimPrefix(moreLinesText(start, ""), " ")))
	}
	if end < len(body) {
		below = theme.Secondary.Render(fmt.Sprintf("↓ %s · PgDn", strings.TrimPrefix(moreLinesText(len(body)-end, ""), " ")))
	}
	return append(append([]string{above}, window...), below)
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

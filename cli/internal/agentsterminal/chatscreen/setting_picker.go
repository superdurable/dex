// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// The session commands whose bare form opens a setting picker.
const (
	modelCommandName  = "model"
	effortCommandName = "effort"
)

// defaultEffortChoice is the /effort choice that removes the setting.
const defaultEffortChoice = "default"

// settingPicker offers the values of one session setting; Enter runs its
// command with the focused value.
type settingPicker struct {
	commandName string
	title       string
	choices     []settingChoice
	index       int
}

// settingChoice is one value a settingPicker offers.
type settingChoice struct {
	value     string
	note      string
	isCurrent bool
}

// openSettingPicker opens the picker of a bare /model or /effort and reports
// whether it had a value to offer.
func (screen *ChatScreen) openSettingPicker(commandName string) bool {
	var picker settingPicker
	switch commandName {
	case modelCommandName:
		picker = newModelPicker(screen.session)
	case effortCommandName:
		picker = newEffortPicker(screen.session)
	}
	if len(picker.choices) == 0 {
		return false
	}
	screen.settingPicker = &picker
	return true
}

// newModelPicker offers the suggested models; a current model outside them
// comes first.
func newModelPicker(session sessionfeed.SessionOpened) settingPicker {
	models := session.Models
	if session.Model != "" && !slices.Contains(models, session.Model) {
		models = append([]string{session.Model}, models...)
	}
	picker := settingPicker{commandName: modelCommandName, title: "Model from the next turn"}
	for _, model := range models {
		picker.choices = append(picker.choices, settingChoice{value: model, isCurrent: model == session.Model})
	}
	picker.focusCurrentChoice()
	return picker
}

// newEffortPicker offers the declared reasoning efforts and default; it has
// no choices when the command declares none.
func newEffortPicker(session sessionfeed.SessionOpened) settingPicker {
	picker := settingPicker{commandName: effortCommandName, title: "Reasoning effort from the next turn"}
	if len(session.ReasoningEffortValues) == 0 {
		return picker
	}
	current := session.ReasoningEffort
	if current == "" {
		current = defaultEffortChoice
	}
	for _, value := range session.ReasoningEffortValues {
		picker.choices = append(picker.choices, settingChoice{value: value, isCurrent: value == current})
	}
	picker.choices = append(picker.choices, settingChoice{
		value: defaultEffortChoice, note: "the model's own default", isCurrent: current == defaultEffortChoice,
	})
	picker.focusCurrentChoice()
	return picker
}

func (picker *settingPicker) focusCurrentChoice() {
	for index, choice := range picker.choices {
		if choice.isCurrent {
			picker.index = index
			return
		}
	}
}

func (screen *ChatScreen) handleSettingPickerKey(keyName string) tea.Cmd {
	picker := screen.settingPicker
	switch keyName {
	case "esc", "q":
		screen.settingPicker = nil
	case "up", "k":
		picker.index = max(picker.index-1, 0)
	case "down", "j":
		picker.index = min(picker.index+1, len(picker.choices)-1)
	case "enter":
		screen.settingPicker = nil
		return screen.submitAction(sessionfeed.RunSlashCommand{Name: picker.commandName, Argument: picker.choices[picker.index].value})
	}
	return nil
}

// renderSettingPicker lists the open picker's choices, at most height lines.
func (screen *ChatScreen) renderSettingPicker(width int, height int) []string {
	picker := screen.settingPicker
	theme := screen.theme
	lines := []string{"", contentIndent + theme.Title.Render(picker.title)}
	room := max(height-len(lines), 1)
	first := max(min(picker.index-room/2, len(picker.choices)-room), 0)
	valueWidth := 0
	for _, choice := range picker.choices {
		valueWidth = max(valueWidth, terminaltheme.DisplayWidth(choice.value))
	}
	for index := first; index < min(first+room, len(picker.choices)); index++ {
		choice := picker.choices[index]
		notes := nonEmptyStrings(choice.note)
		if choice.isCurrent {
			notes = append([]string{"current"}, notes...)
		}
		prefix, value := contentIndent, choice.value
		if len(notes) > 0 {
			value = terminaltheme.PadRight(value, valueWidth)
		}
		if index == picker.index {
			prefix, value = theme.Accent.Render(theme.Symbols.Focus)+" ", theme.Focused.Render(value)
		}
		line := prefix + value
		if len(notes) > 0 {
			line += "  " + theme.Secondary.Render(strings.Join(notes, theme.Symbols.Separator))
		}
		lines = append(lines, terminaltheme.TruncateToWidth(line, width, theme.Symbols.Ellipsis))
	}
	return lines[max(len(lines)-height, 0):]
}

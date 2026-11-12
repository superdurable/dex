// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// settingCommands declare /model and /effort as the remote session does.
var settingCommands = []sessionfeed.SlashCommand{
	{Name: "model", ArgumentHint: "ID", IsArgumentOptional: true, Description: "choose the model from the next turn"},
	{Name: "effort", ArgumentHint: "LEVEL", IsArgumentOptional: true, Description: "choose the reasoning effort from the next turn"},
}

// settingsSession is a session with suggested models and reasoning efforts.
func settingsSession() sessionfeed.SessionOpened {
	return sessionfeed.SessionOpened{
		Command: "openai-sdk", Model: "gpt-5.2", SessionID: "openai-sdk-20261111-142233-a1b2c3",
		Models:                []string{"gpt-5.2", "gpt-5-mini", "gpt-5-nano"},
		ReasoningEffortValues: []string{"low", "medium", "high"},
		IsNewSession:          true, AcceptsTurnControl: true, Workspace: "~/dex-agent-workspace",
	}
}

func newSettingsHarness(t *testing.T, session sessionfeed.SessionOpened) *screenHarness {
	t.Helper()
	harness := newScreenHarness(t, harnessOptions{commands: settingCommands, height: goldenHeight})
	harness.feed(session)
	return harness
}

func (harness *screenHarness) runCommandLine(line string) {
	harness.typeText(line)
	// Esc closes the completion list, so Enter runs the line as typed.
	harness.press(key(tea.KeyEscape), key(tea.KeyEnter))
}

func TestModelPickerSubmitsThePickedModel(t *testing.T) {
	harness := newSettingsHarness(t, settingsSession())
	harness.runCommandLine("/model")
	require.NotNil(t, harness.screen.settingPicker, "/model alone opens the picker")
	require.Empty(t, harness.source.recordedActions(), "opening the picker sends nothing")
	frame := harness.plainFrame()
	require.Contains(t, frame, "Model from the next turn")
	require.Contains(t, frame, "▸ gpt-5.2     current")
	require.Contains(t, frame, "Enter choose · j/k move · Esc close")

	harness.press(textKey('j'), key(tea.KeyDown), key(tea.KeyUp))
	harness.press(key(tea.KeyEnter))
	require.Nil(t, harness.screen.settingPicker)
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "model", Argument: "gpt-5-mini"}, harness.lastAction())
	require.Empty(t, harness.screen.input.Value())
}

func TestModelPickerShowsACurrentModelOutsideTheList(t *testing.T) {
	session := settingsSession()
	session.Model = "my-tuned-model"
	harness := newSettingsHarness(t, session)
	harness.runCommandLine("/model")
	picker := harness.screen.settingPicker
	require.NotNil(t, picker)
	require.Equal(t, settingChoice{value: "my-tuned-model", isCurrent: true}, picker.choices[0])
	require.Len(t, picker.choices, 4)
	require.Equal(t, 0, picker.index)
}

func TestEffortPickerOffersTheValuesAndDefault(t *testing.T) {
	harness := newSettingsHarness(t, settingsSession())
	harness.runCommandLine("/effort")
	picker := harness.screen.settingPicker
	require.NotNil(t, picker)
	require.Equal(t, []settingChoice{
		{value: "low"}, {value: "medium"}, {value: "high"},
		{value: "default", note: "the model's own default", isCurrent: true},
	}, picker.choices)
	require.Contains(t, harness.plainFrame(), "▸ default  current · the model's own default")

	harness.press(textKey('k'), key(tea.KeyEnter))
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "effort", Argument: "high"}, harness.lastAction())

	session := settingsSession()
	session.ReasoningEffort = "high"
	harness.feed(session)
	harness.runCommandLine("/effort")
	require.Equal(t, 2, harness.screen.settingPicker.index, "the current effort has focus")
}

func TestSettingPickerClosesWithoutSending(t *testing.T) {
	harness := newSettingsHarness(t, settingsSession())
	harness.runCommandLine("/model")
	harness.press(key(tea.KeyEscape))
	require.Nil(t, harness.screen.settingPicker)
	harness.runCommandLine("/effort")
	harness.press(ctrlKey('c'))
	require.Nil(t, harness.screen.settingPicker, "Ctrl-C closes the picker first")
	require.False(t, harness.screen.isExiting)
	require.Empty(t, harness.source.recordedActions())
}

func TestSettingCommandsWithAnArgumentSkipThePicker(t *testing.T) {
	harness := newSettingsHarness(t, settingsSession())
	harness.runCommandLine("/model my-tuned-model")
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "model", Argument: "my-tuned-model"}, harness.lastAction())
	harness.runCommandLine("/effort bogus")
	require.Nil(t, harness.screen.settingPicker)
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "effort", Argument: "bogus"}, harness.lastAction(), "the source validates the value")
}

// An old launch document has display.model only and no efforts.
func TestSettingPickersOfAnOldLaunchDocument(t *testing.T) {
	session := settingsSession()
	session.Models, session.ReasoningEffortValues = []string{"gpt-5.2"}, nil
	harness := newSettingsHarness(t, session)
	harness.runCommandLine("/model")
	require.Equal(t, []settingChoice{{value: "gpt-5.2", isCurrent: true}}, harness.screen.settingPicker.choices)
	harness.press(key(tea.KeyEscape))

	harness.runCommandLine("/effort")
	require.Nil(t, harness.screen.settingPicker, "no efforts, no picker")
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "effort"}, harness.lastAction(), "the source explains it")
	require.Empty(t, harness.screen.input.Value())

	session.Model, session.Models = "", nil
	harness.feed(session)
	harness.runCommandLine("/model")
	require.Nil(t, harness.screen.settingPicker)
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "model"}, harness.lastAction())
}

func TestFooterShowsTheReasoningEffort(t *testing.T) {
	session := settingsSession()
	harness := newSettingsHarness(t, session)
	require.Contains(t, harness.plainFrame(), " openai-sdk · gpt-5.2 · ~/dex-agent-workspace")
	session.ReasoningEffort = "high"
	harness.feed(session)
	require.Contains(t, harness.plainFrame(), " openai-sdk · gpt-5.2 · high effort · ~/dex-agent-workspace")
	session.Workspace = ""
	harness.feed(session)
	require.Contains(t, harness.plainFrame(), " openai-sdk · gpt-5.2 · high effort\n")
}

// With a long model ID, the workspace leaves and the model shortens; the effort stays.
func TestFooterKeepsTheEffortWithALongModel(t *testing.T) {
	session := settingsSession()
	session.Model = "openrouter/anthropic/claude-sonnet-5-5-with-a-very-long-deployment-name-for-tests"
	session.ReasoningEffort = "high"
	harness := newSettingsHarness(t, session)
	frame := harness.plainFrame()
	require.Regexp(t, ` openai-sdk · openrouter/anthropic/claude-sonnet-5-5-with-a-very[^ ]*… · high effort\n`, frame)
}

// Below four cells for the model, the row leaves the model out and keeps the effort.
func TestStatusIdentityLeavesTheModelOutBelowItsMinimumWidth(t *testing.T) {
	session := settingsSession()
	session.Model = "openrouter/anthropic/claude-sonnet-5-5"
	session.ReasoningEffort = "high"
	harness := newSettingsHarness(t, session)
	// " openai-sdk · " is 14 cells and " · high effort" 14, so 32 leaves the model 4.
	require.Equal(t, " openai-sdk · ope… · high effort", terminaltheme.StripStyles(harness.screen.statusIdentity(32)))
	require.Equal(t, " openai-sdk · high effort", terminaltheme.StripStyles(harness.screen.statusIdentity(31)))
	require.Equal(t, " openai-sdk · high effort", terminaltheme.StripStyles(harness.screen.statusIdentity(20)), "the row truncates it at its end")
}

func TestHelpAndCompletionListEffort(t *testing.T) {
	harness := newSettingsHarness(t, settingsSession())
	harness.typeText("/ef")
	require.Contains(t, harness.plainFrame(), "/effort [LEVEL]  choose the reasoning effort from the next turn")
	harness.press(key(tea.KeyEnter))
	require.NotNil(t, harness.screen.settingPicker, "Enter on the completion runs /effort alone")
	harness.press(key(tea.KeyEscape))
	help := stripForTest(strings.Join(renderHelpOverlay(harness.screen.commands, 80, harness.screen.theme), "\n"))
	require.Contains(t, help, "/effort [LEVEL] choose the reasoning effort from the next turn")
	require.Contains(t, help, "/model [ID]  choose the model from the next turn")
}

// pickerAndStatusBar renders the open picker and the status bar, leaving out
// the welcome area behind them.
func pickerAndStatusBar(harness *screenHarness, width int) string {
	lines := harness.screen.renderSettingPicker(width, 10)
	lines = append(lines, harness.screen.renderStatusBar(width, harness.now, 0)...)
	return terminaltheme.DownsampleStyledText(harness.profile, strings.Join(lines, "\n")+"\n")
}

func TestSettingPickerFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newSettingsHarness(t, settingsSession())
	harness.runCommandLine("/model")
	set.add("model picker, width 80", pickerAndStatusBar(harness, 80))
	harness.press(key(tea.KeyEscape))
	session := settingsSession()
	session.ReasoningEffort = "medium"
	harness.feed(session)
	harness.runCommandLine("/effort")
	set.add("effort picker, medium current, width 80", pickerAndStatusBar(harness, 80))
	set.add("effort picker, medium current, width 40", pickerAndStatusBar(harness, 40))
	set.compare("setting_picker")
}

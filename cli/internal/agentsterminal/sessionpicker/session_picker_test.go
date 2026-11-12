// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionpicker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

var testNow = time.Date(2026, 11, 11, 14, 30, 0, 0, time.UTC)

// listCall is one ListSessions call of the fake source.
type listCall struct {
	pageToken   string
	titleSearch string
}

// fakeSessions serves pages by token and title search.
type fakeSessions struct {
	pages        map[listCall]sessionlisting.SessionPage
	listErr      map[listCall]error
	calls        []listCall
	resumeChecks map[string]sessionlisting.ResumeCheck
	checked      []string
}

func (source *fakeSessions) ListSessions(_ context.Context, pageToken string, titleSearch string) (sessionlisting.SessionPage, error) {
	call := listCall{pageToken: pageToken, titleSearch: titleSearch}
	source.calls = append(source.calls, call)
	if err := source.listErr[call]; err != nil {
		return sessionlisting.SessionPage{}, err
	}
	return source.pages[call], nil
}

func (source *fakeSessions) CheckResume(_ context.Context, sessionID string) (sessionlisting.ResumeCheck, dex.FlowStatus, error) {
	source.checked = append(source.checked, sessionID)
	check := source.resumeChecks[sessionID]
	if check == sessionlisting.ResumeNotRunning {
		return check, dex.FlowCompleted, nil
	}
	return check, dex.FlowRunning, nil
}

// pickerHarness drives one picker and collects the outcome messages.
type pickerHarness struct {
	t       *testing.T
	picker  *SessionPicker
	outcome []tea.Msg
}

func newPickerHarness(t *testing.T, source *fakeSessions, width int, configure func(*SessionPickerConfig)) *pickerHarness {
	config := SessionPickerConfig{
		Sessions:  source,
		Theme:     terminaltheme.NewTheme(colorprofile.ASCII, true),
		Heading:   "openai-sdk · AgentSession",
		EmptyHint: "No sessions yet.",
		Now:       func() time.Time { return testNow },
	}
	if configure != nil {
		configure(&config)
	}
	harness := &pickerHarness{t: t, picker: NewSessionPicker(context.Background(), config)}
	harness.picker.SetSize(width, 20)
	harness.run(harness.picker.Init())
	return harness
}

// run executes command and feeds its messages back, keeping the outcomes.
func (harness *pickerHarness) run(command tea.Cmd) {
	if command == nil {
		return
	}
	message := command()
	switch typed := message.(type) {
	case tea.BatchMsg:
		for _, inner := range typed {
			harness.run(inner)
		}
	case SessionChosenMsg, PickerClosedMsg:
		harness.outcome = append(harness.outcome, typed)
	case nil:
	default:
		harness.run(harness.picker.Update(typed))
	}
}

func (harness *pickerHarness) press(keys ...string) {
	for _, name := range keys {
		harness.run(harness.picker.Update(keyMessage(name)))
	}
}

func (harness *pickerHarness) typeText(text string) {
	for _, character := range text {
		harness.run(harness.picker.Update(tea.KeyPressMsg{Code: character, Text: string(character)}))
	}
}

func (harness *pickerHarness) screen() string {
	return terminaltheme.StripStyles(harness.picker.View())
}

func keyMessage(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	runes := []rune(name)
	return tea.KeyPressMsg{Code: runes[0], Text: name}
}

func session(id string, status dex.FlowStatus) sessionlisting.SessionSummary {
	return sessionlisting.SessionSummary{SessionID: id, Status: status, StartedAt: time.Date(2026, 11, 10, 9, 15, 0, 0, time.UTC)}
}

func withSummary(summary sessionlisting.SessionSummary, title string, lastActivity time.Time, turns int64) sessionlisting.SessionSummary {
	summary.Title, summary.LastActivityAt, summary.TurnCount = &title, &lastActivity, &turns
	return summary
}

// Sessions without summary Attributes show only ID, STATUS, and STARTED.
func TestPickerWithoutSummaryShowsIDStatusAndStart(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{
			session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning),
			session("openai-sdk-20261110-091502-9f8e7d", dex.FlowCompleted),
		}},
	}}
	screen := newPickerHarness(t, source, 100, nil).screen()
	require.Contains(t, screen, "ID")
	require.Contains(t, screen, "STATUS")
	require.Contains(t, screen, "STARTED")
	require.NotContains(t, screen, "UPDATED")
	require.NotContains(t, screen, "TURNS")
	require.Contains(t, screen, "▸ openai-sdk-20261111-142233-a1b2c3")
	require.Contains(t, screen, "● running")
	require.Contains(t, screen, "✓ completed")
}

// Summaries add UPDATED, TURNS, and a preview; a session without one
// shows dashes.
func TestPickerShowsSummaryColumnsAndPreview(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{
			withSummary(session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning), "Find open issues about flaky tests", testNow.Add(-2*time.Minute), 4),
			session("openai-sdk-20261101-000000-000000", dex.FlowFailed),
		}},
	}}
	screen := newPickerHarness(t, source, 100, nil).screen()
	require.Contains(t, screen, "UPDATED")
	require.Contains(t, screen, "TURNS")
	require.Contains(t, screen, "2m ago")
	require.Contains(t, screen, `"Find open issues about flaky tests"`)
	lines := strings.Split(screen, "\n")
	var failedRow string
	for _, line := range lines {
		if strings.Contains(line, "openai-sdk-20261101") {
			failedRow = line
		}
	}
	require.Equal(t, 2, strings.Count(failedRow, "—"), "UPDATED and TURNS are dashes: %q", failedRow)
}

// A narrow screen drops TURNS and UPDATED first, and shortens long IDs from
// the middle, keeping the random suffix.
func TestPickerNarrowWidth(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{
			withSummary(session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning), "title", testNow, 1),
		}},
	}}
	screen := newPickerHarness(t, source, 50, nil).screen()
	require.NotContains(t, screen, "TURNS")
	require.NotContains(t, screen, "UPDATED")
	require.Contains(t, screen, "a1b2c3")
	for _, line := range strings.Split(screen, "\n") {
		require.LessOrEqual(t, terminaltheme.DisplayWidth(line), 50, "line %q", line)
	}
	require.Equal(t, "openai-sdk-…-a1b2c3", shortenSessionID("openai-sdk-20261111-142233-a1b2c3", 19, "…"))
}

// The Load more row loads the next page with the token, once the cursor
// reaches it.
func TestPickerLoadsTheNextPage(t *testing.T) {
	var first []sessionlisting.SessionSummary
	for index := range 3 {
		first = append(first, session(fmt.Sprintf("openai-sdk-20261111-1422%02d-aaaaa%d", index, index), dex.FlowRunning))
	}
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}:                    {Sessions: first, NextPageToken: "page-2"},
		{pageToken: "page-2"}: {Sessions: []sessionlisting.SessionSummary{session("openai-sdk-20261101-000000-bbbbbb", dex.FlowCompleted)}},
	}}
	harness := newPickerHarness(t, source, 100, nil)
	require.Contains(t, harness.screen(), "Load more")
	require.Contains(t, harness.screen(), "3+")
	harness.press("down", "down", "down")
	require.Equal(t, []listCall{{}, {pageToken: "page-2"}}, source.calls)
	screen := harness.screen()
	require.Contains(t, screen, "openai-sdk-20261101-000000-bbbbbb")
	require.NotContains(t, screen, "Load more")
}

// Typing after / filters the loaded sessions by ID and title; Esc clears.
func TestPickerFiltersLoadedSessions(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{
			session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning),
			withSummary(session("openai-sdk-20261110-091502-9f8e7d", dex.FlowRunning), "Fix the flaky retry test", testNow, 2),
		}},
	}}
	harness := newPickerHarness(t, source, 100, nil)
	harness.press("/")
	harness.typeText("a1b2")
	screen := harness.screen()
	require.Contains(t, screen, "searching 2 loaded sessions")
	require.Contains(t, screen, "a1b2c3")
	require.NotContains(t, screen, "9f8e7d")
	harness.press("esc")
	require.Contains(t, harness.screen(), "9f8e7d")
	harness.press("/")
	harness.typeText("FLAKY")
	require.NotContains(t, harness.screen(), "a1b2c3", "the title matches without case")
	require.Contains(t, harness.screen(), "9f8e7d")
}

// Enter searches titles in Dex, or falls back to loaded IDs without the
// index.
func TestPickerTitleSearch(t *testing.T) {
	source := &fakeSessions{
		pages: map[listCall]sessionlisting.SessionPage{
			{}: {Sessions: []sessionlisting.SessionSummary{session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning)}},
			{titleSearch: "flaky"}: {Sessions: []sessionlisting.SessionSummary{
				withSummary(session("openai-sdk-20261001-000000-cccccc", dex.FlowRunning), "Fix the flaky retry test", testNow, 2),
			}},
		},
	}
	harness := newPickerHarness(t, source, 100, nil)
	harness.press("/")
	harness.typeText("flaky")
	harness.press("enter")
	require.Equal(t, listCall{titleSearch: "flaky"}, source.calls[len(source.calls)-1])
	screen := harness.screen()
	require.Contains(t, screen, "cccccc")
	require.Contains(t, screen, "1 sessions whose titles match")
	harness.press("esc")
	require.Contains(t, harness.screen(), "a1b2c3", "Esc returns to the full list")

	source.listErr = map[listCall]error{{titleSearch: "a1b2"}: sessionlisting.ErrTitleSearchUnavailable}
	harness.press("/")
	harness.typeText("a1b2")
	harness.press("enter")
	screen = harness.screen()
	require.Contains(t, screen, "Title search is off here; filtering 1 loaded sessions by ID.")
	require.Contains(t, screen, "a1b2c3")
	calls := len(source.calls)
	harness.press("esc", "/")
	harness.typeText("zzz")
	harness.press("enter")
	require.Len(t, source.calls, calls, "once off, title search is not tried again")
	require.Contains(t, harness.screen(), "No loaded session matches.")
}

// Enter resumes only a running session that DescribeFlow confirms.
func TestPickerChoosesARunningSession(t *testing.T) {
	source := &fakeSessions{
		pages: map[listCall]sessionlisting.SessionPage{
			{}: {Sessions: []sessionlisting.SessionSummary{
				session("openai-sdk-20261110-091502-9f8e7d", dex.FlowCompleted),
				session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning),
				session("openai-sdk-20261111-150000-dddddd", dex.FlowRunning),
			}},
		},
		resumeChecks: map[string]sessionlisting.ResumeCheck{
			"openai-sdk-20261111-142233-a1b2c3": sessionlisting.ResumeNotRunning,
			"openai-sdk-20261111-150000-dddddd": sessionlisting.ResumeAllowed,
		},
	}
	harness := newPickerHarness(t, source, 100, func(config *SessionPickerConfig) {
		config.NewSessionHint = "start a new one with /new"
	})
	harness.press("enter")
	require.Contains(t, harness.screen(), "This session is completed; start a new one with /new.")
	require.Empty(t, source.checked, "a listed ended session needs no check")
	harness.press("down", "enter")
	require.Equal(t, []string{"openai-sdk-20261111-142233-a1b2c3"}, source.checked)
	require.Contains(t, harness.screen(), "This session is completed")
	require.Empty(t, harness.outcome, "a session that ended since the list loaded does not resume")
	harness.press("down", "enter")
	require.Equal(t, []tea.Msg{SessionChosenMsg{SessionID: "openai-sdk-20261111-150000-dddddd"}}, harness.outcome)
}

// Esc and q close; Ctrl-C asks to exit.
func TestPickerCloses(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{}}
	harness := newPickerHarness(t, source, 100, nil)
	require.Contains(t, harness.screen(), "No sessions yet.")
	harness.press("esc")
	require.Equal(t, []tea.Msg{PickerClosedMsg{}}, harness.outcome)
	harness = newPickerHarness(t, source, 100, nil)
	harness.press("ctrl+c")
	require.Equal(t, []tea.Msg{PickerClosedMsg{IsExitRequested: true}}, harness.outcome)
}

// A closed picker's late page never reaches a new picker, and closing
// cancels the closed picker's requests.
func TestPickerIgnoresAnotherPickersPage(t *testing.T) {
	stale := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{session("openai-sdk-20261101-000000-stale0", dex.FlowRunning)}},
	}}
	closedPicker := NewSessionPicker(context.Background(), SessionPickerConfig{Sessions: stale, Theme: terminaltheme.NewTheme(colorprofile.ASCII, true)})
	stalePage := closedPicker.Init()
	closedPicker.Update(keyMessage("esc"))
	require.ErrorIs(t, closedPicker.ctx.Err(), context.Canceled, "closing cancels the picker's requests")

	fresh := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{session("openai-sdk-20261111-000000-fresh0", dex.FlowRunning)}},
	}}
	harness := newPickerHarness(t, fresh, 100, nil)
	harness.run(harness.picker.Update(stalePage()))
	screen := harness.screen()
	require.Contains(t, screen, "fresh0")
	require.NotContains(t, screen, "stale0")
}

// The current chat's session is marked, and Enter on it just closes.
func TestPickerMarksTheCurrentSession(t *testing.T) {
	source := &fakeSessions{pages: map[listCall]sessionlisting.SessionPage{
		{}: {Sessions: []sessionlisting.SessionSummary{session("openai-sdk-20261111-142233-a1b2c3", dex.FlowRunning)}},
	}}
	harness := newPickerHarness(t, source, 100, func(config *SessionPickerConfig) {
		config.CurrentSessionID = "openai-sdk-20261111-142233-a1b2c3"
	})
	require.Contains(t, harness.screen(), "(this chat)")
	harness.press("enter")
	require.Equal(t, []tea.Msg{PickerClosedMsg{}}, harness.outcome)
	require.Empty(t, source.checked)
}
